package trae

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
)

// AlreadyCheckedInError 标记上游的 "already checked in" 业务状态。
// 运行时将其记录为 CheckinResult{Status: providers.CheckinStatusAlready}，且不报错。
type AlreadyCheckedInError struct {
	Msg string
}

func (e AlreadyCheckedInError) Error() string {
	if strings.TrimSpace(e.Msg) == "" {
		return "trae already checked in"
	}
	return e.Msg
}

func (AlreadyCheckedInError) AlreadyCheckedIn() bool { return true }

// Checkin 通过 ug checkin_credits API 领取 Trae 的每日额度发放。
// 流程：如需则刷新凭据 → status 探测 → 符合条件时领取 →
// 重新探测，使只有在账号确实翻转时才报告成功。
// "今日已签到" 映射为 Status="already"；会话失效时暴露 KindAuth，
// 使管理器能将该账号标记为需要重新登录。
func (client *Client) Checkin(ctx context.Context, accountID string) (providers.CheckinResult, error) {
	credential, err := client.credential(ctx, accountID)
	if err != nil {
		return providers.CheckinResult{}, err
	}
	// 整个流程（status / claim / re-check）使用同一个 device id。
	credential = checkinCredential(credential)
	st, err := client.checkinStatus(ctx, accountID, credential)
	if err != nil {
		var already AlreadyCheckedInError
		if errors.As(err, &already) {
			return providers.CheckinResult{Status: providers.CheckinStatusAlready, Message: already.Msg}, nil
		}
		return providers.CheckinResult{}, err
	}
	if st.checkedIn {
		return providers.CheckinResult{
			Status:        providers.CheckinStatusAlready,
			Message:       "already checked in",
			RewardCredits: float64(st.credits + st.extraCredits),
		}, nil
	}
	if !st.enable {
		return providers.CheckinResult{Status: providers.CheckinStatusSkipped, Message: "checkin disabled"}, nil
	}
	if err := client.checkinClaim(ctx, accountID, credential); err != nil {
		var already AlreadyCheckedInError
		if errors.As(err, &already) {
			return providers.CheckinResult{Status: providers.CheckinStatusAlready, Message: already.Msg}, nil
		}
		return providers.CheckinResult{}, err
	}
	// 即便设备身份被拒（9074）或每日发放已被领取，claim 端点也返回 code 0，
	// 因此成功与否由重新探测决定：真正的领取会把 checked_in 翻转为 true。
	after, err := client.checkinStatus(ctx, accountID, credential)
	if err != nil {
		return providers.CheckinResult{}, err
	}
	if !after.checkedIn {
		return providers.CheckinResult{}, fmt.Errorf("trae checkin did not register (device may be rejected); retry later")
	}
	return providers.CheckinResult{
		Status:        providers.CheckinStatusSuccess,
		Message:       "checkin claimed",
		RewardCredits: float64(after.credits + after.extraCredits),
	}, nil
}

type checkinState struct {
	checkedIn    bool
	credits      int64
	extraCredits int64
	enable       bool
}

// checkinStatus 解码 ug checkin_credits/status 响应。业务上的
// "already checked in" 会产生 AlreadyCheckedInError，使调用方能返回
// Status="already" 而不将其视为失败。
func (client *Client) checkinStatus(ctx context.Context, accountID string, credential Credential) (checkinState, error) {
	body, err := client.CheckinStatus(ctx, accountID, credential)
	if err != nil {
		return checkinState{}, err
	}
	text := strings.TrimSpace(string(body))
	classified := Classify(200, text)
	if classified.Kind == accounts.KindAuth {
		return checkinState{}, fmt.Errorf("trae checkin session dead: re-login required")
	}
	if msg, ok := alreadyCheckedInMessage(text); ok {
		return checkinState{}, AlreadyCheckedInError{Msg: msg}
	}
	var env struct {
		CheckedIn    bool  `json:"checked_in"`
		Credits      int64 `json:"credits"`
		ExtraCredits int64 `json:"extra_credits"`
		Enable       bool  `json:"enable"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return checkinState{}, fmt.Errorf("checkin status parse: %w", err)
	}
	return checkinState{checkedIn: env.CheckedIn, credits: env.Credits, extraCredits: env.ExtraCredits, enable: env.Enable}, nil
}

// checkinClaim 发送 ug checkin_credits/claim 请求。成功时响应为
// HTTP 200 携带业务 body {"code":0}（幂等），或当设备身份被拒时
// 返回非零 code（如 9074）。只有 "already checked in" 标记被当作
// 软成功；其他非零 code 都作为错误暴露。
func (client *Client) checkinClaim(ctx context.Context, accountID string, credential Credential) error {
	body, err := client.CheckinClaim(ctx, accountID, credential)
	if err != nil {
		return err
	}
	text := strings.TrimSpace(string(body))
	classified := Classify(200, text)
	if classified.Kind == accounts.KindAuth {
		return fmt.Errorf("trae checkin session dead: re-login required")
	}
	if msg, ok := alreadyCheckedInMessage(text); ok {
		return AlreadyCheckedInError{Msg: msg}
	}
	var env struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("checkin claim parse: %w", err)
	}
	if env.Code != 0 {
		return fmt.Errorf("trae checkin claim rejected (code %d): %s", env.Code, strings.TrimSpace(env.Message))
	}
	return nil
}

// alreadyCheckedInMessage 匹配上游的 "今日已签到" 业务错误。
// 只匹配无歧义的标记，这样仅仅包含 "checkin" 的 429/5xx body
// 不会被误分类。
func alreadyCheckedInMessage(text string) (string, bool) {
	s := strings.ToLower(text)
	if strings.Contains(s, "已签到") ||
		strings.Contains(s, "already check") ||
		strings.Contains(s, "already checked") {
		return "already checked in", true
	}
	return "", false
}
