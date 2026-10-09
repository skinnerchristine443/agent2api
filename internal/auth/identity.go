package auth

import (
	"context"
)

const (
	// KindNone 表示未出示任何凭据，或未配置任何凭据。它被视为等价于控制台身份，
	// 这样在仅 loopback、未配置密钥的开发构建上，运维仍能访问控制台。
	KindNone = "none"
	// KindConsole 是运维凭据：它解锁 /api/*（也可调用数据面）。
	KindConsole = "console"
	// KindProxy 是交给客户端的数据面凭据。它可以调用 /v1/*，
	// 但绝不能触达 /api/*——这正是拆分两个密钥的全部意义。
	KindProxy = "proxy"
)

type Identity struct {
	Kind string
	Name string
}

func (i Identity) Console() bool {
	return i.Kind == KindNone || i.Kind == KindConsole
}

type ctxKey struct{}

func WithIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, identity)
}

func IdentityFrom(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(ctxKey{}).(Identity)
	return identity, ok
}

func ConsoleIdentity() Identity {
	return Identity{Kind: KindConsole, Name: "console"}
}

func ProxyIdentity() Identity {
	return Identity{Kind: KindProxy, Name: "proxy"}
}
