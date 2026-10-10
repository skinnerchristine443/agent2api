package workbuddy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// 对话活跃上报（growth 域）。
//
// 参照项目 workbuddy2api-panel 的实现（internal/upstream/report.go）实测：
// `POST {billingBase}/v2/report` 携带一条 `chat_request_send` 事件即可
// **点亮 growth 连登（streak）**，且 **CN 与 global（workbuddy.ai）都可用**
// （参照项目 PR #45 实测；此前「global 无活跃体系」的判断已被推翻）。
//
// 与既有 DailyActivity 的区别：DailyActivity 是 Web 控制台 ACP-over-SSE
// 会话驱动（activity.go，900+ 行，用于**领取每日额度发放**）；本文件是一次
// HTTP POST（用于**点亮连登**）。两者用途不同，互不替代。
//
// 实测教训（参照项目 REPORT-active-map.md §2，本实现照抄防护）：
//   - `userId` 必填（= 账号 uid）；缺失时服务端**返回 200 但静默丢弃**。
//     故上报后必须回读 streak 验证（见 VerifyActivityStreak），不能只看 200。
//   - 事件用**全字段**形状（参照项目 34 字段），不能用最小 3 字段——防上游
//     后续加严校验（referenced 事件形状来自客户端 `chat_request_send`）。
//   - `conversationId` 由调用方生成即可，**无需真实会话**（服务端不校验一致性）。

// reportPath 活跃上报通道（参照项目实测）。
const reportPath = "/v2/report"

// activityEventCode 客户端上报的事件码（参照项目 chat_request_send）。
const activityEventCode = "chat_request_send"

// DefaultActivityModelID / Name 是活跃上报的默认模型（参照项目 ReportChatActivity
// 的默认值：deepseek-v4-flash）。上报只点亮连登，不做真实推理，模型名仅用于形状。
const (
	DefaultActivityModelID   = "deepseek-v4-flash"
	DefaultActivityModelName = "DeepSeek V4 Flash"
)

// chatRequestEvent 是客户端 `chat_request_send` 事件的完整形状。
//
// 字段与参照项目 report.go 的 chatRequestEvent **逐字对齐**（含全部 34 个字段）。
// 刻意不用最小集：上游可能对形状加严，缺字段会从「显式报错」退化为「200 静默丢弃」，
// 那是最难排查的失败模式。
type chatRequestEvent struct {
	EventCode             string `json:"eventCode"`
	Timestamp             int64  `json:"timestamp"`
	ReportDelay           int    `json:"reportDelay"`
	Mode                  string `json:"mode"`
	ConversationID        string `json:"conversationId"`
	RequestID             string `json:"requestId"`
	InputLength           int    `json:"inputLength"`
	RequestModelID        string `json:"requestModelId"`
	RequestModelName      string `json:"requestModelName"`
	IsPlan                bool   `json:"isPlan"`
	IsAutoExecuteTerminal bool   `json:"isAutoExecuteTerminal"`
	IsAutoModify          bool   `json:"isAutoModify"`
	CodebaseEnable        bool   `json:"codebaseEnable"`
	MaxToken              int    `json:"maxToken"`
	MaxSteps              int    `json:"maxSteps"`
	Temperature           int    `json:"temperature"`
	MaxRetries            int    `json:"maxRetries"`
	MentionContexts       []any  `json:"mentionContexts"`
	KnowledgeID           []any  `json:"knowledgeId"`
	KnowledgeName         []any  `json:"knowledgeName"`
	CodebaseID            string `json:"codebaseId"`
	MentionContextCount   int    `json:"mentionContextCount"`
	Command               string `json:"command"`
	ExpertID              string `json:"expertId"`
	RecommendID           string `json:"recommendId"`
	SkillID               string `json:"skillId"`
	SkillCount            int    `json:"skillCount"`
	TotalCount            int    `json:"totalCount"`
	FileURI               string `json:"fileUri"`
	PresentAt             int64  `json:"presentAt"`
	TraceID               string `json:"traceId"`
	RootRequestID         string `json:"rootRequestId"`
	ParentConversationID  string `json:"parentConversationId"`
	AgentName             string `json:"agentName"`
	AgentType             string `json:"agentType"`
	UserID                string `json:"userId"`
}

