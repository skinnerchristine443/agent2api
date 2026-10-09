package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/control"
	applogs "agent2api/internal/logs"
)

// t54ErrReader 是一个永远读取失败的请求体，用于驱动 io.ReadAll 的错误分支。
type t54ErrReader struct{}

func (t54ErrReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// 本文件为本批（T54 · console 面）用例提供最小测试替身。
//
// 两个替身都用「内嵌接口」的方式继承完整方法集：只有用例真正触达的方法被实现，
// 其余方法保持未被赋值，一旦误调用会立刻 panic，从而把「测试假设与实现不符」暴露出来，
// 而不是安静地返回零值让断言失真。

// t54Payload 保存一次凭据载荷写入，用于导出路径断言。
type t54Payload struct {
	Format  string
	Payload []byte
}

// t54Store 是 accounts.AccountStore 的最小替身。
type t54Store struct {
	accounts.AccountStore

	mu       sync.Mutex
	rows     map[string]accounts.Account
	payloads map[string]t54Payload
	secrets  map[string]string
	contexts map[string]int
	settings map[string]accounts.ProviderModelSetting
	checkins []accounts.CheckinRecord
	growth   []accounts.GrowthObservation
	cooldown []accounts.CooldownRow
	seq      int

	// 注入错误：非 nil 时对应方法直接失败，用于驱动 handler 的错误分支。
	listErr   error
	getErr    error
	createErr error
	updateErr error
	deleteErr error
	// 列表类读取的注入错误。
	checkinListErr error
	growthErr      error
}

func t54NewStore() *t54Store {
	return &t54Store{
		rows:     map[string]accounts.Account{},
		payloads: map[string]t54Payload{},
		secrets:  map[string]string{},
		contexts: map[string]int{},
		settings: map[string]accounts.ProviderModelSetting{},
	}
}

func (s *t54Store) putAccount(acc accounts.Account) *t54Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[acc.ID] = acc
	return s
}

func (s *t54Store) Create(_ context.Context, input accounts.CreateAccount) (accounts.Account, error) {
	if s.createErr != nil {
		return accounts.Account{}, s.createErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	acc := accounts.Account{
		ID: fmt.Sprintf("acc-%d", s.seq), Name: input.Name,
		Provider: input.Provider, ProviderRegion: input.Region,
		Enabled: input.Enabled, MaxInFlight: input.MaxInFlight, Priority: input.Priority,
	}
	s.rows[acc.ID] = acc
	return acc, nil
}

func (s *t54Store) Get(_ context.Context, id string) (accounts.Account, error) {
	if s.getErr != nil {
		return accounts.Account{}, s.getErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	acc, ok := s.rows[id]
	if !ok {
		return accounts.Account{}, accounts.ErrAccountNotFound
	}
	return acc, nil
}

func (s *t54Store) List(context.Context) ([]accounts.Account, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]accounts.Account, 0, len(s.rows))
	for _, acc := range s.rows {
		out = append(out, acc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *t54Store) Update(_ context.Context, id string, input accounts.UpdateAccount) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	acc, ok := s.rows[id]
	if !ok {
		return accounts.ErrAccountNotFound
	}
	if input.Enabled != nil {
		acc.Enabled = *input.Enabled
	}
	if input.Name != "" {
		acc.Name = input.Name
	}
	s.rows[id] = acc
	return nil
}

func (s *t54Store) Delete(_ context.Context, id string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, id)
	return nil
}

func (s *t54Store) SaveCredentialPayload(_ context.Context, accountID, format string, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.payloads[accountID] = t54Payload{Format: format, Payload: payload}
	return nil
}

func (s *t54Store) LoadCredentialPayload(_ context.Context, accountID string) (string, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.payloads[accountID]
	if !ok {
		return "", nil, accounts.ErrAccountNotFound
	}
	return p.Format, p.Payload, nil
}

func (s *t54Store) ListCheckinRecords(context.Context, string, int) ([]accounts.CheckinRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkins, s.checkinListErr
}

func (s *t54Store) ListGrowthObservations(context.Context, string, int) ([]accounts.GrowthObservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.growth, s.growthErr
}

func (s *t54Store) LoadCooldowns(context.Context) ([]accounts.CooldownRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cooldown, nil
}

func (s *t54Store) ClearCooldown(context.Context, string, string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(len(s.cooldown)), nil
}

func (s *t54Store) GetSecret(_ context.Context, name string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.secrets[name]
	return value, ok, nil
}

func (s *t54Store) SetSecret(_ context.Context, name, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.secrets[name] = value
	return nil
}

func (s *t54Store) SetSecretOrEmpty(_ context.Context, name, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.secrets[name] = value
	return nil
}

func (s *t54Store) WorkBuddyCheckinTimeDefault(context.Context) string { return "09:00" }

func (s *t54Store) GetModelContext(_ context.Context, modelID string) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.contexts[modelID]
	return value, ok, nil
}

func (s *t54Store) SetModelContext(_ context.Context, modelID string, contextLength int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contexts[modelID] = contextLength
	return nil
}

func (s *t54Store) GetProviderModelSetting(_ context.Context, provider, modelID string) (accounts.ProviderModelSetting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings[provider+"|"+modelID], nil
}

func (s *t54Store) SetProviderModelSetting(_ context.Context, provider, modelID string, setting accounts.ProviderModelSetting) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings[provider+"|"+modelID] = setting
	return nil
}

