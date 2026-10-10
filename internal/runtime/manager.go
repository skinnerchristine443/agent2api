package runtime

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"agent2api/internal/providers"
)

var errManagerClosed = errors.New("account manager closed")

type ManagerConfig struct {
	DataDir string
	// ProxyAPIKey 是「密钥轮换 → runtime 知悉」的同步点：由 RotateProxy 经
	// ReplaceProxyAPIKey 写入。当前 runtime 无读取方——真正生效的实时密钥是
	// auth.Verifier 的原子值；保留为显式契约点（评估记录见 docs/02 §6.1）。
	ProxyAPIKey   string
	ProxyURL      string
	MaxLogWriters io.Writer
}

// WorkBuddyMaintainer 是 Phase N 的运维能力面。由 workbuddy.Client 实现；
// 刻意保持窄接口，以免 accounts 在 AccountProber 上长出一个通用的
// 签到能力。
type WorkBuddyMaintainer interface {
	DailyCheckin(ctx context.Context, accountID string) (string, error)
	Keepalive(ctx context.Context, accountID string) error
	// ReportActivity 发送一条对话活跃上报（点亮 growth 连登）。默认关闭，
	// 由 AGENT2API_ACTIVITY_REPORT 环境变量开启（见 runScheduledActivityReport）。
	ReportActivity(ctx context.Context, accountID string) error
	// ActivityStreakDays 回读连登天数，用于上报后的自检（发现「200 但静默丢弃」）。
	ActivityStreakDays(ctx context.Context, accountID string) (int, error)
}

type Manager struct {
	config         ManagerConfig
	store          AccountStore
	poolState      PoolStateStore
	pool           *Pool
	providers      *providers.Registry
	workbuddy      WorkBuddyMaintainer
	checkinRunning map[string]bool
	// modelRequestsDisabled 是关闭了模型请求的账号集合，启动时从 app_secrets
	// 加载，由 SetModelRequestsDisabled 更新。受 mu 保护。它既是池标志、
	// 也是控制台正向语义视图的权威来源。
	modelRequestsDisabled map[string]struct{}
	// capabilityAbsentUntil 备忘 "该账号缺少此能力" 的结论
	// （键 accountID|capability -> 重探时刻）。见 capability_memo.go。受 mu 保护。
	capabilityAbsentUntil map[string]time.Time
	// quotaAlertLogged 记录每个活跃配额告警的上一次日志时间戳
	// （键 accountID|category），使 30s 循环每个冷却期只记一次日志，
	// 并在告警清除后重新武装。见 manager_alerts.go。受 mu 保护。
	quotaAlertLogged map[string]time.Time
	// capabilityClock 是探测 TTL 的测试接缝；为 nil 表示 time.Now。
	capabilityClock func() time.Time
	// maintenanceGate 报告「维护中」（更新器正在替换本容器）；为 nil 表示
	// 无门。维护循环在门开启期间跳过签到 / keepalive 等写入（见
	// runMaintenanceTick）。装配晚于循环启动，故读写受 mu 保护。
	maintenanceGate func() bool
	mu              sync.Mutex
	runCtx          context.Context
	cancel          context.CancelFunc
	// 持久化路径是单个由互斥锁保护的 goroutine。脏集合以账号 ID 为键并在入队时
	// 合并：晚于较新快照到达的陈旧快照（更旧的 StateVersion）会被丢弃，因此无论
	// 观察者的到达顺序如何，最终持久化的状态都是最新的池状态（观察者在 p.mu 释放后
	// 运行，所以两个并发变更可能以非生产顺序入队）。以账号 ID 为键还把集合的大小
	// 限制为账号数量——在 DB 压力下它不会像无界 FIFO 那样无限增长。Close 在同一把锁
	// 下设置关闭标志（没有 channel 要关，也就没有向已关闭 channel 发送的 panic）；
	// Flush 入队一个标记，该标记只在脏集合排空到空时才触发，因此在 Flush 返回前，
	// flush 之前入队的一切都已持久化。
	persistMu         sync.Mutex
	persistCond       *sync.Cond
	persistDirty      map[string]Item   // accountID -> 最新快照（入队时合并）
	persistFlushes    []chan struct{}   // 有序的 flush 标记，脏集合排空时触发
	persistedVersions map[string]uint64 // accountID -> 最后写入 SQLite 的版本
	persistClosed     bool
	persistCloseCh    chan struct{} // 由 Close() 关闭；drainer 的重试退避会监听它
	persistDone       sync.WaitGroup

	// 最新的资源采样（仅服务器进程：每个账号都跑在进程内，因此没有第二个
	// 进程可采样）。由维护循环的 ticker 发布，或在首次读取时惰性采样。
	// atomic.Pointer，在成功的 sampleResources 调用之后永不为 nil。
	resources atomic.Pointer[ResourceSnapshot]
	resMu     sync.Mutex       // 串行化 sampleResources
	cpuPrev   map[int]procTime // pid -> 用于 CPU% 增量的上一次 jiffies 采样
}

