package executor

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"agent2api/internal/providers"
)

const maxRetryAfter = providers.MaxRetryAfter
const minRateLimitCooldown = 30 * time.Second

// hardQuotaCode 是标记硬性配额耗尽的业务码，其
// 窗口在下一个本地 04:00 边界复位（与 HTTP 402 配对）。
const hardQuotaCode = "14018"

// quotaCooldown 为配额耗尽选取复位时间点：
// 402/14018 这一类冷却到下一个本地 04:00 边界，其他所有
// 配额信号保持本地午夜的兜底。
func quotaCooldown(status int, code string) time.Duration {
	return quotaCooldownAt(status, code, time.Now())
}

func quotaCooldownAt(status int, code string, now time.Time) time.Duration {
	if status == 402 || code == hardQuotaCode {
		return NextLocalFourAMCooldownAt(now)
	}
	return NextLocalMidnightCooldownAt(now)
}

type Classified struct {
	Kind       string
	Status     int
	Failover   bool
	Cooldown   time.Duration
	Code       string
	Type       string
	Message    string
	RetryAfter time.Duration
	// Model 将冷却限定到某一个 public model。设置后，
	// 只有该 model 被冷却；账号继续服务其他所有内容。
	Model string
}

// backoffFloor 与 backoffCeiling 限定施加于同一类别
// 重复失败的指数退避。首次失败仍使用分类器
// 自身的时长；退避仅在重复时延长它。
const (
	backoffFloor    = 30 * time.Second
	backoffCeiling  = 6 * time.Hour
	backoffMaxLevel = BackoffMaxLevel
)

func nextBackoffCooldown(base time.Duration, level int) (time.Duration, int) {
	if level < 0 {
		level = 0
	}
	if level >= backoffMaxLevel {
		return backoffCeiling, level
	}
	multiplier := time.Duration(1) << level
	next := base * multiplier
	if next < backoffFloor {
		next = backoffFloor
	}
	if next >= backoffCeiling {
		return backoffCeiling, level
	}
	return next, level + 1
}

func ParseRetryAfter(raw string, fallback time.Duration) time.Duration {
	if parsed := parseRetryAfterValue(raw, time.Now()); parsed > 0 {
		return clampRetryAfter(parsed)
	}
	return clampRetryAfter(fallback)
}

func ParseRetryAfterHint(body, header string, fallback time.Duration) time.Duration {
	now := time.Now()
	if parsed := parseRetryAfterValue(header, now); parsed > 0 {
		return clampRetryAfter(parsed)
	}
	if parsed := retryAfterFromBody(body, now); parsed > 0 {
		return clampRetryAfter(parsed)
	}
	// 最后手段：消息文本内嵌的挂钟复位时刻
	// （"…将在 2026-09-01 13:56:47 UTC+8 重置"），与 provider 侧的
	// 信封解析共用。
	if parsed := providers.ParseResetTimestampCN(body, now); parsed > 0 {
		return clampRetryAfter(parsed)
	}
	return clampRetryAfter(fallback)
}

// parseRetryAfterValue 与 clampRetryAfter 委托给 internal/providers：
// provider 各包读取相同的 header 提示，单一实现可让两侧
// 接受的形态保持一致。
func parseRetryAfterValue(raw string, now time.Time) time.Duration {
	return providers.ParseRetryAfterValue(raw, now)
}

func clampRetryAfter(value time.Duration) time.Duration {
	return providers.ClampRetryAfter(value)
}

func retryAfterFromBody(body string, now time.Time) time.Duration {
	var value any
	if json.Unmarshal([]byte(strings.TrimSpace(body)), &value) != nil {
		return 0
	}
	return retryAfterFromJSON(value, now)
}

func retryAfterFromJSON(value any, now time.Time) time.Duration {
	switch current := value.(type) {
	case map[string]any:
		for _, wanted := range []string{"retry_after", "retryafter", "quotaresetdelay", "resets_in_seconds", "resets_at", "reset_at"} {
			for key, raw := range current {
				if strings.ToLower(strings.TrimSpace(key)) != wanted {
					continue
				}
				if hint := parseRetryAfterJSONValue(raw, now); hint > 0 {
					return hint
				}
			}
		}
		for _, raw := range current {
			if hint := retryAfterFromJSON(raw, now); hint > 0 {
				return hint
			}
		}
	case []any:
		for _, raw := range current {
			if hint := retryAfterFromJSON(raw, now); hint > 0 {
				return hint
			}
		}
	case string:
		var nested any
		if json.Unmarshal([]byte(strings.TrimSpace(current)), &nested) == nil {
			return retryAfterFromJSON(nested, now)
		}
	}
	return 0
}

