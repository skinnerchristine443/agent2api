package runtime

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agent2api/internal/accounts"
	proxyutil "agent2api/internal/proxy"
)

// control.Accounts 使用的账号生命周期辅助函数。持久化以及 enable/import 的顺序
// 住在 control 里；这些方法只变更活池。它们不掌管 Manager.mu、persist channel
// 或 SQLite 写入，控制台视图读取除外。

func (m *Manager) StartAccount(ctx context.Context, account Account) error {
	return m.startAccount(ctx, account)
}

func (m *Manager) StopAccount(id string) error {
	return m.stopAccount(id)
}

func (m *Manager) RemoveAccount(id string) error {
	runtimeDir := filepath.Join(m.config.DataDir, "runtime", id)
	if err := os.RemoveAll(runtimeDir); err != nil {
		return fmt.Errorf("remove account runtime: %w", err)
	}
	return nil
}

func (m *Manager) SyncAccount(ctx context.Context, before, after Account) error {
	// 请求净化按请求生效，所以把它同步进池中，无需重启任何东西。
	if before.DropSystemPrompt != after.DropSystemPrompt {
		m.pool.SetDropSystemPrompt(after.ID, after.DropSystemPrompt)
	}
	// 每日守门设置像提示词策略一样按请求生效，所以它们同步进活池项，
	// 无需重启任何东西。
	if before.ReserveCredits != after.ReserveCredits || before.DailyTokenLimit != after.DailyTokenLimit ||
		before.DailyCreditLimit != after.DailyCreditLimit || before.DailyModelTokenLimit != after.DailyModelTokenLimit {
		m.pool.SetAccountGuards(after.ID, after.ReserveCredits, after.DailyTokenLimit, after.DailyCreditLimit, after.DailyModelTokenLimit)
	}
	// 模型请求开关住在 app_secrets 里，而非我们在此 diff 的 accounts 行里，
	// 因此它是被重新断言到活池项上，而不是被比较。翻转它只改变账号是否被选中：
	// 它绝不能把账号移入或移出池（那是下面 Enabled 做的事）。重新断言在 mu 下
	// 运行，因此不会与并发的翻转交错。
	m.reassertModelRequestsDisabled(after.ID)
	if before.Priority != after.Priority {
		m.pool.SetWeight(after.ID, after.Priority)
	}
	if before.Enabled && !after.Enabled {
		return m.stopAccount(after.ID)
	}
	if !before.Enabled && after.Enabled {
		return m.startAccount(ctx, after)
	}
	// ProxyURL 变化无需任何动作：进程内适配器按请求解析生效的代理。MaxInFlight
	// 挂在池项上，所以重新注册账号即可应用新上限。
	if before.Enabled && after.Enabled && before.MaxInFlight != after.MaxInFlight {
		return m.startAccount(ctx, after)
	}
	return nil
}

// SetModelRequestsEnabled 是账号级模型请求开关的唯一权威写入者。整个
// 读-改-写-持久化都在 mu 下运行，因此两个并发翻转（不同账号，或翻转与创建
// 竞争）不会丢失更新，且持久化的 secret 绝不会偏离驱动路由的内存集合
// ——那种偏离本会让一个被刻意关闭的账号在重启后悄悄恢复服务。池标志在同一
// 临界区内翻转，且该账号被刻意地不启动也不停止：它留在池中，使其
// ready/hot/in_flight 保持活跃，签到、配额刷新与状态持续可用。加载或写入失败
// 会被返回，绝不吞掉。
func (m *Manager) SetModelRequestsEnabled(ctx context.Context, id string, enabled bool) error {
	if m == nil || id == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	set, err := accounts.ReadModelRequestsDisabled(ctx, m.store)
	if err != nil {
		return err
	}
	if enabled {
		delete(set, id)
	} else {
		set[id] = struct{}{}
	}
	if err := m.store.SetSecret(ctx, accounts.ModelRequestsDisabledSecret, accounts.EncodeModelRequestsDisabled(set)); err != nil {
		return err
	}
	m.modelRequestsDisabled = set
	m.applyModelRequestsDisabledLocked(id)
	return nil
}