// ActivityReportID 生成一次活跃上报用的会话/请求标识。
// 参照项目用 `wb2api-<ms>` / `wb2api-adopt-<ms>` 等前缀；本网关统一用 `agent2api-<ms>`。
func ActivityReportID() string {
	return fmt.Sprintf("agent2api-%d", time.Now().UnixMilli())
}

// ReportChatActivity 向上游发送一条对话活跃上报（`chat_request_send`）。
//
// conversationID 由调用方生成（见 ActivityReportID）——服务端不校验它对应真实会话。
// requestID 为空时回落 conversationID（与参照项目语义一致）。
// modelID / modelName 为空时用默认（deepseek-v4-flash）。
//
// 成功语义：HTTP 2xx 且业务 code==0。**注意 200 不等于计分**——上游在 userId
// 缺失等情况下会 200 静默丢弃，故调用方应配合 VerifyActivityStreak 回读验证。
func (client *Client) ReportChatActivity(ctx context.Context, accountID, conversationID, requestID, modelID, modelName string) error {
	credential, err := client.resolvedCredential(ctx, accountID)
	if err != nil {
		return err
	}
	if conversationID == "" {
		conversationID = ActivityReportID()
	}
	if requestID == "" {
		requestID = conversationID
	}
	if modelID == "" {
		modelID = DefaultActivityModelID
	}
	if modelName == "" {
		modelName = modelID
	}
	// userId 必填：缺失会导致上游 200 静默丢弃。UID 为空直接判为调用方错误，
	// 不浪费一次上游请求（也避免把「静默丢弃」误判为成功）。
	if credential.UID == "" {
		return fmt.Errorf("activity report requires uid (userId is mandatory; missing causes silent drop)")
	}

	now := time.Now().UnixMilli()
	event := chatRequestEvent{
		EventCode:             activityEventCode,
		Timestamp:             now,
		ReportDelay:           0,
		Mode:                  "craft",
		ConversationID:        conversationID,
		RequestID:             requestID,
		InputLength:           12,
		RequestModelID:        modelID,
		RequestModelName:      modelName,
		IsPlan:                false,
		IsAutoExecuteTerminal: false,
		IsAutoModify:          false,
		CodebaseEnable:        false,
		MaxToken:              0,
		MaxSteps:              0,
		Temperature:           0,
		MaxRetries:            0,
		MentionContexts:       []any{},
		KnowledgeID:           []any{},
		KnowledgeName:         []any{},
		CodebaseID:            "",
		MentionContextCount:   0,
		Command:               "",
		ExpertID:              "",
		RecommendID:           "",
		SkillID:               "",
		SkillCount:            0,
		TotalCount:            0,
		FileURI:               "",
		PresentAt:             now,
		TraceID:               "",
		RootRequestID:         requestID,
		ParentConversationID:  conversationID,
		AgentName:             "default",
		AgentType:             "conversation",
		UserID:                credential.UID,
	}
	// 上游期望一个事件数组（参照项目同为 []chatRequestEvent）。
	payload, err := json.Marshal([]chatRequestEvent{event})
	if err != nil {
		return err
	}
	body, status, err := client.do(ctx, accountID, "POST", credential.BillingBase()+reportPath, payload,
		func(h http.Header) { SetBillingHeaders(h, credential) })
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("activity report status=%d: %s", status, growthBodyText(body))
	}
	// 解信封：业务 code != 0 视为失败（与 growth 域一致）。
	if _, err := growthUnwrap(body); err != nil {
		return fmt.Errorf("activity report: %w", err)
	}
	return nil
}

// ActivityStreakDays 回读当前连登天数（只读 oracle）。
// 用于 VerifyActivityStreak：上报 200 后回读，确认 streak 真的计分
// （发现「200 但静默丢弃」）。返回值为 0 且 err==nil 表示上游未计分。
func (client *Client) ActivityStreakDays(ctx context.Context, accountID string) (int, error) {
	credential, err := client.resolvedCredential(ctx, accountID)
	if err != nil {
		return 0, err
	}
	raw, err := client.growthRead(ctx, accountID, credential, pathGrowthStreak)
	if err != nil {
		return 0, err
	}
	streak, err := parseGrowthStreak(raw)
	if err != nil {
		return 0, err
	}
	return streak.Days, nil
}
