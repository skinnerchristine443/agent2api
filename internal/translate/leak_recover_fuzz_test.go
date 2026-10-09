package translate

import (
	"encoding/json"
	"strings"
	"testing"
)

// 泄漏恢复的安全属性：没出现 DSML 的文本必须原样透传（fail-open 的
// 前提——这不是过滤器）；恢复出的调用必须是合法 JSON；任何输入不得 panic。
func FuzzExtractLeakedToolCalls(f *testing.F) {
	f.Add("plain text")
	f.Add(`before <|DSML|calls> <|DSML|invoke name="search"><|DSML|parameter name="q">x</|DSML|parameter></|DSML|invoke> </|DSML|calls> after`)
	f.Add("truncated <|DSML|invok")
	f.Add("")

	f.Fuzz(func(t *testing.T, text string) {
		calls, clean, handled := ExtractLeakedToolCalls(text)
		if !strings.Contains(text, "DSML") {
			if handled || calls != nil || clean != text {
				t.Fatalf("无 DSML 文本必须原样透传：handled=%v calls=%s clean=%q", handled, calls, clean)
			}
			return
		}
		if calls != nil && !json.Valid(calls) {
			t.Fatalf("恢复出的调用必须是合法 JSON：%s", calls)
		}
	})
}
