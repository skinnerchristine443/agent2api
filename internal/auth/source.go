package auth

import (
	"fmt"
	"net"
	"strings"
)

// 控制面的入口策略。
//
// 控制台（/api/*）是管理面，因此它与数据面的隔离靠的是「请求从何处进入」
// 而非「谁在调用」：只有 loopback 来源（或运维显式加入白名单的来源）
// 才能触达它。因此泄漏的 proxy key 无法从别处重放来接管控制台。
//
// 白名单只是给确实需要从固定地址访问控制台的运维留的逃生口；注意它天然脆弱
//（客户端地址动态变化、远程反向代理会把来源收窄为代理自身地址）——
// 推荐的方式是在主机上终结的隧道。

// ParseCIDRs 解析一组 CIDR 段，也接受裸 IP 字面量（视为单地址网段）。
// 空条目跳过。非法条目会报错，从而让笔误无法悄悄放宽或架空该策略。
func ParseCIDRs(values []string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, raw := range values {
		for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
			entry := strings.TrimSpace(part)
			if entry == "" {
				continue
			}
			if ip := net.ParseIP(strings.Trim(entry, "[]")); ip != nil {
				bits := 8 * net.IPv6len
				if ip.To4() != nil {
					bits = 8 * net.IPv4len
					ip = ip.To4()
				}
				out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
				continue
			}
			_, network, err := net.ParseCIDR(entry)
			if err != nil {
				return nil, fmt.Errorf("invalid console allowlist entry %q: %w", entry, err)
			}
			out = append(out, network)
		}
	}
	return out, nil
}

// RemoteAllowed 报告 remoteAddr（net/http 的 RemoteAddr，即 "host:port"）
// 是否为 loopback 地址或位于某个白名单网段内。它绝不查询转发头：
// X-Forwarded-For 由调用方控制，信任它会让任何人都能冒充 loopback 来源。
func RemoteAllowed(remoteAddr string, allowlist []*net.IPNet) bool {
	ip := remoteIP(remoteAddr)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, network := range allowlist {
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

// RemoteHostOf 返回 RemoteAddr 的裸主机名，用于诊断；无法解析时返回
// "unknown"。被拒绝的控制台调用会回显它，让运维一眼看到白名单需要覆盖哪个地址——
// 没有它，受来源限制的部署只会以不透明的 403 失败。
func RemoteHostOf(remoteAddr string) string {
	ip := remoteIP(remoteAddr)
	if ip == nil {
		return "unknown"
	}
	return ip.String()
}

// remoteIP 从 RemoteAddr 中提取裸 IP。它兼容 Go http 服务器产生的形式
// （"1.2.3.4:5678"、"[::1]:5678"）、不带端口的裸地址，以及 IPv6 zone 后缀。
func remoteIP(remoteAddr string) net.IP {
	host := strings.TrimSpace(remoteAddr)
	if host == "" {
		return nil
	}
	if h, _, err := net.SplitHostPort(host); err == nil && h != "" {
		host = h
	}
	host = strings.Trim(host, "[]")
	if idx := strings.LastIndex(host, "%"); idx >= 0 {
		host = host[:idx]
	}
	return net.ParseIP(host)
}
