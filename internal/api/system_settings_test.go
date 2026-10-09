package api

import (
	sqlstore "agent2api/internal/store"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/config"
)

func TestEnsureCrossProviderModelPoolDefaultsToEnabled(t *testing.T) {
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	enabled, err := ensureCrossProviderModelPool(context.Background(), store)
	if err != nil || !enabled {
		t.Fatalf("enabled=%v err=%v", enabled, err)
	}
	value, ok, err := store.GetSecret(context.Background(), crossProviderModelPoolSecret)
	if err != nil || !ok || value != "1" {
		t.Fatalf("stored setting=%q ok=%v err=%v", value, ok, err)
	}
}

func TestSystemSettingsRoutePersistsAndAppliesModelPool(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()

	request := loopbackRequest(http.MethodGet, "/api/system/settings", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"cross_provider_model_pool":true`)) {
		t.Fatalf("default settings: %d %s", response.Code, response.Body.String())
	}

	request = loopbackRequest(http.MethodPatch, "/api/system/settings", bytes.NewBufferString(`{"cross_provider_model_pool":false}`))
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"cross_provider_model_pool":false`)) {
		t.Fatalf("updated settings: %d %s", response.Code, response.Body.String())
	}
	if srv.CrossProviderModelPool.Load() {
		t.Fatal("runtime model pool setting remains enabled")
	}

	value, ok, err := srv.Manager.Store().GetSecret(context.Background(), crossProviderModelPoolSecret)
	if err != nil || !ok || value != "0" {
		t.Fatalf("persisted setting=%q ok=%v err=%v", value, ok, err)
	}
}

