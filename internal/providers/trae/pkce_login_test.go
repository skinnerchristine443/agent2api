package trae

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// 登录 URL 必须携带 IDE（PKCE）产品标记和一个 S256 challenge。
func TestBuildLoginURLCarriesPKCEAndIDEMarkers(t *testing.T) {
	u := buildLoginURL("m", "d", "http://127.0.0.1:9/authorize", "trace", "verifier123")
	for _, want := range []string{
		"auth_from=trae",
		"client_id=" + ClientID,
		"x_app_version=" + IDEVersion,
		"code_challenge_method=S256",
		"code_challenge=" + pkceChallenge("verifier123"),
	} {
		if !strings.Contains(u, want) {
			t.Fatalf("missing %q in %s", want, u)
		}
	}
}

// 回调在一个 authCodeInfo JSON blob 内投递 code。
func TestParseCallbackReadsAuthCodeInfo(t *testing.T) {
	raw := "http://127.0.0.1:34047/authorize?isRedirect=true&scope=trae" +
		"&authCodeInfo=%7B%22AuthCode%22%3A%22ABC-123%22%2C%22ExpireAt%22%3A1789845594985%7D" +
		"&loginTraceID=deadbeef&host=https%3A%2F%2Fapi.trae.com.cn&userRegion=cn"
	info, err := ParseCallback(raw)
	if err != nil {
		t.Fatal(err)
	}
	if info.AuthCode != "ABC-123" {
		t.Fatalf("authCode=%q", info.AuthCode)
	}
	if info.Trace != "deadbeef" {
		t.Fatalf("trace=%q", info.Trace)
	}
	if info.APIHost != "https://api.trae.com.cn" {
		t.Fatalf("apiHost=%q", info.APIHost)
	}
}

// 从粘贴的 PKCE 回调完成登录会在 v3 端点交换 code 并存储凭据。
func TestCompleteLoginExchangesAuthCode(t *testing.T) {
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pathExchangeCode:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["ClientID"] != ClientID || body["AuthCode"] != "ABC-123" || body["CodeVerifier"] == "" {
				t.Fatalf("code exchange body=%v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Result": map[string]any{
				"Token": "at", "RefreshToken": "rt2", "TokenExpireAt": time.Now().Add(time.Hour).Unix(),
			}})
		case pathUserInfo:
			_ = json.NewEncoder(w).Encode(map[string]any{"Result": map[string]any{
				"UserID": "u1", "ScreenName": "Tester", "EnterpriseID": "e1",
			}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	session, err := client.StartLogin(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	trace := session.State
	callback := "http://127.0.0.1:9/authorize?loginTraceID=" + trace +
		"&authCodeInfo=%7B%22AuthCode%22%3A%22ABC-123%22%7D"
	if err := client.CompleteLogin(context.Background(), "acc1", callback); err != nil {
		t.Fatal(err)
	}
	_, payload, err := store.LoadCredentialPayload(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := DecodeCredential(payload)
	if err != nil || credential.UID != "u1" || credential.AccessToken != "at" || credential.RefreshToken != "rt2" {
		t.Fatalf("credential=%+v err=%v", credential, err)
	}
	if credential.DevicePrivateKey == "" {
		t.Fatal("device key must be stored for the code exchange")
	}
}

// 旧版账号继续用铸造其 token 的 client 刷新。
func TestExchangeTokenUsesRecordedRefreshClient(t *testing.T) {
	client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)
		if parsed["ClientID"] != LegacyClientID {
			t.Fatalf("refresh must use recorded client, body=%s", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Result": map[string]any{
			"Token": "at2", "TokenExpireAt": time.Now().Add(time.Hour).Unix(),
		}})
	}))
	cred := Credential{RefreshToken: "rt", RefreshClientID: LegacyClientID, Domain: DomainCN, APIHost: OAuthHost}
	refreshed, err := client.ExchangeToken(context.Background(), "acc1", cred)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken != "at2" {
		t.Fatalf("refreshed=%+v", refreshed)
	}
}
