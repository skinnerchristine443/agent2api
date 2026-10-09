package workbuddy

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"testing"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
)

// codebuddy.ai 是 Intl（IDE）realm。把它当作 CN 会把账号路由到错误的主机，
// 在那里它的 Bearer token 是未知的。
func TestRealmDistinguishesIntlFromCNAndGlobal(t *testing.T) {
	cases := []struct {
		domain string
		want   Realm
	}{
		{"codebuddy.cn", RealmCN},
		{"www.codebuddy.cn", RealmCN},
		{"", RealmCN},
		{"example.invalid", RealmCN},
		{"workbuddy.ai", RealmGlobal},
		{"www.workbuddy.ai", RealmGlobal},
		{"codebuddy.ai", RealmIntl},
		{"www.codebuddy.ai", RealmIntl},
	}
	for _, tc := range cases {
		credential := Credential{Domain: tc.domain}
		if got := credential.Realm(); got != tc.want {
			t.Errorf("Realm(%q) = %q, want %q", tc.domain, got, tc.want)
		}
		if credential.IsGlobal() != (tc.want == RealmGlobal) {
			t.Errorf("IsGlobal(%q) disagrees with Realm", tc.domain)
		}
		if credential.IsIntl() != (tc.want == RealmIntl) {
			t.Errorf("IsIntl(%q) disagrees with Realm", tc.domain)
		}
	}
}

func TestRealmBasesAreDistinct(t *testing.T) {
	intl := Credential{Domain: "codebuddy.ai"}
	if got := intl.ChatBase(); got != ChatBaseIntl {
		t.Fatalf("Intl ChatBase = %q, want %q", got, ChatBaseIntl)
	}
	if got := intl.BillingBase(); got != BillingBaseIntl {
		t.Fatalf("Intl BillingBase = %q, want %q", got, BillingBaseIntl)
	}
	if ChatBaseIntl == ChatBaseGlobal || ChatBaseIntl == ChatBaseCN {
		t.Fatal("the three realms must not share a chat base")
	}
}

// Intl 网关期望 IDE 客户端形态，且不携带浏览器的
// XHR 标记。
func TestIntlHeadersPresentTheIDEIdentity(t *testing.T) {
	header := http.Header{}
	SetChatHeaders(header, Credential{Domain: "codebuddy.ai", AccessToken: "at", UID: "u1"})
	if got := header.Get("X-Requested-With"); got != "" {
		t.Fatalf("Intl must not send X-Requested-With, got %q", got)
	}
	if got := header.Get("X-IDE-Type"); got != productTypeIDE {
		t.Fatalf("X-IDE-Type = %q, want %q", got, productTypeIDE)
	}
	if got := header.Get("X-IDE-Version"); got != IDEClientVersion {
		t.Fatalf("X-IDE-Version = %q, want %q", got, IDEClientVersion)
	}
	if got := header.Get("X-Product-Version"); got != IDEClientVersion {
		t.Fatalf("X-Product-Version = %q, want %q", got, IDEClientVersion)
	}
	if got := header.Get("Origin"); got != "https://www.codebuddy.ai" {
		t.Fatalf("Origin = %q", got)
	}
}

func TestCNAndGlobalHeadersKeepTheCLIShape(t *testing.T) {
	cn := http.Header{}
	SetChatHeaders(cn, Credential{Domain: "codebuddy.cn", AccessToken: "at", UID: "u1"})
	if cn.Get("X-Requested-With") != "XMLHttpRequest" {
		t.Fatal("CN must keep X-Requested-With")
	}
	if cn.Get("X-IDE-Type") != productTypeCLI || cn.Get("Origin") != "https://www.codebuddy.cn" {
		t.Fatalf("CN headers = %v", cn)
	}

	global := http.Header{}
	SetChatHeaders(global, Credential{Domain: "workbuddy.ai", AccessToken: "at", UID: "u1"})
	if global.Get("Origin") != "https://www.workbuddy.ai" {
		t.Fatalf("Global Origin = %q", global.Get("Origin"))
	}
	if global.Get("X-IDE-Type") != productTypeCLI {
		t.Fatal("Global must keep the CLI identity")
	}
}

