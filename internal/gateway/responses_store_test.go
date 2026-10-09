package gateway

import (
	"testing"

	"agent2api/internal/translate"
)

// 只有能被明确判定为 "enabled" 的值才会缓存；其他任何情况
// （包括意外的 JSON 类型）都视为 disabled。
func TestResponsesStoreDisabled(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"absent", `{"model":"m","input":"hi"}`, false},
		{"null", `{"model":"m","input":"hi","store":null}`, false},
		{"true", `{"model":"m","input":"hi","store":true}`, false},
		{"false", `{"model":"m","input":"hi","store":false}`, true},
		{"string false", `{"model":"m","input":"hi","store":"false"}`, true},
		{"string true", `{"model":"m","input":"hi","store":"true"}`, true},
		{"number", `{"model":"m","input":"hi","store":0}`, true},
		{"object", `{"model":"m","input":"hi","store":{}}`, true},
		{"array", `{"model":"m","input":"hi","store":[]}`, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			native, err := translate.ParseNativeResponses([]byte(test.body))
			if err != nil {
				t.Fatal(err)
			}
			if got := responsesStoreDisabled(native); got != test.want {
				t.Fatalf("responsesStoreDisabled(%s) = %v, want %v", test.body, got, test.want)
			}
		})
	}
}
