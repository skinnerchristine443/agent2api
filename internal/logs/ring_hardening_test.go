package logs

import (
	"strings"
	"testing"
)

// ring 限制条目 COUNT 但不限制条目 SIZE；一个过大的上游错误页
// 不能占满全部额度。
func TestRingClampsOversizedLine(t *testing.T) {
	r := NewRing(10)
	entry := r.Append(strings.Repeat("x", maxMessageBytes*3))
	if !strings.HasSuffix(entry.Message, "[truncated]") {
		t.Fatalf("expected a truncation marker, got %d bytes", len(entry.Message))
	}
	if len(entry.Message) > maxMessageBytes+len("…[truncated]") {
		t.Fatalf("message not clamped: %d bytes", len(entry.Message))
	}
}

// 裸 JWT 不含 "Bearer " 前缀，因此带前缀的规则会漏掉它。
func TestRingRedactsBareJWT(t *testing.T) {
	r := NewRing(10)
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	entry := r.Append("upstream rejected: " + jwt)
	if strings.Contains(entry.Message, jwt) {
		t.Fatalf("bare JWT leaked: %q", entry.Message)
	}
	if !strings.Contains(entry.Message, "***") {
		t.Fatalf("expected a redaction marker: %q", entry.Message)
	}
}
