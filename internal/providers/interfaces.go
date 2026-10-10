package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"agent2api/internal/translate"
)

// ErrUnsupported 由 provider 未实现的能力接口返回。调用方必须把 nil 适配器
// 视为缺少注册，而非隐式支持。
var ErrUnsupported = errors.New("provider capability unsupported")

type ModelCapabilities struct {
	ContextWindow    int `json:"context_window,omitempty"`
	ContextWindowMax int `json:"context_window_max,omitempty"`
	MaxOutput        int `json:"max_output_tokens,omitempty"`
	PromptMaxTokens  int `json:"prompt_max_tokens,omitempty"`
	// MaxMode 分级：当模型声明了第二个（Max/Max-mode）分级时，这些字段
	// 携带 Max 分级的 prompt/output 上限，使控制台能显示与 max-mode 开关
	// 匹配的值，而不是默认分级的值。
	PromptMaxTokensMax int      `json:"prompt_max_tokens_max,omitempty"`
	MaxOutputMax       int      `json:"max_output_tokens_max,omitempty"`
	MaxMode            bool     `json:"max_mode,omitempty"`
	Tools              bool     `json:"tools"`
	Images             bool     `json:"images"`
	Reasoning          bool     `json:"reasoning"`
	ReasoningOptions   []string `json:"reasoning_options,omitempty"`
	ReasoningDefault   string   `json:"reasoning_default,omitempty"`
	ReasoningType      string   `json:"reasoning_type,omitempty"`
	CanDisableThinking bool     `json:"can_disable_thinking,omitempty"`
}

type ModelInfo struct {
	NativeModel  string            `json:"native_model"`
	PublicModel  string            `json:"public_model"`
	DisplayName  string            `json:"display_name,omitempty"`
	Credits      string            `json:"credits,omitempty"`
	Free         bool              `json:"free,omitempty"`
	Capabilities ModelCapabilities `json:"capabilities"`
	// Rate 是模型在该 provider 上声明的消耗倍率（如 Trae 的 consumption
	// rate）。0 是表示免费的真实值；在按免费处理前
	// 必须先查 RateKnown。调度层偏好低倍率，但绝不能把未上报的倍率当成免费。
	Rate float64 `json:"-"`
	// RateKnown 表示 provider 是否真的声明了倍率。为 false 时，Rate 无意义，
	// 必须当作“未知”处理，绝不是 0。
	RateKnown bool `json:"-"`
	// Scene 是该模型的对话必须使用的 provider 原生 scene/function。
	// 为空表示该 provider 没有 scene 划分。provider 用它把模型路由到
	// 真正为其提供服务的 scene；它是 provider 内部的，绝非公开 API 字段。
	Scene string `json:"-"`
}

// CredentialCodec 校验并存储 provider 凭据。
type CredentialCodec interface {
	Validate(payload []byte) error
}

// LoginSession 描述某个账号的一轮浏览器登录。
type LoginSession struct {
	AuthURL string `json:"auth_url"`
	State   string `json:"state,omitempty"`
}

// LoginSessionProvider 启动并轮询 provider 原生浏览器登录。
type LoginSessionProvider interface {
	StartLogin(ctx context.Context, accountID string) (LoginSession, error)
	PollLogin(ctx context.Context, accountID string) (done bool, message string, err error)
}

// LoginCompleter 接收从浏览器复制的 provider 回调 URL，
// 用于自动 loopback 重定向无法到达本进程的场景。
type LoginCompleter interface {
	CompleteLogin(ctx context.Context, accountID, callbackURL string) error
}

// ChatOutcome 是与 provider 无关的非流式结果。
type ChatOutcome struct {
	Model            string
	Content          string
	Reasoning        string
	ToolCalls        json.RawMessage
	FinishReason     string
	PromptTokens     int
	CompletionTokens int
	CacheReadTokens  *int
	CacheWriteTokens *int
	UsageSource      string
	Credits          *float64
	// ReasoningLevel 是实际发往上游的、经钳制的推理等级；
	// 当 provider 未在 payload 中包含时为空。
	ReasoningLevel string
}

// ResolvedChat 携带流式请求的 provider 侧元数据，
// API 层可能希望将其与转发的上游响应一同记录。
type ResolvedChat struct {
	// ReasoningLevel 是实际发往上游的、经钳制的推理等级；
	// 当 provider 未在 payload 中包含时为空。
	ReasoningLevel string
}

// ProviderChat 为单个账号执行对话。流式实现返回
// 原始上游响应，供 API 层转发。
type ProviderChat interface {
	ChatNonStream(ctx context.Context, accountID string, req translate.ChatRequest) (ChatOutcome, error)
	ChatStream(ctx context.Context, accountID string, req translate.ChatRequest) (*http.Response, ResolvedChat, error)
}

// RequestOptions 是执行器为某个被选中的账号所做的每次尝试的执行决策。
// 它们显式传递，绝不通过 ctx，并在每次故障转移尝试时重新计算。
type RequestOptions struct {
	// Model 是为该账号解析出的上游模型 id（其 catalog 中的拼写），
	// 而非客户端发送的公开 id。
	Model string
	// DropSystemPrompt 在发送前移除调用方的 system/developer prompt
	// （针对带内容审查的上游的账号策略）。
	DropSystemPrompt bool
}

