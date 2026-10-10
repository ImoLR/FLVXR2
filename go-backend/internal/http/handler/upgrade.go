package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"go-backend/internal/http/response"
)

const (
	githubRepo     = "ImoLR/FLVXR2"
	githubAPIBase  = "https://api.github.com"
	githubHTMLBase = "https://github.com"
	upgradeTimeout = 5 * time.Minute
	batchWorkers   = 5

	releaseChannelStable = "stable"
	releaseChannelDev    = "dev"
)

var (
	stableVersionPattern = regexp.MustCompile(`^\d+(?:\.\d+)+(?:-fork\.\d+)?$`)
	forkVersionPattern   = regexp.MustCompile(`^(\d+(?:\.\d+)+)-fork\.(\d+)$`)
	testKeywordPattern   = regexp.MustCompile(`(?i)(alpha|beta|rc)`)
)

type githubRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	PublishedAt string `json:"published_at"`
	Prerelease  bool   `json:"prerelease"`
	Draft       bool   `json:"draft"`
}

func normalizeReleaseChannel(channel string) string {
	// 空字符串返回空，表示不指定通道（获取最新版本）
	if channel == "" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case releaseChannelDev:
		return releaseChannelDev
	default:
		return releaseChannelStable
	}
}

func releaseChannelFromTag(tag string) string {
	normalized := strings.ToLower(strings.TrimSpace(tag))
	if normalized == "" {
		return releaseChannelDev
	}
	if testKeywordPattern.MatchString(normalized) {
		return releaseChannelDev
	}
	if stableVersionPattern.MatchString(normalized) {
		return releaseChannelStable
	}

	return releaseChannelDev
}

func releaseChannelLabel(channel string) string {
	if normalizeReleaseChannel(channel) == releaseChannelDev {
		return "测试版"
	}

	return "正式版"
}

func fetchGitHubReleases(perPage int) ([]githubRelease, error) {
	if perPage <= 0 {
		perPage = 20
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(fmt.Sprintf("%s/repos/%s/releases?per_page=%d", githubAPIBase, githubRepo, perPage))
	if err != nil {
		return nil, fmt.Errorf("请求GitHub API失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GitHub API返回 %d: %s", resp.StatusCode, string(body))
	}

	var releases []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("解析GitHub API响应失败: %v", err)
	}

	return releases, nil
}

func resolveLatestReleaseByChannel(channel string) (string, error) {
	normalizedChannel := normalizeReleaseChannel(channel)
	releases, err := fetchGitHubReleases(50)
	if err != nil {
		return "", err
	}

	// 如果 channel 为空，返回第一个非 draft 的 release（最新版本）
	if normalizedChannel == "" {
		for _, r := range releases {
			if r.Draft {
				continue
			}
			tag := strings.TrimSpace(r.TagName)
			if tag != "" {
				return tag, nil
			}
		}
		return "", fmt.Errorf("未找到版本号")
	}

	// 否则按通道查找
	for _, r := range releases {
		if r.Draft {
			continue
		}
		tag := strings.TrimSpace(r.TagName)
		if tag == "" {
			continue
		}
		if releaseChannelFromTag(tag) == normalizedChannel {
			return tag, nil
		}
	}

	return "", fmt.Errorf("未找到%s版本号", releaseChannelLabel(normalizedChannel))
}

func releaseAssetURL(version, filename string) string {
	return fmt.Sprintf("%s/%s/releases/download/%s/%s", githubHTMLBase, githubRepo, version, filename)
}

// githubMirrorPrefixes are prefix-style GitHub download mirrors
// (<prefix>https://github.com/...), ordered by reliability. They let nodes that
// cannot reach GitHub directly (e.g. mainland-China-only networks) install and
// upgrade. Keep in sync with GH_MIRRORS in install.sh.
var githubMirrorPrefixes = []string{
	"https://ghfast.top/",
	"https://gh-proxy.com/",
	"https://gcode.hostcentral.cc/",
}

