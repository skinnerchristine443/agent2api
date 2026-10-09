package control

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agent2api/internal/executor"
)

// 重启不得让一个已被运行中配置归一化掉的次过期窗口复活。PATCH 处理器会持久化
// 生效（归一化后）的值对，因此仅凭已存 secret 重建连接池得到的窗口，与重启前
// 运行时完全一致。
//
// 复现该分歧：关闭主窗口（这会在运行时把次窗口置零），然后再重新启用。若不持久化
// 被置零的次窗口，"重新启用"后的状态会在内存中保持 secondary=0，而数据库仍保存着
// 604800，于是下次启动会静默地重新启用运维人员已关闭的每周窗口。
func TestSystemPatchExpiryWindowsSurviveRestart(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newTestServices()
	secrets := svc.Settings
	pool := executor.NewPool()
	sys := &System{
		Settings: secrets, Accounts: svc.Accounts, Pool: pool,
		CrossProviderPool: &atomic.Bool{}, Mu: &sync.Mutex{},
	}

	// 启动：按 app.go 的方式播种默认值并应用它们。
	primary, secondary, err := EnsureExpiryWindows(ctx, secrets)
	if err != nil {
		t.Fatal(err)
	}
	pool.SetExpiryWindows(primary, secondary)

	disable := int64(0)
	if err := sys.Patch(ctx, SystemSettingsPatch{ExpiryWindowSeconds: &disable}); err != nil {
		t.Fatal(err)
	}
	reenable := int64(executor.DefaultExpiryWindow / time.Second)
	if err := sys.Patch(ctx, SystemSettingsPatch{ExpiryWindowSeconds: &reenable}); err != nil {
		t.Fatal(err)
	}

	wantPrimary, wantSecondary := pool.ExpiryWindows()
	if wantPrimary != executor.DefaultExpiryWindow || wantSecondary != 0 {
		t.Fatalf("pre-restart windows = (%v,%v), want (72h,0)", wantPrimary, wantSecondary)
	}

	// 重启：一个仅凭已存 secret 全新构建的连接池，走相同的引导路径
	// （不复用运行中的连接池对象）。
	restarted := executor.NewPool()
	rp, rs, err := EnsureExpiryWindows(ctx, secrets)
	if err != nil {
		t.Fatal(err)
	}
	restarted.SetExpiryWindows(rp, rs)

	gotPrimary, gotSecondary := restarted.ExpiryWindows()
	if gotPrimary != wantPrimary || gotSecondary != wantSecondary {
		t.Fatalf("restart diverged: before=(%v,%v) after=(%v,%v)", wantPrimary, wantSecondary, gotPrimary, gotSecondary)
	}
	stored, ok, err := secrets.GetSecret(ctx, secondaryExpiryWindowSecret)
	if err != nil || !ok || stored != "0" {
		t.Fatalf("persisted secondary=%q ok=%v err=%v, want \"0\"", stored, ok, err)
	}
}

// 全新安装会播种当前的 3d/7d 值对。
func TestEnsureExpiryWindowsSeedsThreeAndSevenDays(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newTestServices()
	primary, secondary, err := EnsureExpiryWindows(ctx, svc.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if primary != executor.DefaultExpiryWindow || secondary != executor.DefaultSecondaryExpiryWindow {
		t.Fatalf("seeded windows = (%v,%v), want (%v,%v)",
			primary, secondary, executor.DefaultExpiryWindow, executor.DefaultSecondaryExpiryWindow)
	}
	if primary != 3*24*time.Hour {
		t.Fatalf("seeded primary = %v, want 72h", primary)
	}
	if secondary != 7*24*time.Hour {
		t.Fatalf("seeded secondary = %v, want 168h", secondary)
	}
}

// 默认值只填补缺失的 secret。已经持久化了旧的 36h/30d 值对的安装在升级后会保留它，
// 因此更改默认值不会静默覆盖已存值；运维人员必须 PATCH 才能改动。
func TestEnsureExpiryWindowsKeepsStoredSecondary(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newTestServices()
	if err := svc.Settings.SetSecret(ctx, expiryWindowSecret, "129600"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Settings.SetSecret(ctx, secondaryExpiryWindowSecret, "2592000"); err != nil {
		t.Fatal(err)
	}
	primary, secondary, err := EnsureExpiryWindows(ctx, svc.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if primary != 36*time.Hour {
		t.Fatalf("stored primary = %v, want the persisted 36h kept", primary)
	}
	if secondary != 30*24*time.Hour {
		t.Fatalf("stored secondary = %v, want the persisted 720h kept", secondary)
	}
}

// 对按整天的窗口做 PATCH 必须能在重启后精确往返一致：已存值对与运行时值对保持
// 完全相同（此前修复的 bug）。
func TestSystemPatchExpiryWindowsRoundTripsThroughRestart(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newTestServices()
	secrets := svc.Settings
	pool := executor.NewPool()
	sys := &System{
		Settings: secrets, Accounts: svc.Accounts, Pool: pool,
		CrossProviderPool: &atomic.Bool{}, Mu: &sync.Mutex{},
	}
	if _, _, err := EnsureExpiryWindows(ctx, secrets); err != nil {
		t.Fatal(err)
	}

	primaryDays := int64(5 * 24 * 60 * 60)
	secondaryDays := int64(10 * 24 * 60 * 60)
	if err := sys.Patch(ctx, SystemSettingsPatch{
		ExpiryWindowSeconds:          &primaryDays,
		SecondaryExpiryWindowSeconds: &secondaryDays,
	}); err != nil {
		t.Fatal(err)
	}
	wantPrimary, wantSecondary := pool.ExpiryWindows()
	if wantPrimary != 5*24*time.Hour || wantSecondary != 10*24*time.Hour {
		t.Fatalf("patched windows = (%v,%v), want (120h,240h)", wantPrimary, wantSecondary)
	}

	// 仅凭已存 secret 重启。
	restarted := executor.NewPool()
	rp, rs, err := EnsureExpiryWindows(ctx, secrets)
	if err != nil {
		t.Fatal(err)
	}
	restarted.SetExpiryWindows(rp, rs)
	if gotPrimary, gotSecondary := restarted.ExpiryWindows(); gotPrimary != wantPrimary || gotSecondary != wantSecondary {
		t.Fatalf("restart diverged: before=(%v,%v) after=(%v,%v)", wantPrimary, wantSecondary, gotPrimary, gotSecondary)
	}
}

// 已存值对始终是归一化后的值对：主窗口为 0 时必须在同一次 PATCH 中把次窗口也
// 持久化为 0，使任何启动都无法读回旧窗口。
func TestSystemPatchPrimaryOffPersistsZeroedSecondary(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := newTestServices()
	secrets := svc.Settings
	pool := executor.NewPool()
	sys := &System{
		Settings: secrets, Accounts: svc.Accounts, Pool: pool,
		CrossProviderPool: &atomic.Bool{}, Mu: &sync.Mutex{},
	}
	if _, _, err := EnsureExpiryWindows(ctx, secrets); err != nil {
		t.Fatal(err)
	}

	disable := int64(0)
	if err := sys.Patch(ctx, SystemSettingsPatch{ExpiryWindowSeconds: &disable}); err != nil {
		t.Fatal(err)
	}
	if stored, _, _ := secrets.GetSecret(ctx, expiryWindowSecret); stored != "0" {
		t.Fatalf("persisted primary=%q, want \"0\"", stored)
	}
	if stored, _, _ := secrets.GetSecret(ctx, secondaryExpiryWindowSecret); stored != "0" {
		t.Fatalf("persisted secondary=%q, want \"0\" (normalized with the primary)", stored)
	}
}
