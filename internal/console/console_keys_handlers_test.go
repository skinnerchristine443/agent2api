package console

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"agent2api/internal/config"
	"agent2api/internal/control"
)

func TestAPIKeyPrefixMasksLongSecrets(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"   ", ""},
		{"short", "short"},
		{"123456789012", "123456789012"},   // 恰好 12 位：原样返回
		{"1234567890123", "12345678…0123"}, // 13 位起进入掩码
		{"  abcdefghijklmnop  ", "abcdefgh…mnop"},
	}
	for _, tc := range cases {
		if got := apiKeyPrefix(tc.in); got != tc.want {
			t.Errorf("apiKeyPrefix(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

// GET 只回显指纹，绝不回显完整密钥——它是只读探测面。
func TestHandleConsoleKeyGetNeverLeaksSecret(t *testing.T) {
	handler := &Handler{ConsoleKey: func() string { return "abcdefghijklmnop" }}
	rec := httptest.NewRecorder()
	handler.HandleConsoleKey(rec, httptest.NewRequest(http.MethodGet, "/api/console/key", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["prefix"] != "abcdefgh…mnop" || body["target"] != "console" {
		t.Fatalf("body=%v", body)
	}
	if _, leaked := body["secret"]; leaked {
		t.Fatalf("GET 泄露了完整密钥: %v", body)
	}
	if _, ok := body["rotated"]; ok {
		t.Fatalf("GET 不应声称已轮换: %v", body)
	}

	// 未注入 ConsoleKey 时回落到配置中的运维密钥。
	handler = &Handler{Cfg: &config.Config{ConsoleKey: "abcdefghijklmnop"}}
	rec = httptest.NewRecorder()
	handler.HandleConsoleKey(rec, httptest.NewRequest(http.MethodGet, "/api/console/key", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["prefix"] != "abcdefgh…mnop" {
		t.Fatalf("配置回落失败: %v", body)
	}
}

func TestHandleConsoleKeyRejectsNonRotateAndBadInput(t *testing.T) {
	store := t54NewStore()
	handler := &Handler{KeyRotation: t54KeyRotation(store, func() (string, error) { return "generated", nil })}

	cases := []struct {
		name, body string
	}{
		{"显式 false", `{"rotate":false}`},
		{"空体", ``},
		{"缺字段", `{}`},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		handler.HandleConsoleKey(rec, httptest.NewRequest(http.MethodPost, "/api/console/key", strings.NewReader(tc.body)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d body=%s", tc.name, rec.Code, rec.Body)
			continue
		}
		if msg := t54ErrMessage(t, rec); !strings.Contains(msg, "rotate=true") {
			t.Errorf("%s: msg=%q", tc.name, msg)
		}
	}

	rec := httptest.NewRecorder()
	handler.HandleConsoleKey(rec, httptest.NewRequest(http.MethodPost, "/api/console/key", strings.NewReader(`{`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 JSON status=%d", rec.Code)
	}
	if code := t54ErrCode(t, rec); code != "invalid_request" {
		t.Fatalf("code=%q", code)
	}
}

func TestHandleConsoleKeyRotationNotConfigured(t *testing.T) {
	handler := &Handler{}
	rec := httptest.NewRecorder()
	handler.HandleConsoleKey(rec, httptest.NewRequest(http.MethodPost, "/api/console/key", strings.NewReader(`{"rotate":true}`)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if code := t54ErrCode(t, rec); code != "console_key_rotate_failed" {
		t.Fatalf("code=%q", code)
	}
	if msg := t54ErrMessage(t, rec); msg != "key rotation is not configured" {
		t.Fatalf("msg=%q", msg)
	}
}

// reveal（批次 7）：POST reveal=true 只读返回完整密钥；GET 契约不受影响；
// 缺省目标 console，target=proxy 读数据面钥；未配置时报错。
func TestHandleConsoleKeyRevealsStoredSecret(t *testing.T) {
	store := t54NewStore()
	keys := control.NewKeys(store)
	if err := keys.SetConsoleSecret(context.Background(), "console_key", "stored-console-secret"); err != nil {
		t.Fatal(err)
	}
	if err := keys.SetConsoleSecret(context.Background(), "proxy_api_key", "stored-proxy-secret"); err != nil {
		t.Fatal(err)
	}
	handler := &Handler{KeyRotation: t54KeyRotation(store, func() (string, error) { return "unused", nil })}

	rec := httptest.NewRecorder()
	handler.HandleConsoleKey(rec, httptest.NewRequest(http.MethodPost, "/api/console/key", strings.NewReader(`{"reveal":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Prefix  string `json:"prefix"`
		Target  string `json:"target"`
		Rotated bool   `json:"rotated"`
		Secret  string `json:"secret"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Secret != "stored-console-secret" || body.Target != "console" || body.Rotated {
		t.Fatalf("body=%+v", body)
	}
	if body.Prefix != "stored-c…cret" {
		t.Fatalf("prefix=%q", body.Prefix)
	}
	// 只读：库中值必须原样未变。
	if stored, ok := store.secretValue("console_key"); !ok || stored != "stored-console-secret" {
		t.Fatalf("reveal 改动了 console_key: %q ok=%v", stored, ok)
	}

	rec = httptest.NewRecorder()
	handler.HandleConsoleKey(rec, httptest.NewRequest(http.MethodPost, "/api/console/key", strings.NewReader(`{"reveal":true,"target":"PROXY"}`)))
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Secret != "stored-proxy-secret" || body.Target != "proxy" || body.Rotated {
		t.Fatalf("proxy body=%+v", body)
	}

	// 未配置（空库）：reveal 报错且不泄露任何内容。
	empty := &Handler{KeyRotation: t54KeyRotation(t54NewStore(), func() (string, error) { return "x", nil })}
	rec = httptest.NewRecorder()
	empty.HandleConsoleKey(rec, httptest.NewRequest(http.MethodPost, "/api/console/key", strings.NewReader(`{"reveal":true}`)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("empty store status=%d body=%s", rec.Code, rec.Body)
	}
	if code := t54ErrCode(t, rec); code != "console_key_reveal_failed" {
		t.Fatalf("code=%q", code)
	}

	// rotate 与 reveal 同时给出：拒绝（必须恰好其一）。
	rec = httptest.NewRecorder()
	handler.HandleConsoleKey(rec, httptest.NewRequest(http.MethodPost, "/api/console/key", strings.NewReader(`{"rotate":true,"reveal":true}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("both-actions status=%d", rec.Code)
	}
}

// 轮换控制台密钥：新密钥只落 console_key，且绝不通过 PublishProxy 触达数据面。
func TestHandleConsoleKeyRotatesConsoleByDefault(t *testing.T) {
	store := t54NewStore()
	var published []string
	var publishedProxy []string
	handler := &Handler{KeyRotation: t54KeyRotation(store, func() (string, error) { return "brand-new-console-key", nil })}
	handler.KeyRotation.Publish = func(secret string) { published = append(published, secret) }
	handler.KeyRotation.PublishProxy = func(secret string) { publishedProxy = append(publishedProxy, secret) }

	rec := httptest.NewRecorder()
	handler.HandleConsoleKey(rec, httptest.NewRequest(http.MethodPost, "/api/console/key", strings.NewReader(`{"rotate":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Prefix  string `json:"prefix"`
		Target  string `json:"target"`
		Rotated bool   `json:"rotated"`
		Secret  string `json:"secret"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Rotated || body.Target != "console" || body.Secret != "brand-new-console-key" {
		t.Fatalf("body=%+v", body)
	}
	if stored, ok := store.secretValue("console_key"); !ok || stored != "brand-new-console-key" {
		t.Fatalf("console_key 未持久化: %q ok=%v", stored, ok)
	}
	if _, ok := store.secretValue("proxy_api_key"); ok {
		t.Fatalf("轮换控制台密钥意外写入了 proxy_api_key")
	}
	if len(published) != 1 || published[0] != "brand-new-console-key" {
		t.Fatalf("Publish 未收到新密钥: %v", published)
	}
	if len(publishedProxy) != 0 {
		t.Fatalf("控制台轮换不得触及数据面发布: %v", publishedProxy)
	}
}

// 轮换数据面密钥：落 proxy_api_key，并同步给 runtime 的进程内调用方。
func TestHandleConsoleKeyRotatesProxyTarget(t *testing.T) {
	store := t54NewStore()
	rt := t54NewRuntime(store)
	var published []string
	var publishedProxy []string
	handler := &Handler{
		Control:     &control.Services{Accounts: control.NewAccounts(rt)},
		KeyRotation: t54KeyRotation(store, func() (string, error) { return "brand-new-proxy-key", nil }),
	}
	handler.KeyRotation.Accounts = handler.Control.Accounts
	handler.KeyRotation.Publish = func(secret string) { published = append(published, secret) }
	handler.KeyRotation.PublishProxy = func(secret string) { publishedProxy = append(publishedProxy, secret) }

	rec := httptest.NewRecorder()
	handler.HandleConsoleKey(rec, httptest.NewRequest(http.MethodPost, "/api/console/key",
		strings.NewReader(`{"rotate":true,"target":"PROXY"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Target  string `json:"target"`
		Rotated bool   `json:"rotated"`
		Secret  string `json:"secret"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Rotated || body.Target != "proxy" || body.Secret != "brand-new-proxy-key" {
		t.Fatalf("body=%+v", body)
	}
	if stored, ok := store.secretValue("proxy_api_key"); !ok || stored != "brand-new-proxy-key" {
		t.Fatalf("proxy_api_key 未持久化: %q ok=%v", stored, ok)
	}
	if _, ok := store.secretValue("console_key"); ok {
		t.Fatalf("轮换数据面密钥意外写入了 console_key")
	}
	if len(publishedProxy) != 1 || publishedProxy[0] != "brand-new-proxy-key" {
		t.Fatalf("PublishProxy 未收到新密钥: %v", publishedProxy)
	}
	if len(published) != 0 {
		t.Fatalf("数据面轮换不得触及控制台发布: %v", published)
	}
	if len(rt.replaced) != 1 || rt.replaced[0] != "brand-new-proxy-key" {
		t.Fatalf("runtime 未换用新数据面密钥: %v", rt.replaced)
	}
}

func TestHandleConsoleKeyRejectsUnknownTarget(t *testing.T) {
	store := t54NewStore()
	handler := &Handler{KeyRotation: t54KeyRotation(store, func() (string, error) { return "x", nil })}
	rec := httptest.NewRecorder()
	handler.HandleConsoleKey(rec, httptest.NewRequest(http.MethodPost, "/api/console/key",
		strings.NewReader(`{"rotate":true,"target":"bogus"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if msg := t54ErrMessage(t, rec); msg != "target must be console or proxy" {
		t.Fatalf("msg=%q", msg)
	}
}

func TestHandleConsoleKeyMapsGenerateFailure(t *testing.T) {
	store := t54NewStore()
	handler := &Handler{KeyRotation: t54KeyRotation(store, func() (string, error) { return "", errors.New("entropy exhausted") })}
	rec := httptest.NewRecorder()
	handler.HandleConsoleKey(rec, httptest.NewRequest(http.MethodPost, "/api/console/key", strings.NewReader(`{"rotate":true}`)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if code := t54ErrCode(t, rec); code != "console_key_rotate_failed" {
		t.Fatalf("code=%q", code)
	}
	if _, ok := store.secretValue("console_key"); ok {
		t.Fatalf("生成失败仍写入了 console_key")
	}
}

func TestHandleConsoleKeyMethodGuard(t *testing.T) {
	handler := &Handler{}
	rec := httptest.NewRecorder()
	handler.HandleConsoleKey(rec, httptest.NewRequest(http.MethodPut, "/api/console/key", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", rec.Code)
	}
}

// t54KeyRotation 组装一个可用的 KeyRotation 替身（Mu 必须非 nil，Rotate 会加锁）。
func t54KeyRotation(store *t54Store, generate func() (string, error)) *control.KeyRotation {
	return &control.KeyRotation{
		Keys:     control.NewKeys(store),
		Mu:       &sync.Mutex{},
		Generate: generate,
	}
}