// releaseAssetSourceURLs returns the GitHub release asset URL followed by the
// same asset through every mirror, in the order they should be tried.
func releaseAssetSourceURLs(version, filename string) []string {
	direct := releaseAssetURL(version, filename)
	urls := make([]string, 0, len(githubMirrorPrefixes)+1)
	urls = append(urls, direct)
	for _, prefix := range githubMirrorPrefixes {
		urls = append(urls, prefix+direct)
	}
	return urls
}

func (h *Handler) currentPanelAgentVersion(requested string) (string, error) {
	current := strings.TrimSpace(h.GetFluxVersion())
	if !stableVersionPattern.MatchString(current) {
		return "", fmt.Errorf("当前 Panel 版本 %q 不是可发布的 Agent 版本", current)
	}

	requested = strings.TrimSpace(requested)
	if requested != "" && requested != current {
		return "", fmt.Errorf("节点版本必须与当前 Panel 版本 %s 一致", current)
	}

	return current, nil
}

// buildNodeInstallCommand returns a single copy-pasteable line: fetch install.sh
// from GitHub, falling back through the mirrors (each attempt bounded by a
// connect timeout and a max time), then run it. The frontend appends
// " -n <service>", so the line must end with the install.sh arguments.
func buildNodeInstallCommand(version, panelAddr, secret string) string {
	sources := releaseAssetSourceURLs(version, "install.sh")
	fetches := make([]string, 0, len(sources))
	for _, u := range sources {
		fetches = append(fetches, fmt.Sprintf("curl -fsSL --connect-timeout 5 -m 30 %s -o ./install.sh", u))
	}
	return fmt.Sprintf("{ %s; } && chmod +x ./install.sh && VERSION=%s ./install.sh -a %s -s %s",
		strings.Join(fetches, " || "), version, panelAddr, secret)
}

// agentUpgradeCommandData lists GitHub first, then the mirrors. The agent tries
// downloadUrls in order and pairs checksumUrls[i] with downloadUrls[i].
func agentUpgradeCommandData(version string) map[string]interface{} {
	return map[string]interface{}{
		"downloadUrls": releaseAssetSourceURLs(version, "gost-{ARCH}"),
		"checksumUrls": releaseAssetSourceURLs(version, "gost-{ARCH}.sha256"),
		"version":      version,
	}
}

func (h *Handler) nodeUpgrade(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}

	var req struct {
		ID      int64  `json:"id"`
		Version string `json:"version"`
		Channel string `json:"channel"`
	}
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	if req.ID <= 0 {
		response.WriteJSON(w, response.ErrDefault("节点 ID 无效"))
		return
	}

	version, err := h.currentPanelAgentVersion(req.Version)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}

	result, err := h.wsServer.SendCommand(req.ID, "UpgradeAgent", agentUpgradeCommandData(version), upgradeTimeout)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, fmt.Sprintf("升级失败：%v", err)))
		return
	}

	response.WriteJSON(w, response.OK(map[string]interface{}{
		"version": version,
		"message": result.Message,
	}))
}

func resolveLatestRelease() (string, error) {
	return resolveLatestReleaseByChannel(releaseChannelStable)
}

func resolveLatestReleaseAPI() (string, error) {
	return resolveLatestReleaseByChannel(releaseChannelStable)
}