func (s *t54Store) secretValue(name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.secrets[name]
	return value, ok
}

// t54Runtime 是 control.Runtime 的替身，记录被调用的生命周期动作。
type t54Runtime struct {
	control.Runtime

	store *t54Store

	mu    sync.Mutex
	views []accounts.AccountView
	// viewFn 覆盖 AccountView 的返回，用于针对单个 id 的不同响应。
	viewFn func(id string) (accounts.AccountView, error)

	started    []string
	stopped    []string
	removed    []string
	synced     []string
	requests   []string
	replaced   []string
	reloaded   []string
	refreshAll int

	accountsErr       error
	accountViewErr    error
	refreshAllErr     error
	refreshAccountErr error
	startErr          error
	replaceKeyErr     error

	checkinResult accounts.Account
	checkinErr    error
	clearModel    string
	clearCount    int64
}

func t54NewRuntime(store *t54Store) *t54Runtime {
	return &t54Runtime{store: store}
}

func (r *t54Runtime) Store() accounts.AccountStore { return r.store }

func (r *t54Runtime) Accounts(context.Context) ([]accounts.AccountView, error) {
	if r.accountsErr != nil {
		return nil, r.accountsErr
	}
	return r.views, nil
}

func (r *t54Runtime) AccountView(_ context.Context, id string) (accounts.AccountView, error) {
	if r.accountViewErr != nil {
		return accounts.AccountView{}, r.accountViewErr
	}
	if r.viewFn != nil {
		return r.viewFn(id)
	}
	return accounts.AccountView{Account: accounts.Account{ID: id, Name: "view-" + id}}, nil
}

func (r *t54Runtime) RefreshAll(context.Context, bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshAll++
	return r.refreshAllErr
}

func (r *t54Runtime) RefreshAccount(context.Context, string, bool) error {
	return r.refreshAccountErr
}

func (r *t54Runtime) StartAccount(_ context.Context, acc accounts.Account) error {
	if r.startErr != nil {
		return r.startErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = append(r.started, acc.ID)
	return nil
}

func (r *t54Runtime) StopAccount(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = append(r.stopped, id)
	return nil
}

func (r *t54Runtime) RemoveAccount(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removed = append(r.removed, id)
	return nil
}

func (r *t54Runtime) SyncAccount(_ context.Context, _, after accounts.Account) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.synced = append(r.synced, after.ID)
	return nil
}

func (r *t54Runtime) SetModelRequestsEnabled(_ context.Context, id string, _ bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, id)
	return nil
}

func (r *t54Runtime) CheckinAccount(context.Context, string) (accounts.Account, error) {
	return r.checkinResult, r.checkinErr
}

func (r *t54Runtime) ClearCooldowns(_ context.Context, _, model string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearModel = model
	return r.clearCount, nil
}

func (r *t54Runtime) ReloadProxyURL(_ context.Context, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reloaded = append(r.reloaded, value)
	return nil
}

func (r *t54Runtime) ReplaceProxyAPIKey(_ context.Context, key string) error {
	if r.replaceKeyErr != nil {
		return r.replaceKeyErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.replaced = append(r.replaced, key)
	return nil
}

// t54Handler 组装一个只挂了账号服务的 Handler。
func t54Handler(rt *t54Runtime) *Handler {
	return &Handler{Control: &control.Services{Accounts: control.NewAccounts(rt)}}
}

// t54PersisterOnly 只实现写入面、不实现查询面，使 RequestRecorder.Store() 返回 nil，
// 用来触发 logs 的 ErrUnavailable 分支。
type t54PersisterOnly struct {
	applogs.RequestPersister
}

// t54RequestStore 是 logs.RequestStore 的替身，记录最近一次的查询参数。
type t54RequestStore struct {
	applogs.RequestStore

	mu         sync.Mutex
	lastFilter accounts.RequestLogFilter
	listCalls  int

	list       accounts.RequestLogList
	listErr    error
	getID      string
	getItem    accounts.RequestLog
	getErr     error
	clearCount int64
	clearErr   error

	stats     accounts.RequestStats
	statsFrom time.Time
	statsTo   time.Time
	statsErr  error
}

func (s *t54RequestStore) ListRequestLogs(_ context.Context, filter accounts.RequestLogFilter) (accounts.RequestLogList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastFilter = filter
	s.listCalls++
	return s.list, s.listErr
}

func (s *t54RequestStore) GetRequestLog(_ context.Context, id string) (accounts.RequestLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getID = id
	return s.getItem, s.getErr
}

func (s *t54RequestStore) ClearRequestLogs(context.Context) (int64, error) {
	return s.clearCount, s.clearErr
}

func (s *t54RequestStore) SummarizeRequestLogs(_ context.Context, from, to time.Time) (accounts.RequestStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statsFrom, s.statsTo = from, to
	return s.stats, s.statsErr
}

func (s *t54RequestStore) captured() (accounts.RequestLogFilter, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastFilter, s.listCalls
}

func (s *t54RequestStore) capturedStats() (time.Time, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statsFrom, s.statsTo
}

// t54ErrCode 从错误响应体里取出 error.code，取不到即终止用例。
func t54ErrCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v (%s)", err, rec.Body)
	}
	return body.Error.Code
}

// t54ErrMessage 从错误响应体里取出 error.message。
func t54ErrMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v (%s)", err, rec.Body)
	}
	return body.Error.Message
}
