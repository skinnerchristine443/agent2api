package accounts

import "testing"

// 渠道默认的归一化：max_inflight 落回默认、代理按全局同口径校验、
// 负的日防护被拒。这是渠道级设置写入前的唯一闸门。
func TestNormalizeAccountDefaults(t *testing.T) {
	ok, err := NormalizeAccountDefaults(AccountDefaults{MaxInFlight: 0})
	if err != nil || ok.MaxInFlight != DefaultMaxInFlight {
		t.Fatalf("max_inflight 未落回默认: %+v err=%v", ok, err)
	}

	if _, err := NormalizeAccountDefaults(AccountDefaults{MaxInFlight: 4, ProxyURL: "ftp://x"}); err == nil {
		t.Fatal("非法代理未被拒绝")
	}
	if _, err := NormalizeAccountDefaults(AccountDefaults{MaxInFlight: 4, ProxyURL: "http://127.0.0.1:8080"}); err != nil {
		t.Fatalf("合法 http 代理被拒: %v", err)
	}
	if _, err := NormalizeAccountDefaults(AccountDefaults{MaxInFlight: 4, DailyTokenLimit: -1}); err == nil {
		t.Fatal("负的日限额未被拒绝")
	}
}

// 物化：把渠道默认写进账号行的 7 个字段，且不动优先级（优先级是账号级）。
func TestMaterializeAccountDefaults(t *testing.T) {
	account := Account{
		ID: "a1", Provider: "workbuddy", ProviderRegion: "cn",
		MaxInFlight: 4, Priority: 77, DropSystemPrompt: true,
	}
	defaults := AccountDefaults{
		MaxInFlight: 8, ProxyURL: "http://127.0.0.1:8080", DropSystemPrompt: false,
		ReserveCredits: 100, DailyTokenLimit: 200, DailyCreditLimit: 300, DailyModelTokenLimit: 400,
	}
	got := MaterializeAccountDefaults(account, defaults)
	if got.MaxInFlight != 8 || got.ProxyURL != defaults.ProxyURL || got.DropSystemPrompt {
		t.Fatalf("物化未生效: %+v", got)
	}
	if got.ReserveCredits != 100 || got.DailyTokenLimit != 200 || got.DailyCreditLimit != 300 || got.DailyModelTokenLimit != 400 {
		t.Fatalf("日防护未物化: %+v", got)
	}
	if got.Priority != 77 {
		t.Fatalf("优先级被误改: %d", got.Priority)
	}
}

// 变更检测：值相同不写库（幂等重刷），任一字段不同即需更新。
func TestAccountDefaultsChanged(t *testing.T) {
	account := Account{MaxInFlight: 8, ProxyURL: "http://p", DropSystemPrompt: false, ReserveCredits: 100}
	same := AccountDefaults{MaxInFlight: 8, ProxyURL: "http://p", DropSystemPrompt: false, ReserveCredits: 100}
	if AccountDefaultsChanged(account, same) {
		t.Fatal("值相同时不应报告变更")
	}
	for _, mutate := range []AccountDefaults{
		{MaxInFlight: 9, ProxyURL: "http://p", DropSystemPrompt: false, ReserveCredits: 100},
		{MaxInFlight: 8, ProxyURL: "http://q", DropSystemPrompt: false, ReserveCredits: 100},
		{MaxInFlight: 8, ProxyURL: "http://p", DropSystemPrompt: true, ReserveCredits: 100},
		{MaxInFlight: 8, ProxyURL: "http://p", DropSystemPrompt: false, ReserveCredits: 101},
	} {
		if !AccountDefaultsChanged(account, mutate) {
			t.Fatalf("字段变化未检出: %+v", mutate)
		}
	}
}

// 存储键：`account_defaults.<provider>.<region>`。
func TestAccountDefaultsSecretKey(t *testing.T) {
	if got := AccountDefaultsSecret("workbuddy", "cn"); got != "account_defaults.workbuddy.cn" {
		t.Fatalf("secret 键 = %q", got)
	}
}