func parseRetryAfterJSONValue(value any, now time.Time) time.Duration {
	switch current := value.(type) {
	case string:
		return parseRetryAfterValue(current, now)
	case float64:
		return parseRetryAfterValue(strconv.FormatFloat(current, 'f', -1, 64), now)
	case json.Number:
		return parseRetryAfterValue(current.String(), now)
	default:
		return 0
	}
}

func Classify(status int, body, retryAfter, kindHint string) Classified {
	msg, code, typ, nestedKind := extractError(body)
	kind := strings.TrimSpace(firstNonEmpty(kindHint, nestedKind))
	lower := strings.ToLower(msg + " " + code + " " + typ)
	catalogUnavailable := strings.Contains(lower, "model_catalog_unavailable") ||
		strings.Contains(lower, "dynamic model catalog is unavailable") ||
		strings.Contains(lower, "model catalog unavailable")
	if kind == "" {
		switch {
		case quotaLike(lower, code, typ):
			kind = KindQuota
		case modelNotAvailableLike(lower, code):
			kind = KindModelNotAvailable
		case notReadyLike(lower):
			kind = KindNotReady
		case IsInvalidRequestText(lower):
			kind = KindInvalidRequest
		case authLike(lower) && !quotaLike(lower, code, typ) && !rateLike(lower, code):
			kind = KindAuth
		case rateLike(lower, code) || code == "429" || status == 429:
			kind = KindRateLimit
		case status == 401 || status == 403:
			kind = KindAuth
		default:
			kind = KindUnavailable
		}
	}
	if promptLimitLike(lower) {
		kind = KindInvalidRequest
	}
	if kind == KindAuth && quotaLike(lower, code, typ) {
		kind = KindQuota
	}
	if kind == KindRateLimit && quotaLike(lower, code, typ) {
		kind = KindQuota
	}
	// 不带业务信封的 403（空响应体或 HTML 响应体，且无
	// 错误码）是边缘/WAF 拦截，而非账号问题。重新登录
	// 无济于事，且同一账号稍后可能可用，因此必须保持
	// 瞬态。
	trimmedBody := strings.TrimSpace(body)
	bare403 := status == 403 && strings.TrimSpace(code) == "" &&
		(trimmedBody == "" || strings.HasPrefix(trimmedBody, "<"))
	if bare403 {
		kind = KindUnavailable
	}
	// "接受了请求但未发送任何帧"：瞬态，同一账号
	// 重试即可成功。
	if emptyStreamLike(lower) {
		kind = KindUnavailable
	}
	// plan gate 是账号级的：该套餐不覆盖此模型。其他
	// 账号可以服务它，而这个账号本身是健康的。
	if planGateLike(lower) {
		kind = KindModelNotAvailable
	}

	out := Classified{Kind: kind, Message: strings.TrimSpace(msg), Code: firstNonEmpty(code, kind), Type: firstNonEmpty(typ, "api_error")}
	switch kind {
	case KindQuota:
		out.Status = 429
		out.Failover = false
		out.Cooldown = quotaCooldown(status, code)
		out.Code = firstNonEmpty(code, "insufficient_quota")
		out.Type = "insufficient_quota"
	case KindRateLimit:
		out.Status = 429
		out.Failover = true
		out.Cooldown = ParseRetryAfterHint(body, retryAfter, 60*time.Second)
		if out.Cooldown > 0 && out.Cooldown < minRateLimitCooldown {
			out.Cooldown = minRateLimitCooldown
		}
	case KindAuth:
		if status == 401 {
			out.Status = 401
		} else {
			out.Status = 403
		}
		out.Failover = true
		out.Cooldown = ParseRetryAfterHint(body, retryAfter, 30*time.Second)
		out.Code = firstNonEmpty(code, "unauthorized")
	case KindNotReady:
		out.Status = 503
		out.Failover = true
		out.Cooldown = ParseRetryAfterHint(body, retryAfter, 10*time.Second)
		out.Code = firstNonEmpty(code, "not_ready")
	case KindInvalidRequest:
		// 上游拒绝了该请求内容；在其他账号上重试
		// 也无法成功，而账号本身是健康的。
		if promptLimitLike(lower) {
			out.Status = 400
		} else {
			out.Status = status
			if out.Status < 400 {
				out.Status = 400
			}
		}
		out.Failover = false
		out.Cooldown = 0
		out.Type = firstNonEmpty(typ, "invalid_request_error")
		out.Code = firstNonEmpty(code, "invalid_request")
	case KindModelNotAvailable:
		// 账号是健康的；过期的目录可能仍需在
		// 其他账号上重试。绝不对该账号进行冷却。
		out.Status = 400
		out.Failover = true
		out.Cooldown = 0
		out.Type = firstNonEmpty(typ, "invalid_request_error")
		out.Code = firstNonEmpty(code, "model_not_available")
		if catalogUnavailable {
			out.Status = 503
			out.Type = "api_error"
			out.Code = "model_catalog_unavailable"
		}
	default:
		if status >= 500 {
			out.Status = status
		} else if status >= 400 {
			out.Status = status
		} else {
			out.Status = 502
		}
		out.Failover = true
		out.Cooldown = ParseRetryAfterHint(body, retryAfter, 15*time.Second)
		out.Code = firstNonEmpty(code, "upstream_error")
	}
	out.RetryAfter = out.Cooldown
	hint := hintFor(out.Kind, out.Code, out.Message)
	switch {
	case hint != "" && noiseLike(out.Message):
		// 将网关噪声（HTML 错误页、空响应体）转换成
		// 运维人员可据此行动的内容。
		out.Message = hint
	case out.Message == "":
		out.Message = firstNonEmpty(hint, out.Code)
	}
	return out
}

