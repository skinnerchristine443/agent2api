package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestVerifierAcceptsConfiguredAPIKeyHeaders(t *testing.T) {
	verifier := NewVerifier("secret", "secret")
	for _, header := range []struct {
		name  string
		value string
	}{
		{"Authorization", "Bearer secret"},
		{"x-api-key", "secret"},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
		req.Header.Set(header.name, header.value)
		identity, ok := verifier.Authenticate(context.Background(), req)
		if !ok || !identity.Console() {
			t.Fatalf("header %s was rejected", header.name)
		}
	}
}

func TestVerifierRejectsInvalidKeyAndAllowsDisabledGate(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	if _, ok := NewVerifier("secret", "secret").Authenticate(context.Background(), req); ok {
		t.Fatal("invalid key was accepted")
	}
	if identity, ok := NewVerifier("", "").Authenticate(context.Background(), req); !ok || identity.Kind != KindNone {
		t.Fatal("disabled gate rejected request")
	}
}

func TestVerifierCopiesShareRotatedConsoleKey(t *testing.T) {
	// 两个密钥初始相同。轮换 console 密钥必须对每个 verifier 副本可见，
	// 且必须让（相同的）proxy key 继续可用——它是同一值在不同角色下的体现。
	original := NewVerifier("old-key", "old-key")
	copied := original
	original.SetConsoleKey("  new-key  ")
	for _, v := range []Verifier{original, copied} {
		for _, tc := range []struct {
			key  string
			want string
		}{{"new-key", KindConsole}, {"old-key", KindProxy}} {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("Authorization", "Bearer "+tc.key)
			identity, ok := v.Authenticate(req.Context(), req)
			if !ok || identity.Kind != tc.want {
				t.Fatalf("key %q: ok=%v kind=%q want %q", tc.key, ok, identity.Kind, tc.want)
			}
		}
	}
}

func TestVerifierConcurrentRotationAndAuthentication(t *testing.T) {
	original := NewVerifier("first-key", "first-key")
	copied := original
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			original.SetConsoleKey(fmt.Sprintf("key-%d", i))
		}
	}()
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				req := httptest.NewRequest("GET", "/", nil)
				req.Header.Set("Authorization", "Bearer "+copied.ConsoleKey())
				// 轮换可能发生在这两个操作之间；此处不断言是否接受。
				copied.Authenticate(req.Context(), req)
			}
		}()
	}
	wg.Wait()
	if copied.ConsoleKey() != "key-999" {
		t.Fatal("copy did not observe final key")
	}
}
