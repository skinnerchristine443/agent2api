package translate

import "testing"

// conversation 和 prompt 指名的是此处任何路径都无法兑现的服务端状态，
// 因此必须在前置检查就拒绝，而不是在下游被静默丢弃。
// previous_response_id 有意不在其中：它通过进程本地的
// 续接缓存获得支持。
func TestParseNativeResponsesRejectsStatefulFields(t *testing.T) {
	base := `{"model":"m","input":"hi"`
	for _, extra := range []string{
		`,"conversation":"c1"`,
		`,"prompt":{"id":"p1"}`,
		`,"background":true`,
	} {
		if _, err := ParseNativeResponses([]byte(base + extra + "}")); err == nil {
			t.Fatalf("expected rejection for %s", extra)
		}
	}
}

// previous_response_id 不得再在解析层被拒绝。
func TestParseNativeResponsesAcceptsPreviousResponseID(t *testing.T) {
	body := []byte(`{"model":"m","input":"hi","previous_response_id":"resp_1"}`)
	if _, err := ParseNativeResponses(body); err != nil {
		t.Fatalf("previous_response_id must be accepted: %v", err)
	}
}

// background:false 是文档化的默认值，必须保持被接受。
func TestParseNativeResponsesAcceptsBackgroundFalse(t *testing.T) {
	body := []byte(`{"model":"m","input":"hi","background":false}`)
	if _, err := ParseNativeResponses(body); err != nil {
		t.Fatalf("background:false must be accepted: %v", err)
	}
}
