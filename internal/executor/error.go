package executor

import (
	"context"
	"errors"
	"strings"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/providers"
)

// ExecutionError 是 executor 最终分类后的结果。HTTP 层负责
// 格式化它；它们不得重新运行 Classify 或自行发明 failover/cooldown 策略。
type ExecutionError struct {
	Classified Classified
	Err        error
}

func (e *ExecutionError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	if e.Classified.Message != "" {
		return e.Classified.Message
	}
	return e.Classified.Code
}

func (e *ExecutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func NewExecutionError(classified Classified, err error) *ExecutionError {
	if classified.RetryAfter <= 0 {
		classified.RetryAfter = classified.Cooldown
	}
	return &ExecutionError{Classified: classified, Err: err}
}

// ClassifyUpstreamBody 对上游 HTTP/SSE 错误响应体做一次分类。
func ClassifyUpstreamBody(status int, body string) error {
	return NewExecutionError(Classify(status, body, "", ""), nil)
}

// StreamIncompleteError 表示流在响应体中途结束、未收到 [DONE]。
func StreamIncompleteError() error {
	return newUnavailableStreamError("upstream_stream_incomplete", "stream ended before [DONE]", 502, nil)
}

// StreamReadError 包装流在响应体中途的读取失败。
func StreamReadError(err error) error {
	if err == nil {
		return StreamIncompleteError()
	}
	var execErr *ExecutionError
	if errors.As(err, &execErr) && execErr != nil {
		return err
	}
	var providerErr *providers.Error
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &providerErr) && providerErr != nil) {
		return NewExecutionError(ClassifyError(err), err)
	}
	return newUnavailableStreamError("upstream_stream_interrupted", "stream read error: "+err.Error(), 502, err)
}

func newUnavailableStreamError(code, message string, status int, cause error) error {
	classified := Classify(status, message, "", accounts.KindUnavailable)
	classified.Code = code
	classified.Message = message
	classified.Status = status
	classified.Type = "api_error"
	return NewExecutionError(classified, cause)
}

// ClassifyError 是执行过程与 HTTP 上报共用的唯一适配器错误策略。
func ClassifyError(err error) Classified {
	if err == nil {
		return Classify(0, "", "", accounts.KindUnavailable)
	}
	var execErr *ExecutionError
	if errors.As(err, &execErr) && execErr != nil {
		return execErr.Classified
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return Classified{
			Kind: accounts.KindCanceled, Status: 499, Failover: false,
			Code: "request_canceled", Message: err.Error(),
		}
	}
	var providerErr *providers.Error
	if !errors.As(err, &providerErr) || providerErr == nil {
		return Classify(0, err.Error(), "", "")
	}
	message := strings.TrimSpace(providerErr.Message)
	if message == "" {
		message = providerErr.Error()
	}
	classified := Classify(providerErr.Status, message, "", providerErr.Kind)
	if providerErr.Code != "" {
		classified.Code = providerErr.Code
	}
	if providerErr.Type != "" {
		classified.Type = providerErr.Type
	}
	if providerErr.Message != "" {
		classified.Message = providerErr.Message
	}
	if classified.Kind == accounts.KindRateLimit {
		switch {
		case providerErr.Code == "4011" && providerErr.RetryAfter <= 0:
			classified.Cooldown = 5 * time.Minute
		case providerErr.RetryAfter > 0:
			classified.Cooldown = providerErr.RetryAfter
			if classified.Cooldown < 30*time.Second {
				classified.Cooldown = 30 * time.Second
			}
		}
	}
	classified.RetryAfter = classified.Cooldown
	return classified
}
