package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-gost/core/observer/stats"
	"github.com/go-gost/x/config"
	"github.com/go-gost/x/internal/util/crypto"
	"github.com/go-gost/x/landing"
	"github.com/go-gost/x/logsafe"
	"github.com/go-gost/x/panelconn"
	"github.com/go-gost/x/registry"
)

var reportLogs logsafe.RoutineLogger

var httpReportURL string
var configReportURL string
var httpAESCrypto *crypto.AESCrypto // 新增：HTTP上报加密器
var reportURLPreferenceMutex sync.RWMutex
var preferredUploadURL string
var preferredConfigURL string

// A fallback URL (http after an https failure) is only preferred for
// reportPreferenceTTL; then https is tried first again (plan 122: no scheme stays
// sticky after a transient failure). A port that does not speak TLS at all
// (deterministic) keeps http first for good, as before.
var reportPreferenceTTL = 5 * time.Minute
var reportNow = time.Now

type preferredState struct {
	at     time.Time
	sticky bool
}

var preferredMeta = map[*string]preferredState{} // guarded by reportURLPreferenceMutex
var reportDo = func(ctx context.Context, req *http.Request, timeout time.Duration) (*http.Response, error) {
	client := &http.Client{
		Timeout:   timeout,
		Transport: panelconn.Default,
	}
	return client.Do(req.WithContext(ctx))
}

// TrafficReportItem 流量报告项（压缩格式）
type TrafficReportItem struct {
	N string `json:"n"` // 服务名（name缩写）
	U int64  `json:"u"` // 上行流量（up缩写）
	D int64  `json:"d"` // 下行流量（down缩写）
}

func SetHTTPReportURL(addr string, secret string) {
	uploadURLs, configURLs := buildReportURLCandidates(addr, secret)
	if len(uploadURLs) > 0 {
		httpReportURL = strings.Join(uploadURLs, ",")
	}
	if len(configURLs) > 0 {
		configReportURL = strings.Join(configURLs, ",")
	}
	reportURLPreferenceMutex.Lock()
	preferredUploadURL = ""
	preferredConfigURL = ""
	preferredMeta = map[*string]preferredState{}
	reportURLPreferenceMutex.Unlock()

	// 创建 AES 加密器
	var err error
	httpAESCrypto, err = crypto.NewAESCrypto(secret)
	if err != nil {
		fmt.Printf("❌ 创建 HTTP AES 加密器失败: %v\n", err)
		httpAESCrypto = nil
	} else {
		fmt.Printf("🔐 HTTP AES 加密器创建成功\n")
	}
	// Landing-only nodes also need the periodic uploader before any forward exists.
	GetGlobalTrafficManager()
}

func buildReportURLCandidates(addr string, secret string) (upload []string, config []string) {
	normalizedAddr, explicitScheme := normalizeReportAddress(addr)
	if normalizedAddr == "" {
		normalizedAddr = strings.TrimSpace(addr)
	}

	schemes := []string{"https", "http"}
	if mappedScheme := mapToHTTPScheme(explicitScheme); mappedScheme == "http" {
		schemes = []string{"http", "https"}
	}

	upload = []string{
		schemes[0] + "://" + normalizedAddr + "/flow/upload?secret=" + secret,
		schemes[1] + "://" + normalizedAddr + "/flow/upload?secret=" + secret,
	}
	config = []string{
		schemes[0] + "://" + normalizedAddr + "/flow/config?secret=" + secret,
		schemes[1] + "://" + normalizedAddr + "/flow/config?secret=" + secret,
	}
	return upload, config
}

func normalizeReportAddress(addr string) (string, string) {
	raw := strings.TrimSpace(addr)
	if raw == "" {
		return "", ""
	}

	scheme := ""
	if idx := strings.Index(raw, "://"); idx > 0 {
		scheme = strings.ToLower(strings.TrimSpace(raw[:idx]))
		if parsed, err := url.Parse(raw); err == nil {
			if host := strings.TrimSpace(parsed.Host); host != "" {
				return host, scheme
			}
		}
		raw = raw[idx+3:]
	}

	if idx := strings.IndexAny(raw, "/?#"); idx >= 0 {
		raw = raw[:idx]
	}
	return strings.TrimSpace(raw), scheme
}

func mapToHTTPScheme(scheme string) string {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "https", "wss":
		return "https"
	case "http", "ws":
		return "http"
	default:
		return ""
	}
}

func loadPreferredURL(preferred *string, urls []string) string {
	if preferred == nil {
		return ""
	}

	reportURLPreferenceMutex.RLock()
	defer reportURLPreferenceMutex.RUnlock()
	value := *preferred
	if value == "" || len(urls) == 0 || value == urls[0] {
		return value
	}
	st, ok := preferredMeta[preferred]
	if !ok || st.sticky || reportNow().Sub(st.at) < reportPreferenceTTL {
		return value
	}
	return ""
}

