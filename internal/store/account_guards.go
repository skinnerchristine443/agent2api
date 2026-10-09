package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agent2api/internal/accounts"
)

// 每日限额（daily-guard）的持久化，以及为其提供种子的 request_logs 聚合。

// int64Value 解引用一个可选的 guard 输入；nil 表示零（无 guard）。
func int64Value(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

// upsertAccountGuards 写入某个账号的完整 guard 行。创建时（总是）与更新时
// （只要提供了任一 guard 字段）都会调用，因此该行要么是全新的、要么被完全
// 覆盖 —— 不存在只写了一部分的行状态。
func (s *Store) upsertAccountGuards(ctx context.Context, accountID string, reserveCredits, dailyTokenLimit, dailyCreditLimit, dailyModelTokenLimit int64) error {
	_, err := s.db.ExecContext(ctx, `
	INSERT INTO account_guards (account_id, reserve_credits, daily_token_limit, daily_credit_limit, daily_model_token_limit)
	VALUES (?, ?, ?, ?, ?)
	ON CONFLICT(account_id) DO UPDATE SET
	  reserve_credits = excluded.reserve_credits,
	  daily_token_limit = excluded.daily_token_limit,
	  daily_credit_limit = excluded.daily_credit_limit,
	  daily_model_token_limit = excluded.daily_model_token_limit`,
		accountID, reserveCredits, dailyTokenLimit, dailyCreditLimit, dailyModelTokenLimit)
	if err != nil {
		return fmt.Errorf("upsert account guards: %w", err)
	}
	return nil
}

// AccountDailyUsage 从给定时刻起（调用方传入本地零点）聚合某个账号的
// request_logs，作为 daily-guard 的种子：总 token、总 credits，以及按模型
// 拆分的 token。token 计入整个请求的 prompt + completion；credits 累加上游
// 上报的值（缺失的 credits 按零计）。按模型分组的 key 是规范化模型 ID，
// 以便与 guard 的查询键保持一致。
func (s *Store) AccountDailyUsage(ctx context.Context, accountID string, since time.Time) (accounts.DailyUsage, error) {
	var usage accounts.DailyUsage
	rows, err := s.db.QueryContext(ctx, `
	SELECT requested_model,
	       COALESCE(SUM(COALESCE(prompt_tokens, 0) + COALESCE(completion_tokens, 0)), 0),
	       COALESCE(SUM(COALESCE(credits, 0)), 0)
	FROM request_logs
	WHERE account_id = ? AND created_at >= ?
	GROUP BY requested_model`,
		strings.TrimSpace(accountID), formatTime(since))
	if err != nil {
		return accounts.DailyUsage{}, fmt.Errorf("account daily usage: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var model string
		var tokens int64
		var credits float64
		if err := rows.Scan(&model, &tokens, &credits); err != nil {
			return accounts.DailyUsage{}, fmt.Errorf("scan daily usage: %w", err)
		}
		usage.Tokens += tokens
		usage.Credits += credits
		if key := accounts.CanonicalModelID(model); key != "" && tokens > 0 {
			if usage.ModelTokens == nil {
				usage.ModelTokens = map[string]int64{}
			}
			usage.ModelTokens[key] += tokens
		}
	}
	if err := rows.Err(); err != nil {
		return accounts.DailyUsage{}, fmt.Errorf("iterate daily usage: %w", err)
	}
	return usage, nil
}
