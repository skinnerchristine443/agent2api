// Package trae 实现 Trae CN Solo 进程内 provider。
// 协议常量只存在于本包内。
package trae

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	CredentialFormat = "trae-oauth-v1"

	DomainCN = "trae.cn"

	AgentHost   = "https://trae-api-cn.mchost.guru"
	UgHost      = "https://api.trae.cn"
	OAuthHost   = "https://api.trae.com.cn"
	ConsoleHost = "https://www.trae.cn"

	// Login 使用 IDE 产品标记（PKCE authorization-code 流程）：
	// 下面的 client_id、auth_from=trae、x_app_version=3.3.62。
	ClientID = "ono9krqynydwx5"
	AuthFrom = "trae"
	AppID    = "6eefa01c-1036-4c7e-9ca5-d891f63bfcd8"
	// IDEVersion 是登录 + 交换时发送的 x_app_version / IDEVersion。
	IDEVersion     = "3.3.62"
	IDEVersionCode = "20260811"
	DeviceBrand    = "83DG"
	OSVersion      = "Windows 11 Pro"
	// Function 是不属于任何已获取 scene 的模型所用的兜底 scene
	// （通常不可达，因为任何可服务的模型都来自 catalog）。
	Function = "solo_work_lite"
	// PrimaryScene 吸收整个合并后的 catalog。每个模型默认都通过它提供服务
	// ——它是超集 scene，也是唯一携带 max-mode 分级的 scene。见 sceneFor。
	PrimaryScene = "chat_v3"
	// SecondaryScene 是兜底 scene，仅用于主 scene 未列出的模型
	// （例如 kimi-k2.6、kimi-k2.7-code 被上游从其中隐藏）。
	SecondaryScene = "solo_work_lite"
	DefaultModel   = "glm-5.2"
	PluginVersion  = "2.3.62834"
	UserAgent      = "Trae/" + IDEVersion
	QuotaUnit      = "entitlement_pack"
	// LegacyClientID 铸造了 PKCE 登录切换之前所创建账号的 refresh token；
	// 那些账号将其作为 refresh_client_id 携带，使 token 刷新继续可用。
	LegacyClientID = "en1oxy7wnw8j9n"

	pathChat          = "/api/agent/v3/llm_utils_chat"
	pathModels        = "/api/ide/v1/get_detail_param"
	pathExchange      = "/cloudide/api/v3/trae/oauth/ExchangeToken"
	pathExchangeCode  = "/trae/api/v3/oauth/ExchangeToken"
	pathUserInfo      = "/cloudide/api/v3/trae/GetUserInfo"
	pathCheckinStatus = "/trae/api/v2/ug/checkin_credits/status"
	pathCheckinClaim  = "/trae/api/v2/ug/checkin_credits/claim"
	pathEntUsage      = "/trae/api/v2/pay/ide_user_ent_usage"
	pathAuthorization = "/authorization"
	pathCallback      = "/authorize"
)

const (
	refreshLead     = 24 * time.Hour
	loginPendingTTL = 10 * time.Minute
)

// Credential 是规范化的存储 payload 形态。
type Credential struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresAt        int64  `json:"expires_at"`
	RefreshExpiresAt int64  `json:"refresh_expires_at,omitempty"`
	Domain           string `json:"domain"`
	APIHost          string `json:"api_host"`
	UID              string `json:"uid"`
	EnterpriseID     string `json:"enterprise_id"`
	Nickname         string `json:"nickname"`
	MachineID        string `json:"machine_id"`
	DeviceID         string `json:"device_id"`
	// DevicePrivateKey 是支撑 v3 code 交换所需的 device proof 的
	// PEM EC P-256 密钥。绝不离开 store。
	DevicePrivateKey string `json:"device_private_key,omitempty"`
	// RefreshClientID 记录当 RefreshToken 并非由当前登录 client 铸造时
	// （PKCE 切换之前创建的账号），是哪个 OAuth client 铸造了它。
	RefreshClientID string `json:"refresh_client_id,omitempty"`
	IDEVersion      string `json:"ide_version,omitempty"`
	IDEVersionCode  string `json:"ide_version_code,omitempty"`
}

