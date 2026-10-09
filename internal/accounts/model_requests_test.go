package accounts

import (
	"encoding/json"
	"strings"
	"testing"
)

// T10/T11（语义）：停用集合是 fail-open 的。缺失、为空或畸形的值都必须
// 得到空集，因此坏的 secret 绝不能悄悄把所有账号都移出模型路由。
func TestParseModelRequestsDisabledFailOpen(t *testing.T) {
	cases := []struct {
		name  string
		value string
		found bool
	}{
		{"missing", "", false},
		{"empty", "", true},
		{"blank", "   ", true},
		{"malformed", "{not json", true},
		{"wrong type", `"acc-1"`, true},
		{"null", "null", true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			set := ParseModelRequestsDisabled(test.value, test.found)
			if len(set) != 0 {
				t.Fatalf("value=%q found=%v produced a non-empty set: %v", test.value, test.found, set)
			}
			if !ModelRequestsEnabled(set, "acc-1") {
				t.Fatal("a fail-open set must report every account enabled")
			}
		})
	}

	set := ParseModelRequestsDisabled(`["acc-1"," acc-2 ",""]`, true)
	if len(set) != 2 {
		t.Fatalf("blank ids must be dropped: %v", set)
	}
	if ModelRequestsEnabled(set, "acc-1") || ModelRequestsEnabled(set, "acc-2") {
		t.Fatalf("listed accounts must report disabled: %v", set)
	}
	if !ModelRequestsEnabled(set, "acc-3") {
		t.Fatal("an unlisted account must stay enabled")
	}
}

// T11：EncodeModelRequestsDisabled 必须是稳定且已排序的 JSON 数组，
// 这样存储的 secret 仅在成员变化时才改变。
func TestEncodeModelRequestsDisabledIsStableAndSorted(t *testing.T) {
	a := EncodeModelRequestsDisabled(map[string]struct{}{"b": {}, "a": {}, "c": {}})
	b := EncodeModelRequestsDisabled(map[string]struct{}{"c": {}, "a": {}, "b": {}})
	if a != b || a != `["a","b","c"]` {
		t.Fatalf("encoding is not stable/sorted: %q vs %q", a, b)
	}
	if got := EncodeModelRequestsDisabled(nil); got != "[]" {
		t.Fatalf("empty set = %q, want []", got)
	}
}

// T11（控制台载荷形态）：GET /api/accounts 序列化的是 []AccountView，
// 因此该开关必须始终出现在 model_requests_enabled 键下。
func TestAccountViewJSONExposesModelRequestsEnabled(t *testing.T) {
	encoded, err := json.Marshal(AccountView{
		Account:              Account{ID: "acc-1", Name: "a"},
		ModelRequestsEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"model_requests_enabled":true`) {
		t.Fatalf("enabled view must expose the switch: %s", encoded)
	}

	encoded, err = json.Marshal(AccountView{
		Account:              Account{ID: "acc-1", Name: "a"},
		ModelRequestsEnabled: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"model_requests_enabled":false`) {
		t.Fatalf("disabled view must expose the switch explicitly: %s", encoded)
	}
}
