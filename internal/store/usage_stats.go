package store

import (
	"context"
	"fmt"
	"time"

	"agent2api/internal/accounts"
)

// usageStatsMaxGroups 限制按模型 / 按账号的汇总行数。控制台只渲染一个简短
// 的排行榜，限定结果行数能避免病态的目录（成百上千个一次性的模型 ID）把
// 仪表盘变成一次全表扫描且返回无界响应。
const usageStatsMaxGroups = 12

// usageStatsMaxModelAccounts 限制 模型 × 账号 的汇总，它比单键排行榜需要
// 稍多一些行才能保持有用。
const usageStatsMaxModelAccounts = 20

// usageTpsCondition 选出能携带输出速度样本的行：已交付的响应（ok / 长度
// 截断），且两个时序都已记录、解码窗口为正、有可作除数的 token 数。
const usageTpsCondition = `status IN ('ok', 'incomplete')
  AND COALESCE(completion_tokens, 0) > 0
  AND latency_ms IS NOT NULL AND ttfb_ms IS NOT NULL AND latency_ms > ttfb_ms`

// usageCacheHitRate 是输入 token 中由缓存提供的占比：
// read / (read + prompt)。没有记录任何输入 token 时返回零，因此空窗口
// 报告 0 而不是 NaN。
func usageCacheHitRate(cacheRead, prompt int64) float64 {
	denominator := cacheRead + prompt
	if denominator <= 0 {
		return 0
	}
	return float64(cacheRead) / float64(denominator)
}

// usageTokensPerSecond 把汇总后的 (tokens, 解码毫秒) 二元组换算为聚合速率。
// 用的是“和的比值”，而非逐行比值的平均：一个长响应应当比一个短响应
// 权重更大。
func usageTokensPerSecond(tokens, millis int64) float64 {
	if tokens <= 0 || millis <= 0 {
		return 0
	}
	return float64(tokens) / (float64(millis) / 1000)
}

// UsageStats 把 request_logs 聚合为按天、按模型、按账号、按 模型 × 账号 的
// token/请求汇总。它严格只读，绝不写入。
func (s *Store) UsageStats(ctx context.Context, since time.Time) (accounts.UsageStats, error) {
	until := time.Now().UTC()
	if since.IsZero() {
		// 含今天在内的七天，与控制台的默认窗口一致。
		since = until.AddDate(0, 0, -6)
	}
	since = since.UTC()
	stats := accounts.UsageStats{
		Window:        accounts.UsageStatsWindow{Since: since, Until: until},
		Daily:         make([]accounts.UsageStatsDaily, 0),
		Models:        make([]accounts.UsageStatsGroup, 0),
		Accounts:      make([]accounts.UsageStatsGroup, 0),
		ModelAccounts: make([]accounts.UsageStatsModelAccount, 0),
	}
	if !until.After(since) {
		since = until
		stats.Window.Since = since
		return stats, nil
	}

	// 存储时间戳为定宽纳秒（timestampLayout，30 字符，字符串序即时间序——
	// 写入方保证，历史行由启动时的归一化步骤兜底），因此直接在列上做
	// 区间比较：>= since、< until+1s 与旧的「substr 截到整秒后比较」语义
	// 等价，但列不被函数包裹，可命中 request_logs_created_at 索引
	//（此前 substr 包裹列使索引失效，控制台单次加载触发多次全表扫描）。
	where := " WHERE created_at >= ? AND created_at < ?"
	args := []any{since.Format(timestampLayout), until.Add(time.Second).Format(timestampLayout)}

	var tpsTokens, tpsMillis int64
	row := s.db.QueryRowContext(ctx, `
SELECT
  COUNT(*),
  COALESCE(SUM(COALESCE(prompt_tokens, 0)), 0),
  COALESCE(SUM(COALESCE(completion_tokens, 0)), 0),
  COALESCE(SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN status = 'incomplete' THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN status = 'canceled' THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(COALESCE(cache_read_tokens, 0)), 0),
  COALESCE(SUM(COALESCE(cache_write_tokens, 0)), 0),
  COALESCE(SUM(CASE WHEN `+usageTpsCondition+` THEN completion_tokens ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN `+usageTpsCondition+` THEN (latency_ms - ttfb_ms) ELSE 0 END), 0)
FROM request_logs`+where, args...)
	if err := row.Scan(
		&stats.Totals.Requests, &stats.Totals.PromptTokens,
		&stats.Totals.CompletionTokens, &stats.Totals.Errors,
		&stats.Totals.Incomplete, &stats.Totals.Canceled,
		&stats.Totals.CacheReadTokens, &stats.Totals.CacheWriteTokens,
		&tpsTokens, &tpsMillis,
	); err != nil {
		return accounts.UsageStats{}, fmt.Errorf("usage totals: %w", err)
	}
	stats.Totals.TotalTokens = stats.Totals.PromptTokens + stats.Totals.CompletionTokens
	stats.Totals.CacheHitRate = usageCacheHitRate(stats.Totals.CacheReadTokens, stats.Totals.PromptTokens)
	stats.Totals.OutputTokensPerSecond = usageTokensPerSecond(tpsTokens, tpsMillis)

	daily, err := s.usageStatsDaily(ctx, where, args, since, until)
	if err != nil {
		return accounts.UsageStats{}, err
	}
	stats.Daily = daily
	stats.Window.Days = len(daily)

	stats.Models, err = s.usageStatsGroups(ctx, "COALESCE(NULLIF(requested_model, ''), '(unknown)')", where, args)
	if err != nil {
		return accounts.UsageStats{}, err
	}
	stats.Accounts, err = s.usageStatsGroups(ctx, "COALESCE(NULLIF(account_id, ''), '(unassigned)')", where, args)
	if err != nil {
		return accounts.UsageStats{}, err
	}
	stats.ModelAccounts, err = s.usageStatsModelAccounts(ctx, where, args)
	if err != nil {
		return accounts.UsageStats{}, err
	}
	return stats, nil
}

