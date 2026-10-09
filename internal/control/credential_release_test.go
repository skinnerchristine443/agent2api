package control

import (
	"context"
	"testing"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
)

// credentialStub 满足 CredentialCodec + CredentialImporter。
type credentialStub struct{ format string }

func (s *credentialStub) Validate([]byte) error { return nil }
func (s *credentialStub) Format() string        { return s.format }
func (s *credentialStub) PrepareImport(raw []byte) (providers.CredentialImport, error) {
	return providers.CredentialImport{Payload: raw, Ready: true}, nil
}

// loginStub 满足 LoginSessionProvider + LoginCompleter；onComplete
// 模拟 adapter 持久化一份新凭据。
type loginStub struct {
	onComplete func(ctx context.Context, id string)
}

func (s loginStub) StartLogin(context.Context, string) (providers.LoginSession, error) {
	return providers.LoginSession{AuthURL: "https://example.test/login"}, nil
}
func (s loginStub) PollLogin(context.Context, string) (bool, string, error) { return true, "", nil }
func (s loginStub) CompleteLogin(ctx context.Context, id, _ string) error {
	if s.onComplete != nil {
		s.onComplete(ctx, id)
	}
	return nil
}

func newCredentialFixture() (*Accounts, *fakeStore, *callLog) {
	log := &callLog{}
	store := &fakeStore{log: log, accounts: map[string]accounts.Account{
		"acc1": {ID: "acc1", Name: "a", Provider: "trae", ProviderRegion: "cn"},
	}, payloads: map[string][]byte{}, payloadFormats: map[string]string{}}
	service := NewAccounts(&fakeRuntime{log: log, store: store})
	service.Providers = providers.NewRegistry()
	return service, store, log
}

// 替换 PAT 会改变已存材料：账号的冷却必须被释放（黑名单此前在惩罚旧凭据）。
func TestLoginPATReleasesCooldownsWhenCredentialChanges(t *testing.T) {
	service, store, log := newCredentialFixture()
	store.payloads["acc1"] = []byte(`{"api_key":"old"}`)
	store.payloadFormats["acc1"] = "trae-oauth-v1"
	service.Providers.Register(providers.Adapter{ID: "trae", Credential: &credentialStub{format: "trae-oauth-v1"}})

	if err := service.LoginPAT(context.Background(), "acc1", "new-token"); err != nil {
		t.Fatal(err)
	}
	if !logHas(log, "runtime.ClearCooldowns:acc1|") {
		t.Fatalf("changed credential must release cooldowns: %v", log.names)
	}
}

// 重新保存相同材料不算凭据变更：黑名单保持不变（指纹语义）。
func TestLoginPATKeepsCooldownsWhenCredentialIsUnchanged(t *testing.T) {
	service, store, log := newCredentialFixture()
	store.payloads["acc1"] = []byte(`{"api_key":"same-token"}`)
	store.payloadFormats["acc1"] = "trae-oauth-v1"
	service.Providers.Register(providers.Adapter{ID: "trae", Credential: &credentialStub{format: "trae-oauth-v1"}})

	if err := service.LoginPAT(context.Background(), "acc1", "same-token"); err != nil {
		t.Fatal(err)
	}
	if logHas(log, "runtime.ClearCooldowns:acc1|") {
		t.Fatalf("unchanged credential must keep cooldowns: %v", log.names)
	}
}

// CompleteLogin 遵循相同规则：只有材料真实变化才释放。
func TestCompleteLoginReleasesCooldownsOnlyOnRealChange(t *testing.T) {
	run := func(name string, change bool) {
		t.Helper()
		service, store, log := newCredentialFixture()
		store.payloads["acc1"] = []byte(`{"v":1}`)
		store.payloadFormats["acc1"] = "trae-oauth-v1"
		service.Providers.Register(providers.Adapter{ID: "trae", Login: loginStub{
			onComplete: func(_ context.Context, id string) {
				if change {
					store.payloads[id] = []byte(`{"v":2}`)
				}
			},
		}})
		if err := service.CompleteLogin(context.Background(), "acc1", "code"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		released := logHas(log, "runtime.ClearCooldowns:acc1|")
		if released != change {
			t.Fatalf("%s: released=%v, want %v (%v)", name, released, change, log.names)
		}
	}
	run("changed", true)
	run("unchanged", false)
}
