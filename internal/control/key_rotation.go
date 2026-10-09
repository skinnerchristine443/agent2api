package control

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"agent2api/internal/accounts"
)

var errNoKeyStore = errors.New("key store is not configured")

// Keys 是 secret 持久化接口。此前位于 control/keys.go 的多 API 密钥 CRUD 已被
// 移除；只保留 SetConsoleSecret。本网关保留的两个 secret 存储在 app_secrets 中：
// proxy_api_key（交给客户端的数据面凭据）和 console_key（运维凭据）。二者都不
// 存放在 api_keys 表里。
type Keys struct {
	store accounts.AccountStore
}

func NewKeys(store accounts.AccountStore) *Keys {
	if store == nil {
		return nil
	}
	return &Keys{store: store}
}

func (k *Keys) SetConsoleSecret(ctx context.Context, secretName, secret string) error {
	return k.store.SetSecret(ctx, secretName, secret)
}

// GetSecret 只读读取已存储的 secret。明文仅回给已通过控制台鉴权的调用方
// （reveal 动作，2026-10-09 批次 7：owner 可查看/复制自己的两把密钥；
// GET 指纹契约保持不变，读取只经显式 reveal 动作）。
func (k *Keys) GetSecret(ctx context.Context, secretName string) (string, error) {
	if k == nil || k.store == nil {
		return "", errNoKeyStore
	}
	secret, ok, err := k.store.GetSecret(ctx, secretName)
	if err != nil {
		return "", err
	}
	if !ok || strings.TrimSpace(secret) == "" {
		return "", fmt.Errorf("secret %s is not configured", secretName)
	}
	return secret, nil
}

// KeyRotation 串行化持久化与实时认证发布。新 secret 会在每个进程内 adapter 的下一次
// 请求时到达它们，因此无需重启任何东西。
//
// 两种轮换刻意保持独立：轮换控制台密钥不得使客户端已在使用的代理密钥失效，反之亦然。
type KeyRotation struct {
	Keys     *Keys
	Accounts *Accounts
	Mu       *sync.Mutex
	Generate func() (string, error)
	// Publish 把新的控制台密钥推入实时认证。
	Publish func(string)
	// PublishProxy 把新的数据面密钥推入实时认证。
	PublishProxy func(string)
}

// Reveal 只读返回当前控制台（运维）密钥——供 owner 核对/复制；不修改任何
// 状态、不触碰认证层。代理密钥保持不变。
func (s *KeyRotation) Reveal(ctx context.Context) (string, error) {
	if s.Keys == nil {
		return "", errNoKeyStore
	}
	return s.Keys.GetSecret(ctx, consoleKeySecret)
}

// RevealProxy 只读返回当前数据面密钥。控制台密钥保持不变。
func (s *KeyRotation) RevealProxy(ctx context.Context) (string, error) {
	if s.Keys == nil {
		return "", errNoKeyStore
	}
	return s.Keys.GetSecret(ctx, proxyAPIKeySecret)
}

// Rotate 铸造新的控制台（运维）密钥。代理密钥保持不变。
func (s *KeyRotation) Rotate(ctx context.Context) (string, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	secret, err := s.Generate()
	if err != nil {
		return "", err
	}
	if s.Keys == nil {
		return "", errNoKeyStore
	}
	if err = s.Keys.SetConsoleSecret(ctx, consoleKeySecret, secret); err != nil {
		return "", err
	}
	if s.Publish != nil {
		s.Publish(secret)
	}
	return secret, nil
}

// RotateProxy 铸造新的数据面密钥并交给 runtime，使进程内调用方跟随它。
// 控制台密钥保持不变。
func (s *KeyRotation) RotateProxy(ctx context.Context) (string, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	secret, err := s.Generate()
	if err != nil {
		return "", err
	}
	if s.Keys == nil {
		return "", errNoKeyStore
	}
	if err = s.Keys.SetConsoleSecret(ctx, proxyAPIKeySecret, secret); err != nil {
		return "", err
	}
	if s.PublishProxy != nil {
		s.PublishProxy(secret)
	}
	if s.Accounts != nil {
		if err = s.Accounts.ReplaceProxyAPIKey(ctx, secret); err != nil {
			return "", err
		}
	}
	return secret, nil
}
