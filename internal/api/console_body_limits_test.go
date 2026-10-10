package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent2api/internal/config"
)

// S2 回归：控制台三条读入型路径（单条导入 / 批量导入 / admin action）
// 必须与其它端点一样受 http.MaxBytesReader 约束，超限请求在进入
// 控制层之前就被拒绝，而不是把整个载荷读进内存。
func TestConsoleImportBodyLimits(t *testing.T) {
	srv := New(config.Config{
		Host:        "127.0.0.1",
		Port:        3010,
		ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home:    t.TempDir(),
		DataDir: t.TempDir(),
	})
	defer srv.Close()

	post := func(path, body string) *httptest.ResponseRecorder {
		req := loopbackRequest(http.MethodPost, path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	// 单条导入：上限 1MiB。
	oversized := `{"format":"trae-oauth-v1","name":"B","enabled":false,"user_blob":"` +
		strings.Repeat("A", 2<<20) + `","machine_id":"m"}`
	if rec := post("/api/accounts/import", oversized); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "too large") {
		t.Fatalf("超限单条导入必须被体限拒绝：%d %s", rec.Code, rec.Body.String())
	}

	// 批量导入：上限 8MiB。
	oversizedBatch := `{"items":[{"format":"trae-oauth-v1","name":"B","enabled":false,"user_blob":"` +
		strings.Repeat("A", 9<<20) + `","machine_id":"m"}]}`
	if rec := post("/api/accounts/import/batch", oversizedBatch); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "too large") {
		t.Fatalf("超限批量导入必须被体限拒绝：%d %s", rec.Code, rec.Body.String())
	}

	// 正常尺寸的批量导入不受影响：走逐项报告路径。
	small := `{"items":[{"format":"trae-oauth-v1","name":"B","enabled":false,"user_blob":"Y2lwaGVy","machine_id":"m"}]}`
	rec := post("/api/accounts/import/batch", small)
	if rec.Code != http.StatusOK {
		t.Fatalf("正常尺寸批量导入必须照常处理：%d %s", rec.Code, rec.Body.String())
	}
}