func storePreferredURL(preferred *string, value string, sticky bool) {
	if preferred == nil {
		return
	}

	reportURLPreferenceMutex.Lock()
	defer reportURLPreferenceMutex.Unlock()
	*preferred = value
	preferredMeta[preferred] = preferredState{at: reportNow(), sticky: sticky}
}

func prioritizeURLs(urls []string, preferred string) []string {
	ordered := append([]string(nil), urls...)
	if preferred == "" || len(ordered) < 2 {
		return ordered
	}

	for i, targetURL := range ordered {
		if targetURL == preferred {
			if i > 0 {
				ordered[0], ordered[i] = ordered[i], ordered[0]
			}
			break
		}
	}

	return ordered
}

func postJSONWithFallback(ctx context.Context, urls []string, requestBody []byte, userAgent string, timeout time.Duration, preferred *string) (bool, error) {
	if len(urls) == 0 {
		return false, fmt.Errorf("上报URL未设置")
	}

	loaded := loadPreferredURL(preferred, urls)
	orderedURLs := prioritizeURLs(urls, loaded)

	var errs []string
	noTLS := false // an earlier URL of this call hit a port that does not speak TLS
	for _, targetURL := range orderedURLs {
		safeURL := logsafe.URL(targetURL)
		req, err := http.NewRequest("POST", targetURL, bytes.NewBuffer(requestBody))
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s => 创建请求失败: %v", safeURL, logsafe.Error(err)))
			continue
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", userAgent)

		resp, err := reportDo(ctx, req, timeout)
		if err != nil {
			var rhe tls.RecordHeaderError
			if errors.As(err, &rhe) || errors.Is(err, http.ErrSchemeMismatch) {
				noTLS = true
			}
			errs = append(errs, fmt.Sprintf("%s => 请求失败: %v", safeURL, logsafe.Error(err)))
			continue
		}

		var responseBytes bytes.Buffer
		_, readErr := responseBytes.ReadFrom(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			errs = append(errs, fmt.Sprintf("%s => 读取响应失败: %v", safeURL, logsafe.Error(readErr)))
			continue
		}

		if resp.StatusCode != http.StatusOK {
			errs = append(errs, fmt.Sprintf("%s => HTTP响应错误: %d %s", safeURL, resp.StatusCode, http.StatusText(resp.StatusCode)))
			continue
		}

		responseText := strings.TrimSpace(responseBytes.String())
		if responseText == "ok" {
			// Only a change (or a fallback after a failure in this call) restarts
			// the TTL; using the remembered URL does not extend it.
			if len(errs) > 0 || loaded != targetURL {
				storePreferredURL(preferred, targetURL, noTLS)
			}
			return true, nil
		}

		errs = append(errs, fmt.Sprintf("%s => 服务器响应已隐藏 (期望: ok)", safeURL))
	}

	return false, fmt.Errorf("发送HTTP请求失败: %s", strings.Join(errs, " | "))
}

// sendBatchTrafficReport 批量发送多个服务的流量报告到HTTP接口
func sendBatchTrafficReport(ctx context.Context, reportItems []TrafficReportItem) (bool, error) {
	requestBody, err := buildTrafficReportBody(reportItems)
	if err != nil {
		return false, err
	}
	return postTrafficReportBody(ctx, requestBody)
}

// buildTrafficReportBody serializes (and, with a node secret, encrypts) a traffic report.
// An encrypted body embeds a random nonce, so resending the same bytes lets the panel
// recognize an upload it already counted.
func buildTrafficReportBody(reportItems []TrafficReportItem) ([]byte, error) {
	return buildTrafficReportBodyWithLanding(reportItems, landing.Default().Counters())
}

func buildTrafficReportBodyWithLanding(reportItems []TrafficReportItem, counters []landing.Counter) ([]byte, error) {
	var payload any = reportItems
	if len(counters) > 0 {
		payload = struct {
			Flows           []TrafficReportItem `json:"flows"`
			LandingCounters []landing.Counter   `json:"landing_counters"`
		}{reportItems, counters}
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化报告数据失败: %v", err)
	}

	// 如果有加密器，则加密数据
	if httpAESCrypto == nil {
		return jsonData, nil
	}
	encryptedData, err := httpAESCrypto.Encrypt(jsonData)
	if err != nil {
		reportLogs.Record("TrafficReportEncryption", logsafe.URL(strings.Split(httpReportURL, ",")[0]), "加密失败，发送原始数据: "+logsafe.Error(err))
		return jsonData, nil
	}
	// 创建加密消息包装器
	encryptedMessage := map[string]interface{}{
		"encrypted": true,
		"data":      encryptedData,
		"timestamp": time.Now().Unix(),
	}
	requestBody, err := json.Marshal(encryptedMessage)
	if err != nil {
		reportLogs.Record("TrafficReportEncryption", logsafe.URL(strings.Split(httpReportURL, ",")[0]), "序列化失败，发送原始数据: "+logsafe.Error(err))
		return jsonData, nil
	}
	reportLogs.Record("TrafficReportEncryption", logsafe.URL(strings.Split(httpReportURL, ",")[0]), "")
	return requestBody, nil
}

