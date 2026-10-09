package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"agent2api/internal/config"
)

func TestCheckinWindowSettingsRoute(t *testing.T) {
	server := New(config.Config{Host: "127.0.0.1", ProxyAPIKey: "test-key", ConsoleKey: "test-key", Home: t.TempDir(), DataDir: t.TempDir()})
	defer server.Close()
	request := func(method, body string) *httptest.ResponseRecorder {
		input := loopbackRequest(method, "/api/system/settings", bytes.NewBufferString(body))
		input.Header.Set("Authorization", "Bearer test-key")
		output := httptest.NewRecorder()
		server.Handler().ServeHTTP(output, input)
		return output
	}

	// 全新安装会为每个签到 provider 暴露一个双窗口。
	response := request(http.MethodGet, "")
	if response.Code != http.StatusOK ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"checkin_windows"`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"main_start":"09:00"`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"fallback_start":"21:00"`)) {
		t.Fatalf("default settings: %d %s", response.Code, response.Body.String())
	}

	body := `{"checkin_windows":{"workbuddy":{"main_start":"07:00","main_end":"08:00","fallback_start":"08:00","fallback_end":"09:00"}}}`
	response = request(http.MethodPatch, body)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"fallback_end":"09:00"`)) {
		t.Fatalf("window patch: %d %s", response.Code, response.Body.String())
	}
	// 遗留的单点视图会跟随主开始时间，以兼容旧客户端。
	if !bytes.Contains(response.Body.Bytes(), []byte(`"workbuddy":"07:00"`)) {
		t.Fatalf("legacy mirror missing: %s", response.Body.String())
	}

	for _, invalid := range []string{
		`{"checkin_windows":{"workbuddy":{"main_start":"10:00","main_end":"11:00","fallback_start":"10:30","fallback_end":"11:30"}}}`,
		`{"checkin_windows":{"trae":{"main_start":"23:30","main_end":"00:30","fallback_start":"21:00","fallback_end":"22:00"}}}`,
		`{"checkin_windows":{"trae":{"main_start":"25:00","main_end":"26:00","fallback_start":"21:00","fallback_end":"22:00"}}}`,
	} {
		if response := request(http.MethodPatch, invalid); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid window accepted (%s): %d %s", invalid, response.Code, response.Body.String())
		}
	}
}
