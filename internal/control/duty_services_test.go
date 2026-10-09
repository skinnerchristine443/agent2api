package control

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/executor"
	"agent2api/internal/providers"
)

type stubImporter struct {
	format  string
	payload []byte
	ready   bool
	err     error
}

func (s stubImporter) Validate([]byte) error { return nil }
func (s stubImporter) Format() string        { return s.format }
func (s stubImporter) PrepareImport(raw []byte) (providers.CredentialImport, error) {
	if s.err != nil {
		return providers.CredentialImport{}, s.err
	}
	payload := s.payload
	if len(payload) == 0 {
		payload = raw
	}
	return providers.CredentialImport{Payload: payload, Ready: s.ready}, nil
}

type stubLogin struct {
	session   providers.LoginSession
	startErr  error
	pollDone  bool
	pollMsg   string
	pollErr   error
	callback  string
	complete  error
	started   int
	polled    int
	completed int
}

func (s *stubLogin) StartLogin(context.Context, string) (providers.LoginSession, error) {
	s.started++
	return s.session, s.startErr
}
func (s *stubLogin) PollLogin(context.Context, string) (bool, string, error) {
	s.polled++
	return s.pollDone, s.pollMsg, s.pollErr
}
func (s *stubLogin) CompleteLogin(_ context.Context, _, callbackURL string) error {
	s.completed++
	s.callback = callbackURL
	return s.complete
}

