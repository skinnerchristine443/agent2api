package providers

import (
	"context"
	"strings"
)

// 会话键的上下文传递。
//
// 会话键由 executor 在请求准备阶段解析（入站会话头优先，否则内容指纹），
// 有两处消费方：
//   - executor 自身的会话亲和选号（resolveSessionKey）；
//   - 各渠道的出站请求构造（当前为 WorkBuddy 的 prompt_cache_key 注入）。
//
// 两处的读写必须落在同一个 context key 上；providers 不能反向依赖
// executor，因此定义放在本包：executor 写入，渠道子包读取。

type sessionKeyContextKey struct{}

// WithSessionKey 把解析好的会话键放入上下文；空键（或全空白）原样返回。
func WithSessionKey(ctx context.Context, key string) context.Context {
	key = strings.TrimSpace(key)
	if key == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionKeyContextKey{}, key)
}

// SessionKeyFromContext 读取上下文中的会话键；未设置、为空或 ctx 为 nil 时返回空串。
func SessionKeyFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	key, _ := ctx.Value(sessionKeyContextKey{}).(string)
	return strings.TrimSpace(key)
}