func TestSystemSettingsRoutePersistsAndAppliesRoutingStrategy(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()

	request := loopbackRequest(http.MethodPatch, "/api/system/settings", bytes.NewBufferString(`{"routing_strategy":"fill-first"}`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"routing_strategy":"fill-first"`)) {
		t.Fatalf("updated settings: %d %s", response.Code, response.Body.String())
	}
	if got := srv.Pool.RoutingStrategy(); got != accounts.RoutingStrategyFillFirst {
		t.Fatalf("runtime strategy = %q", got)
	}
	value, ok, err := srv.Manager.Store().GetSecret(context.Background(), routingStrategySecret)
	if err != nil || !ok || value != accounts.RoutingStrategyFillFirst {
		t.Fatalf("persisted strategy=%q ok=%v err=%v", value, ok, err)
	}
}

func TestSystemSettingsRateAndExpiryControls(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()

	request := loopbackRequest(http.MethodGet, "/api/system/settings", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"rate_preference":true`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"expiry_window_seconds":259200`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"secondary_expiry_window_seconds":604800`)) {
		t.Fatalf("default settings: %d %s", response.Code, response.Body.String())
	}

	// 主窗口为 0 会整体关闭过期排序；次级窗口
	// 也必须报告为 0（expiry_windows 归一化）。
	request = loopbackRequest(http.MethodPatch, "/api/system/settings", bytes.NewBufferString(`{"rate_preference":false,"expiry_window_seconds":0}`))
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"rate_preference":false`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"expiry_window_seconds":0`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"secondary_expiry_window_seconds":0`)) {
		t.Fatalf("updated settings: %d %s", response.Code, response.Body.String())
	}
	if srv.Pool.RatePreference() {
		t.Fatal("runtime rate preference remains enabled")
	}
	if primary, secondary := srv.Pool.ExpiryWindows(); primary != 0 || secondary != 0 {
		t.Fatalf("runtime expiry windows = (%v,%v), want (0,0)", primary, secondary)
	}
	stored, ok, err := srv.Manager.Store().GetSecret(context.Background(), "rate_preference")
	if err != nil || !ok || stored != "0" {
		t.Fatalf("persisted rate preference=%q ok=%v err=%v", stored, ok, err)
	}
}

func TestEnsureWorkBuddyCheckinTimeDefaultsToNine(t *testing.T) {
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	value, err := ensureWorkBuddyCheckinTime(context.Background(), store)
	if err != nil || value != accounts.DefaultWorkBuddyCheckinTime {
		t.Fatalf("value=%q err=%v", value, err)
	}
	stored, ok, err := store.GetSecret(context.Background(), accounts.WorkBuddyCheckinTimeSecret)
	if err != nil || !ok || stored != accounts.DefaultWorkBuddyCheckinTime {
		t.Fatalf("stored=%q ok=%v err=%v", stored, ok, err)
	}
}

func TestEnsureCheckinDisabledAccountsDefaultsToFalse(t *testing.T) {
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	value, err := ensureCheckinDisabledAccounts(context.Background(), store)
	if err != nil || value {
		t.Fatalf("value=%v err=%v", value, err)
	}
	stored, ok, err := store.GetSecret(context.Background(), accounts.CheckinDisabledAccountsSecret)
	if err != nil || !ok || stored != "0" {
		t.Fatalf("stored=%q ok=%v err=%v", stored, ok, err)
	}
}

func TestSystemSettingsRoutePersistsWorkBuddyCheckinTime(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()

	request := loopbackRequest(http.MethodGet, "/api/system/settings", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"workbuddy_checkin_time":"09:00"`)) {
		t.Fatalf("default settings: %d %s", response.Code, response.Body.String())
	}

	request = loopbackRequest(http.MethodPatch, "/api/system/settings", bytes.NewBufferString(`{"workbuddy_checkin_time":"18:30"}`))
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"workbuddy_checkin_time":"18:30"`)) {
		t.Fatalf("updated settings: %d %s", response.Code, response.Body.String())
	}
	stored, ok, err := srv.Manager.Store().GetSecret(context.Background(), accounts.WorkBuddyCheckinTimeSecret)
	if err != nil || !ok || stored != "18:30" {
		t.Fatalf("persisted=%q ok=%v err=%v", stored, ok, err)
	}
}

func TestSystemSettingsRoutePersistsDisabledAccountCheckin(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()

	request := loopbackRequest(http.MethodGet, "/api/system/settings", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"checkin_disabled_accounts":false`)) {
		t.Fatalf("default settings: %d %s", response.Code, response.Body.String())
	}

	request = loopbackRequest(http.MethodPatch, "/api/system/settings", bytes.NewBufferString(`{"checkin_disabled_accounts":true}`))
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"checkin_disabled_accounts":true`)) {
		t.Fatalf("updated settings: %d %s", response.Code, response.Body.String())
	}
	stored, ok, err := srv.Manager.Store().GetSecret(context.Background(), accounts.CheckinDisabledAccountsSecret)
	if err != nil || !ok || stored != "1" {
		t.Fatalf("persisted=%q ok=%v err=%v", stored, ok, err)
	}
}

func TestSystemSettingsRejectsInvalidWorkBuddyCheckinTime(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()

	request := loopbackRequest(http.MethodPatch, "/api/system/settings", bytes.NewBufferString(`{"workbuddy_checkin_time":"9:00"}`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid time response: %d %s", response.Code, response.Body.String())
	}
	stored, ok, err := srv.Manager.Store().GetSecret(context.Background(), accounts.WorkBuddyCheckinTimeSecret)
	if err != nil || !ok || stored != accounts.DefaultWorkBuddyCheckinTime {
		t.Fatalf("default overwritten after invalid patch: stored=%q ok=%v err=%v", stored, ok, err)
	}
}

func TestSystemSettingsRejectsInvalidStrategyWithoutPartialUpdate(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()

	request := loopbackRequest(http.MethodPatch, "/api/system/settings", bytes.NewBufferString(`{"cross_provider_model_pool":false,"routing_strategy":"invalid"}`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid strategy response: %d %s", response.Code, response.Body.String())
	}
	if !srv.CrossProviderModelPool.Load() {
		t.Fatal("invalid strategy request partially changed model pool setting")
	}
	value, ok, err := srv.Manager.Store().GetSecret(context.Background(), crossProviderModelPoolSecret)
	if err != nil || !ok || value != "1" {
		t.Fatalf("cross-provider setting after invalid request=%q ok=%v err=%v", value, ok, err)
	}
}

func TestChatRejectsBareModelWhenCrossProviderPoolDisabled(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()
	srv.CrossProviderModelPool.Store(false)

	request := loopbackRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte(`"provider_prefix_required"`)) {
		t.Fatalf("bare model response: %d %s", response.Code, response.Body.String())
	}
}

func TestEnsureProxyURLRejectsSOCKSBootstrap(t *testing.T) {
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, err := ensureProxyURL(context.Background(), store, "socks5://proxy.example:1080"); err == nil {
		t.Fatal("SOCKS bootstrap was accepted for the global proxy")
	}
	if _, ok, err := store.GetSecret(context.Background(), proxyURLSecret); err != nil || ok {
		t.Fatalf("rejected bootstrap was persisted: ok=%v err=%v", ok, err)
	}
}

func TestEnsureProxyURLAcceptsHTTPBootstrap(t *testing.T) {
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	value, err := ensureProxyURL(context.Background(), store, "http://proxy.example:8080")
	if err != nil || value != "http://proxy.example:8080" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	stored, ok, err := store.GetSecret(context.Background(), proxyURLSecret)
	if err != nil || !ok || stored != "http://proxy.example:8080" {
		t.Fatalf("stored=%q ok=%v err=%v", stored, ok, err)
	}
}

func TestSystemSettingsProxyURLRejectsSOCKS(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()

	request := loopbackRequest(http.MethodPatch, "/api/system/settings", bytes.NewBufferString(`{"proxy_url":"socks5://proxy.example:1080"}`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("SOCKS global proxy response: %d %s", response.Code, response.Body.String())
	}
	if _, ok, err := srv.Manager.Store().GetSecret(context.Background(), proxyURLSecret); err != nil || ok {
		t.Fatalf("rejected global proxy was persisted: ok=%v err=%v", ok, err)
	}
}

func TestSystemSettingsProxyURLAcceptsHTTP(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()

	request := loopbackRequest(http.MethodPatch, "/api/system/settings", bytes.NewBufferString(`{"proxy_url":"http://proxy.example:8080"}`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("HTTP global proxy response: %d %s", response.Code, response.Body.String())
	}
	stored, ok, err := srv.Manager.Store().GetSecret(context.Background(), proxyURLSecret)
	if err != nil || !ok || stored != "http://proxy.example:8080" {
		t.Fatalf("stored=%q ok=%v err=%v", stored, ok, err)
	}
}

func TestSystemSettingsProxyURLClearPersistsEmptyValue(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()

	if err := srv.Manager.Store().SetSecret(context.Background(), proxyURLSecret, "http://proxy.example:8080"); err != nil {
		t.Fatal(err)
	}

	req := loopbackRequest(http.MethodPatch, "/api/system/settings", bytes.NewBufferString(`{"proxy_url":""}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear response: %d %s", rec.Code, rec.Body.String())
	}

	// 该行必须以空值保留，使重启不会把这次清除当作
	// 「从未配置」并重新应用环境 bootstrap。
	stored, ok, err := srv.Manager.Store().GetSecret(context.Background(), proxyURLSecret)
	if err != nil || !ok || stored != "" {
		t.Fatalf("cleared proxy: stored=%q ok=%v err=%v (want present empty row)", stored, ok, err)
	}
}

