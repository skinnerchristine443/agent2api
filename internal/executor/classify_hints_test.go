package executor

import (
	"strings"
	"testing"
)

// 不带业务信封的 403 是边缘/WAF 拦截，而非账号问题：
// 重新登录无济于事，且同一账号稍后可能可用。
func TestClassifyBareForbiddenIsTransientNotAuth(t *testing.T) {
	got := Classify(403, "<html><body>403 Forbidden</body></html>", "", "")
	if got.Kind == KindAuth {
		t.Fatalf("a bare 403 must not be classified as auth: %+v", got)
	}
	if got.Kind != KindUnavailable {
		t.Fatalf("Kind = %q, want %q", got.Kind, KindUnavailable)
	}
	if !got.Failover {
		t.Fatal("a bare 403 should still fail over")
	}
	// 确实携带业务信封的 403 仍归为 auth。
	authed := Classify(403, `{"error":{"message":"token revoked","code":"unauthorized","type":"auth_error"}}`, "", "")
	if authed.Kind != KindAuth {
		t.Fatalf("an enveloped 403 must stay auth, got %q", authed.Kind)
	}
}

func TestClassifyEmptyStreamIsTransient(t *testing.T) {
	got := Classify(200, `{"error":{"message":"empty_stream","code":"empty_stream"}}`, "", "")
	if got.Kind != KindUnavailable {
		t.Fatalf("Kind = %q, want %q", got.Kind, KindUnavailable)
	}
	if !got.Failover {
		t.Fatal("an empty stream should fail over to another account")
	}
	if !strings.Contains(got.Message, "stream") {
		t.Fatalf("message should explain the empty stream, got %q", got.Message)
	}
}

// plan gate 是账号级的：故障转移而非冷却此账号。
func TestClassifyPlanGateFailsOverWithoutCooldown(t *testing.T) {
	got := Classify(200, `{"error":{"message":"plan_gate: model requires a higher plan","code":"112"}}`, "", "")
	if got.Kind != KindModelNotAvailable {
		t.Fatalf("Kind = %q, want %q", got.Kind, KindModelNotAvailable)
	}
	if got.Cooldown != 0 {
		t.Fatalf("a plan gate must not cool the account down, got %v", got.Cooldown)
	}
	if !got.Failover {
		t.Fatal("a plan gate should fail over")
	}
}

// HTML 错误页不携带信息；客户端应得到一个提示。
func TestClassifyFoldsGatewayNoiseIntoAHint(t *testing.T) {
	page := "<html><head><title>504 Gateway Time-out</title></head><body>" + strings.Repeat("x", 400) + "</body></html>"
	got := Classify(504, page, "", "")
	if got.Message == page {
		t.Fatal("the raw gateway page must not be echoed back verbatim")
	}
	if !strings.Contains(got.Message, "unavailable") {
		t.Fatalf("expected an actionable hint, got %q", got.Message)
	}
}

// 简短、有意义的上游消息就是最佳答案，必须保留。
func TestClassifyKeepsMeaningfulUpstreamMessages(t *testing.T) {
	got := Classify(429, `{"error":{"message":"slow down","code":"rate_limited"}}`, "30", "")
	if got.Message != "slow down" {
		t.Fatalf("Message = %q, want the upstream text", got.Message)
	}
}
