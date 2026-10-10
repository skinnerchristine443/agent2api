package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"agent2api/internal/config"
)

// T1 回归：五个此前零覆盖的控制台 handler 走完整链路（api → server →
// console → control → store）的端到端冒烟。更新器的网络路径不在此测
// （由 internal/update 的单元测试以脚本化 Agent 覆盖），这里只钉住
// handler 层的解析、校验与响应形状。
func TestConsoleHandlerSmokeCoverage(t *testing.T) {
	srv := New(config.Config{
		Host:        "127.0.0.1",
		Port:        3010,
		ProxyAPIKey: "secret", ConsoleKey: "secret",
		Home:    t.TempDir(),
		DataDir: t.TempDir(),
	})
	defer srv.Close()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		var reader io.Reader
		if body != "" {
			reader = bytes.NewBufferString(body)
		}
		req := loopbackRequest(method, path, reader)
		req.Header.Set("Authorization", "Bearer secret")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	t.Run("alerts", func(t *testing.T) {
		rec := call(http.MethodGet, "/api/alerts", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/alerts = %d %s", rec.Code, rec.Body.String())
		}
		var payload struct {
			Data  []json.RawMessage `json:"data"`
			Count int               `json:"count"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Count != 0 || len(payload.Data) != 0 {
			t.Fatalf("全新库不应有告警：%s", rec.Body.String())
		}
	})

	t.Run("resources", func(t *testing.T) {
		rec := call(http.MethodGet, "/api/system/resources", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/system/resources = %d %s", rec.Code, rec.Body.String())
		}
		if !bytes.Contains(rec.Body.Bytes(), []byte(`"server"`)) {
			t.Fatalf("载荷缺少 server 段：%s", rec.Body.String())
		}
	})

	t.Run("growth observations", func(t *testing.T) {
		importBody := `{"format":"trae-oauth-v1","name":"Obs","enabled":false,"credential":{"access_token":"AT","refresh_token":"RT","uid":"U","expires_at":4102444800}}`
		rec := call(http.MethodPost, "/api/accounts/import", importBody)
		if rec.Code != http.StatusCreated {
			t.Fatalf("import = %d %s", rec.Code, rec.Body.String())
		}
		var created struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		rec = call(http.MethodGet, "/api/accounts/"+created.ID+"/growth/observations", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("observations = %d %s", rec.Code, rec.Body.String())
		}
		var payload struct {
			Data []json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Data) != 0 {
			t.Fatalf("空日志应为空数组：%s", rec.Body.String())
		}
		rec = call(http.MethodPost, "/api/accounts/"+created.ID+"/growth/observations", "")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST observations = %d", rec.Code)
		}
	})

	t.Run("update rollback validation", func(t *testing.T) {
		rec := call(http.MethodPost, "/api/system/update/rollback", `{}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("缺 version 的 rollback = %d %s", rec.Code, rec.Body.String())
		}
		if !bytes.Contains(rec.Body.Bytes(), []byte("version is required")) {
			t.Fatalf("错误语义不对：%s", rec.Body.String())
		}
		rec = call(http.MethodGet, "/api/system/update/rollback", "")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET rollback = %d", rec.Code)
		}
	})
}