// maxUsefulMessage 限定了值得原样回显的内容长度：超过
// 此长度几乎总是边缘网关错误页，而非业务消息。
const maxUsefulMessage = 300

// noiseLike 报告某条消息是否不携带可用信息。
func noiseLike(message string) bool {
	text := strings.TrimSpace(message)
	if text == "" {
		return true
	}
	if strings.HasPrefix(text, "<") {
		return true
	}
	return len(text) > maxUsefulMessage
}

func emptyStreamLike(lower string) bool {
	return strings.Contains(lower, "empty_stream") || strings.Contains(lower, "empty stream")
}

func planGateLike(lower string) bool {
	return strings.Contains(lower, "plan_gate") || strings.Contains(lower, "plan gate")
}

// hintFor 为已知失败类别生成可行动的一行提示，使
// 客户端被告知该做什么，而非收到原始网关页面。当
// 上游消息本身已是最佳答案时（例如请求级
// 拒绝），它返回 ""。
func hintFor(kind, code, message string) string {
	lower := strings.ToLower(code + " " + message)
	switch {
	case emptyStreamLike(lower):
		return "the upstream closed the stream before sending any content; retry the request"
	case kind == KindRateLimit:
		return "upstream rate limit reached; retry shortly (the gateway backs off and rotates accounts)"
	case kind == KindQuota:
		return "the account's quota is exhausted; it stays out of rotation until the upstream window resets"
	case kind == KindAuth:
		return "the account's session is no longer valid; re-login is required"
	case kind == KindNotReady:
		return "the account is still starting up; retry shortly"
	case kind == KindModelNotAvailable:
		if planGateLike(lower) {
			return "this account's plan does not cover the requested model; another account may serve it"
		}
		return "the requested model is not available for this account; refresh the catalog or pick another model"
	case kind == KindUnavailable:
		return "the upstream is temporarily unavailable; retry shortly"
	default:
		return ""
	}
}

