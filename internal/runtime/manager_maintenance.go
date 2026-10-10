package runtime

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"strings"
	"time"
	_ "time/tzdata"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
)

func (manager *Manager) CheckinAccount(ctx context.Context, accountID string) (Account, error) {
	return manager.checkinAccount(ctx, accountID, false)
}

func (manager *Manager) checkinAccount(ctx context.Context, accountID string, allowDisabled bool) (Account, error) {
	if manager == nil {
		return Account{}, fmt.Errorf("account manager unavailable")
	}
	manager.mu.Lock()
	if manager.checkinRunning == nil {
		manager.checkinRunning = make(map[string]bool)
	}
	if manager.checkinRunning[accountID] {
		manager.mu.Unlock()
		return Account{}, fmt.Errorf("check-in is already running for this account")
	}
	manager.checkinRunning[accountID] = true
	manager.mu.Unlock()
	defer func() {
		manager.mu.Lock()
		delete(manager.checkinRunning, accountID)
		manager.mu.Unlock()
	}()
	account, err := manager.store.Get(ctx, accountID)
	if err != nil {
		return Account{}, err
	}
	if !account.Enabled && !allowDisabled {
		return account, fmt.Errorf("account is disabled")
	}
	policy, supported := providers.CheckinFor(account.Provider, account.ProviderRegion)
	adapter, registered := manager.providers.Get(account.Provider)
	if !supported || !registered || adapter.Checkin == nil {
		return account, providers.ErrUnsupported
	}
	location, err := time.LoadLocation(policy.Timezone)
	if err != nil {
		return account, fmt.Errorf("invalid check-in timezone: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, providers.CheckinRequestBudget)
	defer cancel()
	stop := context.AfterFunc(manager.runCtx, cancel)
	defer stop()
	if CheckedInLocalDay(account.LastCheckinAt, account.LastCheckinStatus, time.Now().In(location)) {
		manager.refreshCheckinQuota(ctx, accountID, adapter)
		return manager.store.Get(ctx, accountID)
	}
	// 被备忘的 "能力缺失" 结论会让探测短路：上游答复在 capabilityProbeTTL 内
	// 是确定的，而每一轮都重新询问正是这份备忘要止住的噪声。
	if wait, absent := manager.capabilityAbsentFor(accountID, checkinCapability); absent {
		return account, capabilityUnavailableError(checkinCapability, wait)
	}
	result, checkErr := adapter.Checkin.Checkin(ctx, accountID)
	if checkErr == nil && !result.Valid() {
		checkErr = fmt.Errorf("provider returned an invalid check-in result")
	}
	if checkErr != nil {
		result = providers.CheckinResult{Status: providers.CheckinStatus("error"), Message: checkErr.Error()}
	} else if result.Reason == providers.CheckinReasonCapabilityAbsent {
		manager.markCapabilityAbsent(accountID, checkinCapability)
	} else {
		// 防御性：只在没有有效备忘时才运行探测（过期路径已经把它丢弃），
		// 但一次并发的重新标记不得比一个确定的"非缺失"答复活得更久。
		manager.clearCapabilityAbsent(accountID, checkinCapability)
	}
	recordCtx, stopRecording := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	recordErr := manager.store.RecordCheckin(recordCtx, accountID, string(result.Status), result.Message, time.Now().UTC())
	stopRecording()
	if recordErr != nil {
		return account, errors.Join(checkErr, fmt.Errorf("save check-in result: %w", recordErr))
	}
	if result.Status != providers.CheckinStatusSkipped {
		manager.refreshCheckinQuota(ctx, accountID, adapter)
	}
	readCtx, stopReading := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stopReading()
	updated, err := manager.store.Get(readCtx, accountID)
	return updated, errors.Join(checkErr, err)
}

func (manager *Manager) refreshCheckinQuota(ctx context.Context, accountID string, adapter providers.Adapter) {
	if ctx.Err() != nil {
		return
	}
	if adapter.Prober != nil {
		manager.fetchProviderQuota(ctx, accountID, adapter.Prober)
	}
}

func CheckedInLocalDay(at, status string, now time.Time) bool {
	if !providers.CheckinStatusSettled(status) {
		return false
	}
	return recordedToday(at, now)
}

func recordedToday(at string, now time.Time) bool {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(at))
	if err != nil {
		return false
	}
	local := parsed.In(now.Location())
	return local.Year() == now.Year() && local.YearDay() == now.YearDay()
}

// checkinWindowInstant 返回给定账号在 now 的本地日、于指定窗口（"main" 或
// "fallback"）内触发的确定性时刻。偏移量由（本地日期、provider、账号、窗口 kind）
// 推导，因此在整个一天内稳定，并在午夜自然重新掷点，同时把同一 provider 的
// 多个账号分散在整个窗口内。
func checkinWindowInstant(window accounts.CheckinWindow, providerID, accountID, kind string, now time.Time) (time.Time, error) {
	startClock, endClock := window.MainRange()
	if kind == "fallback" {
		startClock, endClock = window.FallbackRange()
	}
	start, err := time.Parse("15:04", startClock)
	if err != nil {
		return time.Time{}, err
	}
	end, err := time.Parse("15:04", endClock)
	if err != nil {
		return time.Time{}, err
	}
	startAt := time.Date(now.Year(), now.Month(), now.Day(), start.Hour(), start.Minute(), 0, 0, now.Location())
	endAt := time.Date(now.Year(), now.Month(), now.Day(), end.Hour(), end.Minute(), 0, 0, now.Location())
	span := int(endAt.Sub(startAt) / time.Second)
	if span <= 0 {
		return time.Time{}, fmt.Errorf("invalid check-in %s window", kind)
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(now.Format("2006-01-02") + "|" + providerID + "|" + accountID + "|" + kind))
	offset := int(hash.Sum32() % uint32(span+1))
	return startAt.Add(time.Duration(offset) * time.Second), nil
}

// currentCheckinInstant 返回 now 的本地日上已经过去的最新窗口时刻（若有）。
// NormalizeCheckinWindow 强制 main_end <= fallback_start，因此 fallback 时刻
// 绝不早于 main 时刻，单一的 "最新已过" 时刻是明确的。
func currentCheckinInstant(window accounts.CheckinWindow, providerID, accountID string, now time.Time) (time.Time, bool) {
	main, err := checkinWindowInstant(window, providerID, accountID, "main", now)
	if err != nil {
		return time.Time{}, false
	}
	fallback, err := checkinWindowInstant(window, providerID, accountID, "fallback", now)
	if err != nil {
		return time.Time{}, false
	}
	if !now.Before(fallback) {
		return fallback, true
	}
	if !now.Before(main) {
		return main, true
	}
	return time.Time{}, false
}

func checkinDue(account Account, window accounts.CheckinWindow, now time.Time) bool {
	return checkinDueFor(account, window, now, false)
}

func checkinDueFor(account Account, window accounts.CheckinWindow, now time.Time, allowDisabled bool) bool {
	if (!account.Enabled && !allowDisabled) || !account.AutoCheckin {
		return false
	}
	// 今天早些时候记录的终态会压制后续所有时段，包括 fallback：
	// success/already 已无事可做，而 provider 侧的 "活动未开放" 不得被全天猛敲。
	// 只有 "error" 保持资格，这正是让 fallback 成为对失败主窗口的一次重试的原因。
	if recordedToday(account.LastCheckinAt, now) && account.LastCheckinStatus != "error" {
		return false
	}
	due, ok := currentCheckinInstant(window, account.Provider, account.ID, now)
	if !ok || now.Before(due) {
		return false
	}
	last, err := time.Parse(time.RFC3339Nano, account.LastCheckinAt)
	if err != nil || last.Before(due) {
		return true
	}
	return false
}

func (manager *Manager) runScheduledCheckins(ctx context.Context, now time.Time) {
	items, err := manager.store.List(ctx)
	if err != nil {
		log.Printf("checkin schedule list: %v", err)
		return
	}
	allowDisabled := manager.allowDisabledCheckin(ctx)
	for _, account := range items {
		if ctx.Err() != nil {
			return
		}
		policy, supported := providers.CheckinFor(account.Provider, account.ProviderRegion)
		adapter, registered := manager.providers.Get(account.Provider)
		if !supported || !registered || adapter.Checkin == nil || (!account.Enabled && !allowDisabled) || !account.AutoCheckin {
			continue
		}
		location, err := time.LoadLocation(policy.Timezone)
		if err != nil {
			log.Printf("checkin timezone provider=%s: %v", account.Provider, err)
			continue
		}
		window, err := accounts.ResolveCheckinWindow(ctx, manager.store, account)
		if err != nil {
			log.Printf("checkin settings account=%s: %v", account.ID, err)
			continue
		}
		if checkinDueFor(account, window, now.In(location), allowDisabled) {
			if _, err := manager.checkinAccount(ctx, account.ID, allowDisabled); err != nil {
				log.Printf("checkin account=%s: %v", account.ID, err)
			}
		}
	}
}

func (manager *Manager) CheckinOptedIn(ctx context.Context) {
	manager.checkinOptedIn(ctx, time.Now(), "", false)
}

func (manager *Manager) checkinOptedIn(ctx context.Context, now time.Time, scheduledTime string, retryDue bool) {
	items, err := manager.store.List(ctx)
	if err != nil {
		log.Printf("checkin list: %v", err)
		return
	}
	allowDisabled := manager.allowDisabledCheckin(ctx)
	for _, account := range items {
		if (!account.Enabled && !allowDisabled) || !account.AutoCheckin {
			continue
		}
		configuredTime, err := accounts.ResolveCheckinTime(ctx, manager.store, account)
		if err != nil {
			continue
		}
		if scheduledTime != "" {
			if retryDue {
				if configuredTime == scheduledTime || configuredTime > now.Format("15:04") {
					continue
				}
			} else if configuredTime != scheduledTime {
				continue
			}
		}
		if _, err := manager.checkinAccount(ctx, account.ID, allowDisabled); err != nil {
			log.Printf("checkin account=%s: %v", account.ID, err)
		}
	}
}

func (manager *Manager) allowDisabledCheckin(ctx context.Context) bool {
	value, ok, err := manager.store.GetSecret(ctx, accounts.CheckinDisabledAccountsSecret)
	if err != nil || !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "on", "yes":
		return true
	default:
		return false
	}
}

