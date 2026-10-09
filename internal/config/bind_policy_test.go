package config

import (
	"strings"
	"testing"
)

func TestEnsureBindAuthPolicy(t *testing.T) {
	cases := []struct {
		name        string
		host        string
		authEnabled bool
		wantErr     bool
	}{
		{"empty binds every interface", "", false, true},
		{"ipv4 loopback", "127.0.0.1", false, false},
		{"ipv4 loopback range", "127.0.0.5", false, false},
		{"ipv6 loopback", "::1", false, false},
		{"ipv6 loopback brackets", "[::1]", false, false},
		{"localhost", "localhost", false, false},
		{"localhost mixed case", "LocalHost", false, false},
		{"wildcard ipv4", "0.0.0.0", false, true},
		{"wildcard ipv6", "::", false, true},
		{"private class a", "10.0.0.5", false, true},
		{"private class c", "192.168.1.9", false, true},
		{"unresolvable hostname", "example.com", false, true},
		{"wildcard with auth", "0.0.0.0", true, false},
		{"private with auth", "10.0.0.5", true, false},
		{"hostname with auth", "example.com", true, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := EnsureBindAuthPolicy(test.host, test.authEnabled)
			if test.wantErr {
				if err == nil {
					t.Fatalf("host=%q auth=%v: expected an error", test.host, test.authEnabled)
				}
				if !strings.Contains(err.Error(), test.host) {
					t.Fatalf("error %q must name the host %q", err, test.host)
				}
				return
			}
			if err != nil {
				t.Fatalf("host=%q auth=%v: unexpected error %v", test.host, test.authEnabled, err)
			}
		})
	}
}

// T12/T13：绑定守卫的边界反例。只有该函数能证明是 loopback 的值才能通过；
// host:port 形式与非 loopback 字面量在无鉴权时被拒绝，空 host 也被拒绝，
// 因为 ":port" 会绑定所有网卡（该守卫不得与自身的 fail-closed 主张相矛盾）。
func TestEnsureBindAuthPolicyBoundaries(t *testing.T) {
	for _, host := range []string{"[::1]", "LOCALHOST", "127.0.0.1", "127.1.2.3"} {
		if err := EnsureBindAuthPolicy(host, false); err != nil {
			t.Fatalf("host %q must be allowed without auth: %v", host, err)
		}
	}

	for _, host := range []string{"", "0.0.0.0", "::", "10.0.0.5", "192.168.1.9", "example.com", "127.0.0.1:8080", "[::1]:8080", "LOCALHOST:8080"} {
		err := EnsureBindAuthPolicy(host, false)
		if err == nil {
			t.Fatalf("host %q must be refused without auth", host)
		}
		if !strings.Contains(err.Error(), host) {
			t.Fatalf("error %q must name host %q", err, host)
		}
		if err := EnsureBindAuthPolicy(host, true); err != nil {
			t.Fatalf("host %q must be allowed once auth is enabled: %v", host, err)
		}
	}
}