func TestAccountsImportDispatchesCredentialImporter(t *testing.T) {
	svc, _, store, log := newTestServices()
	svc.Accounts.Providers = providers.NewRegistry()
	svc.Accounts.Providers.Register(providers.Adapter{
		ID:         "workbuddy",
		Credential: stubImporter{format: "workbuddy-oauth-v1", payload: []byte(`{"uid":"u1"}`), ready: true},
	})
	account, err := svc.Accounts.Import(context.Background(), AccountImportInput{
		Format: "workbuddy-oauth-v1", Name: "WB", Enabled: true,
	}, []byte(`{"access_token":"tok"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !account.Enabled || store.payloadFormats["acc-1"] != "workbuddy-oauth-v1" {
		t.Fatalf("account=%+v format=%q", account, store.payloadFormats["acc-1"])
	}
	if got := log.names; !equalCalls(got, []string{
		"store.Create", "store.SaveCredentialPayload", "store.Update", "store.Get", "runtime.StartAccount", "store.Get",
	}) {
		t.Fatalf("calls=%v", got)
	}
}

func TestAccountsImportRejectsUnknownFormat(t *testing.T) {
	svc, _, _, _ := newTestServices()
	svc.Accounts.Providers = providers.NewRegistry()
	_, err := svc.Accounts.Import(context.Background(), AccountImportInput{Format: "nope"}, nil)
	var op *OperationError
	if !errors.As(err, &op) || op.Code != "unsupported_format" {
		t.Fatalf("err=%v", err)
	}
}

func TestAccountsLoginOrchestratesAdapterWithoutHTTP(t *testing.T) {
	svc, _, store, _ := newTestServices()
	store.accounts["acc-1"] = accounts.Account{ID: "acc-1", Provider: "trae"}
	login := &stubLogin{session: providers.LoginSession{AuthURL: "https://auth.example/start"}, pollDone: true, pollMsg: "ok"}
	svc.Accounts.Providers = providers.NewRegistry()
	svc.Accounts.Providers.Register(providers.Adapter{ID: "trae", Login: login})

	session, err := svc.Accounts.StartLogin(context.Background(), "acc-1")
	if err != nil || session.AuthURL != "https://auth.example/start" {
		t.Fatalf("session=%+v err=%v", session, err)
	}
	done, message, err := svc.Accounts.PollLogin(context.Background(), "acc-1")
	if err != nil || !done || message != "ok" {
		t.Fatalf("poll done=%v msg=%q err=%v", done, message, err)
	}
	if err := svc.Accounts.CompleteLogin(context.Background(), "acc-1", "http://127.0.0.1/callback"); err != nil {
		t.Fatal(err)
	}
	if login.started != 1 || login.polled != 1 || login.completed != 1 || login.callback != "http://127.0.0.1/callback" {
		t.Fatalf("login=%+v", login)
	}
}

func TestAccountsAdminDispatchesInProcessLogin(t *testing.T) {
	svc, _, store, _ := newTestServices()
	store.accounts["acc-1"] = accounts.Account{ID: "acc-1", Provider: "trae"}
	login := &stubLogin{session: providers.LoginSession{AuthURL: "https://auth.example/start"}}
	svc.Accounts.Providers = providers.NewRegistry()
	svc.Accounts.Providers.Register(providers.Adapter{ID: "trae", Login: login})

	result, err := svc.Accounts.Admin(context.Background(), AccountAdminAction{
		AccountID: "acc-1", Action: "login/device", Method: "POST",
	})
	if err != nil || result.Kind != "login_start" || result.Session.AuthURL != "https://auth.example/start" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if login.started != 1 {
		t.Fatalf("login=%+v", login)
	}
}

func TestAccountsCompleteLoginRequiresCompleter(t *testing.T) {
	svc, _, store, _ := newTestServices()
	store.accounts["acc-1"] = accounts.Account{ID: "acc-1", Provider: "workbuddy"}
	svc.Accounts.Providers = providers.NewRegistry()
	svc.Accounts.Providers.Register(providers.Adapter{ID: "workbuddy", Login: pollOnlyLogin{}})
	err := svc.Accounts.CompleteLogin(context.Background(), "acc-1", "http://127.0.0.1/callback")
	var op *OperationError
	if !errors.As(err, &op) || op.Code != "provider_unsupported" {
		t.Fatalf("err=%v", err)
	}
}

type pollOnlyLogin struct{}

func (pollOnlyLogin) StartLogin(context.Context, string) (providers.LoginSession, error) {
	return providers.LoginSession{}, nil
}
func (pollOnlyLogin) PollLogin(context.Context, string) (bool, string, error) {
	return false, "", nil
}

func TestKeyRotationPersistsPublishesAndReloads(t *testing.T) {
	svc, runtime, store, log := newTestServices()
	var published string
	rot := &KeyRotation{
		Keys: svc.Keys, Accounts: svc.Accounts, Mu: &sync.Mutex{},
		Generate: func() (string, error) { return "new-secret", nil },
		Publish:  func(secret string) { published = secret },
	}
	secret, err := rot.Rotate(context.Background())
	if err != nil || secret != "new-secret" || published != "new-secret" {
		t.Fatalf("secret=%q published=%q err=%v", secret, published, err)
	}
	// 轮换控制台密钥不得触碰代理密钥：客户端在运维轮换前后继续可用。
	if store.secrets["console_key"] != "new-secret" {
		t.Fatalf("store console_key=%q", store.secrets["console_key"])
	}
	if _, ok := store.secrets["proxy_api_key"]; ok {
		t.Fatalf("console rotation rewrote proxy_api_key=%q", store.secrets["proxy_api_key"])
	}
	if runtime.proxyAPIKey != "" {
		t.Fatalf("console rotation pushed runtime key=%q", runtime.proxyAPIKey)
	}
	if got := log.names; !equalCalls(got, []string{"store.SetSecret"}) {
		t.Fatalf("calls=%v", got)
	}
}

func TestRotateProxyUpdatesRuntimeAndLeavesConsoleKeyAlone(t *testing.T) {
	svc, runtime, store, log := newTestServices()
	var publishedProxy string
	rot := &KeyRotation{
		Keys: svc.Keys, Accounts: svc.Accounts, Mu: &sync.Mutex{},
		Generate:     func() (string, error) { return "new-proxy", nil },
		PublishProxy: func(secret string) { publishedProxy = secret },
	}
	secret, err := rot.RotateProxy(context.Background())
	if err != nil || secret != "new-proxy" || publishedProxy != "new-proxy" {
		t.Fatalf("secret=%q published=%q err=%v", secret, publishedProxy, err)
	}
	if store.secrets["proxy_api_key"] != "new-proxy" || runtime.proxyAPIKey != "new-proxy" {
		t.Fatalf("store=%q runtime=%q", store.secrets["proxy_api_key"], runtime.proxyAPIKey)
	}
	if _, ok := store.secrets["console_key"]; ok {
		t.Fatalf("proxy rotation rewrote console_key=%q", store.secrets["console_key"])
	}
	if got := log.names; !equalCalls(got, []string{"store.SetSecret", "runtime.ReplaceProxyAPIKey"}) {
		t.Fatalf("calls=%v", got)
	}
}

func TestKeyRotationSerializesConcurrentRotates(t *testing.T) {
	svc, _, _, _ := newTestServices()
	started := make(chan struct{})
	release := make(chan struct{})
	var n atomic.Int32
	rot := &KeyRotation{
		Keys: svc.Keys, Accounts: svc.Accounts, Mu: &sync.Mutex{},
		Generate: func() (string, error) {
			seq := n.Add(1)
			if seq == 1 {
				close(started)
				<-release
			}
			return fmt.Sprintf("secret-%d", seq), nil
		},
		Publish: func(string) {},
	}
	first := make(chan string, 1)
	second := make(chan string, 1)
	go func() {
		secret, err := rot.Rotate(context.Background())
		if err != nil {
			t.Error(err)
		}
		first <- secret
	}()
	<-started
	go func() {
		secret, err := rot.Rotate(context.Background())
		if err != nil {
			t.Error(err)
		}
		second <- secret
	}()
	time.Sleep(20 * time.Millisecond)
	select {
	case got := <-second:
		t.Fatalf("second rotate finished before lock released: %s", got)
	default:
	}
	close(release)
	if got := <-first; got != "secret-1" {
		t.Fatalf("first=%s", got)
	}
	if got := <-second; got != "secret-2" {
		t.Fatalf("second=%s", got)
	}
}

func TestSystemPatchPersistsAndAppliesRuntime(t *testing.T) {
	svc, runtime, store, _ := newTestServices()
	pool := executor.NewPool()
	var cross atomic.Bool
	sys := &System{
		Settings: svc.Settings, Accounts: svc.Accounts, Pool: pool,
		CrossProviderPool: &cross, Mu: &sync.Mutex{},
	}
	enabled := true
	strategy := accounts.RoutingStrategyFillFirst
	proxyURL := "http://127.0.0.1:8888"
	if err := sys.Patch(context.Background(), SystemSettingsPatch{
		CrossProviderModelPool: &enabled,
		RoutingStrategy:        &strategy,
		ProxyURL:               &proxyURL,
	}); err != nil {
		t.Fatal(err)
	}
	if !cross.Load() || pool.RoutingStrategy() != accounts.RoutingStrategyFillFirst {
		t.Fatalf("cross=%v strategy=%s", cross.Load(), pool.RoutingStrategy())
	}
	if store.secrets["proxy_url"] != proxyURL || runtime.proxyURL != proxyURL {
		t.Fatalf("proxy store=%q runtime=%q", store.secrets["proxy_url"], runtime.proxyURL)
	}
	current := sys.Current(context.Background())
	if current.RoutingStrategy != accounts.RoutingStrategyFillFirst || !current.CrossProviderModelPool {
		t.Fatalf("current=%+v", current)
	}
}

func TestSystemPatchPersistsDisabledAccountCheckinSetting(t *testing.T) {
	svc, _, store, _ := newTestServices()
	pool := executor.NewPool()
	var cross atomic.Bool
	sys := &System{
		Settings: svc.Settings, Accounts: svc.Accounts, Pool: pool,
		CrossProviderPool: &cross, Mu: &sync.Mutex{},
	}
	enabled := true
	if err := sys.Patch(context.Background(), SystemSettingsPatch{CheckinDisabledAccounts: &enabled}); err != nil {
		t.Fatal(err)
	}
	if store.secrets[checkinDisabledAccountsSecret] != "1" {
		t.Fatalf("stored setting=%q", store.secrets[checkinDisabledAccountsSecret])
	}
	if !sys.Current(context.Background()).CheckinDisabledAccounts {
		t.Fatal("current setting is disabled")
	}
}