// postTrafficReportBody posts a prepared traffic report body to the panel.
func postTrafficReportBody(ctx context.Context, requestBody []byte) (bool, error) {
	if httpReportURL == "" {
		return false, fmt.Errorf("流量上报URL未设置")
	}
	return postJSONWithFallback(
		ctx,
		strings.Split(httpReportURL, ","),
		requestBody,
		"GOST-Traffic-Reporter/1.0",
		5*time.Second,
		&preferredUploadURL,
	)
}

// sendConfigReport 发送配置报告到HTTP接口
func sendConfigReport(ctx context.Context) (bool, error) {
	if configReportURL == "" {
		return false, fmt.Errorf("配置上报URL未设置")
	}

	// 获取配置数据
	configData, err := getConfigData()
	if err != nil {
		return false, fmt.Errorf("获取配置数据失败: %v", err)
	}

	var requestBody []byte

	// 如果有加密器，则加密数据
	if httpAESCrypto != nil {
		var encryptedData string
		encryptedData, err = httpAESCrypto.Encrypt(configData)
		if err != nil {
			reportLogs.Record("ConfigReportEncryption", logsafe.URL(strings.Split(configReportURL, ",")[0]), "加密失败，发送原始数据: "+logsafe.Error(err))
			requestBody = configData
		} else {
			// 创建加密消息包装器
			encryptedMessage := map[string]interface{}{
				"encrypted": true,
				"data":      encryptedData,
				"timestamp": time.Now().Unix(),
			}
			requestBody, err = json.Marshal(encryptedMessage)
			if err != nil {
				reportLogs.Record("ConfigReportEncryption", logsafe.URL(strings.Split(configReportURL, ",")[0]), "序列化失败，发送原始数据: "+logsafe.Error(err))
				requestBody = configData
			}
		}
	} else {
		requestBody = configData
	}

	if err == nil {
		reportLogs.Record("ConfigReportEncryption", logsafe.URL(strings.Split(configReportURL, ",")[0]), "")
	}
	return postJSONWithFallback(
		ctx,
		strings.Split(configReportURL, ","),
		requestBody,
		"Config-Reporter/1.0",
		10*time.Second,
		&preferredConfigURL,
	)
}

func reportConfigResult(ctx context.Context) {
	success, err := sendConfigReport(ctx)
	if err == nil && !success {
		err = fmt.Errorf("配置上报未成功")
	}
	reportLogs.Record("ConfigReport", logsafe.URL(strings.Split(configReportURL, ",")[0]), logsafe.Error(err))
}

// StartConfigReporter 启动配置定时上报器（每10分钟上报一次）
func StartConfigReporter(ctx context.Context) {
	if configReportURL == "" {
		fmt.Printf("⚠️ 配置上报URL未设置，跳过定时上报\n")
		return
	}

	fmt.Printf("🚀 配置定时上报器已启动，每10分钟上报一次（WebSocket连接稳定后启动）\n")

	// 创建10分钟定时器
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	// 立即执行一次配置上报
	go func() {
		reportConfigResult(ctx)
	}()

	// 定时上报循环
	for {
		select {
		case <-ticker.C:
			go func() {
				reportConfigResult(ctx)
			}()

		case <-ctx.Done():
			fmt.Printf("⏹️ 配置定时上报器已停止\n")
			return
		}
	}
}

// serviceStatus 接口定义
type serviceStatus interface {
	Status() *Status
}

// getConfigResponse 配置响应结构
type getConfigResponse struct {
	Config *config.Config `json:"config"`
}

// getConfigData 获取配置数据（避免循环依赖）
func getConfigData() ([]byte, error) {
	config.OnUpdate(func(c *config.Config) error {
		for _, svc := range c.Services {
			if svc == nil {
				continue
			}
			s := registry.ServiceRegistry().Get(svc.Name)
			ss, ok := s.(serviceStatus)
			if ok && ss != nil {
				status := ss.Status()
				svc.Status = &config.ServiceStatus{
					CreateTime: status.CreateTime().Unix(),
					State:      string(status.State()),
				}
				if st := status.Stats(); st != nil {
					svc.Status.Stats = &config.ServiceStats{
						TotalConns:   st.Get(stats.KindTotalConns),
						CurrentConns: st.Get(stats.KindCurrentConns),
						TotalErrs:    st.Get(stats.KindTotalErrs),
						InputBytes:   st.Get(stats.KindInputBytes),
						OutputBytes:  st.Get(stats.KindOutputBytes),
					}
				}
				for _, ev := range status.Events() {
					if !ev.Time.IsZero() {
						svc.Status.Events = append(svc.Status.Events, config.ServiceEvent{
							Time: ev.Time.Unix(),
							Msg:  ev.Message,
						})
					}
				}
			}
		}
		return nil
	})

	var resp getConfigResponse
	resp.Config = config.Global()

	buf := &bytes.Buffer{}
	resp.Config.Write(buf, "json")
	return buf.Bytes(), nil
}
