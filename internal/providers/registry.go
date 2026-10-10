package providers

import (
	"fmt"
	"strings"
)

type RuntimeKind string

const (
	// RuntimeInProcess 是唯一的运行时：每个 provider 适配器都在本进程内
	// 直接与其上游通信。历史上曾有按账号的 Node 子进程运行时，已完全移除。
	RuntimeInProcess RuntimeKind = "in_process"
)

type AuthType string

const (
	AuthNone  AuthType = "none"
	AuthOAuth AuthType = "oauth"
	AuthPAT   AuthType = "pat"
)

// ProviderCapabilities 是控制台渲染所用的：这些标记决定 provider 提供哪些
// 标签页与操作。两条规则使其保持诚实，因为一个过度声明的标记会产生一个
// 总是失败的按钮：
//
//   - Chat、ModelCatalog、Login、BrowserLogin、Checkin 和 Growth 描述适配器
//     接线，并由 internal/app 中的 TestCapabilitiesMatchAdapterWiring 针对
//     Adapter.Supports 断言。
//   - ImportExport 不描述适配器接线：已不存在 ImportExporter。导入由凭据
//     codec 的 CredentialImporter 提供，或者——对于拥有原生凭据格式的
//     provider——由控制面导入的专用分支提供。TestCapabilitiesMatchAdapterWiring
//     断言所声明的导入拥有这两条路径之一。
//   - PATLogin 为手工维护，目前处处为 false。LoginPAT 还要求粘贴的
//     {"api_key": …} payload 通过 codec 的 PrepareImport，这只在该 provider
//     的 token 确为 personal access token 时成立。
type ProviderCapabilities struct {
	Chat         bool `json:"chat"`
	Stream       bool `json:"stream"`
	Tools        bool `json:"tools"`
	Images       bool `json:"images"`
	Reasoning    bool `json:"reasoning"`
	ModelCatalog bool `json:"model_catalog"`
	Usage        bool `json:"usage"`
	Login        bool `json:"login"`
	BrowserLogin bool `json:"browser_login"`
	PATLogin     bool `json:"pat_login"`
	ImportExport bool `json:"import_export"`
	// Growth 描述适配器的成长中心接线（Adapter.Growth）。控制台的
	// 成长中心账号选择器据此过滤——把没有活动中心的渠道整个排除，
	// 而不是让用户选中后才以 provider_unsupported 作答。
	Growth bool `json:"growth"`
}

type RegionDescriptor struct {
	ID            string         `json:"id"`
	Label         string         `json:"label"`
	ChatBase      string         `json:"chat_base"`
	BillingBase   string         `json:"billing_base"`
	AuthBase      string         `json:"auth_base"`
	DefaultDomain string         `json:"default_domain"`
	Checkin       *CheckinPolicy `json:"checkin,omitempty"`
}

type ProviderDescriptor struct {
	ID                string               `json:"id"`
	Label             string               `json:"label"`
	Runtime           RuntimeKind          `json:"runtime"`
	AuthTypes         []AuthType           `json:"auth_types"`
	CredentialFormats []string             `json:"credential_formats"`
	Capabilities      ProviderCapabilities `json:"capabilities"`
	Regions           []RegionDescriptor   `json:"regions"`
	DefaultRegion     string               `json:"default_region"`
}

func (d ProviderDescriptor) Region(id string) (RegionDescriptor, bool) {
	for _, region := range d.Regions {
		if region.ID == id {
			return region, true
		}
	}
	return RegionDescriptor{}, false
}

func (d ProviderDescriptor) SupportsCredentialFormat(format string) bool {
	for _, f := range d.CredentialFormats {
		if f == format {
			return true
		}
	}
	return false
}

func (d ProviderDescriptor) SupportsAuthType(auth AuthType) bool {
	for _, a := range d.AuthTypes {
		if a == auth {
			return true
		}
	}
	return false
}

// WorkBuddy 描述符。协议常量保留在 internal/providers/workbuddy；
// registry 只携带控制面元数据。
var WorkBuddy = ProviderDescriptor{
	ID:                "workbuddy",
	Label:             "WorkBuddy",
	Runtime:           RuntimeInProcess,
	AuthTypes:         []AuthType{AuthNone, AuthOAuth},
	CredentialFormats: []string{"workbuddy-oauth-v1"},
	Capabilities: ProviderCapabilities{
		Chat: true, Stream: true, Tools: true, Images: false, Reasoning: true,
		ModelCatalog: true, Usage: true, Login: true, BrowserLogin: true,
		PATLogin: false, ImportExport: true, Growth: true,
	},
	Regions: []RegionDescriptor{
		{
			ID: "cn", Label: "CN", ChatBase: "https://copilot.tencent.com",
			BillingBase: "https://www.codebuddy.cn", AuthBase: "https://copilot.tencent.com",
			DefaultDomain: "codebuddy.cn",
			Checkin:       &CheckinPolicy{Timezone: "Asia/Shanghai"},
		},
		{
			ID: "global", Label: "Global", ChatBase: "https://www.workbuddy.ai",
			BillingBase: "https://www.workbuddy.ai", AuthBase: "https://www.workbuddy.ai",
			DefaultDomain: "workbuddy.ai",
			Checkin:       &CheckinPolicy{Timezone: "Asia/Shanghai"},
		},
		{
			// CodeBuddy Intl (IDE) realm 是第三个部署：Global token 在那里
			// 不被接受，反之亦然。故意不声明 CheckinPolicy —— Intl 的签到
			// 契约未知，且用 Intl token 调用 CN 端点会被网关拒绝。将 Checkin
			// 留为 nil 使运行时报告其为 unsupported。
			ID: "intl", Label: "Intl", ChatBase: "https://www.codebuddy.ai",
			BillingBase: "https://www.codebuddy.ai", AuthBase: "https://www.codebuddy.ai",
			DefaultDomain: "codebuddy.ai",
		},
	},
	DefaultRegion: "cn",
}

