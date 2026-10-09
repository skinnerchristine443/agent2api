package logs

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Ring.Write 是 log 包与 ring 的适配面：按行拆分、逐行入 ring；nil ring
// 必须吞掉输入并如实报告消费长度（供 io.MultiWriter 使用）。
func TestRingWriteSplitsLines(t *testing.T) {
	ring := NewRing(5)
	input := "alpha\r\n\nbeta\ngamma"
	n, err := ring.Write([]byte(input))
	if err != nil || n != len(input) {
		t.Fatalf("Write = (%d, %v)，期望 (%d, nil)", n, err, len(input))
	}
	entries := ring.Latest(10)
	if len(entries) != 3 {
		t.Fatalf("len=%d entries=%+v", len(entries), entries)
	}
	if !strings.Contains(entries[0].Message, "gamma") || !strings.Contains(entries[2].Message, "alpha") {
		t.Fatalf("行序不符（应最新在前）：%+v", entries)
	}

	var nilRing *Ring
	if n, err := nilRing.Write([]byte("x")); err != nil || n != 1 {
		t.Fatalf("nil ring 应报告消费长度：(%d, %v)", n, err)
	}
}

// clampMessage 的守点：短输入原样；超长输入只截断到上限并附标记；
// 合法 UTF-8 的截断不得切坏 rune（历史 bug 面：按字节切会切碎多字节字符）。
func FuzzClampMessage(f *testing.F) {
	f.Add("short")
	f.Add(strings.Repeat("a", maxMessageBytes+10))
	f.Add(strings.Repeat("中", maxMessageBytes))
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		out := clampMessage(s)
		if len(s) <= maxMessageBytes {
			if out != s {
				t.Fatalf("短输入必须原样返回（len=%d）", len(s))
			}
			return
		}
		if len(out) > maxMessageBytes+len("…[truncated]") {
			t.Fatalf("输出超界：%d", len(out))
		}
		if !strings.HasSuffix(out, "…[truncated]") {
			t.Fatal("超长输入必须带截断标记")
		}
		if utf8.ValidString(s) && !utf8.ValidString(out) {
			t.Fatal("截断切坏了合法的 UTF-8 序列")
		}
	})
}
