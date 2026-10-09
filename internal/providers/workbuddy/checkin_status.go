package workbuddy

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// checkinStatus 是客户端读取的签到状态载荷的子集。
//
// 每个字段都刻意设为 json.RawMessage：上游在不同 realm 间对相同的键混用布尔、
// 整数和数字字符串，而一个意外的编码不得让整个载荷失败。值由 raw* 辅助函数惰性
// 解码，它们把任何无法识别的值视为「缺失」，而不是零值或 false。
type checkinStatus struct {
	Active         json.RawMessage `json:"active"`
	ActivityName   json.RawMessage `json:"activity_name"`
	TodayCheckedIn json.RawMessage `json:"today_checked_in"`
	TodayCredit    json.RawMessage `json:"today_credit"`
	DailyCredit    json.RawMessage `json:"daily_credit"`
	StreakDays     json.RawMessage `json:"streak_days"`
	TotalCredits   json.RawMessage `json:"total_credits"`
	IsStreakDay    json.RawMessage `json:"is_streak_day"`
	NextStreakDay  json.RawMessage `json:"next_streak_day"`
}

// checkinStatusReport 是该载荷解码后的、防御性类型的视图。
// Has* 标志把「上游说了 0」与「上游没说」区分开来。
type checkinStatusReport struct {
	Active         *bool
	ActivityName   string
	TodayCheckedIn bool
	TodayCredit    float64
	HasTodayCredit bool
	StreakDays     int
	HasStreakDays  bool
	TotalCredits   float64
	HasTotal       bool
	IsStreakDay    bool
}

// inactive 报告上游是否显式关闭了该活动。`active` 缺失或无法解析绝不视为未开启：
// 调用方此时会继续去领取，而不会因一次解析未命中就跳过一天。
func (r checkinStatusReport) inactive() bool {
	return r.Active != nil && !*r.Active
}

// detail 把已知的计数器渲染为一小段括号后缀，或 ""。
func (r checkinStatusReport) detail() string {
	bits := make([]string, 0, 3)
	if r.HasTodayCredit {
		bits = append(bits, "+"+formatCreditValue(r.TodayCredit)+" 积分")
	}
	if r.HasStreakDays {
		bits = append(bits, fmt.Sprintf("连续 %d 天", r.StreakDays))
	}
	if r.HasTotal {
		bits = append(bits, "累计 "+formatCreditValue(r.TotalCredits)+" 积分")
	}
	if len(bits) == 0 {
		return ""
	}
	return "（" + strings.Join(bits, "，") + "）"
}

// checkinStatusMessage 把状态明细追加到人类可读前缀之后。结果就是落入签到记录的
// 内容，因此连续签到和累计计数器无需改变 schema 就能进入控制台历史。
func checkinStatusMessage(prefix string, r checkinStatusReport) string {
	if detail := r.detail(); detail != "" {
		return prefix + detail
	}
	return prefix
}

// CheckinActivityStatus 读取 CN 签到状态。它是建议性的：调用方用它跳过冗余写入并
// 报告连续签到计数器，但失败绝不能阻断发放本身。
func (client *Client) CheckinActivityStatus(ctx context.Context, accountID string) (checkinStatusReport, error) {
	credential, err := client.resolvedCredential(ctx, accountID)
	if err != nil {
		return checkinStatusReport{}, err
	}
	body, status, err := client.do(ctx, accountID, http.MethodPost, credential.BillingBase()+pathCheckinStatus, []byte("{}"),
		func(h http.Header) { SetBillingHeaders(h, credential) })
	if err != nil {
		return checkinStatusReport{}, err
	}
	if status >= 300 {
		detail := strings.TrimSpace(string(body))
		if len(detail) > 240 {
			detail = detail[:240]
		}
		return checkinStatusReport{}, fmt.Errorf("checkin status=%d: %s", status, detail)
	}
	return parseCheckinStatus(body)
}

func parseCheckinStatus(body []byte) (checkinStatusReport, error) {
	raw := body
	// 状态对象有时在顶层，有时包在常见的 {code,msg,data} 信封里；两种情况都接受。
	var env envelope
	if json.Unmarshal(body, &env) == nil && len(env.Data) > 0 && env.Data[0] == '{' {
		raw = env.Data
	}
	var s checkinStatus
	if err := json.Unmarshal(raw, &s); err != nil {
		return checkinStatusReport{}, fmt.Errorf("checkin status parse: %w", err)
	}
	report := checkinStatusReport{
		TodayCheckedIn: rawBool(s.TodayCheckedIn),
		ActivityName:   rawString(s.ActivityName),
		IsStreakDay:    rawBool(s.IsStreakDay),
	}
	if active, ok := rawBoolOK(s.Active); ok {
		report.Active = &active
	}
	if v, ok := rawFloat(s.TodayCredit); ok {
		report.TodayCredit, report.HasTodayCredit = v, true
	} else if v, ok := rawFloat(s.DailyCredit); ok {
		report.TodayCredit, report.HasTodayCredit = v, true
	}
	if v, ok := rawFloat(s.StreakDays); ok {
		report.StreakDays, report.HasStreakDays = int(v), true
	}
	if v, ok := rawFloat(s.TotalCredits); ok {
		report.TotalCredits, report.HasTotal = v, true
	}
	return report, nil
}

// rawBool 解码一个宽松类型的布尔值，把任何无法识别的值视为 false。
func rawBool(raw json.RawMessage) bool {
	v, _ := rawBoolOK(raw)
	return v
}

func rawBoolOK(raw json.RawMessage) (bool, bool) {
	text := strings.TrimSpace(string(raw))
	switch text {
	case "", "null":
		return false, false
	case "true":
		return true, true
	case "false":
		return false, true
	}
	if f, err := strconv.ParseFloat(text, 64); err == nil {
		return f != 0, true
	}
	return false, false
}

// rawFloat 解码一个可能以 JSON 数字或数字字符串形式到达的数值。非有限值会被拒绝：
// json.Unmarshal 接受 Infinity 和 NaN，而两者都会污染排序键。
func rawFloat(raw json.RawMessage) (float64, bool) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return 0, false
	}
	if strings.HasPrefix(text, `"`) {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return 0, false
		}
		text = strings.TrimSpace(s)
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// rawString 解码一个字符串字段，其它任何类型返回 ""。
// 裸数字按其字面文本原样传递。
func rawString(raw json.RawMessage) string {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return ""
	}
	if strings.HasPrefix(text, `"`) {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	return text
}

// formatCreditValue 打印额度计数时不带多余的尾部小数
// （显示 1234 而非 1234.0），同时保留真正的小数部分。
func formatCreditValue(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}