func (h *Handler) nodeBatchUpgrade(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}

	var req struct {
		IDs     []int64 `json:"ids"`
		Version string  `json:"version"`
		Channel string  `json:"channel"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	if len(req.IDs) == 0 {
		response.WriteJSON(w, response.ErrDefault("ids不能为空"))
		return
	}

	version, err := h.currentPanelAgentVersion(req.Version)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}

	type upgradeResult struct {
		ID      int64  `json:"id"`
		Success bool   `json:"success"`
		Message string `json:"message"`
	}

	results := make([]upgradeResult, len(req.IDs))
	sem := make(chan struct{}, batchWorkers)
	var wg sync.WaitGroup

	for i, id := range req.IDs {
		wg.Add(1)
		go func(index int, nodeID int64) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			result, err := h.wsServer.SendCommand(nodeID, "UpgradeAgent", agentUpgradeCommandData(version), upgradeTimeout)
			if err != nil {
				results[index] = upgradeResult{ID: nodeID, Success: false, Message: err.Error()}
				return
			}
			results[index] = upgradeResult{ID: nodeID, Success: true, Message: result.Message}
		}(i, id)
	}
	wg.Wait()

	response.WriteJSON(w, response.OK(map[string]interface{}{
		"version": version,
		"results": results,
	}))
}

func (h *Handler) listReleases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}

	var req struct {
		Channel string `json:"channel"`
	}
	if err := decodeJSON(r.Body, &req); err != nil && err != io.EOF {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}

	version, err := h.currentPanelAgentVersion("")
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}

	type releaseItem struct {
		Version     string `json:"version"`
		Name        string `json:"name"`
		PublishedAt string `json:"publishedAt"`
		Prerelease  bool   `json:"prerelease"`
		Channel     string `json:"channel"`
	}

	channel := releaseChannelFromTag(version)
	items := []releaseItem{{
		Version:    version,
		Name:       version,
		Prerelease: channel == releaseChannelDev,
		Channel:    channel,
	}}

	response.WriteJSON(w, response.OK(items))
}

func (h *Handler) onNodeOnline(nodeID int64) {
	h.scheduleNodeRedeploy(nodeID)
	if h.quotaGroups != nil {
		h.quotaGroups.NodeOnline(nodeID)
	}
}

func (h *Handler) redeployNodeRuntime(nodeID int64) {
	tunnelIDs, err := h.repo.ListActiveTunnelIDsByNode(nodeID)
	if err != nil {
		fmt.Printf("redeploy: list tunnels for node %d failed: %v\n", nodeID, err)
		return
	}
	forwardIDs, err := h.repo.ListActiveForwardIDsByNode(nodeID)
	if err != nil {
		fmt.Printf("redeploy: list forwards for node %d failed: %v\n", nodeID, err)
		return
	}
	forwardsByTunnel := make(map[int64][]int64)
	seen := make(map[int64]bool)
	for _, tunnelID := range tunnelIDs {
		seen[tunnelID] = true
	}
	for _, forwardID := range forwardIDs {
		forward, getErr := h.getForwardRecord(forwardID)
		if getErr != nil || forward == nil {
			continue
		}
		forwardsByTunnel[forward.TunnelID] = append(forwardsByTunnel[forward.TunnelID], forwardID)
		if !seen[forward.TunnelID] {
			seen[forward.TunnelID] = true
			tunnelIDs = append(tunnelIDs, forward.TunnelID)
		}
	}
	tunnelsOK, tunnelsFailed, forwardsOK, forwardsFailed := 0, 0, 0, 0
	for _, tunnelID := range tunnelIDs {
		func() {
			unlock := h.lockTunnelRuntime(tunnelID)
			defer unlock()
			tunnel, getErr := h.getTunnelRecord(tunnelID)
			if getErr != nil || tunnel == nil || tunnel.Status != 1 {
				return
			}
			var pairErr error
			if tunnel.Type == 2 {
				pairErr = h.redeployTunnelRuntimeOnNodeLocked(tunnelID, nodeID)
				if errors.Is(pairErr, errTunnelRuntimeInactive) {
					return
				}
				if pairErr != nil {
					tunnelsFailed++
					fmt.Printf("redeploy: tunnel %d failed on node %d: %v\n", tunnelID, nodeID, pairErr)
				} else {
					tunnelsOK++
				}
			}
			for _, forwardID := range forwardsByTunnel[tunnelID] {
				forward, getErr := h.getForwardRecord(forwardID)
				if getErr != nil || forward == nil || forward.Status != 1 || forward.TunnelID != tunnelID {
					continue
				}
				if syncErr := h.syncForwardServicesOnNode(forward, nodeID); syncErr != nil {
					pairErr = syncErr
					forwardsFailed++
					fmt.Printf("redeploy: forward %d failed on node %d: %v\n", forwardID, nodeID, syncErr)
				} else {
					forwardsOK++
				}
			}
			h.recordTunnelRuntimeResult(tunnelID, nodeID, pairErr)
		}()
	}
	fmt.Printf("redeploy: node %d done: tunnels ok=%d failed=%d, forwards ok=%d failed=%d\n", nodeID, tunnelsOK, tunnelsFailed, forwardsOK, forwardsFailed)
}

type PanelUpgradeCheckResponse struct {
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	HasUpdate      bool   `json:"hasUpdate"`
}

func (h *Handler) panelUpgradeCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}

	var req struct {
		Channel string `json:"channel"`
	}
	if err := decodeJSON(r.Body, &req); err != nil && err != io.EOF {
		req.Channel = ""
	}

	currentVersion := h.GetFluxVersion()
	latestVersion, err := resolveLatestReleaseByChannel(req.Channel)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, fmt.Sprintf("获取最新版本失败：%v", err)))
		return
	}

	hasUpdate := compareVersions(currentVersion, latestVersion) < 0

	response.WriteJSON(w, response.OK(PanelUpgradeCheckResponse{
		CurrentVersion: currentVersion,
		LatestVersion:  latestVersion,
		HasUpdate:      hasUpdate,
	}))
}

func (h *Handler) panelReleases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}

	var req struct {
		Channel string `json:"channel"`
	}
	if err := decodeJSON(r.Body, &req); err != nil && err != io.EOF {
		req.Channel = ""
	}

	releases, err := fetchGitHubReleases(50)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, fmt.Sprintf("获取版本列表失败：%v", err)))
		return
	}

	type releaseItem struct {
		Version     string `json:"version"`
		Name        string `json:"name"`
		PublishedAt string `json:"publishedAt"`
		Prerelease  bool   `json:"prerelease"`
		Channel     string `json:"channel"`
	}

	items := make([]releaseItem, 0, len(releases))
	for _, r := range releases {
		if r.Draft {
			continue
		}
		tag := strings.TrimSpace(r.TagName)
		if tag == "" {
			continue
		}
		itemChannel := releaseChannelFromTag(tag)
		if req.Channel != "" && itemChannel != req.Channel {
			continue
		}
		items = append(items, releaseItem{
			Version:     tag,
			Name:        r.Name,
			PublishedAt: r.PublishedAt,
			Prerelease:  itemChannel == releaseChannelDev,
			Channel:     itemChannel,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].PublishedAt > items[j].PublishedAt
	})

	response.WriteJSON(w, response.OK(items))
}

// panelUpgrade is the legacy upgrade endpoint. It shares the version-pinned
// release flow of systemUpgrade instead of running the main-branch
// panel_install.sh, which resolves releases from a different repository.
func (h *Handler) panelUpgrade(w http.ResponseWriter, r *http.Request) {
	h.systemUpgrade(w, r)
}

func (h *Handler) broadcastPanelUpgradeProgress(stage string, percent int, message string, hasError bool) {
	if h.wsServer == nil {
		return
	}
	payload := map[string]interface{}{
		"stage":   stage,
		"percent": percent,
		"message": message,
		"error":   hasError,
	}
	data, _ := json.Marshal(payload)
	h.wsServer.BroadcastToAdmins(fmt.Sprintf(`{"type":"panel_upgrade_progress","data":%s}`, string(data)))
}

func (h *Handler) executePanelUpgrade(currentVersion, targetVersion string) error {
	fmt.Printf("开始升级面板：%s -> %s\n", currentVersion, targetVersion)
	h.broadcastPanelUpgradeProgress("starting", 0, "开始升级面板...", false)

	installDir := "/opt/flvx-svc"
	envFile := installDir + "/.env"
	composeFile := installDir + "/docker-compose.yml"
	backupEnvFile := envFile + ".backup"
	backupComposeFile := composeFile + ".backup"

	h.broadcastPanelUpgradeProgress("backing_up", 5, "备份配置文件...", false)
	if err := backupFile(envFile, backupEnvFile); err != nil {
		h.broadcastPanelUpgradeProgress("failed", 0, fmt.Sprintf("备份 .env 失败：%v", err), true)
		return fmt.Errorf("备份 .env 失败：%v", err)
	}
	defer func() {
		_ = os.Remove(backupEnvFile)
	}()

	if err := backupFile(composeFile, backupComposeFile); err != nil {
		h.broadcastPanelUpgradeProgress("failed", 0, fmt.Sprintf("备份 docker-compose.yml 失败：%v", err), true)
		return fmt.Errorf("备份 docker-compose.yml 失败：%v", err)
	}
	defer func() {
		_ = os.Remove(backupComposeFile)
	}()

	latestComposeURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/docker-compose-v4.yml", githubRepo, targetVersion)
	downloadURL := latestComposeURL

	h.broadcastPanelUpgradeProgress("downloading", 10, "下载 docker-compose.yml...", false)
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(downloadURL)
	if err != nil {
		h.broadcastPanelUpgradeProgress("failed", 0, fmt.Sprintf("下载 docker-compose.yml 失败：%v", err), true)
		return fmt.Errorf("下载 docker-compose.yml 失败：%v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		h.broadcastPanelUpgradeProgress("failed", 0, fmt.Sprintf("下载 docker-compose.yml 失败：HTTP %d", resp.StatusCode), true)
		return fmt.Errorf("下载 docker-compose.yml 失败：HTTP %d", resp.StatusCode)
	}

	composeContent, err := io.ReadAll(resp.Body)
	if err != nil {
		h.broadcastPanelUpgradeProgress("failed", 0, fmt.Sprintf("读取 docker-compose.yml 失败：%v", err), true)
		return fmt.Errorf("读取 docker-compose.yml 失败：%v", err)
	}

	if err := os.WriteFile(composeFile, composeContent, 0644); err != nil {
		h.broadcastPanelUpgradeProgress("failed", 0, fmt.Sprintf("写入 docker-compose.yml 失败：%v", err), true)
		return fmt.Errorf("写入 docker-compose.yml 失败：%v", err)
	}

	h.broadcastPanelUpgradeProgress("updating", 20, "更新版本配置...", false)
	if err := updateEnvVersion(envFile, targetVersion); err != nil {
		h.broadcastPanelUpgradeProgress("failed", 0, fmt.Sprintf("更新 .env 中的版本号失败：%v", err), true)
		return fmt.Errorf("更新 .env 中的版本号失败：%v", err)
	}

	fmt.Println("拉取最新镜像...")
	h.broadcastPanelUpgradeProgress("pulling", 30, "拉取镜像...", false)
	if err := runDockerComposePull(installDir); err != nil {
		h.broadcastPanelUpgradeProgress("failed", 0, fmt.Sprintf("拉取镜像失败：%v", err), true)
		return fmt.Errorf("拉取镜像失败：%v", err)
	}

	fmt.Println("启动服务（自动重建容器）...")
	h.broadcastPanelUpgradeProgress("starting_containers", 80, "启动新服务...", false)
	if err := runDockerComposeUp(installDir); err != nil {
		h.broadcastPanelUpgradeProgress("failed", 0, fmt.Sprintf("启动服务失败：%v", err), true)
		return fmt.Errorf("启动服务失败：%v", err)
	}

	fmt.Println("等待服务健康检查...")
	h.broadcastPanelUpgradeProgress("health_check", 90, "等待服务就绪...", false)
	if err := waitForBackendHealthy(); err != nil {
		h.broadcastPanelUpgradeProgress("failed", 0, fmt.Sprintf("服务健康检查失败：%v", err), true)
		return fmt.Errorf("服务健康检查失败：%v", err)
	}

	fmt.Printf("面板升级成功：%s -> %s\n", currentVersion, targetVersion)
	h.broadcastPanelUpgradeProgress("completed", 100, "升级完成", false)
	return nil
}

func (h *Handler) rollbackPanelUpgrade(version string) {
	fmt.Printf("回滚面板到版本：%s\n", version)
	installDir := "/opt/flvx-svc"
	envFile := installDir + "/.env"

	if err := updateEnvVersion(envFile, version); err != nil {
		fmt.Printf("回滚 .env 失败：%v\n", err)
		return
	}

	if err := runDockerComposeUp(installDir); err != nil {
		fmt.Printf("回滚启动服务失败：%v\n", err)
	}
}

func backupFile(src, dst string) error {
	content, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, content, 0644)
}

func updateEnvVersion(envFile, version string) error {
	content, err := os.ReadFile(envFile)
	if err != nil {
		return err
	}

	lines := strings.Split(string(content), "\n")
	updated := false
	for i, line := range lines {
		if strings.HasPrefix(line, "FLUX_VERSION=") {
			lines[i] = "FLUX_VERSION=" + version
			updated = true
			break
		}
	}

	if !updated {
		lines = append(lines, "FLUX_VERSION="+version)
	}

	return os.WriteFile(envFile, []byte(strings.Join(lines, "\n")), 0644)
}

func runDockerComposePull(workDir string) error {
	cmd := exec.Command("docker", "compose", "-f", workDir+"/docker-compose.yml", "pull")
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runDockerComposeDown(workDir string) error {
	cmd := exec.Command("docker", "compose", "-f", workDir+"/docker-compose.yml", "down")
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runDockerComposeUp(workDir string) error {
	cmd := exec.Command("docker", "compose", "-f", workDir+"/docker-compose.yml", "up", "-d")
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func waitForBackendHealthy() error {
	client := &http.Client{Timeout: 5 * time.Second}
	for i := 0; i < 60; i++ {
		time.Sleep(5 * time.Second)
		resp, err := client.Get("http://localhost:6365/flow/test")
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return nil
		}
		if err == nil {
			resp.Body.Close()
		}
	}
	return fmt.Errorf("等待后端服务健康检查超时")
}

func compareVersions(current, target string) int {
	current = strings.TrimPrefix(current, "v")
	target = strings.TrimPrefix(target, "v")

	currentBase, currentFork, currentRevision := splitForkVersion(current)
	targetBase, targetFork, targetRevision := splitForkVersion(target)
	if currentFork || targetFork {
		if compareNumericVersions(currentBase, targetBase) != 0 {
			// Fork maintenance releases and official releases from different
			// baselines are separate upgrade lines.
			return 0
		}
		if currentRevision < targetRevision {
			return -1
		}
		if currentRevision > targetRevision {
			return 1
		}
		return 0
	}

	return compareNumericVersions(current, target)
}

func splitForkVersion(version string) (base string, fork bool, revision int) {
	match := forkVersionPattern.FindStringSubmatch(version)
	if match == nil {
		return version, false, 0
	}
	fmt.Sscanf(match[2], "%d", &revision)
	return match[1], true, revision
}

func compareNumericVersions(current, target string) int {

	currentParts := strings.Split(current, ".")
	targetParts := strings.Split(target, ".")

	maxLen := len(currentParts)
	if len(targetParts) > maxLen {
		maxLen = len(targetParts)
	}

	for i := 0; i < maxLen; i++ {
		var currNum, targetNum int
		if i < len(currentParts) {
			fmt.Sscanf(currentParts[i], "%d", &currNum)
		}
		if i < len(targetParts) {
			fmt.Sscanf(targetParts[i], "%d", &targetNum)
		}
		if currNum < targetNum {
			return -1
		}
		if currNum > targetNum {
			return 1
		}
	}
	return 0
}