func extractError(body string) (msg, code, typ, kind string) {
	text := strings.TrimSpace(body)
	if text == "" {
		return "", "", "", ""
	}
	var parsed map[string]any
	if json.Unmarshal([]byte(text), &parsed) == nil {
		if errObj, ok := parsed["error"].(map[string]any); ok {
			msg, _ = errObj["message"].(string)
			if msg == "" {
				msg, _ = errObj["msg"].(string)
			}
			code = stringifyJSONCode(errObj["code"])
			typ, _ = errObj["type"].(string)
			kind, _ = errObj["kind"].(string)
			if data, ok := errObj["data"].(map[string]any); ok {
				if msg == "" {
					msg, _ = data["message"].(string)
					if msg == "" {
						msg, _ = data["msg"].(string)
					}
				}
				if code == "" {
					code = stringifyJSONCode(data["code"])
				}
				if typ == "" {
					typ, _ = data["type"].(string)
				}
				if kind == "" {
					kind, _ = data["kind"].(string)
				}
			}
			return msg, code, typ, kind
		}
		if m, ok := parsed["message"].(string); ok {
			msg = m
		}
		code = stringifyJSONCode(parsed["code"])
		if t, ok := parsed["type"].(string); ok {
			typ = t
		}
		if k, ok := parsed["kind"].(string); ok {
			kind = k
		}
		if msg != "" || code != "" {
			return msg, code, typ, kind
		}
		if nested, ok := parsed["body"]; ok {
			switch value := nested.(type) {
			case string:
				if innerMsg, innerCode, innerType, innerKind := extractError(value); innerMsg != "" || innerCode != "" {
					return innerMsg, innerCode, innerType, innerKind
				}
			case map[string]any:
				encoded, err := json.Marshal(value)
				if err == nil {
					if innerMsg, innerCode, innerType, innerKind := extractError(string(encoded)); innerMsg != "" || innerCode != "" {
						return innerMsg, innerCode, innerType, innerKind
					}
				}
			}
		}
	}
	return text, "", "", ""
}

func stringifyJSONCode(v any) string {
	switch c := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(c)
	case float64:
		return strconv.FormatFloat(c, 'f', -1, 64)
	case json.Number:
		return strings.TrimSpace(c.String())
	default:
		return ""
	}
}

func quotaLike(lower, code, typ string) bool {
	if code == "insufficient_quota" || typ == "insufficient_quota" || code == "1005" || code == "4008" || code == "14018" {
		return true
	}
	return strings.Contains(lower, "insufficient_quota") ||
		strings.Contains(lower, "exceeded your current quota") ||
		strings.Contains(lower, "额度已用尽") ||
		strings.Contains(lower, "额度用尽") ||
		strings.Contains(lower, "购买加量包")
}

func promptLimitLike(lower string) bool {
	return IsPromptLimitText(lower)
}

// rateLike 在一张表中匹配限流信号，使每条分类
// 路径一致：英文措辞、WorkBuddy 业务码 6004，以及
// 其中文用量上限消息。code 是从信封中提取的精确业务码
// （绝不是子串）。
func rateLike(lower, code string) bool {
	return code == "6004" ||
		strings.Contains(lower, "too many requests") ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "rate-limit") ||
		strings.Contains(lower, "response code=429") ||
		strings.Contains(lower, "resource_exhausted") ||
		strings.Contains(lower, "rate_limit_exceeded") ||
		strings.Contains(lower, "account busy") ||
		strings.Contains(lower, "in-flight") ||
		strings.Contains(lower, "超出频率限制")
}

func authLike(lower string) bool {
	return strings.Contains(lower, "null pointer") ||
		strings.Contains(lower, "forbidden") ||
		strings.Contains(lower, "duplicate request") ||
		strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "401") ||
		strings.Contains(lower, "403") ||
		strings.Contains(lower, "credential") ||
		strings.Contains(lower, "refresh token") ||
		strings.Contains(lower, "access token")
}

func notReadyLike(lower string) bool {
	return strings.Contains(lower, "hot context not ready") ||
		strings.Contains(lower, "auth manager not captured") ||
		strings.Contains(lower, "not ready")
}

func modelNotAvailableLike(lower, code string) bool {
	if code == "model_not_available" || code == "model_catalog_unavailable" {
		return true
	}
	return strings.Contains(lower, "model_not_available") ||
		strings.Contains(lower, "model_catalog_unavailable") ||
		strings.Contains(lower, "no accounts serve model")
}
