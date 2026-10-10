package store

import (
	"context"
	"path/filepath"
	"testing"

	"agent2api/internal/accounts"
)

// 竞态中落败的一次刷新不得覆盖胜者所存的 token：凭证端点会轮换 refresh
// token，因此较晚的写入会使磁盘上已有的值失效。
func TestSaveCredentialPayloadIfUnchangedIsACompareAndWrite(t *testing.T) {
	ctx := context.Background()
	s, err := OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	defer s.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	account, err := s.Create(ctx, accounts.CreateAccount{Name: "wb", Provider: "workbuddy", Region: "cn"})
	if err != nil {
		t.Fatal(err)
	}
	const format = "workbuddy-oauth-v1"
	if err := s.SaveCredentialPayload(ctx, account.ID, format, []byte(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}

	_, _, version, err := s.LoadCredentialPayloadWithVersion(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}

	written, err := s.SaveCredentialPayloadIfUnchanged(ctx, account.ID, format, []byte(`{"v":2}`), version)
	if err != nil || !written {
		t.Fatalf("first writer: written=%v err=%v, want true/nil", written, err)
	}

	written, err = s.SaveCredentialPayloadIfUnchanged(ctx, account.ID, format, []byte(`{"v":3}`), version)
	if err != nil {
		t.Fatal(err)
	}
	if written {
		t.Fatal("a writer holding a stale version must not be allowed to write")
	}

	_, payload, err := s.LoadCredentialPayload(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"v":2}` {
		t.Fatalf("payload = %s, want the winner's value", payload)
	}
}
