package control

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"agent2api/internal/accounts"
)

type callLog struct {
	names []string
}

func (l *callLog) add(name string) {
	l.names = append(l.names, name)
}

type fakeStore struct {
	log             *callLog
	accounts        map[string]accounts.Account
	payloads        map[string][]byte
	payloadFormats  map[string]string
	checkins        map[string][]accounts.CheckinRecord
	growth          []accounts.GrowthObservation
	secrets         map[string]string
	contexts        map[string]int
	providerSetting accounts.ProviderModelSetting
	backup          accounts.Backup
	createErr       error
	payloadErr      error
	getErr          error
	getFailOnce     bool
	updateErr       error
	deleteErr       error
	backupErr       error
}

func newFakeStore(log *callLog) *fakeStore {
	return &fakeStore{
		log:            log,
		accounts:       map[string]accounts.Account{},
		payloads:       map[string][]byte{},
		payloadFormats: map[string]string{},
		checkins:       map[string][]accounts.CheckinRecord{},
		secrets:        map[string]string{},
		contexts:       map[string]int{},
	}
}

func (s *fakeStore) RecordPoolState(context.Context, accounts.PoolState) error { return nil }
func (s *fakeStore) SaveCooldowns(context.Context, string, []accounts.CooldownRow) error {
	return nil
}
func (s *fakeStore) LoadCooldowns(context.Context) ([]accounts.CooldownRow, error) {
	return nil, nil
}
func (s *fakeStore) ClearCooldown(_ context.Context, accountID, model string) (int64, error) {
	s.log.add("store.ClearCooldown:" + accountID + "|" + model)
	return 1, nil
}
func (s *fakeStore) Close() error { return nil }
func (s *fakeStore) Backup(_ context.Context, directory string, keep int) (accounts.Backup, error) {
	s.log.add("store.Backup")
	if s.backupErr != nil {
		return accounts.Backup{}, s.backupErr
	}
	if s.backup.Name == "" {
		s.backup = accounts.Backup{Name: "snap.db", Path: filepath.Join(directory, "snap.db"), CreatedAt: time.Unix(0, 0).UTC()}
	}
	s.backup.Path = filepath.Join(directory, s.backup.Name)
	_ = keep
	return s.backup, nil
}
func (s *fakeStore) Create(_ context.Context, input accounts.CreateAccount) (accounts.Account, error) {
	s.log.add("store.Create")
	if s.createErr != nil {
		return accounts.Account{}, s.createErr
	}
	// 与真实 store 一致的最小校验（审查 F5 契约测试锁定）。
	if strings.TrimSpace(input.Name) == "" {
		return accounts.Account{}, errors.New("account name required")
	}
	account := accounts.Account{ID: "acc-1", Name: input.Name, Provider: input.Provider, ProviderRegion: input.Region, Enabled: input.Enabled}
	s.accounts[account.ID] = account
	return account, nil
}
func (s *fakeStore) Get(_ context.Context, id string) (accounts.Account, error) {
	s.log.add("store.Get")
	if s.getFailOnce {
		s.getFailOnce = false
		return accounts.Account{}, errors.New("get after write failed")
	}
	if s.getErr != nil {
		return accounts.Account{}, s.getErr
	}
	account, ok := s.accounts[id]
	if !ok {
		return accounts.Account{}, accounts.ErrAccountNotFound
	}
	return account, nil
}
func (s *fakeStore) List(context.Context) ([]accounts.Account, error) {
	out := make([]accounts.Account, 0, len(s.accounts))
	for _, account := range s.accounts {
		out = append(out, account)
	}
	// 真实实现按 created_at, id 排序；fake 没有时间戳，用 id 保证确定性顺序，
	// 使依赖列表顺序的测试可复现（审查 F5）。
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (s *fakeStore) Update(_ context.Context, id string, input accounts.UpdateAccount) error {
	s.log.add("store.Update")
	if s.updateErr != nil {
		return s.updateErr
	}
	account, ok := s.accounts[id]
	if !ok {
		return accounts.ErrAccountNotFound
	}
	if input.Enabled != nil {
		account.Enabled = *input.Enabled
	}
	s.accounts[id] = account
	return nil
}
func (s *fakeStore) Delete(_ context.Context, id string) error {
	s.log.add("store.Delete")
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.accounts, id)
	delete(s.payloads, id)
	return nil
}
func (s *fakeStore) SaveCredentialPayload(_ context.Context, accountID, format string, payload []byte) error {
	s.log.add("store.SaveCredentialPayload")
	if s.payloadErr != nil {
		return s.payloadErr
	}
	s.payloads[accountID] = payload
	s.payloadFormats[accountID] = format
	return nil
}
func (s *fakeStore) LoadCredentialPayload(_ context.Context, accountID string) (string, []byte, error) {
	s.log.add("store.LoadCredentialPayload")
	return s.payloadFormats[accountID], s.payloads[accountID], nil
}
func (s *fakeStore) Observe(_ context.Context, id, _, _, _, _ string) error {
	// 与真实 store 语义一致：未知账号返回 ErrAccountNotFound（审查 F5）。
	if _, ok := s.accounts[id]; !ok {
		return accounts.ErrAccountNotFound
	}
	return nil
}
func (s *fakeStore) SaveQuota(context.Context, string, *accounts.QuotaSnapshot) error { return nil }
func (s *fakeStore) RecordCheckin(context.Context, string, string, string, time.Time) error {
	return nil
}
func (s *fakeStore) ListCheckinRecords(_ context.Context, accountID string, _ int) ([]accounts.CheckinRecord, error) {
	s.log.add("store.ListCheckinRecords")
	return s.checkins[accountID], nil
}
func (s *fakeStore) RecordGrowthObservations(_ context.Context, accountID string, observations []accounts.GrowthObservation) error {
	s.log.add("store.RecordGrowthObservations")
	for index := range observations {
		observations[index].AccountID = accountID
	}
	s.growth = append(s.growth, observations...)
	return nil
}
func (s *fakeStore) ListGrowthObservations(_ context.Context, accountID string, _ int) ([]accounts.GrowthObservation, error) {
	s.log.add("store.ListGrowthObservations")
	out := make([]accounts.GrowthObservation, 0)
	for _, observation := range s.growth {
		if observation.AccountID == accountID {
			out = append(out, observation)
		}
	}
	return out, nil
}
func (s *fakeStore) GetSecret(_ context.Context, name string) (string, bool, error) {
	s.log.add("store.GetSecret")
	value, ok := s.secrets[name]
	return value, ok, nil
}
func (s *fakeStore) SetSecret(_ context.Context, name, value string) error {
	s.log.add("store.SetSecret")
	s.secrets[name] = value
	return nil
}
func (s *fakeStore) SetSecretOrEmpty(_ context.Context, name, value string) error {
	s.log.add("store.SetSecretOrEmpty")
	s.secrets[name] = value
	return nil
}
func (s *fakeStore) WorkBuddyCheckinTimeDefault(context.Context) string {
	s.log.add("store.WorkBuddyCheckinTimeDefault")
	return "09:00"
}
func (s *fakeStore) GetModelContext(_ context.Context, modelID string) (int, bool, error) {
	value, ok := s.contexts[modelID]
	return value, ok, nil
}
func (s *fakeStore) SetModelContext(_ context.Context, modelID string, contextLength int) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	if contextLength == 0 {
		delete(s.contexts, modelID)
		return nil
	}
	s.contexts[modelID] = contextLength
	return nil
}
func (s *fakeStore) ListModelContexts(context.Context) (map[string]int, error) {
	return s.contexts, nil
}
func (s *fakeStore) GetProviderModelSetting(context.Context, string, string) (accounts.ProviderModelSetting, error) {
	if s.getErr != nil {
		return accounts.ProviderModelSetting{}, s.getErr
	}
	return s.providerSetting, nil
}

