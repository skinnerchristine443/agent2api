package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
)

// Runtime 是 control 调用的进程生命周期接口。持久化以及启用/导入顺序都在这里；
// Manager 只负责启动、停止和同步连接池。
type Runtime interface {
	StartAccount(ctx context.Context, account accounts.Account) error
	StopAccount(id string) error
	RemoveAccount(id string) error
	SyncAccount(ctx context.Context, before, after accounts.Account) error
	// SetModelRequestsEnabled 在运行中的连接池里切换某个账号的模型请求开关，
	// 不启动也不停止它。底层 secret 的整个读-改-写过程由 Manager 在同一把锁下
	// 完成，因此 control 自身绝不执行该读-改-写。
	SetModelRequestsEnabled(ctx context.Context, id string, enabled bool) error
	AccountView(ctx context.Context, id string) (accounts.AccountView, error)
	Accounts(ctx context.Context) ([]accounts.AccountView, error)
	RefreshAccount(ctx context.Context, id string, forceQuota bool) error
	RefreshAll(ctx context.Context, forceQuota bool) error
	CheckinAccount(ctx context.Context, accountID string) (accounts.Account, error)
	// ClearCooldowns 将账号从冷却中释放，内存与磁盘两处都要。二者缺一不可：
	// 只清理已持久化的行会被下一次连接池回写覆盖。
	ClearCooldowns(ctx context.Context, accountID, model string) (int64, error)
	ReloadProxyURL(ctx context.Context, value string) error
	ReplaceProxyAPIKey(ctx context.Context, key string) error
	Store() accounts.AccountStore
}

// Accounts 通过 Store + Runtime 编排控制台的账号操作。
// HTTP 处理器仍负责解码与错误码映射。
type Accounts struct {
	Providers *providers.Registry
	runtime   Runtime
}

func NewAccounts(runtime Runtime) *Accounts {
	if runtime == nil {
		return nil
	}
	return &Accounts{runtime: runtime}
}

func (a *Accounts) store() accounts.AccountStore {
	return a.runtime.Store()
}

func (a *Accounts) List(ctx context.Context, refresh bool) ([]accounts.AccountView, error) {
	if refresh {
		_ = a.runtime.RefreshAll(ctx, true)
	}
	return a.runtime.Accounts(ctx)
}

func (a *Accounts) Get(ctx context.Context, id string) (accounts.AccountView, error) {
	return a.runtime.AccountView(ctx, id)
}

func (a *Accounts) Create(ctx context.Context, input accounts.CreateAccount) (accounts.Account, error) {
	account, err := a.store().Create(ctx, input)
	if err != nil {
		return accounts.Account{}, err
	}
	// 该开关不是账号表的一行：要在启动账号之前通过 runtime（底层 secret 的唯一
	// 权威）持久化它，这样连接池项才会以正确的值注册。
	if input.ModelRequestsEnabled != nil {
		if err := a.runtime.SetModelRequestsEnabled(ctx, account.ID, *input.ModelRequestsEnabled); err != nil {
			return account, err
		}
	}
	if account.Enabled {
		if err := a.runtime.StartAccount(ctx, account); err != nil {
			return account, err
		}
	}
	return account, nil
}

func (a *Accounts) Update(ctx context.Context, id string, input accounts.UpdateAccount) (accounts.Account, error) {
	before, err := a.store().Get(ctx, id)
	if err != nil {
		return accounts.Account{}, err
	}
	if err := a.store().Update(ctx, id, input); err != nil {
		return accounts.Account{}, err
	}
	after, err := a.store().Get(ctx, id)
	if err != nil {
		return accounts.Account{}, err
	}
	if input.ModelRequestsEnabled != nil {
		if err := a.runtime.SetModelRequestsEnabled(ctx, id, *input.ModelRequestsEnabled); err != nil {
			return after, err
		}
	}
	if err := a.runtime.SyncAccount(ctx, before, after); err != nil {
		return after, err
	}
	return after, nil
}

func (a *Accounts) Delete(ctx context.Context, id string) error {
	if err := a.runtime.StopAccount(id); err != nil {
		return err
	}
	if err := a.store().Delete(ctx, id); err != nil {
		return err
	}
	return a.runtime.RemoveAccount(id)
}