// Trae 描述符。协议常量保留在 internal/providers/trae；
// registry 只携带控制面元数据。CN Solo 是唯一已实现的区域；
// 不要拉起官方 traecli。
var Trae = ProviderDescriptor{
	ID:                "trae",
	Label:             "Trae",
	Runtime:           RuntimeInProcess,
	AuthTypes:         []AuthType{AuthOAuth},
	CredentialFormats: []string{"trae-oauth-v1"},
	Capabilities: ProviderCapabilities{
		Chat: true, Stream: true, Tools: true, Images: false, Reasoning: true,
		ModelCatalog: true, Usage: true, Login: true, BrowserLogin: true,
		PATLogin: false, ImportExport: true,
	},
	Regions: []RegionDescriptor{
		{
			ID: "cn", Label: "CN Solo", ChatBase: "https://trae-api-cn.mchost.guru",
			BillingBase: "https://api.trae.cn", AuthBase: "https://api.trae.com.cn",
			DefaultDomain: "trae.cn",
			Checkin:       &CheckinPolicy{Timezone: "Asia/Shanghai"},
		},
	},
	DefaultRegion: "cn",
}

// builtinProviders 是内置 provider 集合的唯一事实来源。
// 查找 registry 和 List() 都派生自它，因此 provider 不再可能被加入一个
// 面而被遗忘在另一个面——这正是此前需要让手写 map 和手写 slice 保持
// 同步的失败模式。
var builtinProviders = []ProviderDescriptor{WorkBuddy, Trae}

// registry 是 init 时由 builtinProviders 构建的可变查找索引。
// 只有 RegisterTestDescriptor 会修改它（一个测试接缝），这就是查找通过
// map 进行、而非每次调用都扫描 builtinProviders 的原因。
var registry = func() map[string]ProviderDescriptor {
	index := make(map[string]ProviderDescriptor, len(builtinProviders))
	for _, d := range builtinProviders {
		index[canonicalProviderID(d.ID)] = d
	}
	return index
}()

func Get(id string) (ProviderDescriptor, bool) {
	d, ok := registry[canonicalProviderID(id)]
	return d, ok
}

// RegisterTestDescriptor 将 d 加入查找 registry，替换同 ID 的任何已有条目，
// 并返回一个恢复函数。它的存在是为了让测试能演练 registry 驱动的行为
// （例如“一个没有签到策略的 provider”），而不依赖可能被增删的真实
// provider。它是一个测试接缝：生产代码不得调用它。List() 不受影响，
// 因为它读取 builtinProviders，而非此 map。
func RegisterTestDescriptor(d ProviderDescriptor) func() {
	id := canonicalProviderID(d.ID)
	prev, existed := registry[id]
	registry[id] = d
	return func() {
		if existed {
			registry[id] = prev
		} else {
			delete(registry, id)
		}
	}
}

// List 按注册顺序返回内置 provider 描述符的副本。该副本防止调用方
// 修改共享的描述符集合。
func List() []ProviderDescriptor {
	out := make([]ProviderDescriptor, len(builtinProviders))
	copy(out, builtinProviders)
	return out
}

// Resolve 校验 provider/region 组合。provider 没有隐式默认：
// 空值将以 unknown provider 报错。
func Resolve(provider, region string) (ProviderDescriptor, RegionDescriptor, error) {
	p := strings.TrimSpace(strings.ToLower(provider))
	r := strings.TrimSpace(strings.ToLower(region))
	descriptor, ok := Get(p)
	if !ok {
		return ProviderDescriptor{}, RegionDescriptor{}, fmt.Errorf("unknown provider %q", provider)
	}
	if r == "" {
		r = descriptor.DefaultRegion
	}
	regionDesc, ok := descriptor.Region(r)
	if !ok {
		return ProviderDescriptor{}, RegionDescriptor{}, fmt.Errorf("unknown region %q for provider %q", region, descriptor.ID)
	}
	return descriptor, regionDesc, nil
}

// ValidateCredentialFormat 拒绝 provider 无法存储的格式。
func ValidateCredentialFormat(provider, format string) error {
	descriptor, _, err := Resolve(provider, "")
	if err != nil {
		return err
	}
	if !descriptor.SupportsCredentialFormat(format) {
		return fmt.Errorf("credential format %q is not supported by provider %q", format, descriptor.ID)
	}
	return nil
}
