package workbuddy

import (
	"errors"
	"strings"
	"testing"
)

const encryptedEnvelope = `{"$wbEncrypted":1,"envelope":"eyJzdWl0ZSI6MX0="}`

// 每种被接受的载荷形态都必须把加密信封报告为其专属错误，
// 使不支持的格式不会被误认为格式错误的载荷。
func TestDecodeCredentialRejectsEncryptedEnvelopeWithDedicatedError(t *testing.T) {
	cases := map[string]string{
		"nested auth": `{"account":{"uid":"u1"},"auth":{"accessToken":` + encryptedEnvelope + `,"refreshToken":"rt"}}`,
		"flat snake":  `{"access_token":` + encryptedEnvelope + `,"refresh_token":"rt"}`,
		"flat camel":  `{"accessToken":` + encryptedEnvelope + `,"refreshToken":"rt"}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeCredential([]byte(payload)); !errors.Is(err, ErrEncryptedCredential) {
				t.Fatalf("DecodeCredential err=%v, want ErrEncryptedCredential", err)
			}
			if err := ValidateCredential([]byte(payload)); !errors.Is(err, ErrEncryptedCredential) {
				t.Fatalf("ValidateCredential err=%v, want ErrEncryptedCredential", err)
			}
		})
	}
}

// 对于只是缺少 token 的明文载荷，必须保留通用消息：
// 那是一处真正的调用方错误，而非不支持的格式。
func TestDecodeCredentialKeepsGenericErrorWithoutToken(t *testing.T) {
	_, err := DecodeCredential([]byte(`{"auth":{"refreshToken":"rt"}}`))
	if err == nil || errors.Is(err, ErrEncryptedCredential) {
		t.Fatalf("want the generic missing-token error, got %v", err)
	}
	if !strings.Contains(err.Error(), "requires access_token") {
		t.Fatalf("err=%v", err)
	}
}

func TestHasEncryptedTokenEnvelope(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    bool
	}{
		{"nested", `{"auth":{"accessToken":` + encryptedEnvelope + `}}`, true},
		{"flat snake", `{"access_token":` + encryptedEnvelope + `}`, true},
		{"flat camel", `{"accessToken":` + encryptedEnvelope + `}`, true},
		{"plaintext token", `{"accessToken":"tok"}`, false},
		{"marker outside the token", `{"note":{"$wbEncrypted":1},"access_token":"tok"}`, false},
		{"not json", `not json`, false},
		{"token is a plain string", `{"auth":{"accessToken":"tok"}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasEncryptedTokenEnvelope([]byte(tc.payload)); got != tc.want {
				t.Fatalf("hasEncryptedTokenEnvelope(%s)=%v, want %v", tc.payload, got, tc.want)
			}
		})
	}
}