// loadModelRequestsDisabled 在启动时从 secret 播种内存集合。读取与赋值共用
// mu，因此并发的 SetModelRequestsEnabled 绝不会被陈旧的启动快照覆盖。
//
// 读取失败会报告给调用方，好让启动 fail closed。它刻意不等同于损坏的值：
// 畸形值在 ReadModelRequestsDisabled 内部按 fail-open（空集合）解析，永远不会
// 走到这条错误路径。对不可读的存储采取 fail closed 是安全的选择，因为我们无法
// 知道运维关闭了哪些账号，而静默地为它们全部恢复模型流量，正是这个开关存在的
// 意义所在。
func (m *Manager) loadModelRequestsDisabled(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	set, err := accounts.ReadModelRequestsDisabled(ctx, m.store)
	if err != nil {
		// 高可见、显式的失败：拒绝启动胜过静默地为每个被关闭的账号重新启用
		// 模型请求。
		log.Printf("ERROR: %s is unreadable; refusing to start so every account is not silently re-enabled for model requests: %v", accounts.ModelRequestsDisabledSecret, err)
		return fmt.Errorf("load %s: %w", accounts.ModelRequestsDisabledSecret, err)
	}
	m.modelRequestsDisabled = set
	return nil
}

// applyModelRequestsDisabledLocked 把 id 的内存开关镜像到活池项上。
// 必须在持有 m.mu 时调用。
func (m *Manager) applyModelRequestsDisabledLocked(id string) {
	if m.pool == nil {
		return
	}
	_, disabled := m.modelRequestsDisabled[id]
	m.pool.SetModelRequestsDisabled(id, disabled)
}

// reassertModelRequestsDisabled 在 mu 下把 id 的池标志与内存集合重新同步，
// 使 SyncAccount 无法与并发的翻转交错并留下陈旧的路由标志。
func (m *Manager) reassertModelRequestsDisabled(id string) {
	if m == nil || id == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.applyModelRequestsDisabledLocked(id)
}

// modelRequestsDisabledFor 报告当前集合中 id 是否被关闭了模型请求。
func (m *Manager) modelRequestsDisabledFor(id string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, disabled := m.modelRequestsDisabled[id]
	return disabled
}

// modelRequestsDisabledSnapshot 复制 disabled 集合，使视图构建器无需持有 mu
// 即可读取。空集合返回 nil。
func (m *Manager) modelRequestsDisabledSnapshot() map[string]struct{} {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.modelRequestsDisabled) == 0 {
		return nil
	}
	copied := make(map[string]struct{}, len(m.modelRequestsDisabled))
	for id := range m.modelRequestsDisabled {
		copied[id] = struct{}{}
	}
	return copied
}

func (m *Manager) Create(ctx context.Context, input CreateAccount) (Account, error) {
	account, err := m.store.Create(ctx, input)
	if err != nil {
		return Account{}, err
	}
	if account.Enabled {
		if err := m.startAccount(ctx, account); err != nil {
			return account, err
		}
	}
	return account, nil
}

func (m *Manager) Update(ctx context.Context, id string, input UpdateAccount) error {
	before, err := m.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := m.store.Update(ctx, id, input); err != nil {
		return err
	}
	after, err := m.store.Get(ctx, id)
	if err != nil {
		return err
	}
	return m.SyncAccount(ctx, before, after)
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	if err := m.stopAccount(id); err != nil {
		return err
	}
	if err := m.store.Delete(ctx, id); err != nil {
		return err
	}
	return m.RemoveAccount(id)
}

// RefreshAccount 重探一个账号的健康、配额和模型目录。forceQuota 绕过该账号的
// 缓存配额，对应控制台的 "Refresh credits" 按钮。它只刷新被请求的账号，因此
// ClearCooldowns 会在内存和磁盘上把账号从冷却中释放。
//
// 内存池最先被清除：它是活的权威来源，且其观察者会把快照写回 SQLite，所以只删
// 持久化的行会被下一次回写撤销。
func (m *Manager) ClearCooldowns(ctx context.Context, accountID, model string) (int64, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return 0, ErrAccountNotFound
	}
	if m.pool != nil {
		if model == "" {
			// MarkOK 清除账号级窗口；模型级窗口在下面被清除，因此一次调用
			// 即可释放一切。
			m.pool.MarkOK(accountID, "")
		}
		m.pool.MarkOK(accountID, model)
	}
	if m.poolState == nil {
		return 0, nil
	}
	return m.poolState.ClearCooldown(ctx, accountID, model)
}

