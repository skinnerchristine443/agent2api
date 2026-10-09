package config

import (
	"fmt"
	"net"
	"strings"
)

// EnsureBindAuthPolicy 拒绝一种启动配置：它会在非 loopback 地址上暴露未鉴权服务。
// 它是纯函数（不读 env、不用 socket），因此单测成本很低。
//
// 判定是 fail-closed 的：只有该函数能证明是 loopback 的地址才能在无鉴权时通过。
// 空 host 不是 loopback——传给 ListenAndServe 的 "%s:port" 会绑定所有网卡，
// 与 0.0.0.0 完全一样——因此它也被拒绝。（config.Load 将 HOST 默认设为 127.0.0.1，
// 所以空 host 只可能来自手工构造的 Config。）0.0.0.0、::、RFC1918 地址、
// host:port 形式，以及无法解析的主机名，在未启用鉴权时一律拒绝。
func EnsureBindAuthPolicy(host string, authEnabled bool) error {
	if authEnabled {
		return nil
	}
	normalized := strings.TrimSpace(host)
	if normalized == "" {
		return fmt.Errorf("监听地址为空（等价于绑定所有网卡）且未启用 API 鉴权：拒绝在非环回地址上暴露未鉴权服务；请显式设置 127.0.0.1/::1，或为该地址配置访问密钥")
	}
	if strings.EqualFold(normalized, "localhost") {
		return nil
	}
	// host 是裸主机名，不是 host:port 地址，因此这里不剥离端口。
	// 去掉方括号，让 IPv6 字面量 [::1] 也受信任。
	if ip := net.ParseIP(strings.Trim(normalized, "[]")); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("监听地址 %q 不是环回地址，且未启用 API 鉴权：拒绝在非环回地址上暴露未鉴权服务；请改回 127.0.0.1/::1，或为该地址配置访问密钥", normalized)
}
