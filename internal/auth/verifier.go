package auth

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"sync/atomic"
)

// Verifier 持有该网关接受的两个密钥：
//
//   - console（运维）密钥，解锁 /api/*，也可调用数据面；
//   - proxy key，客户端用它访问 /v1/*，绝不能解锁 /api/*。
//
// 二者相互独立，因此把 proxy key 交给客户端（或泄漏它）都无法升级为控制台控制权。
// 当两者都为空时，verifier 保留旧的 fail-open 行为，并报告 Identity{Kind: KindNone}。
type Verifier struct {
	consoleKey *atomic.Pointer[string]
	proxyKey   *atomic.Pointer[string]
}

// NewVerifier 围绕 console 密钥与 proxy key 构建 verifier。
// 两者传同一个值即复现历史上的单密钥行为。
func NewVerifier(consoleKey, proxyKey string) Verifier {
	console := &atomic.Pointer[string]{}
	proxy := &atomic.Pointer[string]{}
	v := Verifier{consoleKey: console, proxyKey: proxy}
	v.SetConsoleKey(consoleKey)
	v.SetProxyKey(proxyKey)
	return v
}

// ConsoleKey 读取共享的实时 console 密钥。Verifier 的副本共享状态。
func (v Verifier) ConsoleKey() string {
	return readSecret(v.consoleKey)
}

// ProxyKey 读取实时的数据面密钥。Verifier 的副本共享状态。
func (v Verifier) ProxyKey() string {
	return readSecret(v.proxyKey)
}

// SetConsoleKey 轮换由 NewVerifier 创建的 verifier，而无需替换 HTTP handler
// 已持有的 verifier 副本。
func (v Verifier) SetConsoleKey(secret string) {
	storeSecret(v.consoleKey, secret)
}

// SetProxyKey 原地轮换数据面密钥。
func (v Verifier) SetProxyKey(secret string) {
	storeSecret(v.proxyKey, secret)
}

func readSecret(p *atomic.Pointer[string]) string {
	if p == nil {
		return ""
	}
	key := p.Load()
	if key == nil {
		return ""
	}
	return *key
}

func storeSecret(p *atomic.Pointer[string], secret string) {
	if p == nil {
		return
	}
	key := strings.TrimSpace(secret)
	p.Store(&key)
}

func bearerSecret(r *http.Request) string {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if got == "" {
		got = r.Header.Get("x-api-key")
	}
	return strings.TrimSpace(got)
}

// constantTimeEqual 比较两个密钥，不通过时序泄漏长度或内容。
func constantTimeEqual(left, right string) bool {
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func (v Verifier) Authenticate(ctx context.Context, r *http.Request) (Identity, bool) {
	consoleKey := v.ConsoleKey()
	proxyKey := v.ProxyKey()
	if consoleKey == "" && proxyKey == "" {
		return Identity{Kind: KindNone}, true
	}
	secret := bearerSecret(r)
	if secret == "" {
		return Identity{}, false
	}
	// 先检查 console 密钥，这样两个密钥仍取同一值的部署会继续报告控制台身份。
	if consoleKey != "" && constantTimeEqual(secret, consoleKey) {
		return ConsoleIdentity(), true
	}
	if proxyKey != "" && constantTimeEqual(secret, proxyKey) {
		return ProxyIdentity(), true
	}
	return Identity{}, false
}
