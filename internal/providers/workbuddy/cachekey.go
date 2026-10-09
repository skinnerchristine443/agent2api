package workbuddy

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
)

// prompt_cache_key 注入：让同一客户端对同一账号的连续请求命中上游前缀缓存。
//
// 设计来由：参照情报曾称同一段约 8k token 前缀"不带键命中 0、带键命中接近
// 全量"——该说法被本仓 2026-10-09 live 实测修正（见下文实测结论）。键必须
// 同时满足：
//   - 账号隔离：不同账号绝不同键——复用他人键会命中错账号的前缀缓存；
//   - 会话稳定：同一会话的连续调用同键（否则每轮都重建前缀缓存）。
//
// 键格式 `wb2a-<uid8>-<convHex>`：
//   - uid8 = 账号 UID 前 8 字符（UID 为空则 "-"，仍保留隔离段形态）；
//   - convHex = sha256(uid|会话键) 前 16 字节的 hex——同账号同会话稳定、
//     跨会话不同；会话键为空时退化为 uid 单独哈希（空会话不复用前缀）。
//
// 客户端已显式携带（translate.ChatRequest.PromptCacheKey）时原样保留、绝不覆盖。
//
// ⚠️ 实测结论（2026-10-09，CN 域 live 对照）：**默认关闭**。
//   - 无键时上游的**自动前缀缓存已经生效**（deepseek-v4.1-flash 与 glm-5.3 复问
//     命中均 >98%）——与早期参照情报（"不带→0"）不符；
//   - 带键在 **glm-5.3 上为负优化**（复问命中从 12608 掉到 0），deepseek 上无增量；
//   - 结论：默认路径不做注入；本开关保留为复测/上游变化后的 A/B 手段。
//
// 启用方式：AGENT2API_WORKBUDDY_CACHE_KEY=1（默认关；禁用后行为 = 旧版：
// 客户端键同样不再透传）。

const cacheKeyEnv = "AGENT2API_WORKBUDDY_CACHE_KEY"

// cacheKeyEnabled 报告前缀缓存键处理是否运行。
// 默认关闭（实测无收益、部分模型有害）；只有显式肯定才启用。
func cacheKeyEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(cacheKeyEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// applyPromptCacheKey 在出站 body 上落实 prompt_cache_key：
// 客户端已带（非空白）则保留原值；否则注入按「账号 + 会话」生成的稳定键。
// 关闭时不做任何事（既不注入也不透传）。
func applyPromptCacheKey(body map[string]any, clientKey, uid, session string) {
	if !cacheKeyEnabled() {
		return
	}
	if key := strings.TrimSpace(clientKey); key != "" {
		body["prompt_cache_key"] = key
		return
	}
	body["prompt_cache_key"] = buildPromptCacheKey(uid, session)
}

// buildPromptCacheKey 生成 `wb2a-<uid8>-<convHex>`。键语义见文件头注释。
func buildPromptCacheKey(uid, session string) string {
	uid = strings.TrimSpace(uid)
	uid8 := uid
	if len(uid8) > 8 {
		uid8 = uid8[:8]
	}
	if uid8 == "" {
		uid8 = "-"
	}
	sum := sha256.Sum256([]byte(uid + "|" + session))
	convHex := hex.EncodeToString(sum[:16])
	return "wb2a-" + uid8 + "-" + convHex
}