// Intl 账号绝不能被发往 CN 签到端点：CN 网关会拒绝 Intl token，
// 且没有实现任何 Intl 签到契约。
func TestIntlCheckinIsUnsupportedAndMakesNoRequest(t *testing.T) {
	called := false
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	}))
	payload, err := Credential{
		AccessToken: "at", UID: "u1", Domain: "codebuddy.ai",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	store.items = map[string][]byte{"acc1": payload}
	// 账号的控制面区域决定 realm；一个 Intl 账号必须携带 "intl"
	// （凭据 domain 本身只是兜底）。
	store.region = "intl"

	if _, err := client.Checkin(context.Background(), "acc1"); !errors.Is(err, providers.ErrUnsupported) {
		t.Fatalf("Checkin err = %v, want ErrUnsupported", err)
	}
	if called {
		t.Fatal("the Intl realm must not reach any check-in endpoint")
	}
}

// 区域与凭据 domain 在 Intl 上必须一致；不一致时以区域（运维意图）为准，
// 并将它应用到凭据存储的 domain 上。
func TestIntlRegionOverridesAStaleCredentialDomain(t *testing.T) {
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s", r.URL.Path)
		w.WriteHeader(http.StatusTeapot)
	}))
	credential := Credential{
		AccessToken: "at", UID: "u1", Domain: "workbuddy.ai",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}
	payload, err := credential.Encode()
	if err != nil {
		t.Fatal(err)
	}
	store.items = map[string][]byte{"acc1": payload}
	store.region = "intl"

	resolved, err := client.resolvedCredential(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Realm() != RealmIntl {
		t.Fatalf("Realm = %q, want intl", resolved.Realm())
	}
	if resolved.ChatBase() != ChatBaseIntl {
		t.Fatalf("ChatBase = %q, want %q", resolved.ChatBase(), ChatBaseIntl)
	}
}

func jwtWithIssuer(issuer string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"` + issuer + `"}`))
	return "header." + payload + ".signature"
}

// 存储的 Domain 与自身 token 不一致的凭据（导入错误，或在 realm 之间迁移过）必须
// 计费到 token 所指明的部署。用错计费主机是一种静默失败：每个请求都 401 却没有任何
// 线索说明原因。
func TestBillingBasePrefersTokenIssuerOverStoredDomain(t *testing.T) {
	mismatched := Credential{AccessToken: jwtWithIssuer("https://www.codebuddy.ai"), Domain: DomainCN}
	if got := mismatched.BillingBase(); got != BillingBaseIntl {
		t.Fatalf("BillingBase = %q, want %q (token issuer must win)", got, BillingBaseIntl)
	}
	cnToken := Credential{AccessToken: jwtWithIssuer("https://www.codebuddy.cn"), Domain: DomainIntl}
	if got := cnToken.BillingBase(); got != BillingBaseCN {
		t.Fatalf("BillingBase = %q, want %q", got, BillingBaseCN)
	}
}

// 不透明的 token、格式错误的 token，或本包不认识的签发者，必须回退到存储的
// realm，而不是臆造主机。
func TestBillingBaseFallsBackWhenIssuerUnusable(t *testing.T) {
	for name, token := range map[string]string{
		"opaque":      "not-a-jwt",
		"two-segment": "header.payload",
		"bad-base64":  "header.!!!not-base64!!!.sig",
		"no-iss":      "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"u"}`)) + ".sig",
		"unknown-iss": jwtWithIssuer("https://billing.example.test"),
	} {
		cred := Credential{AccessToken: token, Domain: DomainIntl}
		if got := cred.BillingBase(); got != BillingBaseIntl {
			t.Fatalf("%s: BillingBase = %q, want the stored-realm fallback %q", name, got, BillingBaseIntl)
		}
	}
}

// 11102 表示模型未在该 realm/账号下注册；账号本身是健康的，
// 必须留在轮换中。
func TestClassifyModelNotRegisteredDoesNotCoolDown(t *testing.T) {
	got := Classify(http.StatusOK, `{"code":11102,"msg":"model not registered"}`)
	if got.Kind != accounts.KindModelNotAvailable {
		t.Fatalf("Kind = %q, want %q", got.Kind, accounts.KindModelNotAvailable)
	}
}