func NewManager(config ManagerConfig, store AccountStore) *Manager {
	runCtx, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		config:                config,
		store:                 store,
		poolState:             store,
		pool:                  NewPool(),
		runCtx:                runCtx,
		cancel:                cancel,
		modelRequestsDisabled: map[string]struct{}{},
		persistDirty:          map[string]Item{},
		persistedVersions:     map[string]uint64{},
		persistCloseCh:        make(chan struct{}),
		cpuPrev:               map[int]procTime{},
	}
	manager.persistCond = sync.NewCond(&manager.persistMu)
	manager.persistDone.Add(1)
	go manager.drainCooldowns()
	manager.pool.SetObserver(func(item Item) {
		// 合并进按账号划分的脏集合。快照是在 p.mu 下克隆并在那里打上
		// StateVersion 的；观察者在 p.mu 释放后运行，所以两个并发变更可能以
		// 非生产顺序入队。版本检查会丢弃陈旧快照（集合中已有更旧的版本），
		// 因此脏集合总是持有每个账号的最新已知状态。以账号 ID 为键把集合
		// 的大小限制为账号数量。
		manager.persistMu.Lock()
		if !manager.persistClosed {
			if existing, ok := manager.persistDirty[item.ID]; !ok || item.StateVersion >= existing.StateVersion {
				manager.persistDirty[item.ID] = item
				manager.persistCond.Signal()
			}
		}
		manager.persistMu.Unlock()
	})
	return manager
}

// drainCooldowns 一次持久化一个账号的池状态变更，在发出任何 flush 信号之前
// 先把按账号划分的脏集合排空到空。因为脏集合中的每一项都是其账号的最新已知快照
// （陈旧版本在入队时被丢弃），把整个集合排空到空即可保证最终持久化的状态是最新的
// 池状态。flush 标记只在脏集合为空时触发，因此在 Flush 返回前，flush 调用之前
// 入队的一切都已持久化。
func (m *Manager) drainCooldowns() {
	defer m.persistDone.Done()
	ctx := context.Background()
	for {
		m.persistMu.Lock()
		for len(m.persistDirty) == 0 && len(m.persistFlushes) == 0 && !m.persistClosed {
			m.persistCond.Wait()
		}
		// 在触发任何 flush 之前先排空整个脏集合，好让 flush 的调用方
		// 看到每个账号的最新状态都已落盘。
		if len(m.persistDirty) == 0 {
			if len(m.persistFlushes) > 0 {
				flushes := m.persistFlushes
				m.persistFlushes = nil
				m.persistMu.Unlock()
				for _, done := range flushes {
					close(done)
				}
				continue
			}
			if m.persistClosed {
				m.persistMu.Unlock()
				return
			}
			m.persistMu.Unlock()
			continue
		}
		// 选取一个稳定的账号（最小 ID）以获得确定的排空顺序。
		id := ""
		for k := range m.persistDirty {
			if id == "" || k < id {
				id = k
			}
		}
		item := m.persistDirty[id]
		delete(m.persistDirty, id)
		// 丢弃比磁盘上已有状态更旧的快照：当这个更旧的快照躺在脏集合里时，
		// 一个更新的变更可能已经持久化了。没有这道守卫，迟到到达的陈旧快照
		// 就会覆盖更新的 SQLite 状态。
		if persisted, ok := m.persistedVersions[id]; ok && item.StateVersion <= persisted {
			m.persistMu.Unlock()
			continue
		}
		m.persistMu.Unlock()

		err := m.poolState.RecordPoolState(ctx, poolStateFromItem(item))
		if err == nil {
			err = m.poolState.SaveCooldowns(ctx, item.ID, cooldownRows(item))
		}
		// 免费/付费的判定搭同一份快照（T36）。只有非空 map 才写入：
		// 空/nil map 表示 "本次快照中没有判定"（启动顺序竞态），而抹掉已存的
		// 知识比保留一行陈旧数据更糟。
		if err == nil && len(item.ModelFree) > 0 {
			err = m.store.SaveAccountModelFree(ctx, item.ID, item.ModelFree)
		}
		m.persistMu.Lock()
		if err != nil {
			// 写入失败（SQLite 被锁、磁盘错误、连接问题）。把快照放回脏集合
			// 以便重试；若期间有更新的快照到达，合并的版本检查会保留更新的那个。
			// 只在成功时推进 persistedVersions，否则一个稍后的陈旧快照可能会被
			// 丢弃，而更新的状态却从未到达 SQLite。记录错误并在下次尝试前退避，
			// 以避免对着卡死的 DB 忙转。
			log.Printf("persist cooldown account=%s version=%d: %v", id, item.StateVersion, err)
			if existing, ok := m.persistDirty[id]; !ok || item.StateVersion >= existing.StateVersion {
				m.persistDirty[id] = item
			}
			m.persistMu.Unlock()
			// 退避，但仍对关闭保持响应。Close() 设置 persistClosed 并关闭
			// persistCloseCh，然后等待 drainer；如果退避只监听 runCtx
			// （Close 只在等待之后才取消它），卡死的 DB 会让 Close 永远阻塞。
			// 监听 persistCloseCh 让 drainer 在关闭时退出，放弃未保存的脏状态
			// ——在持续的 DB 故障下本来就没什么可持久化的。
			select {
			case <-time.After(persistRetryBackoff):
			case <-m.runCtx.Done():
				return
			case <-m.persistCloseCh:
				return
			}
			continue
		}
		// 在锁内记录已持久化的版本，使稍后入队的陈旧快照在覆盖此状态之前被丢弃。
		if m.persistedVersions[id] < item.StateVersion {
			m.persistedVersions[id] = item.StateVersion
		}
		m.persistMu.Unlock()
	}
}

