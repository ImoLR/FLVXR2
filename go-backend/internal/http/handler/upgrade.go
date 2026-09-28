package handler

import (
	"encoding/json"
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
	stableVersionPattern  = regexp.MustCompile(`^\d+(?:\.\d+)+(?:-fork\.\d+)?$`)
	forkVersionPattern    = regexp.MustCompile(`^(\d+(?:\.\d+)+)-fork\.(\d+)$`)
	previewVersionPattern = regexp.MustCompile(`^(\d+(?:\.\d+)+)-(alpha|beta|rc)[.-]?(\d*)$`)
	testKeywordPattern    = regexp.MustCompile(`(?i)(alpha|beta|rc)`)
)

type githubRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	PublishedAt string `json:"published_at"`
	Prerelease  bool   `json:"prerelease"`
	Draft       bool   `json:"draft"`
}

func normalizeReleaseChannel(channel string) string {
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

func releaseAssetURL(version, filename string) string {
	return fmt.Sprintf("%s/%s/releases/download/%s/%s", githubHTMLBase, githubRepo, version, filename)
}

func latestReleaseAssetURL(filename string) string {
	return fmt.Sprintf("%s/%s/releases/latest/download/%s", githubHTMLBase, githubRepo, filename)
}

func agentUpgradeCommandData(version string) map[string]interface{} {
	return map[string]interface{}{
		"downloadUrls": []string{releaseAssetURL(version, "gost-{ARCH}")},
		"checksumUrls": []string{releaseAssetURL(version, "gost-{ARCH}.sha256")},
		"version":      version,
	}
}

func resolveLatestReleaseByChannel(channel string) (string, error) {
	normalizedChannel := normalizeReleaseChannel(channel)
	releases, err := fetchGitHubReleases(50)
	if err != nil {
		return "", err
	}

	// 按通道查找；空通道由 normalizeReleaseChannel 归一为 stable。
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

	channel := normalizeReleaseChannel(req.Channel)
	version := strings.TrimSpace(req.Version)
	if version == "" {
		var err error
		version, err = resolveLatestReleaseByChannel(channel)
		if err != nil {
			response.WriteJSON(w, response.Err(-2, fmt.Sprintf("获取最新%s失败：%v", releaseChannelLabel(channel), err)))
			return
		}
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

	channel := normalizeReleaseChannel(req.Channel)
	version := strings.TrimSpace(req.Version)
	if version == "" {
		var err error
		version, err = resolveLatestReleaseByChannel(channel)
		if err != nil {
			response.WriteJSON(w, response.Err(-2, fmt.Sprintf("获取最新%s失败：%v", releaseChannelLabel(channel), err)))
			return
		}
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

	channel := normalizeReleaseChannel(req.Channel)

	releases, err := fetchGitHubReleases(50)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, fmt.Sprintf("获取版本列表失败: %v", err)))
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
		if itemChannel != channel {
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

func (h *Handler) onNodeOnline(nodeID int64) {
	// 节点重新上线时自动下发隧道和转发规则
	// 适用于：续费后上线、网络恢复、节点重启、升级后重连
	h.redeployNodeRuntime(nodeID)
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

	tunnelFailed := make(map[int64]struct{})
	for _, tunnelID := range tunnelIDs {
		if err := h.redeployTunnelAndForwards(tunnelID); err != nil {
			tunnelFailed[tunnelID] = struct{}{}
			fmt.Printf("redeploy: tunnel %d failed on node %d: %v\n", tunnelID, nodeID, err)
		}
	}

	for _, forwardID := range forwardIDs {
		forward, getErr := h.getForwardRecord(forwardID)
		if getErr != nil || forward == nil {
			continue
		}
		if _, skipped := tunnelFailed[forward.TunnelID]; skipped {
			continue
		}
		if err := h.syncForwardServices(forward, "UpdateService", true); err != nil {
			fmt.Printf("redeploy: forward %d failed on node %d: %v\n", forwardID, nodeID, err)
		}
	}
}

type PanelUpgradeRequest struct {
	Version string `json:"version"`
	Channel string `json:"channel"`
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

func (h *Handler) panelUpgrade(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}

	var req PanelUpgradeRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}

	channel := normalizeReleaseChannel(req.Channel)
	targetVersion := strings.TrimSpace(req.Version)
	if targetVersion == "" {
		var err error
		targetVersion, err = resolveLatestReleaseByChannel(channel)
		if err != nil {
			response.WriteJSON(w, response.Err(-2, fmt.Sprintf("获取最新版本失败：%v", err)))
			return
		}
	}

	currentVersion := h.GetFluxVersion()

	// 使用 panel_install.sh 脚本升级（更可靠）
	go func() {
		fmt.Printf("开始升级面板：%s -> %s\n", currentVersion, targetVersion)

		// 下载并执行 panel_install.sh
		cmd := exec.Command("bash", "-c", `
			curl -fL "$1" -o /tmp/panel_install.sh && \
			chmod +x /tmp/panel_install.sh && \
			printf '2\n' | /tmp/panel_install.sh "$2"
		`, "flvx-panel-upgrade", releaseAssetURL(targetVersion, "panel_install.sh"), targetVersion)
		output, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Printf("面板升级失败：%v\n输出：%s\n", err, string(output))
			return
		}
		fmt.Printf("面板升级完成：%s\n", string(output))
	}()

	response.WriteJSON(w, response.OK(map[string]string{
		"message": "升级任务已提交，面板正在后台升级，完成后将自动重启",
	}))
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

	latestComposeURL := releaseAssetURL(targetVersion, "docker-compose-v4.yml")
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

type parsedVersion struct {
	core         []int
	stageRank    int
	stageNumber  int
	fork         bool
	forkRevision int
	valid        bool
}

func parseNumericVersion(value string) ([]int, bool) {
	parts := strings.Split(value, ".")
	numbers := make([]int, len(parts))
	for i, part := range parts {
		if part == "" {
			return nil, false
		}
		if _, err := fmt.Sscanf(part, "%d", &numbers[i]); err != nil {
			return nil, false
		}
		if fmt.Sprintf("%d", numbers[i]) != part {
			return nil, false
		}
	}
	return numbers, true
}

func parseComparableVersion(value string) parsedVersion {
	normalized := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "v")))
	if match := forkVersionPattern.FindStringSubmatch(normalized); match != nil {
		core, ok := parseNumericVersion(match[1])
		var revision int
		_, revisionErr := fmt.Sscanf(match[2], "%d", &revision)
		return parsedVersion{core: core, stageRank: 4, fork: true, forkRevision: revision, valid: ok && revisionErr == nil}
	}
	if stableVersionPattern.MatchString(normalized) {
		core, ok := parseNumericVersion(normalized)
		return parsedVersion{core: core, stageRank: 4, valid: ok}
	}
	if match := previewVersionPattern.FindStringSubmatch(normalized); match != nil {
		core, ok := parseNumericVersion(match[1])
		stageRank := map[string]int{"alpha": 1, "beta": 2, "rc": 3}[match[2]]
		var stageNumber int
		if match[3] != "" {
			if _, err := fmt.Sscanf(match[3], "%d", &stageNumber); err != nil {
				ok = false
			}
		}
		return parsedVersion{core: core, stageRank: stageRank, stageNumber: stageNumber, valid: ok}
	}
	return parsedVersion{}
}