func DecodeCredential(payload []byte) (Credential, error) {
	var nested struct {
		Account struct {
			UID          string `json:"uid"`
			EnterpriseID string `json:"enterpriseId"`
			Nickname     string `json:"nickname"`
		} `json:"account"`
		Auth struct {
			AccessToken      string `json:"accessToken"`
			RefreshToken     string `json:"refreshToken"`
			ExpiresAt        int64  `json:"expiresAt"`
			RefreshExpiresAt int64  `json:"refreshExpiresAt"`
			Domain           string `json:"domain"`
			APIHost          string `json:"apiHost"`
			MachineID        string `json:"machineId"`
			DeviceID         string `json:"deviceId"`
		} `json:"auth"`
	}
	// 命中条件只认 token，绝不以 uid 命中：uid 键在 camelCase 与 snake_case 两形态
	// 同名，若拿它当分支判据，camelCase 载荷会被 flat 分支吞掉并返回空 token
	// （err 仍为 nil）。三个分支统一「只认 token」后，残缺（仅 uid）载荷会落到
	// 末尾的错误返回，与 ValidateCredential 的契约一致。
	if err := json.Unmarshal(payload, &nested); err == nil &&
		(nested.Auth.RefreshToken != "" || nested.Auth.AccessToken != "") {
		return Credential{
			AccessToken:      nested.Auth.AccessToken,
			RefreshToken:     nested.Auth.RefreshToken,
			ExpiresAt:        unixSeconds(nested.Auth.ExpiresAt),
			RefreshExpiresAt: unixSeconds(nested.Auth.RefreshExpiresAt),
			Domain:           nested.Auth.Domain,
			APIHost:          nested.Auth.APIHost,
			UID:              nested.Account.UID,
			EnterpriseID:     nested.Account.EnterpriseID,
			Nickname:         nested.Account.Nickname,
			MachineID:        nested.Auth.MachineID,
			DeviceID:         nested.Auth.DeviceID,
		}, nil
	}
	var flat Credential
	if err := json.Unmarshal(payload, &flat); err == nil &&
		(flat.AccessToken != "" || flat.RefreshToken != "") {
		flat.ExpiresAt = unixSeconds(flat.ExpiresAt)
		flat.RefreshExpiresAt = unixSeconds(flat.RefreshExpiresAt)
		return flat, nil
	}
	var camel struct {
		AccessToken      string `json:"accessToken"`
		RefreshToken     string `json:"refreshToken"`
		ExpiresAt        int64  `json:"expiresAt"`
		RefreshExpiresAt int64  `json:"refreshExpiresAt"`
		Domain           string `json:"domain"`
		APIHost          string `json:"apiHost"`
		UID              string `json:"uid"`
		EnterpriseID     string `json:"enterpriseId"`
		Nickname         string `json:"nickname"`
		MachineID        string `json:"machineId"`
		DeviceID         string `json:"deviceId"`
	}
	if err := json.Unmarshal(payload, &camel); err == nil &&
		(camel.AccessToken != "" || camel.RefreshToken != "") {
		return Credential{
			AccessToken:      camel.AccessToken,
			RefreshToken:     camel.RefreshToken,
			ExpiresAt:        unixSeconds(camel.ExpiresAt),
			RefreshExpiresAt: unixSeconds(camel.RefreshExpiresAt),
			Domain:           camel.Domain,
			APIHost:          camel.APIHost,
			UID:              camel.UID,
			EnterpriseID:     camel.EnterpriseID,
			Nickname:         camel.Nickname,
			MachineID:        camel.MachineID,
			DeviceID:         camel.DeviceID,
		}, nil
	}
	return Credential{}, fmt.Errorf("trae credential requires refresh_token or access_token")
}

func (c Credential) Encode() ([]byte, error) {
	if c.Domain == "" {
		c.Domain = DomainCN
	}
	if c.APIHost == "" {
		c.APIHost = OAuthHost
	}
	if c.IDEVersion == "" {
		c.IDEVersion = IDEVersion
	}
	if c.IDEVersionCode == "" {
		c.IDEVersionCode = IDEVersionCode
	}
	return json.Marshal(c)
}

func (c Credential) Ready() bool {
	return strings.TrimSpace(c.RefreshToken) != "" && strings.TrimSpace(c.UID) != ""
}

func (c Credential) ChatBase() string    { return AgentHost }
func (c Credential) BillingBase() string { return UgHost }
func (c Credential) AuthBase() string {
	if strings.TrimSpace(c.APIHost) != "" {
		return strings.TrimRight(c.APIHost, "/")
	}
	return OAuthHost
}

func ValidateCredential(payload []byte) error {
	credential, err := DecodeCredential(payload)
	if err != nil {
		return err
	}
	if strings.TrimSpace(credential.RefreshToken) == "" && strings.TrimSpace(credential.AccessToken) == "" {
		return fmt.Errorf("trae credential requires refreshToken")
	}
	return nil
}

func (c Credential) needsRefresh(now time.Time) bool {
	if strings.TrimSpace(c.AccessToken) == "" {
		return true
	}
	if c.ExpiresAt <= 0 {
		return true
	}
	return now.Add(refreshLead).Unix() >= c.ExpiresAt
}

func (c Credential) refreshExpired(now time.Time) bool {
	return c.RefreshExpiresAt > 0 && now.Unix() >= c.RefreshExpiresAt
}

func unixSeconds(value int64) int64 {
	if value > 1e12 {
		return value / 1000
	}
	return value
}

func randomHex(n int) string {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw)
}

// randomNumericDeviceID 返回一个 16 位十进制 id，即 Trae 自家 IDE 客户端
// 作为 device_id 发送的形态。裸 32 字符十六进制 id 会被 UG 签到后端
// 拒绝（code 9074）；<=16 位的数字 id 会被接受。
func randomNumericDeviceID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%016d", time.Now().UnixNano()%1e16)
	}
	n := uint64(0)
	for _, b := range buf {
		n = n<<8 | uint64(b)
	}
	return fmt.Sprintf("%016d", n%1e16)
}

func EnsureDevice(credential Credential) Credential {
	if strings.TrimSpace(credential.MachineID) == "" {
		// Trae 的 IDE 客户端发送 64 位十六进制的 machine id；匹配该形态。
		credential.MachineID = randomHex(32)
	}
	if strings.TrimSpace(credential.DeviceID) == "" {
		credential.DeviceID = randomNumericDeviceID()
	}
	return credential
}

// EnsureDeviceKey 在凭据没有 EC P-256 设备密钥对时创建一个，
// 使 v3 code 交换能呈递一个设备公钥。
func EnsureDeviceKey(credential Credential) Credential {
	if strings.TrimSpace(credential.DevicePrivateKey) != "" {
		return credential
	}
	if priv, _, err := generateDeviceKey(); err == nil {
		credential.DevicePrivateKey = priv
	}
	return credential
}
