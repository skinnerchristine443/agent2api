package accounts

import (
	"context"
	"errors"
	"testing"
)

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
	if got := AccountDefaultsSecret(" trae ", " cn "); got != "account_defaults.trae.cn" {
		t.Fatalf("空白未裁剪: %q", got)
	}
}

// 内置默认：最大并发沿用账号级默认、丢弃系统提示词默认开、四道日防护为 0。
func TestDefaultAccountDefaults(t *testing.T) {
	got := DefaultAccountDefaults()
	if got.MaxInFlight != DefaultMaxInFlight || !got.DropSystemPrompt {
		t.Fatalf("内置默认 = %+v", got)
	}
	if got.ProxyURL != "" || got.ReserveCredits != 0 || got.DailyTokenLimit != 0 || got.DailyCreditLimit != 0 || got.DailyModelTokenLimit != 0 {
		t.Fatalf("内置默认应为空代理/零防护: %+v", got)
	}
}

// 编解码往返：Encode→decode 稳定；空值/损坏值/非法值都回落。
func TestAccountDefaultsEncodeDecodeRoundTrip(t *testing.T) {
	defaults := AccountDefaults{MaxInFlight: 6, ProxyURL: "http://127.0.0.1:8080", DropSystemPrompt: false, ReserveCredits: 11, DailyTokenLimit: 22, DailyCreditLimit: 33, DailyModelTokenLimit: 44}
	encoded := EncodeAccountDefaults(defaults)
	if encoded == "" {
		t.Fatal("编码为空")
	}
	decoded, ok := decodeAccountDefaults(encoded)
	if !ok || decoded != defaults {
		t.Fatalf("往返不一致: %+v ok=%v", decoded, ok)
	}
}

func TestDecodeAccountDefaultsFallsBack(t *testing.T) {
	if _, ok := decodeAccountDefaults("   "); ok {
		t.Fatal("空值不应命中")
	}
	if _, ok := decodeAccountDefaults("{not json"); ok {
		t.Fatal("损坏 JSON 不应命中")
	}
	if _, ok := decodeAccountDefaults(`{"daily_token_limit":-1}`); ok {
		t.Fatal("非法值（负日限额）不应命中")
	}
}

// 渠道默认解析：nil store 回落内置默认；命中持久化值；存储错误向上传播。
func TestAccountDefaultsFor(t *testing.T) {
	ctx := context.Background()

	got, err := AccountDefaultsFor(ctx, nil, "workbuddy", "cn")
	if err != nil || got != DefaultAccountDefaults() {
		t.Fatalf("nil store = %+v err=%v", got, err)
	}

	stored := AccountDefaults{MaxInFlight: 9, DropSystemPrompt: false, ReserveCredits: 5}
	store := t54Store{AccountDefaultsSecret("workbuddy", "cn"): EncodeAccountDefaults(stored)}
	got, err = AccountDefaultsFor(ctx, store, "workbuddy", "cn")
	if err != nil || got != stored {
		t.Fatalf("命中值 = %+v err=%v", got, err)
	}

	// 键缺失 → 内置默认。
	got, err = AccountDefaultsFor(ctx, store, "trae", "cn")
	if err != nil || got != DefaultAccountDefaults() {
		t.Fatalf("缺失键 = %+v err=%v", got, err)
	}

	// 损坏值 → 内置默认（不报错）。
	broken := t54Store{AccountDefaultsSecret("workbuddy", "cn"): "{broken"}
	if got, err = AccountDefaultsFor(ctx, broken, "workbuddy", "cn"); err != nil || got != DefaultAccountDefaults() {
		t.Fatalf("损坏值 = %+v err=%v", got, err)
	}

	// 存储错误 → 原样传播。
	if _, err = AccountDefaultsFor(ctx, t54ErrStore{err: errors.New("db down")}, "workbuddy", "cn"); err == nil {
		t.Fatal("存储错误未传播")
	}
}

// 全量枚举：nil store 返回空 map；正常时按 provider.region 建键。
func TestAllAccountDefaults(t *testing.T) {
	ctx := context.Background()
	combos := [][2]string{{"workbuddy", "cn"}, {"trae", "cn"}}

	empty, err := AllAccountDefaults(ctx, nil, combos)
	if err != nil || len(empty) != 0 {
		t.Fatalf("nil store = %+v err=%v", empty, err)
	}

	store := t54Store{AccountDefaultsSecret("trae", "cn"): EncodeAccountDefaults(AccountDefaults{MaxInFlight: 3})}
	got, err := AllAccountDefaults(ctx, store, combos)
	if err != nil {
		t.Fatal(err)
	}
	if got["workbuddy.cn"] != DefaultAccountDefaults() || got["trae.cn"].MaxInFlight != 3 {
		t.Fatalf("枚举结果 = %+v", got)
	}

	if _, err := AllAccountDefaults(ctx, t54ErrStore{err: errors.New("boom")}, combos); err == nil {
		t.Fatal("存储错误未传播")
	}
}

// 区域校验：provider / region 任一为空（或纯空白）都必须被拒。
func TestValidateAccountDefaultsRegion(t *testing.T) {
	if err := ValidateAccountDefaultsRegion("workbuddy", "cn"); err != nil {
		t.Fatalf("合法组合被拒: %v", err)
	}
	for _, bad := range [][2]string{{"", "cn"}, {"workbuddy", ""}, {"  ", "cn"}, {"workbuddy", "  "}} {
		if err := ValidateAccountDefaultsRegion(bad[0], bad[1]); err == nil {
			t.Fatalf("非法组合未被拒: %q/%q", bad[0], bad[1])
		}
	}
}
