package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent2api/internal/app"
	"agent2api/internal/executor"
	"agent2api/internal/providers"
)

// T8（HTTP）：当所有账号的模型请求都被关闭时，
// 请求必须以真实的 HTTP 503 和 model_requests_disabled
// code 作答——不是 502，不是 200，且不触碰上游。
func TestResponsesAllModelRequestsDisabledReturns503(t *testing.T) {
	pool := executor.NewPool()
	pool.Upsert(executor.Item{
		ID: "account-a", Provider: "workbuddy", Region: "cn", Runtime: string(providers.RuntimeInProcess),
		ModelRequestsDisabled: true,
	})
	registry := providers.NewRegistry()
	registry.Register(providers.Adapter{ID: "workbuddy", Chat: &compatWorkerChat{worker: func(http.ResponseWriter, *http.Request) {
		t.Fatal("the upstream must not be reached when every account is switched off")
	}}})
	chatExecutor := executor.NewChatExecutor(pool)
	chatExecutor.Providers = registry
	server := &Server{App: &app.App{Executor: chatExecutor, Pool: pool}}

	recorder := httptest.NewRecorder()
	server.handleResponses(recorder, loopbackRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"workbuddy/glm-5.2","input":"hi"}`)))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"code":"model_requests_disabled"`) {
		t.Fatalf("body must carry the model_requests_disabled code: %s", body)
	}
	if !strings.Contains(body, `"type":"api_error"`) {
		t.Fatalf("body must be an api_error: %s", body)
	}
}
