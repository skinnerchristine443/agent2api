package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"agent2api/internal/accounts"
)

// T3 回归：Observe 的 quota_exhausted 防降级护栏——"ready 观测不得把已耗尽
// 配额的账号降级回 ready"是写死在 UPDATE 的 CASE 里的状态保留规则，此前
// 零覆盖（唯一调用点丢弃了返回值）。这里钉死保留语义与两个对照语义。
func TestObservePreservesQuotaExhaustedStatus(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "observe.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	account, err := s.Create(ctx, accounts.CreateAccount{Name: "Obs", Provider: "workbuddy", Region: "global"})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Observe(ctx, account.ID, "u-1", "quota_exhausted", "额度已用尽", "quota"); err != nil {
		t.Fatal(err)
	}
	// 护栏：ready 观测不得降级已耗尽账号。
	if err := s.Observe(ctx, account.ID, "u-1", "ready", "", ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "quota_exhausted" {
		t.Fatalf("ready 观测不得降级已耗尽账号：status=%q", got.Status)
	}
	if got.RemoteUID != "u-1" {
		t.Fatalf("remote_uid 应照常更新：%q", got.RemoteUID)
	}

	// 对照一：非保留态照常被覆盖（error 观测改写 status）。
	if err := s.Observe(ctx, account.ID, "u-1", "error", "boom", "auth"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, account.ID)
	if got.Status != "error" {
		t.Fatalf("error 观测应改写 status：%q", got.Status)
	}

	// 对照二：普通账号的 ready 观测照常置为 ready。
	other, err := s.Create(ctx, accounts.CreateAccount{Name: "Plain", Provider: "workbuddy", Region: "global"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Observe(ctx, other.ID, "u-2", "ready", "", ""); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, other.ID)
	if got.Status != "ready" {
		t.Fatalf("ready 观测应置为 ready：%q", got.Status)
	}

	// 未知账号 → ErrAccountNotFound。
	if err := s.Observe(ctx, "missing", "", "ready", "", ""); !errors.Is(err, accounts.ErrAccountNotFound) {
		t.Fatalf("未知账号应返回 ErrAccountNotFound：%v", err)
	}
}