// Flush 等待迄今为止排队的全部冷却写入都被持久化。标记只在脏集合排空到空时触发，
// 因此在 Flush 返回前，本次调用之前入队的每个账号的最新状态都已持久化。
func (m *Manager) Flush() {
	if m == nil || m.persistCond == nil {
		return
	}
	done := make(chan struct{})
	m.persistMu.Lock()
	if m.persistClosed {
		m.persistMu.Unlock()
		return
	}
	m.persistFlushes = append(m.persistFlushes, done)
	m.persistCond.Signal()
	m.persistMu.Unlock()
	select {
	case <-done:
	case <-m.runCtx.Done():
	}
}

// cooldownRows 把一个池项展平为若干持久化的冷却行：账号级冷却加上任何模型级
// 冷却。每一行携带自己作用域的退避梯度和先前 kind，使按模型的退避与 last-kind
// 能在重启后存活，且不会跨模型混淆。
func cooldownRows(item Item) []CooldownRow {
	rows := make([]CooldownRow, 0, 1+len(item.ModelDownUntil))
	if !item.DownUntil.IsZero() {
		rows = append(rows, CooldownRow{
			AccountID: item.ID, DownUntil: item.DownUntil,
			BackoffLevel: item.BackoffLevel, Kind: item.LastKind, Message: item.LastError,
		})
	}
	for model, until := range item.ModelDownUntil {
		if until.IsZero() {
			continue
		}
		// 模型级行只携带该模型自己的退避梯度，而非账号级的 BackoffLevel。
		// 在此持久化账号级 level 会在恢复时把它写入 item.BackoffLevel，
		// 从而污染其他每个模型的账号级梯度。
		level := 0
		if item.ModelBackoff != nil {
			level = item.ModelBackoff[model]
		}
		kind := item.LastKind
		if item.ModelLastKind != nil {
			if mk, ok := item.ModelLastKind[model]; ok && mk != "" {
				kind = mk
			}
		}
		rows = append(rows, CooldownRow{
			AccountID: item.ID, Model: model, DownUntil: until,
			BackoffLevel: level, Kind: kind, Message: item.LastError,
			ModelKind: kind,
		})
	}
	return rows
}

// restoreCooldowns 在账号注册之后把持久化的冷却重新载入池中。受管的更新会定期
// 重建容器，若没有这一步，被限流的账号在启动时会被立刻重试。
func (m *Manager) restoreCooldowns(ctx context.Context) {
	rows, err := m.poolState.LoadCooldowns(ctx)
	if err != nil {
		log.Printf("restore cooldowns: %v", err)
		return
	}
	restored := 0
	for _, row := range rows {
		item, ok := m.pool.ByID(row.AccountID)
		if !ok {
			continue
		}
		if row.Model == "" {
			level := clampBackoffLevel(row.BackoffLevel)
			if level > item.BackoffLevel {
				item.BackoffLevel = level
			}
			item.DownUntil = row.DownUntil
		} else {
			// 模型级行：只恢复该模型自己的退避，而非账号级梯度。把模型的
			// level 写入 item.BackoffLevel 会污染账号级梯度，使得后续的账号级
			// 失败从该模型的 level 而非 0 开始。账号级的 BackoffLevel 只从上方的
			// 账号级行恢复。
			if item.ModelDownUntil == nil {
				item.ModelDownUntil = map[string]time.Time{}
			}
			item.ModelDownUntil[row.Model] = row.DownUntil
			if level := clampBackoffLevel(row.BackoffLevel); level > 0 {
				if item.ModelBackoff == nil {
					item.ModelBackoff = map[string]int{}
				}
				if level > item.ModelBackoff[row.Model] {
					item.ModelBackoff[row.Model] = level
				}
			}
			if row.ModelKind != "" {
				if item.ModelLastKind == nil {
					item.ModelLastKind = map[string]string{}
				}
				if item.ModelLastKind[row.Model] == "" {
					item.ModelLastKind[row.Model] = row.ModelKind
				}
			}
		}
		m.pool.Upsert(item)
		restored++
	}
	if restored > 0 {
		log.Printf("restored %d cooldown(s) from SQLite", restored)
	}
}

