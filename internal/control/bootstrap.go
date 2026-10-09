package control

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

type SecretStore interface {
	GetSecret(ctx context.Context, name string) (string, bool, error)
	SetSecret(ctx context.Context, name, value string) error
}

const proxyAPIKeySecret = "proxy_api_key"

// consoleKeySecret 是运维密钥。它刻意与 proxyAPIKeySecret 分开：代理密钥会交给
// 客户端，绝不能用来访问控制台界面。
const consoleKeySecret = "console_key"

func EnsureProxyAPIKey(ctx context.Context, store SecretStore, bootstrap string) (string, bool, error) {
	if value, ok, err := store.GetSecret(ctx, proxyAPIKeySecret); err != nil {
		return "", false, err
	} else if ok && strings.TrimSpace(value) != "" {
		return value, false, nil
	}

	key := strings.TrimSpace(bootstrap)
	if key == "" || key == "change-me" || key == "dev-key" {
		generated, err := GenerateAPIKey()
		if err != nil {
			return "", false, err
		}
		key = generated
	}
	if err := store.SetSecret(ctx, proxyAPIKeySecret, key); err != nil {
		return "", false, err
	}
	return key, true, nil
}

// EnsureConsoleKey 返回运维密钥，不存在时则创建。
//
// 首启即生成**独立随机值**（由调用方在启动日志只打印一次）——不再从代理
// 密钥播种：播种会让"已经在客户端手里"的代理密钥在轮换之前同时就是控制台
// 口令，等于把两个 secret 合二为一（审查 S1；OWASP 密钥管理指引：共享凭据
// 危及撤销与归因，应以独立凭据替代）。既有的已播种部署行为不变：存值优先，
// 启动告警提示两钥相同，在控制台轮换一次即完成拆分。显式的 bootstrap 值
// （环境变量）仍然优先，且同样只在首启播种。
func EnsureConsoleKey(ctx context.Context, store SecretStore, bootstrap string) (string, bool, error) {
	if value, ok, err := store.GetSecret(ctx, consoleKeySecret); err != nil {
		return "", false, err
	} else if ok && strings.TrimSpace(value) != "" {
		return value, false, nil
	}

	key := strings.TrimSpace(bootstrap)
	if key == "" || key == "change-me" || key == "dev-key" {
		generated, err := GenerateAPIKey()
		if err != nil {
			return "", false, err
		}
		key = generated
	}
	if err := store.SetSecret(ctx, consoleKeySecret, key); err != nil {
		return "", false, err
	}
	return key, true, nil
}

// RotateConsoleKey 持久化新的运维密钥。ProxyAPIKey 刻意保持不动：轮换控制台
// 凭据不得使已在用数据面密钥的客户端失效。
func RotateConsoleKey(ctx context.Context, store SecretStore, secret string) error {
	return store.SetSecret(ctx, consoleKeySecret, secret)
}

// RotateProxyAPIKey 持久化新的数据面密钥。
func RotateProxyAPIKey(ctx context.Context, store SecretStore, secret string) error {
	return store.SetSecret(ctx, proxyAPIKeySecret, secret)
}

func GenerateAPIKey() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate proxy api key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}
