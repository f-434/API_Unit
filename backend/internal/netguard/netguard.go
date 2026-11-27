// Package netguard 校验出站请求地址，避免把 API Key 通过明文 HTTP 发到公网，
// 也阻止图片下载被上游响应诱导去访问云元数据等链路本地地址。
package netguard

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
)

var allowInsecure atomic.Bool

// Configure 设置是否允许明文 HTTP 访问公网地址，进程启动时调用一次。
func Configure(allow bool) { allowInsecure.Store(allow) }

// CheckURL 在发起携带凭据的请求前调用。https 始终放行；
// http 仅放行本机与内网地址，公网地址需要显式配置 ALLOW_INSECURE_UPSTREAM=true。
func CheckURL(raw string) error {
	u, err := parse(raw)
	if err != nil {
		return err
	}
	if strings.EqualFold(u.Scheme, "https") {
		return nil
	}
	if allowInsecure.Load() {
		return nil
	}
	if isLocalHost(u.Hostname()) {
		return nil
	}
	return fmt.Errorf("拒绝通过明文 HTTP 向 %s 发送 API Key：请改用 https，或设置 ALLOW_INSECURE_UPSTREAM=true 明确放行", u.Host)
}

// CheckFetchURL 用于不带凭据的下载请求（例如上游返回的图片地址）。
// 不限制公网 http，只拒绝链路本地地址，防止被诱导访问云元数据服务。
func CheckFetchURL(raw string) error {
	u, err := parse(raw)
	if err != nil {
		return err
	}
	host := strings.ToLower(u.Hostname())
	if host == "metadata.google.internal" || host == "metadata" {
		return fmt.Errorf("拒绝访问云元数据地址 %s", u.Host)
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("拒绝访问链路本地地址 %s", u.Host)
		}
	}
	return nil
}

func parse(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("地址解析失败: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("仅支持 http/https 地址")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("地址缺少主机名")
	}
	return u, nil
}

// isLocalHost 判断主机名是否只在本机或内网可达。
func isLocalHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate()
	}
	return false
}