func (m *Manager) AccountView(ctx context.Context, id string) (AccountView, error) {
	if m == nil || m.store == nil {
		return AccountView{}, fmt.Errorf("account manager not ready")
	}
	account, err := m.store.Get(ctx, id)
	if err != nil {
		return AccountView{}, err
	}
	// 唯一获准的构造函数：它从权威集合推导 ModelRequestsEnabled，因此这条路径
	// 绝不会把它留在 false 零值上。
	view := accounts.NewAccountView(account, m.modelRequestsDisabledSnapshot())
	view.ProxyURL = proxyutil.Redact(account.ProxyURL)
	if item, ok := m.pool.ByID(account.ID); ok {
		view.Ready = item.Ready == nil || *item.Ready
		view.Hot = item.Hot != nil && *item.Hot
		view.InFlight = item.InFlight
		view.Restarts = item.Restarts
		view.RuntimeState = item.RuntimeState
		view.RestartBackoffLevel = item.RestartBackoffLevel
		if !item.NextRestartAt.IsZero() && time.Now().Before(item.NextRestartAt) {
			view.NextRestartAt = item.NextRestartAt.UTC().Format(time.RFC3339)
		}
		if item.Quota != nil {
			view.Quota = item.Quota
		}
		view.ModelCooldowns = activeModelCooldowns(item.ModelDownUntil)
		if !item.DownUntil.IsZero() && time.Now().Before(item.DownUntil) {
			view.DownUntil = item.DownUntil.UTC().Format(time.RFC3339)
		}
		if view.LastError == "" {
			view.LastError = item.LastError
		}
		if view.LastErrorKind == "" {
			view.LastErrorKind = item.LastKind
		}
	}
	return view, nil
}

func (m *Manager) Accounts(ctx context.Context) ([]AccountView, error) {
	stored, err := m.store.List(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]AccountView, 0, len(stored))
	disabled := m.modelRequestsDisabledSnapshot()
	for _, account := range stored {
		// 唯一获准的构造函数（见 AccountView）：从权威集合推导该开关，
		// 而不是采用裸字面量的 false 零值。
		view := accounts.NewAccountView(account, disabled)
		view.ProxyURL = proxyutil.Redact(account.ProxyURL)
		if item, ok := m.pool.ByID(account.ID); ok {
			view.Ready = item.Ready == nil || *item.Ready
			view.Hot = item.Hot != nil && *item.Hot
			view.InFlight = item.InFlight
			view.Restarts = item.Restarts
			view.RuntimeState = item.RuntimeState
			view.RestartBackoffLevel = item.RestartBackoffLevel
			if !item.NextRestartAt.IsZero() && time.Now().Before(item.NextRestartAt) {
				view.NextRestartAt = item.NextRestartAt.UTC().Format(time.RFC3339)
			}
			view.Quota = item.Quota
			view.ModelCooldowns = activeModelCooldowns(item.ModelDownUntil)
			if !item.DownUntil.IsZero() && time.Now().Before(item.DownUntil) {
				view.DownUntil = item.DownUntil.UTC().Format(time.RFC3339)
			}
			if view.LastError == "" {
				view.LastError = item.LastError
			}
			if view.LastErrorKind == "" {
				view.LastErrorKind = item.LastKind
			}
		}
		views = append(views, view)
	}
	return views, nil
}

func activeModelCooldowns(cooldowns map[string]time.Time) map[string]string {
	if len(cooldowns) == 0 {
		return nil
	}
	now := time.Now()
	active := make(map[string]string, len(cooldowns))
	for model, until := range cooldowns {
		if now.Before(until) {
			active[model] = until.UTC().Format(time.RFC3339)
		}
	}
	if len(active) == 0 {
		return nil
	}
	return active
}
