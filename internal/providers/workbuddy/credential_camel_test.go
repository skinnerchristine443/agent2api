package workbuddy

import "testing"

// 扁平的 camelCase 导出形态必须能解码：一个单字符的逻辑笔误（err != nil）
// 曾使该分支变为死代码，静默地拒绝有效凭据。
func TestDecodeCredentialAcceptsFlatCamelCase(t *testing.T) {
	payload := []byte(`{"accessToken":"at","refreshToken":"rt","expiresAt":4102444800,` +
		`"domain":"workbuddy.ai","uid":"u1","enterpriseId":"e1","nickname":"n"}`)
	got, err := DecodeCredential(payload)
	if err != nil {
		t.Fatalf("flat camelCase payload rejected: %v", err)
	}
	if got.AccessToken != "at" || got.UID != "u1" || got.EnterpriseID != "e1" {
		t.Fatalf("decoded = %+v", got)
	}
	if !got.IsGlobal() {
		t.Fatal("workbuddy.ai must be recognised as the global realm")
	}
}
