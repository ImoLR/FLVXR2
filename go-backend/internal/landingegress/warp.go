package landingegress

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// WarpAPIBase is the Cloudflare client API; tests point it at a mock server.
var WarpAPIBase = "https://api.cloudflareclient.com/v0a5641"

const (
	warpUserAgent     = "1.1.1.1/6.38.9-5641 (Android 16.0.0)"
	warpClientVersion = "a-6.38.9-5641"
)

// WarpAccount is one registered WARP device (one per landing server and template).
type WarpAccount struct {
	DeviceID      string
	Token         string
	PrivateKey    string
	PublicKey     string
	PeerPublicKey string
	AddressV4     string
	AddressV6     string
	EndpointV4    string
	EndpointV6    string
	Reserved      []int
	AccountType   string
}

type warpEndpoint struct {
	V4   string `json:"v4"`
	V6   string `json:"v6"`
	Host string `json:"host"`
}

type warpReg struct {
	ID      string `json:"id"`
	Token   string `json:"token"`
	Account struct {
		AccountType string `json:"account_type"`
		WarpPlus    bool   `json:"warp_plus"`
	} `json:"account"`
	Config struct {
		ClientID string `json:"client_id"`
		Peers    []struct {
			PublicKey string       `json:"public_key"`
			Endpoint  warpEndpoint `json:"endpoint"`
		} `json:"peers"`
		Interface struct {
			Addresses struct {
				V4 string `json:"v4"`
				V6 string `json:"v6"`
			} `json:"addresses"`
		} `json:"interface"`
	} `json:"config"`
}

func warpRequest(ctx context.Context, client *http.Client, method, path, token string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(WarpAPIBase, "/")+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", warpUserAgent)
	req.Header.Set("CF-Client-Version", warpClientVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("无法连接 Cloudflare WARP 接口")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("Cloudflare WARP 接口返回 HTTP %d", resp.StatusCode)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return errors.New("Cloudflare WARP 接口响应无法解析")
		}
	}
	return nil
}

func endpointWithPort(addr string) string {
	if addr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = strings.Trim(addr, "[]")
	}
	return net.JoinHostPort(host, "2408")
}

func (r warpReg) account(private, public string) (WarpAccount, error) {
	a := WarpAccount{DeviceID: r.ID, Token: r.Token, PrivateKey: private, PublicKey: public, AccountType: r.Account.AccountType, AddressV4: r.Config.Interface.Addresses.V4, AddressV6: r.Config.Interface.Addresses.V6}
	if len(r.Config.Peers) == 0 || r.ID == "" || r.Token == "" {
		return a, errors.New("Cloudflare WARP 接口响应缺少设备或对端信息")
	}
	a.PeerPublicKey = r.Config.Peers[0].PublicKey
	a.EndpointV4 = endpointWithPort(r.Config.Peers[0].Endpoint.V4)
	a.EndpointV6 = endpointWithPort(r.Config.Peers[0].Endpoint.V6)
	if b, err := base64.StdEncoding.DecodeString(r.Config.ClientID); err == nil && len(b) == 3 {
		a.Reserved = []int{int(b[0]), int(b[1]), int(b[2])}
	}
	if !decodeKey(a.PeerPublicKey, false, 32) || (a.AddressV4 == "" && a.AddressV6 == "") {
		return a, errors.New("Cloudflare WARP 接口响应的密钥或地址无效")
	}
	return a, nil
}

// RegisterWarp registers a free device from the panel host and, when a WARP+
// license is given, binds it. A license failure keeps the free account and is
// reported as the second return value.
func RegisterWarp(ctx context.Context, client *http.Client, license string) (WarpAccount, error, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return WarpAccount{}, err, nil
	}
	private := base64.StdEncoding.EncodeToString(key.Bytes())
	public := base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())
	body := map[string]any{"key": public, "install_id": "", "fcm_token": "", "tos": time.Now().UTC().Format(time.RFC3339Nano), "model": "PC", "serial_number": "", "locale": "en_US", "os_version": "16.0.0", "key_type": "curve25519", "tunnel_type": "wireguard"}
	var reg warpReg
	if err := warpRequest(ctx, client, http.MethodPost, "/reg", "", body, &reg); err != nil {
		return WarpAccount{}, fmt.Errorf("WARP 自动注册失败：%w", err), nil
	}
	account, err := reg.account(private, public)
	if err != nil {
		return account, err, nil
	}
	var licenseErr error
	if license != "" {
		if err := warpRequest(ctx, client, http.MethodPut, "/reg/"+account.DeviceID+"/account", account.Token, map[string]string{"license": license}, nil); err != nil {
			licenseErr = fmt.Errorf("WARP+ 许可证绑定失败：%w", err)
		} else {
			var refreshed warpReg
			if err := warpRequest(ctx, client, http.MethodGet, "/reg/"+account.DeviceID, account.Token, nil, &refreshed); err == nil && refreshed.Account.AccountType != "" {
				account.AccountType = refreshed.Account.AccountType
			}
		}
	}
	return account, nil, licenseErr
}

// DeleteWarp removes a device on re-registration so accounts do not pile up.
func DeleteWarp(ctx context.Context, client *http.Client, deviceID, token string) error {
	if deviceID == "" || token == "" {
		return nil
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return warpRequest(ctx, client, http.MethodDelete, "/reg/"+deviceID, token, nil, nil)
}
