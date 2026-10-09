package trae

import "testing"

// TestDecodeCredentialShapeMatrix 覆盖 DecodeCredential 的五种载荷形态。
//
// 核心契约：命中条件只认 token，绝不以 uid 命中。uid 键在 camelCase 与
// snake_case 两形态同名，一旦某分支拿它当判据，就会吞掉另一形态并返回空 token
// （err 仍为 nil）。本表把 snake_case / camelCase / nested 三种「有 token」形态
// 的 AT/RT/UID 与 refresh 过期时间都钉死，并要求纯 uid 与空载荷必须报错。
//
// 反向注入：把 flat 分支条件改回 `|| flat.UID != ""`，camelCase 与 uid_only
// 用例必须变红；把 camel 分支改回含 UID，uid_only 用例必须变红。
func TestDecodeCredentialShapeMatrix(t *testing.T) {
	cases := []struct {
		name             string
		payload          string
		wantErr          bool
		wantAccessToken  string
		wantRefreshToken string
		wantUID          string
		wantRefreshExpAt int64
	}{
		{
			name:             "snake_case",
			payload:          `{"access_token":"AT","refresh_token":"RT","refresh_expires_at":4102444800,"uid":"U"}`,
			wantAccessToken:  "AT",
			wantRefreshToken: "RT",
			wantUID:          "U",
			wantRefreshExpAt: 4102444800,
		},
		{
			name:             "camelCase",
			payload:          `{"accessToken":"AT","refreshToken":"RT","refreshExpiresAt":4102444800,"uid":"U"}`,
			wantAccessToken:  "AT",
			wantRefreshToken: "RT",
			wantUID:          "U",
			wantRefreshExpAt: 4102444800,
		},
		{
			name:             "nested",
			payload:          `{"auth":{"accessToken":"AT","refreshToken":"RT","refreshExpiresAt":4102444800},"account":{"uid":"U"}}`,
			wantAccessToken:  "AT",
			wantRefreshToken: "RT",
			wantUID:          "U",
			wantRefreshExpAt: 4102444800,
		},
		{name: "uid_only", payload: `{"uid":"U"}`, wantErr: true},
		{name: "nested_uid_only", payload: `{"account":{"uid":"U"}}`, wantErr: true},
		{name: "empty", payload: `{}`, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			credential, err := DecodeCredential([]byte(tc.payload))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("DecodeCredential(%s) = %+v, want error", tc.payload, credential)
				}
				if verr := ValidateCredential([]byte(tc.payload)); verr == nil {
					t.Fatalf("ValidateCredential(%s) accepted a token-less payload", tc.payload)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeCredential(%s) error = %v", tc.payload, err)
			}
			if credential.AccessToken != tc.wantAccessToken ||
				credential.RefreshToken != tc.wantRefreshToken ||
				credential.UID != tc.wantUID {
				t.Fatalf("DecodeCredential(%s) = AT=%q RT=%q UID=%q, want AT=%q RT=%q UID=%q",
					tc.payload, credential.AccessToken, credential.RefreshToken, credential.UID,
					tc.wantAccessToken, tc.wantRefreshToken, tc.wantUID)
			}
			if credential.RefreshExpiresAt != tc.wantRefreshExpAt {
				t.Fatalf("DecodeCredential(%s) RefreshExpiresAt = %d, want %d",
					tc.payload, credential.RefreshExpiresAt, tc.wantRefreshExpAt)
			}
			if verr := ValidateCredential([]byte(tc.payload)); verr != nil {
				t.Fatalf("ValidateCredential(%s) error = %v", tc.payload, verr)
			}
		})
	}
}
