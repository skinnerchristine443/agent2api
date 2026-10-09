package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agent2api/internal/auth"
	"agent2api/internal/buildinfo"
	"agent2api/internal/config"
	appconsole "agent2api/internal/console"
	appsvc "agent2api/internal/control"
	"agent2api/internal/executor"
	apigateway "agent2api/internal/gateway"
	applogs "agent2api/internal/logs"
	"agent2api/internal/providers"
	"agent2api/internal/providers/trae"
	"agent2api/internal/providers/workbuddy"
	accountruntime "agent2api/internal/runtime"
	httpserver "agent2api/internal/server"
	sqlstore "agent2api/internal/store"
	appupdate "agent2api/internal/update"
)

// App 持有进程级资源，并组装 gateway/console/server。
// 它不是各模块的服务定位器（service locator）。
type App struct {
	Cfg                    config.Config
	Auth                   auth.Verifier
	Executor               executor.ChatExecutor
	Pool                   *executor.Pool
	Manager                *accountruntime.Manager
	Control                *appsvc.Services
	Providers              *providers.Registry
	Recorder               *applogs.RequestRecorder
	Ring                   *applogs.Ring
	stopLogs               chan struct{}
	stopLogsOnce           sync.Once
	cancelRefresh          context.CancelFunc
	SettingsMu             sync.Mutex
	CrossProviderModelPool atomic.Bool
	Gateway                *apigateway.Handler
	Console                *appconsole.Handler
	Update                 *appupdate.Coordinator
	HTTP                   *httpserver.Server
	consoleAllowed         []*net.IPNet
}

func New(cfg config.Config) (*App, error) {
	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = filepath.Join(cfg.Home, ".proxy-data")
	}
	cfg.DataDir = dataDir
	store, err := sqlstore.OpenStore(filepath.Join(dataDir, "agent2api.db"))
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	proxyAPIKey, initialized, err := EnsureProxyAPIKey(context.Background(), store, cfg.ProxyAPIKey)
	if err != nil {
		return nil, err
	}
	if initialized {
		deliverInitialSecret(dataDir,
			fmt.Sprintf("[security] initialized API key and stored it in SQLite: %s", proxyAPIKey),
			"proxy_api_key="+proxyAPIKey)
	}
	// console key 是独立密钥：首启即生成独立随机值并只打印一次（审查 S1），
	// 这样即使把 proxy key 交给客户端，也绝不可能解锁控制台。
	// 既有的已播种部署保持原样（存值优先）：启动告警提示两钥相同，
	// 在控制台轮换一次即完成拆分。
	consoleKey, consoleInitialized, err := EnsureConsoleKey(context.Background(), store, cfg.ConsoleKey)
	if err != nil {
		return nil, err
	}
	if consoleInitialized && consoleKey != proxyAPIKey {
		deliverInitialSecret(dataDir,
			fmt.Sprintf("[security] initialized console (operator) key (shown once): %s", consoleKey),
			"console_key="+consoleKey)
	}
	consoleAllowed, err := auth.ParseCIDRs(cfg.ConsoleAllowedCIDRs)
	if err != nil {
		return nil, err
	}
	if len(consoleAllowed) == 0 {
		log.Printf("[security] console surface accepts loopback sources only")
	} else {
		log.Printf("[security] console surface accepts loopback plus: %s", strings.Join(cfg.ConsoleAllowedCIDRs, ", "))
	}
	if consoleKey == proxyAPIKey {
		log.Printf("[security] console key currently equals the API key — rotate the console key once to split the two secrets")
	}
	proxyURL, err := EnsureProxyURL(context.Background(), store, cfg.ProxyURL)
	if err != nil {
		return nil, err
	}
	crossProviderModelPool, err := EnsureCrossProviderModelPool(context.Background(), store)
	if err != nil {
		return nil, err
	}
	routingStrategy, err := EnsureRoutingStrategy(context.Background(), store)
	if err != nil {
		return nil, err
	}
	ratePreference, err := EnsureRatePreference(context.Background(), store)
	if err != nil {
		return nil, err
	}
	expiryWindow, secondaryExpiryWindow, err := EnsureExpiryWindows(context.Background(), store)
	if err != nil {
		return nil, err
	}
	if _, err := EnsureWorkBuddyCheckinTime(context.Background(), store); err != nil {
		return nil, err
	}
	if err := EnsureCheckinWindows(context.Background(), store); err != nil {
		return nil, err
	}
	if _, err := EnsureCheckinDisabledAccounts(context.Background(), store); err != nil {
		return nil, err
	}
	cfg.ProxyAPIKey = proxyAPIKey
	cfg.ConsoleKey = consoleKey
	runtimeDir := cfg.RuntimeDir
	if runtimeDir == "" {
		runtimeDir = filepath.Join("/tmp", "agent2api-runtime")
	}
	ring := applogs.NewRing(2000)
	log.SetOutput(io.MultiWriter(os.Stderr, ring))
	manager := accountruntime.NewManager(accountruntime.ManagerConfig{
		DataDir: runtimeDir, ProxyAPIKey: proxyAPIKey, ProxyURL: proxyURL,
		MaxLogWriters: io.MultiWriter(os.Stderr, ring),
	}, store)
	if err := manager.Start(context.Background()); err != nil {
		return nil, fmt.Errorf("start runtime manager: %w", err)
	}
	pool := manager.Pool()
	pool.SetRoutingStrategy(routingStrategy)
	pool.SetRatePreference(ratePreference)
	pool.SetExpiryWindows(expiryWindow, secondaryExpiryWindow)
	providerReg := providers.NewRegistry()
	workbuddyClient := workbuddy.NewClient(store)
	providerReg.Register(workbuddyClient.Adapter())
	providerReg.Register(trae.NewClient(store).Adapter())
	manager.SetProviders(providerReg)
	manager.SetWorkBuddy(workbuddyClient)
	// 绑定可取消的上下文：Close 时取消在途刷新（此前用 Background 无法取消）。
	refreshCtx, cancelRefresh := context.WithCancel(context.Background())
	go manager.RefreshAll(refreshCtx, false)
	recorder := applogs.NewRequestRecorder(store)
	stopLogs := make(chan struct{})
	go recorder.PurgeLoop(stopLogs, time.Hour)
	go manager.RunMaintenanceLoop(stopLogs)
	checker := appupdate.NewChecker(buildinfo.Version, appupdate.NewGitHubReleaseSource("skinnerchristine443/agent2api", cfg.UpdateGitHubToken))
	var agent appupdate.Agent = appupdate.NewUnixAgentClient(cfg.UpdateSocketPath)
	if strings.TrimSpace(cfg.UpdateAgentURL) != "" {
		agent = appupdate.NewHTTPAgentClient(cfg.UpdateAgentURL, cfg.UpdateAgentToken)
	}
	chatExecutor := executor.NewChatExecutor(pool)
	chatExecutor.MaxAttempts = cfg.MaxRetryAccounts
	chatExecutor.Providers = providerReg
	chatExecutor.OnAttempt = recorder.Attempt
	a := &App{
		Cfg:            cfg,
		Auth:           auth.NewVerifier(consoleKey, proxyAPIKey),
		Executor:       chatExecutor,
		Pool:           pool,
		Manager:        manager,
		Control:        appsvc.New(manager),
		Providers:      providerReg,
		Recorder:       recorder,
		Ring:           ring,
		stopLogs:       stopLogs,
		cancelRefresh:  cancelRefresh,
		consoleAllowed: consoleAllowed,
	}
	a.CrossProviderModelPool.Store(crossProviderModelPool)
	a.Control.Catalog = appsvc.NewCatalog(a.FetchWorkerModelsForMode)
	a.Control.Catalog.Snapshots = store
	if err := a.Control.Catalog.LoadSnapshots(context.Background()); err != nil {
		log.Printf("[catalog] persisted snapshot unavailable: %v", err)
	}
	if a.Control.Settings != nil {
		a.Control.Settings.BindCatalog(a.Control.Catalog)
	}
	a.Update = a.newUpdateCoordinator(checker, agent)
	// 更新器替换容器期间（含失败回滚窗口），维护循环跳过签到 / keepalive
	// 写入：回滚会以备份恢复数据库，这些写入注定被覆盖（审查 P2-6）。
	manager.SetMaintenanceGate(a.Update.Maintenance.Load)
	a.Gateway = a.newGateway()
	a.Console = a.newConsole()
	a.Console.Update = a.Update
	a.HTTP = a.newHTTP()
	return a, nil
}

