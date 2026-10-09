package store

import (
	"context"
	"path/filepath"
	"testing"

	"agent2api/internal/accounts"
)

func TestAccountModelFreeRoundTripAndReplace(t *testing.T) {
	ctx := context.Background()
	s, err := OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	account, err := s.Create(ctx, accounts.CreateAccount{Name: "a", Provider: "workbuddy", Region: "cn"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Create(ctx, accounts.CreateAccount{Name: "b", Provider: "workbuddy", Region: "cn"})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SaveAccountModelFree(ctx, account.ID, map[string]bool{"glm-5": true, "deepseek": false}); err != nil {
		t.Fatal(err)
	}
	verdicts, err := s.LoadAccountModelFree(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !verdicts["glm-5"] || verdicts["deepseek"] {
		t.Fatalf("roundtrip = %+v", verdicts)
	}

	// 一次保存会整体替换：被丢弃的 key 随之消失。
	if err := s.SaveAccountModelFree(ctx, account.ID, map[string]bool{"other-model": true}); err != nil {
		t.Fatal(err)
	}
	verdicts, _ = s.LoadAccountModelFree(ctx, account.ID)
	if len(verdicts) != 1 || !verdicts["other-model"] {
		t.Fatalf("wholesale replace = %+v", verdicts)
	}

	// 空 map 是 no-op，绝不是清空：调用方无法区分“尚无判定”与“清空”。
	if err := s.SaveAccountModelFree(ctx, account.ID, nil); err != nil {
		t.Fatal(err)
	}
	verdicts, _ = s.LoadAccountModelFree(ctx, account.ID)
	if len(verdicts) != 1 {
		t.Fatalf("empty save must not wipe: %+v", verdicts)
	}

	// 账号之间相互隔离。
	if got, _ := s.LoadAccountModelFree(ctx, other.ID); len(got) != 0 {
		t.Fatalf("other account sees verdicts: %+v", got)
	}

	// 删除账号会一并删掉它的判定。
	if err := s.Delete(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM account_model_free WHERE account_id = ?", account.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("verdict rows after delete = %d", rows)
	}
}
