package translate

import (
	"io"
	"strings"
	"testing"
)

// T47① 硬化：快路径必须逐字节等价、上限必须 fail-open（放行而非吞掉）。
// 背景：上游是 Go encoding/json（HTML 转义），"<" 以 `\u003c` 出现——
// 快路径谓词必须同时覆盖裸字节与转义形式（既有泄漏用例已把只认裸字节
// 的版本当场抓红）。

func readHoldbackOutput(t *testing.T, input string) string {
	t.Helper()
	body := NewLeakHoldbackBody(io.NopCloser(strings.NewReader(input)))
	defer body.Close()
	out, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// 无标记（且无 "<" 及其转义）的常规流：输出必须与输入字节完全相同。
func TestFastPathKeepsPlainStreamByteIdentical(t *testing.T) {
	input := strings.Join([]string{
		`data: {"id":"c1","model":"m","choices":[{"delta":{"role":"assistant"}}]}`,
		`data: {"id":"c1","model":"m","choices":[{"delta":{"content":"普通文本，没有任何标记。"}}]}`,
		`data: {"id":"c1","model":"m","choices":[{"delta":{"content":"继续输出。"}}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`,
		`data: [DONE]`,
		``,
	}, "\n\n")
	if got := readHoldbackOutput(t, input); got != input {
		t.Fatalf("无标记流必须字节等价：\n got=%q\nwant=%q", got, input)
	}
}

// 含 "<"（裸字节或 \u003c 转义）但不是 DSML 的内容：只进暂存暂缓、
// 一个字符都不能丢，且绝不合成 tool_calls。
func TestAngleMarkupWithoutDSMLSurvivesLossless(t *testing.T) {
	for name, content := range map[string]string{
		"raw-angle":     `代码里的 a<b 与 c>d 讨论`,
		"escaped-angle": `标签形如 <tag> 的说明`,
	} {
		t.Run(name, func(t *testing.T) {
			payload := strings.ReplaceAll(content, `\`, `\\`)
			payload = strings.ReplaceAll(payload, `"`, `\"`)
			input := `data: {"id":"c1","model":"m","choices":[{"delta":{"content":"` + payload + `"}}]}` + "\n\n" +
				`data: [DONE]` + "\n\n"
			out := readHoldbackOutput(t, input)
			var delivered strings.Builder
			for _, block := range strings.Split(out, "\n\n") {
				if block == "" {
					continue
				}
				if parsed, ok := frameData(strings.Split(block, "\n")); ok {
					if delta, ok := parsed["choices"].([]any); ok && len(delta) > 0 {
						if choice, ok := delta[0].(map[string]any); ok {
							if d, ok := choice["delta"].(map[string]any); ok {
								if text, ok := d["content"].(string); ok {
									delivered.WriteString(text)
								}
								if _, synthesized := d["tool_calls"]; synthesized {
									t.Fatalf("非标记内容不得合成 tool_calls：%s", out)
								}
							}
						}
					}
				}
			}
			if delivered.String() != content {
				t.Fatalf("内容必须无损传递：got %q want %q", delivered.String(), content)
			}
		})
	}
}

// 文本上限：越限即 fail-open——标记文本与超限内容整体放行，
// 不吞内容、不合成调用。
func TestHoldingTextCapReleasesFailOpen(t *testing.T) {
	marker := `<｜DSML｜function_calls>`
	big := strings.Repeat("z", holdMaxText+1)
	input := `data: {"id":"c1","model":"m","choices":[{"delta":{"content":"` + marker + `"}}]}` + "\n\n" +
		`data: {"id":"c1","model":"m","choices":[{"delta":{"content":"` + big + `"}}]}` + "\n\n" +
		`data: {"id":"c1","model":"m","choices":[{"delta":{"content":"收尾"}}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	out := readHoldbackOutput(t, input)
	if !strings.Contains(out, "｜DSML｜function_calls") {
		t.Fatal("越过上限的扣留文本必须整体放行，而不是被吞掉")
	}
	if got := strings.Count(out, "z"); got != len(big) {
		t.Fatalf("超限内容必须一个字符不丢：z 计数 = %d，期望 %d", got, len(big))
	}
	for _, block := range strings.Split(out, "\n\n") {
		if block == "" {
			continue
		}
		if parsed, ok := frameData(strings.Split(block, "\n")); ok {
			if choices, ok := parsed["choices"].([]any); ok && len(choices) > 0 {
				if choice, ok := choices[0].(map[string]any); ok {
					if delta, ok := choice["delta"].(map[string]any); ok {
						if _, synthesized := delta["tool_calls"]; synthesized {
							t.Fatalf("fail-open 路径不得合成 tool_calls：%s", out)
						}
					}
				}
			}
		}
	}
}

// 帧数上限：非内容帧越过 holdMaxFrames 同样 fail-open，按到达顺序放行。
func TestHoldingFrameCapReleasesFailOpen(t *testing.T) {
	input := `data: {"id":"c1","model":"m","choices":[{"delta":{"content":"<｜DSML｜function_calls>"}}]}` + "\n\n"
	meta := `data: {"id":"c1","model":"m","choices":[{"delta":{"role":"assistant"}}]}`
	for i := 0; i < holdMaxFrames+1; i++ {
		input += meta + "\n\n"
	}
	input += `data: [DONE]` + "\n\n"
	out := readHoldbackOutput(t, input)
	if !strings.Contains(out, "｜DSML｜function_calls") {
		t.Fatal("标记文本必须被放行")
	}
	if got := strings.Count(out, `"role":"assistant"`); got != holdMaxFrames+1 {
		t.Fatalf("保留帧必须一帧不丢：计数 = %d，期望 %d", got, holdMaxFrames+1)
	}
}
