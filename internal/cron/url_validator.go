package cron

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

// URLValidator 校验 webhook URL 是否允许访问。
// 默认只允许 http/https scheme，并禁止解析到 loopback、link-local
// 或私有地址的主机。可通过 AllowPrivate 显式放行私有地址（通常用于
// 内网部署或测试场景）。
type URLValidator struct {
	// AllowPrivate 为 true 时允许 loopback/link-local/private 地址。
	AllowPrivate bool
}

// ValidateWebhookURL 校验 urlStr 是否适合作为 cron webhook 目标。
// 返回 nil 表示允许；否则返回描述性错误。
func (v *URLValidator) ValidateWebhookURL(urlStr string) error {
	u, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q (only http/https allowed)", u.Scheme)
	}
	// 要求显式 host，不允许空 host 或相对路径。
	if u.Host == "" {
		return fmt.Errorf("URL host is empty")
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL host is empty")
	}

	// 纯 IP 地址：直接解析为 netip.Addr 并做本地/私有判断。
	if addr, ipErr := netip.ParseAddr(host); ipErr == nil {
		if v.isDisallowed(addr) {
			return fmt.Errorf("address %s is not allowed (private/loopback/link-local)", host)
		}
		return nil
	}

	// DNS 主机名：先做受限解析。只取第一个 IP 即可，因为只要有一个解析到
	// 私有地址本文就会拒绝。
	ips, dnsErr := net.LookupIP(host)
	if dnsErr != nil {
		// DNS 无法解析时不放行——避免通过不能解析的主机名绕过检查，
		// 实际 HTTP 库也可能重新解析并命中私有地址。
		return fmt.Errorf("could not resolve host %q: %w", host, dnsErr)
	}
	if len(ips) == 0 {
		return fmt.Errorf("host %q resolved to no addresses", host)
	}
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip)
		if !ok {
			continue
		}
		if v.isDisallowed(addr) {
			return fmt.Errorf("host %q resolves to disallowed address %s (private/loopback/link-local)", host, addr)
		}
	}
	return nil
}

// isDisallowed 返回 addr 是否属于受默认禁止的本地/私有/特殊地址。
func (v *URLValidator) isDisallowed(addr netip.Addr) bool {
	if v.AllowPrivate {
		return false
	}
	// Loopback（含 IPv4 127.0.0.0/8 与 IPv6 ::1）。
	if addr.IsLoopback() {
		return true
	}
	// Link-local（IPv4 169.254.0.0/16、IPv6 fe80::/10）。
	if addr.IsLinkLocalUnicast() {
		return true
	}
	// Private（RFC1918：10/8、172.16/12、192.168/16 等）。
	if addr.IsPrivate() {
		return true
	}
	// 还包括 IPv4 的 0.0.0.0/8（0.0.0.0 自身含在 IsUnspecified 中）。
	if addr.IsUnspecified() {
		return true
	}
	return false
}

// NormalizePrivateEnv 把常见环境变量字符串规范化为 bool。
// 接受 "true"/"1"/"yes" 为真；空字符串或未知值视为 false。
func NormalizePrivateEnv(v string) bool {
	s := strings.ToLower(strings.TrimSpace(v))
	return s == "true" || s == "1" || s == "yes"
}