func (a *App) Close() error {
	if a.cancelRefresh != nil {
		a.cancelRefresh()
	}
	if a.stopLogs != nil {
		a.stopLogsOnce.Do(func() { close(a.stopLogs) })
	}
	managerErr := a.Manager.Close()
	a.Recorder.Close()
	return errors.Join(managerErr, a.Manager.Store().Close())
}

func (a *App) Handler() http.Handler {
	if a == nil || a.HTTP == nil {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	}
	return a.HTTP.Handler()
}

func (a *App) RebuildHTTP() {
	if a == nil {
		return
	}
	if a.Gateway == nil {
		a.Gateway = a.newGateway()
	} else {
		a.Gateway.Executor = a.Executor
		a.Gateway.Pool = a.Pool
		a.Gateway.Recorder = a.Recorder
		a.Gateway.CrossProviderPool = &a.CrossProviderModelPool
		if a.Control != nil {
			a.Gateway.ModelContexts = a.Control.Settings
		}
		if a.Manager != nil {
			a.Gateway.Catalogs = a.Manager
		}
		if a.Recorder != nil {
			a.Gateway.Logs = a.Recorder
		}
	}
	if a.Console == nil {
		a.Console = a.newConsole()
	}
	a.Console.Update = a.Update
	if a.Console.Chat == nil && a.Gateway != nil {
		a.Console.Chat = a.Gateway.HandleChatCompletions
	}
	a.HTTP = a.newHTTP()
}

func (a *App) requestIdentity(r *http.Request) auth.Identity {
	identity, ok := auth.IdentityFrom(r.Context())
	if ok {
		return identity
	}
	return auth.Identity{Kind: auth.KindNone}
}

func (a *App) newHTTP() *httpserver.Server {
	return httpserver.New(httpserver.Server{
		Auth:           a.Auth,
		Gateway:        a.Gateway,
		Console:        a.Console,
		Update:         a.Update,
		CrossProvider:  &a.CrossProviderModelPool,
		EnablePprof:    a.Cfg.EnablePprof,
		ConsoleAllowed: a.consoleAllowed,
		CORSOrigins:    a.Cfg.CORSOrigins,
	})
}
