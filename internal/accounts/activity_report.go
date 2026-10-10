package accounts

import (
	"strings"
	"time"
)

// 对话活跃上报（activity report）配置。
//
// 上游 growth 连登由「对话活跃事件」点亮，通道为 `POST {billingBase}/v2/report`
// （见 providers/workbuddy/activity_report.go）。参照项目实测「国际版同样可用」，
// 本项目 A/B 取证亦通过（见 docs/03）。
//
// **默认关闭**：开启前需确认账号形态适配；关闭时排程零上游调用。开关与时刻按全局
// 设置存储（secret），使控制台可改而无需重启容器（与保活同一约定）。
const (
	// ActivityReportEnabledSecret 是活跃上报总开关（"1"/"0"）。缺省 = 关闭。
	ActivityReportEnabledSecret = "activity_report_enabled"
	// ActivityReportTimeSecret 是每日上报的本地 HH:MM 时刻。
	ActivityReportTimeSecret = "activity_report_time"
	// DefaultActivityReportTime 缺省触发时刻：早于常见签到窗口，使连登与签到解耦。
	DefaultActivityReportTime = "09:00"
	// ActivityReportEnvFallback 是开关的环境变量兜底名（显式置 1 时开启）。
	// 保留它使「无控制台」的部署也能开启；控制台设置优先。
	ActivityReportEnvFallback = "AGENT2API_ACTIVITY_REPORT"
)

// NormalizeActivityReportTime 校验并归一 HH:MM。
func NormalizeActivityReportTime(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) != 5 {
		return "", errActivityReportTime
	}
	if _, err := time.Parse("15:04", value); err != nil {
		return "", errActivityReportTime
	}
	return value, nil
}

// ActivityReportEnabled 报告布尔开关值是否表示「开启」。缺省（空）= 关闭。
func ActivityReportEnabled(value string) bool {
	return strings.TrimSpace(value) == "1"
}

var errActivityReportTime = errActivity("activity_report_time must use HH:mm")

type errActivity string

func (e errActivity) Error() string { return string(e) }
