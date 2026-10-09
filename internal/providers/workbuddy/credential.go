// Package workbuddy 实现 WorkBuddy / CodeBuddy 的进程内 provider。
// 协议常量只存在于本包中。
package workbuddy

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	CredentialFormat = "workbuddy-oauth-v1"

	DomainCN     = "codebuddy.cn"
	DomainGlobal = "workbuddy.ai"
	// DomainIntl 是 CodeBuddy IDE 的 realm。它是第三种部署，而非 DomainGlobal
	// 的别名：Intl 主机接受 Intl token，而 Global 主机不提供相同的端点。
	DomainIntl = "codebuddy.ai"

	ChatBaseCN      = "https://copilot.tencent.com"
	ChatBaseGlobal  = "https://www.workbuddy.ai"
	ChatBaseIntl    = "https://www.codebuddy.ai"
	BillingBaseCN   = "https://www.codebuddy.cn"
	BillingBaseIntl = "https://www.codebuddy.ai"

	// UserAgent 与官方 @tencent-ai/codebuddy-code 2.139.0 一致，采用 WorkBuddy
	// chat 仍接受的双 token 形式。Origin/Referer 保持区域相关，绝不能把 CN 与
	// Global 混用。
	CLIVersion = "2.139.0"
	UserAgent  = "CLI/" + CLIVersion + " CodeBuddy/" + CLIVersion

	// DesktopUserAgent 仅用于 /v3/config。实测 A/B 显示同一个 Global 账号在
	// desktop UA 下会返回 IDE 下拉框（含 deepseek-v4.1-flash），而 CLI UA
	// 返回的是另一组模型。
	DesktopVersion   = "5.4.2"
	DesktopUserAgent = "WorkBuddy/" + DesktopVersion

	// Intl realm 把自己呈现为 CodeBuddy IDE 而非 CLI。
	IDEClientVersion = "1.100.0"
	IDEName          = "CodeBuddy"

	productTypeCLI     = "CLI"
	productTypeIDE     = "IDE"
	agentIntentDefault = "craft"
	agentTypeMain      = "main"

	pathAuthState    = "/v2/plugin/auth/state"
	pathAuthToken    = "/v2/plugin/auth/token"
	pathAuthAccount  = "/v2/plugin/login/account"
	pathTokenRefresh = "/v2/plugin/auth/token/refresh"
	pathChat         = "/v2/chat/completions"
	// CN 仍向 Bearer token 提供控制台目录。Global 的
	// /console/enterprises/personal/models 是一个 OIDC 页面：未认证时 302 到
	// Keycloak，已认证时返回 500 HTML。插件 JSON 目录是
	// /v2/enterprises/personal/models。IDE 模型下拉框不是那个目录：desktop
	// 加载的是已认证的 GET /v3/config（产品配置）。
	pathModelsCN      = "/console/enterprises/personal/models"
	pathModelsGlobal  = "/v2/enterprises/personal/models"
	pathProductConfig = "/v3/config"
	pathUserResource  = "/v2/billing/meter/get-user-resource"
	pathDailyCheckin  = "/v2/billing/meter/daily-checkin"
	// pathCheckinStatus 是 pathDailyCheckin 的只读伴随接口：它报告今天的发放是否
	// 已被领取，以及连续签到和累计额度计数。先查询它能把一次「已签到」的答复从
	// 盲写变成结构化读取。
	pathCheckinStatus = "/v2/billing/meter/checkin-activity-status"

	sessionDeadCode         = 12153
	sessionDeadText         = "Offline user session not found"
	missingSystemPromptCode = 11128
	missingSystemPromptText = "first message is not system prompt"
	toolCallSequenceCode    = 11148
	toolCallSequenceText    = "tool calls and tool results do not match"

	// modelNotRegisteredCode 表示请求的模型未在该 realm/账号下注册：
	// 账号本身是健康的，因此这不得让它冷却。
	modelNotRegisteredCode = 11102

	// rateLimitCode 标记一次用量限制，其响应携带绝对重置时间戳。按通用限流兜底
	// 去冷却会在几秒后重试，并烧掉更多额度。
	rateLimitCode = 6004

	// quotaExhaustedCode 标记一次硬性的额度耗尽（"额度已用尽"）：账号在额度
	// 窗口滚动之前无法服务，因此必须冷却，而不是作为请求级错误做故障转移。
	quotaExhaustedCode = 14018
)

// quotaResetLocation 是 WorkBuddy 在其 CN 用量限制消息中用于重置时间戳
// （"... UTC+8 重置"）的固定时区。它是个 var，因为 time.FixedZone 返回
// *time.Location，不能作为常量。
var quotaResetLocation = time.FixedZone("UTC+8", 8*60*60)

// Credential 是规范化的存储载荷结构。
type Credential struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	Domain       string `json:"domain"`
	UID          string `json:"uid"`
	EnterpriseID string `json:"enterprise_id"`
	Nickname     string `json:"nickname"`
}