func (s *fakeStore) AccountDailyUsage(context.Context, string, time.Time) (accounts.DailyUsage, error) {
	return accounts.DailyUsage{}, nil
}

func (s *fakeStore) LoadAccountModelFree(context.Context, string) (map[string]bool, error) {
	return nil, nil
}

func (s *fakeStore) SaveAccountModelFree(context.Context, string, map[string]bool) error {
	return nil
}
func (s *fakeStore) SetProviderModelSetting(_ context.Context, _, _ string, setting accounts.ProviderModelSetting) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	s.providerSetting = setting
	return nil
}

type fakeRuntime struct {
	log         *callLog
	store       *fakeStore
	startErr    error
	stopErr     error
	syncErr     error
	refreshErr  error
	checkinErr  error
	started     []string
	stopped     []string
	removed     []string
	views       []accounts.AccountView
	view        accounts.AccountView
	checkedIn   accounts.Account
	refreshAll  bool
	forceQuota  bool
	proxyURL    string
	proxyAPIKey string

	modelRequestsDisabled map[string]bool
}

func (r *fakeRuntime) StartAccount(_ context.Context, account accounts.Account) error {
	r.log.add("runtime.StartAccount")
	r.started = append(r.started, account.ID)
	if r.startErr != nil {
		return r.startErr
	}
	return nil
}
func (r *fakeRuntime) StopAccount(id string) error {
	r.log.add("runtime.StopAccount")
	r.stopped = append(r.stopped, id)
	return r.stopErr
}
func (r *fakeRuntime) RemoveAccount(id string) error {
	r.log.add("runtime.RemoveAccount")
	r.removed = append(r.removed, id)
	return nil
}
func (r *fakeRuntime) SyncAccount(_ context.Context, _, after accounts.Account) error {
	r.log.add("runtime.SyncAccount")
	if after.Enabled {
		r.started = append(r.started, after.ID)
	} else {
		r.stopped = append(r.stopped, after.ID)
	}
	return r.syncErr
}
func (r *fakeRuntime) SetModelRequestsEnabled(ctx context.Context, id string, enabled bool) error {
	r.log.add("runtime.SetModelRequestsEnabled")
	if r.modelRequestsDisabled == nil {
		r.modelRequestsDisabled = map[string]bool{}
	}
	// 代替真实的 Manager，它掌管底层 secret 的整个读-改-写过程：把它镜像到 fake
	// store 上，使 control 层的测试可以断言被持久化的集合及其写入顺序。
	set, err := accounts.ReadModelRequestsDisabled(ctx, r.store)
	if err != nil {
		return err
	}
	if enabled {
		delete(set, id)
	} else {
		set[id] = struct{}{}
	}
	if err := r.store.SetSecret(ctx, accounts.ModelRequestsDisabledSecret, accounts.EncodeModelRequestsDisabled(set)); err != nil {
		return err
	}
	r.modelRequestsDisabled[id] = !enabled
	return nil
}
func (r *fakeRuntime) AccountView(context.Context, string) (accounts.AccountView, error) {
	r.log.add("runtime.AccountView")
	return r.view, nil
}
func (r *fakeRuntime) Accounts(context.Context) ([]accounts.AccountView, error) {
	r.log.add("runtime.Accounts")
	return r.views, nil
}
func (r *fakeRuntime) RefreshAccount(_ context.Context, _ string, forceQuota bool) error {
	r.log.add("runtime.RefreshAccount")
	r.forceQuota = forceQuota
	return r.refreshErr
}
func (r *fakeRuntime) RefreshAll(_ context.Context, forceQuota bool) error {
	r.log.add("runtime.RefreshAll")
	r.refreshAll = true
	r.forceQuota = forceQuota
	return r.refreshErr
}
func (r *fakeRuntime) ClearCooldowns(_ context.Context, accountID, model string) (int64, error) {
	r.log.add("runtime.ClearCooldowns:" + accountID + "|" + model)
	return 1, nil
}
func (r *fakeRuntime) CheckinAccount(_ context.Context, id string) (accounts.Account, error) {
	r.log.add("runtime.CheckinAccount")
	if r.checkinErr != nil {
		return accounts.Account{}, r.checkinErr
	}
	if r.checkedIn.ID == "" {
		r.checkedIn = r.store.accounts[id]
	}
	return r.checkedIn, nil
}
func (r *fakeRuntime) ReloadProxyURL(_ context.Context, value string) error {
	r.log.add("runtime.ReloadProxyURL")
	r.proxyURL = value
	return nil
}
func (r *fakeRuntime) ReplaceProxyAPIKey(_ context.Context, key string) error {
	r.log.add("runtime.ReplaceProxyAPIKey")
	r.proxyAPIKey = key
	return nil
}
func (r *fakeRuntime) Store() accounts.AccountStore {
	return r.store
}