func TestEnsureProxyURLDoesNotReapplyBootstrapAfterClear(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.OpenStore(filepath.Join(t.TempDir(), "agent2api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// 模拟用户清除 proxy：该行以空值存在。
	if err := store.SetSecretOrEmpty(ctx, proxyURLSecret, ""); err != nil {
		t.Fatal(err)
	}

	value, err := ensureProxyURL(ctx, store, "http://env-bootstrap.example:8080")
	if err != nil {
		t.Fatalf("ensureProxyURL: %v", err)
	}
	if value != "" {
		t.Fatalf("cleared proxy was re-bootstrapped from the environment: %q", value)
	}
}

// 两次并发 proxy 保存不得让 SQLite 与运行中的 worker
// 出现不一致。对已持久化值的读取、Preserve、校验以及
// 变更比较，都必须与保存和重载处于同一个 settingsMu
// 临界区内。本测试持有 settingsMu，发出一个提交 *旧* 值的
// PATCH，在它之下改变已存储的值，然后
// 释放锁。若读取在锁之外，请求会看到
// 过期的旧值、跳过写入，并把运行时重载为 old.example，而
// 数据库里是 new.example。正确行为是在锁内重新读取、
// 察觉差异并持久化自己的值，使两者一致。
func TestSystemSettingsProxyURLReadIsInsideSettingsLock(t *testing.T) {
	srv := New(config.Config{
		Host: "127.0.0.1", Port: 3010, ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home: t.TempDir(), DataDir: t.TempDir(),
	})
	defer srv.Close()
	ctx := context.Background()

	const oldProxy = "http://old.example:8080"
	const newProxy = "http://new.example:9090"
	if err := srv.Manager.Store().SetSecret(ctx, proxyURLSecret, oldProxy); err != nil {
		t.Fatal(err)
	}

	// 取得 settings 锁，使 PATCH 无法进入临界区。
	srv.SettingsMu.Lock()

	type result struct {
		code int
		body string
	}
	done := make(chan result, 1)
	go func() {
		req := loopbackRequest(http.MethodPatch, "/api/system/settings", bytes.NewBufferString(`{"proxy_url":"`+oldProxy+`"}`))
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		done <- result{code: rec.Code, body: rec.Body.String()}
	}()

	// 给该 goroutine 一点时间到达（并阻塞在）settings 锁上，然后
	// 像并发请求那样改变已存储的值。
	time.Sleep(100 * time.Millisecond)
	if err := srv.Manager.Store().SetSecret(ctx, proxyURLSecret, newProxy); err != nil {
		t.Fatal(err)
	}
	srv.SettingsMu.Unlock()

	res := <-done
	if res.code != http.StatusOK {
		t.Fatalf("patch response: %d %s", res.code, res.body)
	}

	// 请求提交的是 old.example，而该行持有 new.example，因此它
	// 必然持久化了 old.example。若它拿锁之前读取的旧值来
	// 比较，就会跳过写入，把 new.example 留在磁盘上，
	// 却让运行时指向 old.example。
	stored, ok, err := srv.Manager.Store().GetSecret(ctx, proxyURLSecret)
	if err != nil || !ok {
		t.Fatalf("stored proxy missing: ok=%v err=%v", ok, err)
	}
	if stored != oldProxy {
		t.Fatalf("database/runtime split: database=%q, the request's runtime value=%q", stored, oldProxy)
	}
}