// NativeResponsesStreamer 是可选的 OpenAI Responses 原生能力。
// 适配器仅在其上游使用 Responses 协议、且能从客户端原始 Responses body
// 构建上游请求时实现它，从而保留 item 身份、reasoning 重放和未知成员。
// 返回的 body 始终是上游 Responses SSE 流；非流式客户端调用会将其收集起来。
// 未实现它的适配器通过 ChatStream / ChatNonStream 和共享 chat 形式
// 服务 /v1/responses。
type NativeResponsesStreamer interface {
	ResponsesStream(ctx context.Context, accountID string, req *translate.NativeResponsesRequest, options RequestOptions) (*http.Response, ResolvedChat, error)
}

// ModelCatalogProvider 列出账号当前可服务的模型。
type ModelCatalogProvider interface {
	Models(ctx context.Context, accountID string) ([]ModelInfo, error)
}

// ErrorClassifier 将 provider 错误映射到内部分类体系。
type ErrorClassifier interface {
	Classify(status int, body string) ClassifiedError
}

type ClassifiedError struct {
	Kind    string `json:"kind"`
	Status  int    `json:"status"`
	Message string `json:"message"`
}

// AccountHealth 是进程内账号的、与 provider 无关的就绪快照
// （不存在按账号的 /health 端点）。
type AccountHealth struct {
	Ready     bool
	Hot       bool
	UID       string
	InFlight  int
	LastError string
}

// QuotaWindow 是一个 provider 原生用量周期。控制台卡片可以渲染
// 每个窗口；路由仍使用更严格的顶层 QuotaInfo 值。
type QuotaWindow struct {
	ID         string
	Label      string
	Used       float64
	Total      float64
	Remaining  float64
	Percentage float64
	Unit       string
	ResetAt    string
	Exceeded   bool
}

// QuotaPackage 是一个带自身过期时间的上游额度/资源包。
// EndsAt 是 Unix 秒；EndTime 是 provider 原生的挂钟时间字符串。
type QuotaPackage struct {
	Remain  float64
	Used    float64
	Size    float64
	Unit    string
	EndsAt  int64
	EndTime string
}

// QuotaInfo 是面向控制台和已耗尽账号路由的账号用量。
// 调用方必须独立看待探测就绪与配额；配额错误绝不翻转 Ready。
type QuotaInfo struct {
	Used       float64
	Total      float64
	Remaining  float64
	Percentage float64
	Unit       string
	Exceeded   bool
	FetchedAt  string
	Windows    []QuotaWindow
	// ProviderID 标识产生此信息的适配器。调用方用它来对 provider 特有字段
	// （如 package 过期时间）做门控，而不是假定每个适配器都会填充它们。
	ProviderID string
	// Plan 是上游订阅层级，例如 Codex 的 plan_type "plus"。
	// 为空表示 provider 未上报。
	Plan string
	// ExpiresAt 是最早的 package 过期时间（Unix 秒）；0 表示 provider
	// 未上报。ExpiringRemain 是在该时间过期的剩余额度。
	// Packages 携带每个 pack 的过期明细。
	ExpiresAt      int64
	ExpiringRemain float64
	Packages       []QuotaPackage
}

// AccountProber 刷新 provider 原生就绪状态与可选的展示配额。
type AccountProber interface {
	Probe(ctx context.Context, accountID string) (AccountHealth, error)
	Quota(ctx context.Context, accountID string) (*QuotaInfo, error)
}

// Error 是进程内适配器错误，执行器会为其分类以实现
// 冷却与故障转移。Kind 必须是 accounts 的分类值。
type Error struct {
	Kind       string
	Status     int
	Message    string
	Code       string
	Type       string
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return e.Kind
}

// Adapter 打包可选的能力接口。每个字段都可能为 nil；
// 调用前用 Supports() 判断，使不支持的路径显式失败。
type Adapter struct {
	ID         string
	Credential CredentialCodec
	Login      LoginSessionProvider
	Chat       ProviderChat
	Models     ModelCatalogProvider
	Classifier ErrorClassifier
	Prober     AccountProber
	Checkin    AccountCheckiner
	// Growth 是可选的成长中心能力。对于没有成长活动中心的
	// provider 均为 nil。
	Growth AccountGrowthRunner
	// GrowthSummary 是可选的轻量成长计数（跨账号总览用，只打任务清单）。
	// 为 nil 时总览跳过该渠道的账号。
	GrowthSummary AccountGrowthSummarizer
	// NativeResponses 是可选的原生 Responses 能力。设置后，
	// 路由到该适配器的 /v1/responses 请求会跳过 chat 形式。
	NativeResponses NativeResponsesStreamer
}

func (a Adapter) Supports(capability string) bool {
	switch capability {
	case "credential":
		return a.Credential != nil
	case "login":
		return a.Login != nil
	case "chat":
		return a.Chat != nil
	case "models":
		return a.Models != nil
	case "classifier":
		return a.Classifier != nil
	case "prober":
		return a.Prober != nil
	case "checkin":
		return a.Checkin != nil
	case "growth":
		return a.Growth != nil
	case "native_responses":
		return a.NativeResponses != nil
	default:
		return false
	}
}

// CredentialImport 在不持久化的情况下准备规范化 payload。Ready 控制
// 导入的账号是否可以立即启用。
type CredentialImport struct {
	Payload []byte
	Ready   bool
}
type CredentialImporter interface {
	Format() string
	PrepareImport([]byte) (CredentialImport, error)
}

type ActionError struct {
	Code string
	Err  error
}

func (e *ActionError) Error() string { return e.Err.Error() }
func (e *ActionError) Unwrap() error { return e.Err }