// ImportCredentialPayload 创建一个禁用的进程内账号，写入 provider 凭据 blob，
// 然后可选地启用它。凭据写入失败会删除该账号，与此前 HTTP 处理器的行为一致。
// 写入成功后的 Get 是尽力而为的，同样与该处理器一致。
func (a *Accounts) ImportCredentialPayload(ctx context.Context, input accounts.CreateAccount, format string, payload []byte, enable bool) (accounts.Account, error) {
	input.Enabled = false
	account, err := a.store().Create(ctx, input)
	if err != nil {
		return accounts.Account{}, err
	}
	if err := a.store().SaveCredentialPayload(ctx, account.ID, format, payload); err != nil {
		_ = a.store().Delete(ctx, account.ID)
		_ = a.runtime.RemoveAccount(account.ID)
		return accounts.Account{}, err
	}
	if enable {
		enabled := true
		if err := a.store().Update(ctx, account.ID, accounts.UpdateAccount{Enabled: &enabled}); err != nil {
			return accounts.Account{}, err
		}
		account, err = a.store().Get(ctx, account.ID)
		if err != nil {
			return accounts.Account{}, err
		}
		if err := a.runtime.StartAccount(ctx, account); err != nil {
			return account, err
		}
	}
	imported, _ := a.store().Get(ctx, account.ID)
	return imported, nil
}

func (a *Accounts) RefreshAll(ctx context.Context, forceQuota bool) error {
	return a.runtime.RefreshAll(ctx, forceQuota)
}

func (a *Accounts) RefreshAccount(ctx context.Context, id string, forceQuota bool) error {
	return a.runtime.RefreshAccount(ctx, id, forceQuota)
}

func (a *Accounts) GetStored(ctx context.Context, id string) (accounts.Account, error) {
	return a.store().Get(ctx, id)
}

func (a *Accounts) ListCheckins(ctx context.Context, id string, limit int) ([]accounts.CheckinRecord, error) {
	return a.store().ListCheckinRecords(ctx, id, limit)
}

// ListGrowthObservations 返回账号的成长日志（最新的在前）。
func (a *Accounts) ListGrowthObservations(ctx context.Context, id string, limit int) ([]accounts.GrowthObservation, error) {
	return a.store().ListGrowthObservations(ctx, id, limit)
}

// ListQuotaAlerts 推导出所有账号中处于活跃状态的额度告警。
// 该列表在每次调用时重新计算；不持久化任何内容。
func (a *Accounts) ListQuotaAlerts(ctx context.Context) ([]accounts.QuotaAlert, error) {
	list, err := a.store().List(ctx)
	if err != nil {
		return nil, err
	}
	alerts := make([]accounts.QuotaAlert, 0)
	for _, account := range list {
		alerts = append(alerts, accounts.QuotaAlerts(account)...)
	}
	return alerts, nil
}

func (a *Accounts) Checkin(ctx context.Context, id string) (accounts.Account, error) {
	account, err := a.GetStored(ctx, id)
	if err != nil {
		return accounts.Account{}, err
	}
	if _, supported := providers.CheckinFor(account.Provider, account.ProviderRegion); !supported {
		return accounts.Account{}, operationError("provider_unsupported", "check-in is not available for this provider and region")
	}
	return a.runtime.CheckinAccount(ctx, id)
}

// GrowthRuntime 是成长中心背后的可选 runtime 能力。Manager 实现它；control 采用
// 类型断言而不是扩展 Runtime，这样不支持成长的 runtime 替身仍然有效，且会显式报错。
type GrowthRuntime interface {
	GrowthStatus(ctx context.Context, accountID string) (providers.GrowthStatus, error)
	ClaimGrowthRewards(ctx context.Context, accountID string) (providers.GrowthClaimResult, error)
}

// GrowthStatus 返回某个账号的只读成长中心聚合数据。
// 它不执行任何写入，也不启动任何调度。
func (a *Accounts) GrowthStatus(ctx context.Context, id string) (providers.GrowthStatus, error) {
	runner, ok := a.runtime.(GrowthRuntime)
	if !ok {
		return providers.GrowthStatus{}, growthUnsupported()
	}
	status, err := runner.GrowthStatus(ctx, id)
	if errors.Is(err, providers.ErrUnsupported) {
		return providers.GrowthStatus{}, growthUnsupported()
	}
	return status, err
}

// ClaimGrowthRewards 为某个账号执行幂等的成长中心领取。
// 它把结果记录到 runtime，自身从不调度。
func (a *Accounts) ClaimGrowthRewards(ctx context.Context, id string) (providers.GrowthClaimResult, error) {
	runner, ok := a.runtime.(GrowthRuntime)
	if !ok {
		return providers.GrowthClaimResult{}, growthUnsupported()
	}
	result, err := runner.ClaimGrowthRewards(ctx, id)
	if errors.Is(err, providers.ErrUnsupported) {
		return providers.GrowthClaimResult{}, growthUnsupported()
	}
	return result, err
}

// growthUnsupported 是面向控制台的错误，用于不支持成长能力的 provider。
// 它映射为 400 provider_unsupported，与签到保持一致。
func growthUnsupported() error {
	return operationError("provider_unsupported", "growth center is not available for this provider")
}

func (a *Accounts) LoadCredentialPayload(ctx context.Context, id string) (string, []byte, error) {
	return a.store().LoadCredentialPayload(ctx, id)
}

func (a *Accounts) ReloadProxyURL(ctx context.Context, value string) error {
	return a.runtime.ReloadProxyURL(ctx, value)
}