func compareNumericParts(left, right []int) int {
	maxLen := len(left)
	if len(right) > maxLen {
		maxLen = len(right)
	}
	for i := 0; i < maxLen; i++ {
		var a, b int
		if i < len(left) {
			a = left[i]
		}
		if i < len(right) {
			b = right[i]
		}
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
	}
	return 0
}

func compareVersions(current, target string) int {
	left := parseComparableVersion(current)
	right := parseComparableVersion(target)
	if !left.valid || !right.valid {
		return 0
	}

	coreOrder := compareNumericParts(left.core, right.core)
	if left.fork != right.fork && coreOrder != 0 {
		// A fork maintenance release and an official release from a different
		// base are separate lines, so neither is an automatic upgrade target.
		return 0
	}
	if coreOrder != 0 {
		return coreOrder
	}
	if left.fork && right.fork {
		if left.forkRevision < right.forkRevision {
			return -1
		}
		if left.forkRevision > right.forkRevision {
			return 1
		}
		return 0
	}
	if left.fork != right.fork {
		if left.fork {
			return 1
		}
		return -1
	}
	if left.stageRank < right.stageRank {
		return -1
	}
	if left.stageRank > right.stageRank {
		return 1
	}
	if left.stageNumber < right.stageNumber {
		return -1
	}
	if left.stageNumber > right.stageNumber {
		return 1
	}
	return 0
}
