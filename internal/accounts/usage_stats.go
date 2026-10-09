package accounts

import "time"

// UsageStats 是控制台用量仪表盘背后的只读 token/请求汇总。每个桶都在
// 查询时从 request_logs 派生；此处没有任何持久化，因此数字始终跟随
// 实时的保留窗口。
type UsageStats struct {
	Window        UsageStatsWindow         `json:"window"`
	Totals        UsageStatsTotals         `json:"totals"`
	Daily         []UsageStatsDaily        `json:"daily"`
	Models        []UsageStatsGroup        `json:"models"`
	Accounts      []UsageStatsGroup        `json:"accounts"`
	ModelAccounts []UsageStatsModelAccount `json:"model_accounts"`
}

// UsageStatsWindow 回显解析后的查询范围，使 UI 无需从序列重新推导
// 即可为各桶加标签。
type UsageStatsWindow struct {
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
	Days  int       `json:"days"`
}

// UsageStatsTotals 是整个窗口范围的汇总。Outcome 保存四种终态
// （ok / incomplete / error / canceled）；Cache* 与
// OutputTokensPerSecond 来自逐行的计时与缓存列。
type UsageStatsTotals struct {
	Requests         int   `json:"requests"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	Errors           int   `json:"errors"`
	Incomplete       int   `json:"incomplete"`
	Canceled         int   `json:"canceled"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	// CacheHitRate 是输入 token 中由缓存提供的占比：
	// read / (read + prompt)；没有记录到输入 token 时为 0。
	CacheHitRate float64 `json:"cache_hit_rate"`
	// OutputTokensPerSecond 是在已交付响应（ok / 因长度截断）
	// 且计时可用的情况下的聚合解码速度。
	OutputTokensPerSecond float64 `json:"output_tokens_per_second"`
}

// UsageStatsDaily 是一个 UTC 日历日。没有流量的日子仍会以零值
// 输出，使趋势线没有空缺。
type UsageStatsDaily struct {
	Date                  string  `json:"date"`
	Requests              int     `json:"requests"`
	PromptTokens          int64   `json:"prompt_tokens"`
	CompletionTokens      int64   `json:"completion_tokens"`
	TotalTokens           int64   `json:"total_tokens"`
	Errors                int     `json:"errors"`
	Incomplete            int     `json:"incomplete"`
	Canceled              int     `json:"canceled"`
	CacheReadTokens       int64   `json:"cache_read_tokens"`
	OutputTokensPerSecond float64 `json:"output_tokens_per_second"`
}

// UsageStatsGroup 是按键的汇总（按模型或按账号）。
type UsageStatsGroup struct {
	Key                   string  `json:"key"`
	Requests              int     `json:"requests"`
	PromptTokens          int64   `json:"prompt_tokens"`
	CompletionTokens      int64   `json:"completion_tokens"`
	TotalTokens           int64   `json:"total_tokens"`
	Errors                int     `json:"errors"`
	CacheReadTokens       int64   `json:"cache_read_tokens"`
	CacheHitRate          float64 `json:"cache_hit_rate"`
	OutputTokensPerSecond float64 `json:"output_tokens_per_second"`
}

// UsageStatsModelAccount 是模型 × 账号的汇总：哪个账号承载了
// 某模型多少流量，以及它解码得多快。
type UsageStatsModelAccount struct {
	Model                 string  `json:"model"`
	Account               string  `json:"account"`
	Requests              int     `json:"requests"`
	TotalTokens           int64   `json:"total_tokens"`
	Errors                int     `json:"errors"`
	OutputTokensPerSecond float64 `json:"output_tokens_per_second"`
}