func newTestServices() (*Services, *fakeRuntime, *fakeStore, *callLog) {
	log := &callLog{}
	store := newFakeStore(log)
	runtime := &fakeRuntime{log: log, store: store}
	return New(runtime), runtime, store, log
}

func TestImportCredentialPayloadDeletesOnPayloadFailure(t *testing.T) {
	svc, _, store, log := newTestServices()
	store.payloadErr = errors.New("blob write failed")
	_, err := svc.Accounts.ImportCredentialPayload(context.Background(), accounts.CreateAccount{
		Name: "Trae", Provider: "trae",
	}, "trae-oauth-v1", []byte(`{"uid":"u1"}`), true)
	if err == nil || err.Error() != "blob write failed" {
		t.Fatalf("err=%v", err)
	}
	if _, ok := store.accounts["acc-1"]; ok {
		t.Fatal("failed payload import left the account")
	}
	if got := log.names; !equalCalls(got, []string{
		"store.Create", "store.SaveCredentialPayload", "store.Delete", "runtime.RemoveAccount",
	}) {
		t.Fatalf("calls=%v", got)
	}
}

func TestImportCredentialPayloadEnablesAfterCredentialWrite(t *testing.T) {
	svc, _, _, log := newTestServices()
	account, err := svc.Accounts.ImportCredentialPayload(context.Background(), accounts.CreateAccount{
		Name: "WB", Provider: "workbuddy", Enabled: true,
	}, "workbuddy-oauth-v1", []byte(`{"uid":"u1"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if !account.Enabled {
		t.Fatalf("imported %+v", account)
	}
	if got := log.names; !equalCalls(got, []string{
		"store.Create", "store.SaveCredentialPayload", "store.Update", "store.Get", "runtime.StartAccount", "store.Get",
	}) {
		t.Fatalf("calls=%v", got)
	}
}

func TestImportCredentialPayloadIgnoresGetError(t *testing.T) {
	svc, _, store, _ := newTestServices()
	store.getFailOnce = true
	account, err := svc.Accounts.ImportCredentialPayload(context.Background(), accounts.CreateAccount{
		Name: "WorkBuddy", Provider: "workbuddy",
	}, "workbuddy-oauth-v1", []byte(`{"uid":"u1"}`), false)
	if err != nil {
		t.Fatalf("Get failure after import must stay 201-equivalent: %v", err)
	}
	if account.ID != "" {
		t.Fatalf("ignored Get should return zero account, got %+v", account)
	}
}

func TestCreatePassesThroughStartFailure(t *testing.T) {
	svc, runtime, store, _ := newTestServices()
	runtime.startErr = errors.New("start failed")
	account, err := svc.Accounts.Create(context.Background(), accounts.CreateAccount{Name: "Alpha", Provider: "workbuddy", Region: "cn", Enabled: true})
	if err == nil || err.Error() != "start failed" {
		t.Fatalf("err=%v", err)
	}
	if account.ID != "acc-1" {
		t.Fatalf("row must already exist: %+v", account)
	}
	if _, ok := store.accounts["acc-1"]; !ok {
		t.Fatal("start failure must not roll back the row")
	}
}

func TestUpdateReadsStoreAfterRuntime(t *testing.T) {
	svc, _, store, log := newTestServices()
	store.accounts["acc-1"] = accounts.Account{ID: "acc-1", Name: "before"}
	enabled := true
	account, err := svc.Accounts.Update(context.Background(), "acc-1", accounts.UpdateAccount{Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	if !account.Enabled {
		t.Fatalf("updated %+v", account)
	}
	if got := log.names; !equalCalls(got, []string{"store.Get", "store.Update", "store.Get", "runtime.SyncAccount"}) {
		t.Fatalf("calls=%v", got)
	}
}

func TestListRefreshUsesForceQuotaThenAccounts(t *testing.T) {
	svc, runtime, _, log := newTestServices()
	runtime.views = []accounts.AccountView{{Account: accounts.Account{ID: "acc-1"}}}
	items, err := svc.Accounts.List(context.Background(), true)
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	if !runtime.refreshAll || !runtime.forceQuota {
		t.Fatalf("refreshAll=%v forceQuota=%v", runtime.refreshAll, runtime.forceQuota)
	}
	if got := log.names; !equalCalls(got, []string{"runtime.RefreshAll", "runtime.Accounts"}) {
		t.Fatalf("calls=%v", got)
	}
}

func TestListWithoutRefreshSkipsRefreshAll(t *testing.T) {
	svc, runtime, _, log := newTestServices()
	if _, err := svc.Accounts.List(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if runtime.refreshAll {
		t.Fatal("refresh=0 must not call RefreshAll")
	}
	if got := log.names; !equalCalls(got, []string{"runtime.Accounts"}) {
		t.Fatalf("calls=%v", got)
	}
}

func TestBackupSnapshotCallsStore(t *testing.T) {
	svc, _, _, log := newTestServices()
	backup, err := svc.Backup.Snapshot(context.Background(), "/tmp/backups", 5)
	if err != nil {
		t.Fatal(err)
	}
	if backup.Name != "snap.db" {
		t.Fatalf("backup=%+v", backup)
	}
	if got := log.names; !equalCalls(got, []string{"store.Backup"}) {
		t.Fatalf("calls=%v", got)
	}
}

func equalCalls(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