// usageStatsDaily 为 [since, until] 区间内的每个 UTC 日返回一条记录，
// 并为零流量日补零，使序列没有空缺。
func (s *Store) usageStatsDaily(ctx context.Context, where string, args []any, since, until time.Time) ([]accounts.UsageStatsDaily, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT substr(created_at, 1, 10),
       COUNT(*),
       COALESCE(SUM(COALESCE(prompt_tokens, 0)), 0),
       COALESCE(SUM(COALESCE(completion_tokens, 0)), 0),
       COALESCE(SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN status = 'incomplete' THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN status = 'canceled' THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(COALESCE(cache_read_tokens, 0)), 0),
       COALESCE(SUM(CASE WHEN `+usageTpsCondition+` THEN completion_tokens ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN `+usageTpsCondition+` THEN (latency_ms - ttfb_ms) ELSE 0 END), 0)
FROM request_logs`+where+`
GROUP BY 1 ORDER BY 1 ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("usage daily: %w", err)
	}
	defer rows.Close()

	type dayTotals struct {
		requests, errors, incomplete, canceled int
		promptTokens, completionTokens         int64
		cacheReadTokens                        int64
		tpsTokens, tpsMillis                   int64
	}
	seen := make(map[string]dayTotals)
	for rows.Next() {
		var date string
		var totals dayTotals
		if err := rows.Scan(&date, &totals.requests, &totals.promptTokens, &totals.completionTokens,
			&totals.errors, &totals.incomplete, &totals.canceled, &totals.cacheReadTokens,
			&totals.tpsTokens, &totals.tpsMillis); err != nil {
			return nil, fmt.Errorf("scan usage daily: %w", err)
		}
		seen[date] = totals
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	start := time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, time.UTC)
	end := time.Date(until.Year(), until.Month(), until.Day(), 0, 0, 0, 0, time.UTC)
	if end.Before(start) {
		return []accounts.UsageStatsDaily{}, nil
	}
	out := make([]accounts.UsageStatsDaily, 0, int(end.Sub(start).Hours()/24)+1)
	for cursor := start; !cursor.After(end); cursor = cursor.AddDate(0, 0, 1) {
		date := cursor.Format("2006-01-02")
		totals := seen[date]
		out = append(out, accounts.UsageStatsDaily{
			Date:                  date,
			Requests:              totals.requests,
			PromptTokens:          totals.promptTokens,
			CompletionTokens:      totals.completionTokens,
			TotalTokens:           totals.promptTokens + totals.completionTokens,
			Errors:                totals.errors,
			Incomplete:            totals.incomplete,
			Canceled:              totals.canceled,
			CacheReadTokens:       totals.cacheReadTokens,
			OutputTokensPerSecond: usageTokensPerSecond(totals.tpsTokens, totals.tpsMillis),
		})
	}
	return out, nil
}