func (manager *Manager) KeepaliveWorkBuddy(ctx context.Context, onlyOptIn bool) {
	if manager == nil || manager.workbuddy == nil {
		return
	}
	items, err := manager.store.List(ctx)
	if err != nil {
		log.Printf("workbuddy keepalive list: %v", err)
		return
	}
	for _, account := range items {
		if ctx.Err() != nil {
			return
		}
		if account.Provider != "workbuddy" || !account.Enabled || (onlyOptIn && !account.AutoCheckin) {
			continue
		}
		if err := manager.workbuddy.Keepalive(ctx, account.ID); err != nil {
			log.Printf("workbuddy keepalive account=%s: %v", account.ID, err)
		}
	}
}

// keepaliveConfig 读取保活开关与本地时刻。缺省 = 开启、22:00（与旧硬编码一致）。
func (manager *Manager) keepaliveConfig(ctx context.Context) (bool, string, error) {
	store := manager.store
	if store == nil {
		return true, accounts.DefaultKeepaliveTime, nil
	}
	enabled := true
	if raw, ok, err := store.GetSecret(ctx, accounts.KeepaliveEnabledSecret); err != nil {
		return false, "", err
	} else if ok {
		enabled = strings.TrimSpace(raw) != "0"
	}
	at := accounts.DefaultKeepaliveTime
	if raw, ok, err := store.GetSecret(ctx, accounts.KeepaliveTimeSecret); err != nil {
		return false, "", err
	} else if ok && strings.TrimSpace(raw) != "" {
		if normalized, err := accounts.NormalizeKeepaliveTime(raw); err == nil {
			at = normalized
		}
	}
	return enabled, at, nil
}

