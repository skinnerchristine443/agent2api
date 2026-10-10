// Package trae 实现 Trae CN Solo 进程内 provider。
// 协议常量只存在于本包内。
package trae

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
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

// DecodeCredential 解码存储 / 导入的凭据载荷，并把超界的存量设备号
// 归一（见 normalizeDeviceID）。导入、登录、刷新、聊天与签到都经由本
// 函数取号，因此一处归一即全链路生效。
func DecodeCredential(payload []byte) (Credential, error) {
	credential, err := decodeCredential(payload)
	if err != nil {
		return credential, err
	}
	credential.DeviceID = normalizeDeviceID(credential.DeviceID)
	return credential, nil
}

func decodeCredential(payload []byte) (Credential, error) {
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

// deviceIDRejectAt 是实测观察到的拒绝分界：x-device-id 数值 >= 此值的号
// 一律被 UG 签到后端拒绝（9074，文案「当前参与用户太多」是误导）。
// 同池 15 个 16 位号按数值完美切分——首位 0–3（< 4.0e15，10 个）全部正常、
// 首位 5–9（> 5.0e15，5 个）全部 9074，分界落在 2^52。
const deviceIDRejectAt uint64 = 1 << 52

// deviceIDRejectDigits 是 deviceIDRejectAt 的十进制形态（16 位），
// 供等长数字串做字典序比较，避免为此引入大整数依赖。
const deviceIDRejectDigits = "4503599627370496"

// deviceIDCeiling 是本仓产出设备号的目标上界。取 1e15 —— 比实测拒绝分界
// 低一整个量级，落在**已在生产验证可用的区间**内（10 个在役号均 < 4.0e15，
// 随机 15 位号实测亦通过），不与尚未探明的边界贴边。形态仍是 16 位零填充。
const deviceIDCeiling uint64 = 1_000_000_000_000_000

// randomNumericDeviceID 返回一个 16 位十进制 id，即 Trae 自家 IDE 客户端
// 作为 device_id 发送的形态（客户端真号同样以 0 补满 16 位）。裸 32 字符
// 十六进制 id 会被 UG 签到后端拒绝（code 9074）；取值必须小于
// deviceIDRejectAt，否则同样 9074——这里直接钉在 deviceIDCeiling 以下。
func randomNumericDeviceID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%016d", uint64(time.Now().UnixNano())%deviceIDCeiling)
	}
	n := uint64(0)
	for _, b := range buf {
		n = n<<8 | uint64(b)
	}
	return fmt.Sprintf("%016d", n%deviceIDCeiling)
}

// normalizeDeviceID 把超出可接受范围的存量设备号确定性地压回安全区。
//
// 只处理「纯数字且数值 >= deviceIDRejectAt」的号：空值、非数字、拒绝分界
// 以下的号一律原样返回，因此对现存可用号零副作用。映射用取模 ⇒ 同一输入
// 恒得同一输出，不会在请求之间抖动（设备号反复变化本身就是风控信号）。
func normalizeDeviceID(deviceID string) string {
	trimmed := strings.TrimSpace(deviceID)
	if trimmed == "" || !isAllDigits(trimmed) {
		return deviceID
	}
	significant := strings.TrimLeft(trimmed, "0")
	if significant == "" || len(significant) < len(deviceIDRejectDigits) {
		return deviceID // 位数少于分界 ⇒ 必然在范围内
	}
	if len(significant) == len(deviceIDRejectDigits) {
		if significant < deviceIDRejectDigits {
			return deviceID
		}
	} else {
		// 位数多于分界：截取低位参与映射（同一输入恒定）
		significant = significant[len(significant)-len(deviceIDRejectDigits):]
	}
	value, err := strconv.ParseUint(significant, 10, 64)
	if err != nil {
		return deviceID
	}
	mapped := value % deviceIDCeiling
	if mapped == 0 {
		mapped = 1
	}
	return fmt.Sprintf("%016d", mapped)
}

func EnsureDevice(credential Credential) Credential {
	// 历史生成器会产出超界号：这里先归一，再对空值补新号，
	// 使导入与登录落盘的号必定可用。
	credential.DeviceID = normalizeDeviceID(credential.DeviceID)
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
