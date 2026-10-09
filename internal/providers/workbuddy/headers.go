package workbuddy

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

func setCommonHeaders(header http.Header, realm Realm) {
	origin := "https://www.codebuddy.cn"
	switch realm {
	case RealmGlobal:
		origin = "https://www.workbuddy.ai"
	case RealmIntl:
		origin = "https://www.codebuddy.ai"
	}
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/json, text/plain, */*")
	// Intl 网关期望 IDE 客户端形态，不想要浏览器的 XHR 标记。
	if realm != RealmIntl {
		header.Set("X-Requested-With", "XMLHttpRequest")
	}
	header.Set("Origin", origin)
	header.Set("Referer", origin+"/")
	header.Set("User-Agent", UserAgent)
}

// setCLIChannelHeaders 仅用于 chat。取值来自 CodeBuddy CLI 2.139.0
// （craft / CLI / SaaS）。CN 与 Global 共用这些名称；Origin/host 保持分离。
// Intl realm 呈现 CodeBuddy IDE 身份而非 CLI 身份。
func setCLIChannelHeaders(header http.Header, credential Credential) {
	requestID := newCLIRequestID()
	header.Set("X-Request-ID", requestID)
	header.Set("X-Conversation-ID", requestID)
	header.Set("X-Session-ID", requestID)
	header.Set("X-Conversation-Request-ID", requestID)
	header.Set("X-Conversation-Message-ID", requestID)
	header.Set("X-Agent-Type", agentTypeMain)
	header.Set("X-Agent-Intent", agentIntentDefault)
	header.Set("X-IDE-Type", productTypeCLI)
	header.Set("X-IDE-Name", productTypeCLI)
	header.Set("X-IDE-Version", CLIVersion)
	header.Set("X-Product-Version", CLIVersion)
	header.Set("X-Private-Data", "false")
	if credential.IsIntl() {
		header.Set("X-IDE-Type", productTypeIDE)
		header.Set("X-IDE-Name", IDEName)
		header.Set("X-IDE-Version", IDEClientVersion)
		header.Set("X-Product-Version", IDEClientVersion)
	}
}

func newCLIRequestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "agent2api"
	}
	return hex.EncodeToString(raw[:])
}

func setIdentityHeaders(header http.Header, credential Credential) {
	setCommonHeaders(header, credential.Realm())
	if credential.AccessToken != "" {
		header.Set("Authorization", "Bearer "+credential.AccessToken)
	} else {
		header.Set("X-No-Authorization", "1")
	}
	if credential.UID != "" {
		header.Set("X-User-Id", credential.UID)
	} else {
		header.Set("X-No-User-Id", "1")
	}
	if credential.EnterpriseID != "" {
		header.Set("X-Enterprise-Id", credential.EnterpriseID)
	} else {
		header.Set("X-No-Enterprise-Id", "1")
	}
	if credential.Domain != "" {
		header.Set("X-Domain", credential.Domain)
	} else {
		header.Set("X-No-Department-Info", "1")
	}
	header.Set("X-Product", "SaaS")
}

// SetCatalogHeaders 为产品配置目录（/v3/config）携带访问身份。仅用于 chat 的 CLI
// 通道请求头不在这条路径上。
//
// UA 有意按区域区分：
//   - Global：desktop UA 匹配 WorkBuddy AI 下拉框（同一账号下 CLI UA 会漏掉
//     deepseek-v4.1-flash）。
//   - CN：保留 CLI UA。实测 A/B 显示在 CN 上用 WorkBuddy desktop UA 会返回一个
//     不同的、更差的集合（没有 glm-5.3-flash / cli agent 白名单为空）。
//
// Intl realm 也保留 CLI UA：其目录行为尚未针对真实账号验证过。
func SetCatalogHeaders(header http.Header, credential Credential) {
	setIdentityHeaders(header, credential)
	if credential.IsGlobal() {
		header.Set("User-Agent", DesktopUserAgent)
	}
}

// SetChatHeaders 携带访问身份，但绝不携带 refresh token。
func SetChatHeaders(header http.Header, credential Credential) {
	setIdentityHeaders(header, credential)
	setCLIChannelHeaders(header, credential)
}

// RefreshSourcePlugin 是每个官方客户端在 X-Auth-Refresh-Source 中发送的值。上游
// 刷新端点以该值作为其会话记账的键；任何其它值都会让刷新看起来像另一个客户端并可能
// 被拒绝（在 Intl realm 上表现为 invalid_grant）。
const RefreshSourcePlugin = "plugin"

// SetRefreshHeaders 是唯一允许携带 X-Refresh-Token 的路径。
func SetRefreshHeaders(header http.Header, credential Credential) {
	setCommonHeaders(header, credential.Realm())
	header.Set("X-Refresh-Token", credential.RefreshToken)
	if credential.EnterpriseID != "" {
		header.Set("X-Enterprise-Id", credential.EnterpriseID)
	}
	header.Set("X-Auth-Refresh-Source", RefreshSourcePlugin)
}

// SetBillingHeaders 为独立的计费主机携带身份。
func SetBillingHeaders(header http.Header, credential Credential) {
	setCommonHeaders(header, credential.Realm())
	if credential.AccessToken != "" {
		header.Set("Authorization", "Bearer "+credential.AccessToken)
	}
	if credential.UID != "" {
		header.Set("X-User-Id", credential.UID)
	}
	if credential.EnterpriseID != "" {
		header.Set("X-Enterprise-Id", credential.EnterpriseID)
		header.Set("X-Tenant-Id", credential.EnterpriseID)
	}
	if credential.Domain != "" {
		header.Set("X-Domain", credential.Domain)
	}
}
