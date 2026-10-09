package trae

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// 旧代凭据（无 client 绑定）在「client 不匹配」失败后自动以 LegacyClientID
// 重试一次，并把绑定随凭据返回（供调用方持久化）。覆盖两种失败出口：
// 200 含错误体（无 token 出口）与 >=300（分类错误出口）。
func TestExchangeTokenSelfHealsLegacyClientBinding(t *testing.T) {
	mismatch := `{"ResponseMetadata":{"Error":{"Code":"10101","Data":{"__Message.error":"refresh token is not matched to the client"}}}}`
	for _, tc := range []struct {
		name     string
		failCode int
	}{
		{"200 无 token 出口", http.StatusOK},
		{">=300 分类错误出口", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seen []string
			client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				var parsed map[string]any
				_ = json.Unmarshal(body, &parsed)
				cid, _ := parsed["ClientID"].(string)
				seen = append(seen, cid)
				if cid != LegacyClientID {
					w.WriteHeader(tc.failCode)
					_, _ = w.Write([]byte(mismatch))
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"Result": map[string]any{
					"Token": "at2", "TokenExpireAt": time.Now().Add(time.Hour).Unix(), "RefreshToken": "rt2",
				}})
			}))

			cred := Credential{RefreshToken: "rt1", Domain: DomainCN, APIHost: OAuthHost}
			next, err := client.ExchangeToken(context.Background(), "acc1", cred)
			if err != nil {
				t.Fatalf("自愈应当成功: %v", err)
			}
			if len(seen) != 2 || seen[0] != ClientID || seen[1] != LegacyClientID {
				t.Fatalf("刷新 client 序列 = %v，期望 [%s %s]", seen, ClientID, LegacyClientID)
			}
			if next.AccessToken != "at2" || next.RefreshToken != "rt2" {
				t.Fatalf("凭据未更新: %+v", next)
			}
			if next.RefreshClientID != LegacyClientID {
				t.Fatalf("成功绑定必须随凭据返回（供持久化）: %q", next.RefreshClientID)
			}
		})
	}
}

// 已绑定 legacy 仍失败、或错误与 client 不匹配无关时：不重试（单次请求）、
// 不掩盖原错误。
func TestExchangeTokenNoHealWhenBoundOrUnrelated(t *testing.T) {
	cases := []struct {
		name     string
		cred     Credential
		response string
	}{
		{
			name:     "已绑定 legacy 仍失败：不重试",
			cred:     Credential{RefreshToken: "rt", RefreshClientID: LegacyClientID},
			response: `{"ResponseMetadata":{"Error":{"Code":"10101"}}}`,
		},
		{
			name:     "无关错误（plan limit）：不重试",
			cred:     Credential{RefreshToken: "rt"},
			response: `{"code":1005,"message":"plan limit"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				_, _ = w.Write([]byte(tc.response))
			}))
			tc.cred.Domain = DomainCN
			tc.cred.APIHost = OAuthHost
			if _, err := client.ExchangeToken(context.Background(), "acc1", tc.cred); err == nil {
				t.Fatal("应当失败")
			}
			if calls != 1 {
				t.Fatalf("不应重试：calls=%d", calls)
			}
		})
	}
}
