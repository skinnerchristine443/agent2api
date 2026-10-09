package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// 来自实测用量学习（guardrail ③）的 free/paid 判定结果持久化。
// 每个 (account, model) 一行；该值随最新样本翻转。

// SaveAccountModelFree 整体替换该账号已存储的判定结果。调用方传入完整的
// 当前 map，因此在一个事务里做 DELETE + INSERT 能让表精确同步，即使某个
// 判定消失了。空 map 被有意设计为 no-op：调用方无法区分“尚无判定”与
// “清空全部”，而在启动顺序竞态下抹掉知识，比保留一行陈旧数据更糟
// （判定是增量知识；一个 paid 样本就会翻转它们）。
func (s *Store) SaveAccountModelFree(ctx context.Context, accountID string, verdicts map[string]bool) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" || len(verdicts) == 0 {
		return nil
	}
	models := make([]string, 0, len(verdicts))
	for model := range verdicts {
		models = append(models, model)
	}
	sort.Strings(models)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("save account model free: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_model_free WHERE account_id = ?`, accountID); err != nil {
		return fmt.Errorf("save account model free: %w", err)
	}
	now := formatTime(time.Now().UTC())
	for _, model := range models {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO account_model_free (account_id, model, free, updated_at) VALUES (?, ?, ?, ?)`,
			accountID, model, verdicts[model], now); err != nil {
			return fmt.Errorf("save account model free: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("save account model free: %w", err)
	}
	return nil
}

// LoadAccountModelFree 返回某个账号已存储的判定结果。账号或表状态缺失时
// 返回空 map，绝不返回错误：该学习是尽力而为的知识，读取失败不得阻塞
// 账号启动。
func (s *Store) LoadAccountModelFree(ctx context.Context, accountID string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT model, free FROM account_model_free WHERE account_id = ?`, strings.TrimSpace(accountID))
	if err != nil {
		return nil, fmt.Errorf("load account model free: %w", err)
	}
	defer rows.Close()
	verdicts := map[string]bool{}
	for rows.Next() {
		var model string
		var free bool
		if err := rows.Scan(&model, &free); err != nil {
			return nil, fmt.Errorf("scan account model free: %w", err)
		}
		verdicts[model] = free
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load account model free: %w", err)
	}
	return verdicts, nil
}