func (m *Manager) Start(ctx context.Context) error {
	stored, err := m.store.List(ctx)
	if err != nil {
		return err
	}
	// 在注册账号之前加载模型请求开关集合，使每个新池项都以正确的值启动。
	// 缺少该 secret 意味着每个账号都服务模型请求。读取失败会让启动 fail closed
	// （见 loadModelRequestsDisabled）；损坏的值则保持 fail-open。
	if err := m.loadModelRequestsDisabled(ctx); err != nil {
		return err
	}
	for _, account := range stored {
		if !account.Enabled {
			continue
		}
		if err := m.startAccount(ctx, account); err != nil {
			log.Printf("account %s initial start failed: %v", account.ID, err)
		}
	}
	// 放在注册之后，这样才有可恢复进去的池项。
	m.restoreCooldowns(ctx)
	return nil
}

func (m *Manager) Pool() *Pool         { return m.pool }
func (m *Manager) Store() AccountStore { return m.store }

// ProxyAPIKey 返回同步点写入的数据面密钥（见 ManagerConfig.ProxyAPIKey）。
// 当前无生产读取方；保留以维持轮换链路的显式契约。
func (m *Manager) ProxyAPIKey() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.config.ProxyAPIKey
}

// SetProviders 接入可选的进程内账号探测器（WorkBuddy 等）。
func (m *Manager) SetProviders(registry *providers.Registry) {
	if m == nil {
		return
	}
	m.providers = registry
}

// SetWorkBuddy 接入 Phase N 的签到 / 保活，而无需引入第二个调度器包。
func (m *Manager) SetWorkBuddy(ops WorkBuddyMaintainer) {
	if m == nil {
		return
	}
	m.workbuddy = ops
}

// SetMaintenanceGate 注入「维护中」门（更新器替换容器期间为 true）。
// 装配晚于维护循环启动，因此写入受 mu 保护。
func (m *Manager) SetMaintenanceGate(gate func() bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.maintenanceGate = gate
	m.mu.Unlock()
}

// maintenanceActive 报告当前是否处于维护中；门为 nil 表示无门。
func (m *Manager) maintenanceActive() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	gate := m.maintenanceGate
	m.mu.Unlock()
	return gate != nil && gate()
}

func (m *Manager) Close() error {
	// 在入队所用的同一把锁下停止接受观察者更新，然后通知 drainer 排空剩余部分
	// 并退出。在 persistMu 下设置 persistClosed 保证此后没有观察者能入队
	// ——没有 channel 要关，所以旧的 "向已关闭 channel 发送" panic 不会发生。
	// 关闭 persistCloseCh 会解除一个正退避重试卡死 DB 的 drainer 的阻塞：
	// runCtx 只在 drainer 退出后（见下）才被取消，所以若没有 persistCloseCh，
	// 重试循环会在持续的 DB 故障下永远阻塞 Close。
	m.persistMu.Lock()
	if !m.persistClosed {
		m.persistClosed = true
		close(m.persistCloseCh)
	}
	m.persistCond.Broadcast()
	m.persistMu.Unlock()
	m.persistDone.Wait()
	m.mu.Lock()
	m.cancel()
	m.mu.Unlock()
	return nil
}

const persistRetryBackoff = 500 * time.Millisecond

func (m *Manager) TestPersistDirtyLen() int {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	return len(m.persistDirty)
}

func (m *Manager) TestPersistSnapshot(id string) (item Item, version uint64, ok bool) {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	item, ok = m.persistDirty[id]
	version = m.persistedVersions[id]
	return item, version, ok
}

func PersistRetryBackoff() time.Duration { return persistRetryBackoff }

func (m *Manager) TestCheckinOptedIn(ctx context.Context, now time.Time, scheduledTime string, retryDue bool) {
	m.checkinOptedIn(ctx, now, scheduledTime, retryDue)
}

func (m *Manager) TestRestoreCooldowns(ctx context.Context) {
	m.restoreCooldowns(ctx)
}

func (m *Manager) TestStartAccount(ctx context.Context, account Account) error {
	return m.startAccount(ctx, account)
}
