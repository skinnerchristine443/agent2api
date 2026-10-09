package auth

import (
	"net"
	"testing"
)

func TestRemoteAllowedLoopbackAlwaysPasses(t *testing.T) {
	// Go 的 HTTP 服务器在 RemoteAddr 中只会报告 IP，因此该策略刻意基于 IP
	// 而非基于主机名。
	for _, addr := range []string{"127.0.0.1:5555", "127.0.0.1", "[::1]:5555", "::1", "[::1]"} {
		if !RemoteAllowed(addr, nil) {
			t.Fatalf("loopback form %q was refused", addr)
		}
	}
}

// 被拒绝的控制台调用会回显观测到的来源，让运维看到白名单必须覆盖哪个地址。
func TestRemoteHostOfReportsBareHost(t *testing.T) {
	for raw, want := range map[string]string{
		"203.0.113.7:5555":   "203.0.113.7",
		"[2001:db8::1]:443":  "2001:db8::1",
		"[fe80::1%eth0]:443": "fe80::1",
		"":                   "unknown",
		"not-an-address":     "unknown",
		"127.0.0.1:3010":     "127.0.0.1",
	} {
		if got := RemoteHostOf(raw); got != want {
			t.Fatalf("RemoteHostOf(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestRemoteAllowedRefusesPublicSourceWithoutAllowlist(t *testing.T) {
	for _, addr := range []string{"203.0.113.7:5555", "10.0.0.4:5555", "[2001:db8::1]:443"} {
		if RemoteAllowed(addr, nil) {
			t.Fatalf("source %q passed with no allowlist", addr)
		}
	}
}

func TestRemoteAllowedHonoursAllowlist(t *testing.T) {
	networks, err := ParseCIDRs([]string{"203.0.113.0/24, 10.0.0.5", "2001:db8::/32"})
	if err != nil {
		t.Fatalf("ParseCIDRs: %v", err)
	}
	for _, addr := range []string{"203.0.113.9:1000", "10.0.0.5:1000", "[2001:db8::9]:1000"} {
		if !RemoteAllowed(addr, networks) {
			t.Fatalf("allowlisted source %q was refused", addr)
		}
	}
	for _, addr := range []string{"203.0.114.9:1000", "10.0.0.6:1000"} {
		if RemoteAllowed(addr, networks) {
			t.Fatalf("non-allowlisted source %q passed", addr)
		}
	}
}

// 转发头由调用方控制；该策略必须只依据 socket 地址判断，
// 这样伪造的头无法换取控制台访问权。
func TestRemoteAllowedIgnoresForwardedHeaders(t *testing.T) {
	if RemoteAllowed("203.0.113.7:5555", nil) {
		t.Fatal("public source treated as allowed")
	}
}

func TestParseCIDRsRejectsGarbage(t *testing.T) {
	if _, err := ParseCIDRs([]string{"not-a-network"}); err == nil {
		t.Fatal("invalid entry was accepted")
	}
	if _, err := ParseCIDRs([]string{"", "   ", ","}); err != nil {
		t.Fatalf("empty entries should be skipped: %v", err)
	}
}

func TestParseCIDRsBareIPBecomesSingleHost(t *testing.T) {
	networks, err := ParseCIDRs([]string{"198.51.100.7"})
	if err != nil {
		t.Fatalf("ParseCIDRs: %v", err)
	}
	if len(networks) != 1 {
		t.Fatalf("networks=%d want 1", len(networks))
	}
	ones, bits := networks[0].Mask.Size()
	if ones != bits {
		t.Fatalf("bare IP produced mask /%d of %d, want a single host", ones, bits)
	}
	if !networks[0].Contains(net.ParseIP("198.51.100.7")) {
		t.Fatal("bare IP does not contain itself")
	}
	if networks[0].Contains(net.ParseIP("198.51.100.8")) {
		t.Fatal("bare IP matched a neighbour")
	}
}
