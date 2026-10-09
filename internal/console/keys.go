package console

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

type consoleKeyView struct {
	Prefix  string `json:"prefix"`
	Hint    string `json:"hint"`
	Target  string `json:"target,omitempty"`
	Rotated bool   `json:"rotated,omitempty"`
	Secret  string `json:"secret,omitempty"`
}

// apiKeyPrefix 渲染密钥的短小、非机密指纹用于展示。它对应随多 API 密钥系统
// 一起被移除的旧 accounts.APIKeyPrefix 辅助函数。
func apiKeyPrefix(secret string) string {
	secret = strings.TrimSpace(secret)
	if len(secret) <= 12 {
		return secret
	}
	return secret[:8] + "…" + secret[len(secret)-4:]
}

// HandleConsoleKey 管理两个相互独立的凭据。默认读取并轮换控制台（运维）密钥；
// 提交 target=proxy 则改为操作数据面密钥。控制台密钥绝不可能被代理调用方轮换：
// 能到达此处理器本身就已经要求持有控制台密钥。
//
// GET 永远只回指纹（只读探测面）；完整明文仅经 POST 的显式动作返回：
// `{"rotate":true}` 轮换并一次性展示新钥；`{"reveal":true}`（批次 7）
// 只读返回当前密钥——供 owner 核对/复制，任何状态与认证层都不被触碰。
func (h *Handler) HandleConsoleKey(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		secret := h.consoleKey()
		writeJSON(w, http.StatusOK, consoleKeyView{
			Prefix: apiKeyPrefix(secret),
			Target: "console",
			Hint:   "Operator key: unlocks this console. Keep it on your own devices only — clients should use the proxy key instead.",
		})
	case http.MethodPost:
		var input struct {
			Rotate bool   `json:"rotate"`
			Reveal bool   `json:"reveal"`
			Target string `json:"target"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConsoleBodyBytes)).Decode(&input); err != nil && err != io.EOF {
			writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if input.Rotate == input.Reveal { // 恰好其一：既非「只轮换」也非「只读取」
			writeErr(w, http.StatusBadRequest, "invalid_request", "set rotate=true or reveal=true")
			return
		}
		target := strings.ToLower(strings.TrimSpace(input.Target))
		if target == "" {
			target = "console"
		}
		if h.KeyRotation == nil {
			writeErr(w, http.StatusInternalServerError, "console_key_rotate_failed", "key rotation is not configured")
			return
		}

		var (
			secret string
			err    error
			code   string
			hint   string
		)
		if input.Rotate {
			code = "console_key_rotate_failed"
			switch target {
			case "console":
				secret, err = h.KeyRotation.Rotate(r.Context())
			case "proxy":
				secret, err = h.KeyRotation.RotateProxy(r.Context())
			default:
				writeErr(w, http.StatusBadRequest, "invalid_request", "target must be console or proxy")
				return
			}
			hint = "Store this value now. The console will ask for the operator key on the next sign-in."
			if target == "proxy" {
				hint = "Store this value now and update your clients: the previous data-plane key stops working immediately."
			}
		} else {
			code = "console_key_reveal_failed"
			switch target {
			case "console":
				secret, err = h.KeyRotation.Reveal(r.Context())
			case "proxy":
				secret, err = h.KeyRotation.RevealProxy(r.Context())
			default:
				writeErr(w, http.StatusBadRequest, "invalid_request", "target must be console or proxy")
				return
			}
			hint = "Revealed to the operator; read-only — use rotate to change it."
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, code, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, consoleKeyView{
			Prefix:  apiKeyPrefix(secret),
			Target:  target,
			Hint:    hint,
			Rotated: input.Rotate,
			Secret:  secret,
		})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or POST only")
	}
}