func (a *Accounts) ReplaceProxyAPIKey(ctx context.Context, key string) error {
	return a.runtime.ReplaceProxyAPIKey(ctx, key)
}

type AccountExport struct {
	Format     string
	Name       string
	Provider   string
	Region     string
	Credential []byte
}

func (a *Accounts) Export(ctx context.Context, id string) (AccountExport, error) {
	account, err := a.GetStored(ctx, id)
	if err != nil {
		return AccountExport{}, err
	}
	// 导出以 payload 表为准：它是运行时唯一权威读取面（凭据刷新后的最新值所在）。
	format, payload, err := a.LoadCredentialPayload(ctx, id)
	if err != nil {
		return AccountExport{}, err
	}
	return AccountExport{
		Format: format, Name: account.Name, Provider: account.Provider, Region: account.ProviderRegion, Credential: payload,
	}, nil
}

type AccountAdminAction struct {
	AccountID   string
	Action      string
	Method      string
	ContentType string
	Body        []byte
	CallbackURL string
}

type AccountAdminResult struct {
	Kind        string
	Session     providers.LoginSession
	LoginDone   bool
	LoginStatus string
	LoginMsg    string
	Cooldowns   []accounts.CooldownRow
	Cleared     int64
}

func (a *Accounts) Admin(ctx context.Context, input AccountAdminAction) (AccountAdminResult, error) {
	account, storeErr := a.GetStored(ctx, input.AccountID)
	// 每个 provider 都是进程内的；登录经由 adapter 执行。
	inProcess := storeErr == nil && account.Provider != ""
	switch input.Action {
	case "checkins", "checkin":
		if storeErr != nil {
			return AccountAdminResult{}, storeErr
		}
		if _, supported := providers.CheckinFor(account.Provider, account.ProviderRegion); !supported {
			return AccountAdminResult{}, operationError("provider_unsupported", "check-in is not available for this provider and region")
		}
		return AccountAdminResult{Kind: input.Action}, nil
	case "cooldowns", "cooldowns/clear":
		if storeErr != nil {
			return AccountAdminResult{}, storeErr
		}
		if input.Action == "cooldowns" {
			rows, err := a.runtime.Store().LoadCooldowns(ctx)
			if err != nil {
				return AccountAdminResult{}, err
			}
			scoped := make([]accounts.CooldownRow, 0, len(rows))
			for _, row := range rows {
				if row.AccountID == input.AccountID {
					scoped = append(scoped, row)
				}
			}
			return AccountAdminResult{Kind: "cooldowns", Cooldowns: scoped}, nil
		}
		var payload struct {
			Model string `json:"model"`
		}
		if len(bytes.TrimSpace(input.Body)) > 0 {
			if err := json.Unmarshal(input.Body, &payload); err != nil {
				return AccountAdminResult{}, operationError("invalid_request", "body must be a JSON object")
			}
		}
		cleared, err := a.runtime.ClearCooldowns(ctx, input.AccountID, payload.Model)
		if err != nil {
			return AccountAdminResult{}, err
		}
		return AccountAdminResult{Kind: "cooldowns_clear", Cleared: cleared}, nil
	case "login/device":
		if !inProcess {
			return AccountAdminResult{}, storeErr
		}
		session, err := a.StartLogin(ctx, input.AccountID)
		if err != nil {
			return AccountAdminResult{}, err
		}
		return AccountAdminResult{Kind: "login_start", Session: session, LoginStatus: "pending"}, nil
	case "login/status":
		if !inProcess {
			return AccountAdminResult{}, storeErr
		}
		done, message, err := a.PollLogin(ctx, input.AccountID)
		if err != nil {
			return AccountAdminResult{}, err
		}
		status := "pending"
		if done {
			status = "ok"
		}
		return AccountAdminResult{Kind: "login_status", LoginDone: done, LoginStatus: status, LoginMsg: message}, nil
	case "login/callback":
		if err := a.CompleteLogin(ctx, input.AccountID, input.CallbackURL); err != nil {
			return AccountAdminResult{}, err
		}
		return AccountAdminResult{Kind: "login_complete", LoginStatus: "ok", LoginMsg: "login complete"}, nil
	case "login/pat":
		if storeErr != nil {
			return AccountAdminResult{}, storeErr
		}
		var payload struct {
			PAT string `json:"pat"`
		}
		if err := json.Unmarshal(input.Body, &payload); err != nil {
			return AccountAdminResult{}, operationError("invalid_request", err.Error())
		}
		if err := a.LoginPAT(ctx, input.AccountID, payload.PAT); err != nil {
			return AccountAdminResult{}, err
		}
		return AccountAdminResult{Kind: "login_complete", LoginStatus: "ok", LoginMsg: "login complete"}, nil
	default:
		return AccountAdminResult{}, operationError("not_found", "unknown account action")
	}
}
