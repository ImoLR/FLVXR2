package socket

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/go-gost/x/logsafe"
)

const upgradeSourceStallTimeout = 30 * time.Second
const upgradeSourceTimeout = 5 * time.Minute

// Both timers cover connection setup and the body. A source that trickles bytes
// can reset the stall timer, but cannot extend the overall deadline.
func downloadWithLimits(url string, out io.Writer, stall, total time.Duration, progress func(int64, int64)) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), total)
	defer cancel()
	timer := time.AfterFunc(stall, cancel)
	defer timer.Stop()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "flvxt2-agent")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}
	var downloaded int64
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			timer.Reset(stall)
			written, err := out.Write(buf[:n])
			downloaded += int64(written)
			if err != nil {
				return downloaded, err
			}
			if written != n {
				return downloaded, io.ErrShortWrite
			}
			if progress != nil {
				progress(downloaded, resp.ContentLength)
			}
		}
		if readErr == io.EOF {
			return downloaded, nil
		}
		if readErr != nil {
			return downloaded, readErr
		}
	}
}

// Recreate the temporary file and hash for each attempt: a partial body from a
// failed source must never be prepended to the next mirror's binary.
func downloadUpgradeSources(urls []string, path string, stall, total time.Duration, progress func(string, int, string)) (int, int64, []byte, error) {
	var lastErr error
	for i, rawURL := range urls {
		url := strings.ReplaceAll(rawURL, "{ARCH}", runtime.GOARCH)
		source := fmt.Sprintf("源 %d/%d (%s)", i+1, len(urls), logsafe.URL(url))
		progress("downloading", 0, "尝试下载"+source)
		out, err := os.Create(path)
		if err != nil {
			return 0, 0, nil, err
		}
		hasher := sha256.New()
		lastPercent := 0
		n, err := downloadWithLimits(url, io.MultiWriter(out, hasher), stall, total, func(n, size int64) {
			if size > 0 {
				percent := int(n * 100 / size)
				if percent-lastPercent >= 10 {
					lastPercent = percent
					progress("downloading", percent, fmt.Sprintf("%s 下载中... %d%%", source, percent))
				}
			}
		})
		closeErr := out.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil && n == 0 {
			err = fmt.Errorf("下载的升级包为空")
		}
		if err == nil {
			return i, n, hasher.Sum(nil), nil
		}
		lastErr = err
		_ = os.Remove(path)
		progress("downloading", 0, fmt.Sprintf("%s 失败，尝试下一源: %s", source, logsafe.Error(err)))
	}
	return 0, 0, nil, fmt.Errorf("所有下载源都不可用: %s", logsafe.Error(lastErr))
}
