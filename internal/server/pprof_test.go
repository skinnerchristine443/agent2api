package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"agent2api/internal/auth"
)

// TestPprofOffByDefault：标准部署（EnablePprof false）不得暴露
// 任何 /debug/pprof 路由。
func TestPprofOffByDefault(t *testing.T) {
	s := New(Server{Auth: auth.NewVerifier("console-secret", "console-secret"), EnablePprof: false})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, consoleRequest("/debug/pprof/", "127.0.0.1:1234", ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("pprof off: status = %d, want 404", rec.Code)
	}
}

// pprof 共用 console 闸门，因此既需 key 又受来源限制：
// 即便是正确的 key，公网来源也必须被拒绝。
func TestPprofRefusesNonLoopbackSource(t *testing.T) {
	s := New(Server{Auth: auth.NewVerifier("console-secret", "console-secret"), EnablePprof: true})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, consoleRequest("/debug/pprof/", "203.0.113.7:5555", "console-secret"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("pprof from public source: status = %d, want 403", rec.Code)
	}
}

// TestPprofRequiresConsoleKey：启用后，pprof 必须拒绝缺失的 key
// （401）和非 console key（由 API-key 闸门返回 401），并放行
// console key（200）。
func TestPprofRequiresConsoleKey(t *testing.T) {
	s := New(Server{Auth: auth.NewVerifier("console-secret", "console-secret"), EnablePprof: true})
	h := s.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, consoleRequest("/debug/pprof/", "127.0.0.1:1234", ""))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("pprof without key: status = %d, want 401", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, consoleRequest("/debug/pprof/", "127.0.0.1:1234", "wrong-secret"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("pprof with bad key: status = %d, want 401", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, consoleRequest("/debug/pprof/goroutine", "127.0.0.1:1234", "console-secret"))
	if rec.Code != http.StatusOK {
		t.Fatalf("pprof with console key: status = %d, want 200", rec.Code)
	}
}