// usageStatsGroups 按给定的分组表达式汇总。该表达式是调用方传入的
// 编译期常量，绝非用户输入。
func (s *Store) usageStatsGroups(ctx context.Context, groupExpr, where string, args []any) ([]accounts.UsageStatsGroup, error) {
	query := `
SELECT ` + groupExpr + `,
       COUNT(*),
       COALESCE(SUM(COALESCE(prompt_tokens, 0)), 0),
       COALESCE(SUM(COALESCE(completion_tokens, 0)), 0),
       COALESCE(SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(COALESCE(cache_read_tokens, 0)), 0),
       COALESCE(SUM(CASE WHEN ` + usageTpsCondition + ` THEN completion_tokens ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN ` + usageTpsCondition + ` THEN (latency_ms - ttfb_ms) ELSE 0 END), 0)
FROM request_logs` + where + `
GROUP BY 1
ORDER BY COALESCE(SUM(COALESCE(prompt_tokens, 0)), 0) + COALESCE(SUM(COALESCE(completion_tokens, 0)), 0) DESC, COUNT(*) DESC, 1 ASC
LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, append(append([]any{}, args...), usageStatsMaxGroups)...)
	if err != nil {
		return nil, fmt.Errorf("usage groups: %w", err)
	}
	defer rows.Close()

	items := make([]accounts.UsageStatsGroup, 0, usageStatsMaxGroups)
	for rows.Next() {
		var item accounts.UsageStatsGroup
		var tpsTokens, tpsMillis int64
		if err := rows.Scan(&item.Key, &item.Requests, &item.PromptTokens, &item.CompletionTokens,
			&item.Errors, &item.CacheReadTokens, &tpsTokens, &tpsMillis); err != nil {
			return nil, fmt.Errorf("scan usage groups: %w", err)
		}
		item.TotalTokens = item.PromptTokens + item.CompletionTokens
		item.CacheHitRate = usageCacheHitRate(item.CacheReadTokens, item.PromptTokens)
		item.OutputTokensPerSecond = usageTokensPerSecond(tpsTokens, tpsMillis)
		items = append(items, item)
	}
	return items, rows.Err()
}

// usageStatsModelAccounts 按请求模型 × 账号汇总：某个模型的流量各由哪个
// 账号承担了多少。按流量排序，与其他排行榜一样有界。
func (s *Store) usageStatsModelAccounts(ctx context.Context, where string, args []any) ([]accounts.UsageStatsModelAccount, error) {
	query := `
SELECT COALESCE(NULLIF(requested_model, ''), '(unknown)'),
       COALESCE(NULLIF(account_id, ''), '(unassigned)'),
       COUNT(*),
       COALESCE(SUM(COALESCE(prompt_tokens, 0) + COALESCE(completion_tokens, 0)), 0),
       COALESCE(SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN ` + usageTpsCondition + ` THEN completion_tokens ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN ` + usageTpsCondition + ` THEN (latency_ms - ttfb_ms) ELSE 0 END), 0)
FROM request_logs` + where + `
GROUP BY 1, 2
ORDER BY 4 DESC, 3 DESC, 1 ASC, 2 ASC
LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, append(append([]any{}, args...), usageStatsMaxModelAccounts)...)
	if err != nil {
		return nil, fmt.Errorf("usage model accounts: %w", err)
	}
	defer rows.Close()

	items := make([]accounts.UsageStatsModelAccount, 0, usageStatsMaxModelAccounts)
	for rows.Next() {
		var item accounts.UsageStatsModelAccount
		var tpsTokens, tpsMillis int64
		if err := rows.Scan(&item.Model, &item.Account, &item.Requests, &item.TotalTokens,
			&item.Errors, &tpsTokens, &tpsMillis); err != nil {
			return nil, fmt.Errorf("scan usage model accounts: %w", err)
		}
		item.OutputTokensPerSecond = usageTokensPerSecond(tpsTokens, tpsMillis)
		items = append(items, item)
	}
	return items, rows.Err()
}
