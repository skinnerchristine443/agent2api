package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/proxy"
)

// Webhook 通知出口（G4）：把配额/账号告警（额度即将耗尽、账号掉线、来源封锁等）
// 发到一个可配的 URL。纯本地出口，不改变任何上游行为。
//
// 设计：
//   - URL 存 app_secrets（`webhook_url`），属敏感配置，读取时脱敏回显；
//   - 逐条告警 POST 一个 JSON 体；失败只记日志，绝不阻断维护循环；
//   - 有界超时（5s），避免停滞的接收端拖住调度；
//   - 默认关闭（URL 为空即不发）。

// notifyWebhook 异步投递一条告警。fire-and-forget：绝不阻塞维护循环。
func (manager *Manager) notifyWebhook(alert accounts.QuotaAlert) {
	if manager == nil || manager.store == nil {
		return
	}
	raw, ok, err := manager.store.GetSecret(context.Background(), webhookURLSecret)
	if err != nil || !ok {
		return
	}
	url := strings.TrimSpace(raw)
	if url == "" {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"type":         "quota_alert",
		"category":     alert.Category,
		"account_id":   alert.AccountID,
		"account_name": alert.AccountName,
		"message":      alert.Message,
		"remaining":    alert.Remaining,
		"unit":         alert.Unit,
		"raised_at":    alert.RaisedAt,
		"time":         time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 5 * time.Second, Transport: proxy.SharedInheritTransport()}
		resp, err := client.Do(req)
		if err != nil {
			return
		}
		_ = resp.Body.Close()
	}()
}

// webhookURLSecret 是 Webhook 出口 URL 的 secret 键。
const webhookURLSecret = "webhook_url"