func (manager *Manager) RunMaintenanceLoop(stop <-chan struct{}) {
	ctx, cancel := context.WithCancel(manager.runCtx)
	defer cancel()
	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	lastKeepaliveDay := ""
	lastActivityDay := ""
	for {
		if ctx.Err() != nil {
			return
		}
		manager.runMaintenanceTick(ctx, time.Now(), &lastKeepaliveDay, &lastActivityDay)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// runMaintenanceTick 执行一轮维护周期。维护中（更新器正在替换本容器）
// 跳过会产生写入的周期任务（签到 / keepalive）：更新失败会以备份恢复
// 数据库，这些写入注定被覆盖回滚，跳过它们可缩小回滚丢失面；只读的
// 资源采样与告警评估不受影响。
func (manager *Manager) runMaintenanceTick(ctx context.Context, now time.Time, lastKeepaliveDay *string, lastActivityDay *string) {
	if !manager.maintenanceActive() {
		manager.runScheduledCheckins(ctx, now)
		// 冷账号补探：开机时全部账号都没有健康判定，只会主动探测补齐。
		manager.ProbeColdAccounts(ctx)
		// 保活：开关 + 本地时刻均可配（设置 › 签到）。旧行为 = 22:00 一次性。
		if enabled, at, err := manager.keepaliveConfig(ctx); err == nil && enabled && accounts.KeepaliveDue(now, at, *lastKeepaliveDay) {
			keepaliveCtx, stopKeepalive := context.WithTimeout(ctx, 2*time.Minute)
			manager.KeepaliveWorkBuddy(keepaliveCtx, true)
			stopKeepalive()
			*lastKeepaliveDay = now.Format("2006-01-02")
		}
		// 对话活跃上报（点亮 growth 连登）。默认关闭（AGENT2API_ACTIVITY_REPORT）；
		// 关闭时零上游调用。每号每天 1 次，本地时刻可配（缺省 09:00）。
		if activityReportEnabled() && activityReportDue(now, activityReportTime(), *lastActivityDay) {
			reportCtx, stopReport := context.WithTimeout(ctx, 5*time.Minute)
			manager.runScheduledActivityReport(reportCtx)
			stopReport()
			*lastActivityDay = now.Format("2006-01-02")
		}
	}
	manager.sampleResources()
	manager.evaluateQuotaAlerts(ctx)
}

// coldProbeBatch 是单次维护 tick 最多补探的账号数。
//
// 账号启动只登记池项，不做探测；未探测过的池项 Ready/Hot 都是 nil，
// 视图层对两者的默认取值方向相反（Ready 取 nil ⇒ 判就绪，Hot 取 nil ⇒
// 判非热）⇒ 界面表现为「就绪但非热就绪」，配额与模型目录也是空的，
// 且没有任何机制会自己把它探回来。开机时全部账号都处于这个状态，
// 一次性全探会形成上游突发，因此每次只补一小批，由 30 秒一拍的
// 维护循环自然摊平。
const coldProbeBatch = 4

// ProbeColdAccounts 为「尚无健康判定」的账号补一次探测，返回本次探测个数。
// 已有判定（无论成败）的账号不在此列——那些交给显式刷新，避免对已知
// 故障账号反复打扰上游。
func (manager *Manager) ProbeColdAccounts(ctx context.Context) int {
	if manager == nil || manager.pool == nil {
		return 0
	}
	probed := 0
	for _, item := range manager.pool.Items() {
		if probed >= coldProbeBatch || ctx.Err() != nil {
			break
		}
		if item.Ready != nil {
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		if err := manager.refreshOne(probeCtx, item, false); err != nil {
			log.Printf("cold probe account=%s: %v", item.ID, err)
		}
		cancel()
		probed++
	}
	return probed
}