// DecodeCredential 同时接受规范化的扁平载荷和嵌套的
// {account, auth} 导出结构。
func DecodeCredential(payload []byte) (Credential, error) {
	var nested struct {
		Account struct {
			UID          string `json:"uid"`
			EnterpriseID string `json:"enterpriseId"`
			Nickname     string `json:"nickname"`
		} `json:"account"`
		Auth struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			ExpiresAt    int64  `json:"expiresAt"`
			Domain       string `json:"domain"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(payload, &nested); err == nil &&
		(nested.Auth.AccessToken != "" || nested.Account.UID != "") {
		return Credential{
			AccessToken:  nested.Auth.AccessToken,
			RefreshToken: nested.Auth.RefreshToken,
			ExpiresAt:    nested.Auth.ExpiresAt,
			Domain:       nested.Auth.Domain,
			UID:          nested.Account.UID,
			EnterpriseID: nested.Account.EnterpriseID,
			Nickname:     nested.Account.Nickname,
		}, nil
	}
	var flat struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresAt    int64  `json:"expires_at"`
		Domain       string `json:"domain"`
		UID          string `json:"uid"`
		EnterpriseID string `json:"enterprise_id"`
		Nickname     string `json:"nickname"`
	}
	if err := json.Unmarshal(payload, &flat); err == nil && flat.AccessToken != "" {
		return Credential{
			AccessToken:  flat.AccessToken,
			RefreshToken: flat.RefreshToken,
			ExpiresAt:    flat.ExpiresAt,
			Domain:       flat.Domain,
			UID:          flat.UID,
			EnterpriseID: flat.EnterpriseID,
			Nickname:     flat.Nickname,
		}, nil
	}
	// 也接受扁平的、上游风格的 camelCase 键。
	var camel struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresAt    int64  `json:"expiresAt"`
		Domain       string `json:"domain"`
		UID          string `json:"uid"`
		EnterpriseID string `json:"enterpriseId"`
		Nickname     string `json:"nickname"`
	}
	if err := json.Unmarshal(payload, &camel); err == nil && camel.AccessToken != "" {
		return Credential(camel), nil
	}
	if hasEncryptedTokenEnvelope(payload) {
		return Credential{}, ErrEncryptedCredential
	}
	return Credential{}, fmt.Errorf("workbuddy credential requires access_token")
}

// ErrEncryptedCredential 报告一个以加密信封（$wbEncrypted / sym-v1）存储的
// access token，这是较新的 desktop 客户端会写入的形式。该信封用只有 desktop
// 客户端本地存储持有的密钥密封，因此这个无头构建无法解封。若没有这个专用错误，
// 该失败会以通用的 "requires access_token" 暴露，读起来像载荷格式错误，而非
// 不支持的格式。
var ErrEncryptedCredential = errors.New("workbuddy credential access_token is encrypted and cannot be unsealed by this build; export a plaintext credential instead")

// hasEncryptedTokenEnvelope 报告载荷是否携带加密信封，覆盖 DecodeCredential
// 接受的三种形态：顶层的 accessToken/access_token，或嵌套在 auth 下的这两种之一。
func hasEncryptedTokenEnvelope(payload []byte) bool {
	var top map[string]json.RawMessage
	if json.Unmarshal(payload, &top) != nil {
		return false
	}
	sources := []map[string]json.RawMessage{top}
	var nested struct {
		Auth map[string]json.RawMessage `json:"auth"`
	}
	if json.Unmarshal(payload, &nested) == nil && nested.Auth != nil {
		sources = append(sources, nested.Auth)
	}
	for _, source := range sources {
		for _, key := range []string{"accessToken", "access_token"} {
			if isEncryptedEnvelope(source[key]) {
				return true
			}
		}
	}
	return false
}

// isEncryptedEnvelope 匹配 {"$wbEncrypted": 1, "envelope": "..."}。
func isEncryptedEnvelope(raw json.RawMessage) bool {
	if len(raw) == 0 || raw[0] != '{' {
		return false
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil {
		return false
	}
	_, ok := envelope["$wbEncrypted"]
	return ok
}

func (c Credential) Encode() ([]byte, error) {
	return json.Marshal(c)
}

func (c Credential) Ready() bool {
	return strings.TrimSpace(c.AccessToken) != "" && strings.TrimSpace(c.UID) != ""
}

// Realm 标识一个账号属于哪个 WorkBuddy / CodeBuddy 部署。这三者是各自独立的
// 服务，有各自的 host 和请求头集合：CN（codebuddy.cn / copilot.tencent.com）、
// Global（workbuddy.ai）与 Intl（codebuddy.ai，IDE realm）。把 Intl token 提交给
// CN 主机会被直接拒绝，因此这些区分绝不能合并。
type Realm string

const (
	RealmCN     Realm = "cn"
	RealmGlobal Realm = "global"
	RealmIntl   Realm = "intl"
)

// Realm 从凭据存储的 domain 解析 realm，当 domain 缺失或无法识别时默认 CN。
func (c Credential) Realm() Realm {
	domain := strings.ToLower(strings.TrimSpace(c.Domain))
	switch {
	case domain == "":
		return RealmCN
	case strings.Contains(domain, DomainCN):
		return RealmCN
	case strings.Contains(domain, DomainIntl):
		return RealmIntl
	case strings.Contains(domain, DomainGlobal) || strings.Contains(domain, "workbuddy"):
		return RealmGlobal
	default:
		return RealmCN
	}
}

// IsGlobal 报告账号是否属于 Global realm。它有意保持窄口径：Intl 是另一项服务，
// 因此表达「非 CN」之意的调用方必须改用 Realm()。
func (c Credential) IsGlobal() bool { return c.Realm() == RealmGlobal }

// IsIntl 报告账号是否属于 CodeBuddy Intl realm。
func (c Credential) IsIntl() bool { return c.Realm() == RealmIntl }

func (c Credential) ChatBase() string {
	if c.IsGlobal() {
		return ChatBaseGlobal
	}
	if c.IsIntl() {
		return ChatBaseIntl
	}
	return ChatBaseCN
}

// productConfigPath 是 IDE 下拉框的数据源（CloudProductManager /v3/config）。
func (c Credential) productConfigPath() string {
	return pathProductConfig
}

func isCLIAgent(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "cli", "codebuddy", "workbuddy":
		return true
	default:
		return false
	}
}

// billingBaseByIssuer 把 access token 自身的 `iss` 来源映射到签发它的计费主机。
// 只列出本包已知的主机：未知签发者回退到存储的 realm，而不是臆造主机。
var billingBaseByIssuer = map[string]string{
	"https://www.codebuddy.cn": BillingBaseCN,
	"https://www.codebuddy.ai": BillingBaseIntl,
	"https://www.workbuddy.ai": ChatBaseGlobal,
}

// IssuerBillingBase 从 access token 的 `iss` 声明中读取计费主机，这是 token
// 自己对签发它的部署的陈述。当 token 不透明、格式错误，或指向本包不认识的主机时，
// 它返回 ""。
//
// 它存在的原因是：一个存储的 Domain 与其 token 不一致的凭据（导入错误，或在
// realm 之间迁移过）仍能计费到正确的部署，而不是被悄悄指向错误的部署。该声明只
// 读取、绝不校验——上游才是 token 是否有效的唯一权威，伪造的 `iss` 除了换来另一
// 台主机的 401 之外别无益处。
func (c Credential) IssuerBillingBase() string {
	parts := strings.Split(strings.TrimSpace(c.AccessToken), ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		Issuer string `json:"iss"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	parsed, err := url.Parse(strings.TrimSpace(claims.Issuer))
	if err != nil || parsed.Host == "" {
		return ""
	}
	origin := strings.ToLower(parsed.Scheme + "://" + parsed.Host)
	base, ok := billingBaseByIssuer[origin]
	if !ok {
		return ""
	}
	return base
}

func (c Credential) BillingBase() string {
	if base := c.IssuerBillingBase(); base != "" {
		return base
	}
	if c.IsGlobal() {
		return ChatBaseGlobal
	}
	if c.IsIntl() {
		return BillingBaseIntl
	}
	return BillingBaseCN
}

// ValidateCredential 强制导入契约：账号要就绪必须有 token 加 uid。
// 缺失 uid 会被存储但永不就绪。
func ValidateCredential(payload []byte) error {
	credential, err := DecodeCredential(payload)
	if err != nil {
		return err
	}
	if strings.TrimSpace(credential.AccessToken) == "" {
		return fmt.Errorf("workbuddy credential requires accessToken")
	}
	return nil
}

// workbuddyRealmForRegion 把控制面区域映射到 realm。当区域不表态（为空或未知）
// 时第二个返回值为 false，使调用方能让凭据自身的 domain 来决定。
func workbuddyRealmForRegion(region string) (Realm, bool) {
	switch strings.ToLower(strings.TrimSpace(region)) {
	case "intl":
		return RealmIntl, true
	case "global":
		return RealmGlobal, true
	case "cn":
		return RealmCN, true
	default:
		return "", false
	}
}

// chatBaseForRealm 返回某个 realm 的 chat/auth 主机。
func chatBaseForRealm(realm Realm) string {
	switch realm {
	case RealmIntl:
		return ChatBaseIntl
	case RealmGlobal:
		return ChatBaseGlobal
	default:
		return ChatBaseCN
	}
}

// domainForRealm 返回为某个 realm 持久化的 domain 标记。
func domainForRealm(realm Realm) string {
	switch realm {
	case RealmIntl:
		return DomainIntl
	case RealmGlobal:
		return DomainGlobal
	default:
		return DomainCN
	}
}
