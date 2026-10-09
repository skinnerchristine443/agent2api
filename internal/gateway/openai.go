package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"agent2api/internal/accounts"
	"agent2api/internal/executor"
	"agent2api/internal/translate"
)

// maxChatRequestBytes 限制入站的 /v1/chat/completions 或 /v1/messages
// 请求体大小。对话请求携带完整消息历史，因此该值保持宽松
// （64 MiB，与 translate.MaxNativeRequestBytes 相同）——它的存在是为了阻止
// 异常请求体占住不受限的内存，而非限制 prompt。
const maxChatRequestBytes = 64 << 20

func (h *Handler) HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	var req translate.ChatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxChatRequestBytes)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if len(req.Messages) == 0 {
		writeErr(w, http.StatusBadRequest, "invalid_request", "messages required")
		return
	}
	if err := translate.ValidateChatRequest(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	execution, err := h.PrepareChatExecution(r, req)
	if err != nil {
		writeChatHTTPError(w, err)
		return
	}
	req = execution.Request
	publicModel := execution.PublicModel
	prefer := execution.Prefer
	providerFilter := execution.ProviderFilter
	requestID := execution.RequestID
	started := execution.Started
	ctx := execution.Context
	w.Header().Set("X-Request-Id", requestID)

	if req.Stream {
		upstream, err := h.Executor.ChatStreamProxy(ctx, req, prefer, providerFilter)
		if err != nil {
			h.finishRequestLog(requestID, started, req, publicModel, upstream.AccountID, firstNonEmpty(upstream.Provider, providerFilter), upstream.Routing, accounts.RequestStatusError, upstream.TTFBMs, nil, err, upstream.AttemptCount, upstream.ReasoningLevel)
			WriteClassifiedErr(w, err)
			return
		}
		defer upstream.Response.Body.Close()
		h.finishRequestLog(requestID, started, req, publicModel, upstream.AccountID, firstNonEmpty(upstream.Provider, providerFilter), upstream.Routing, accounts.RequestStatusStreaming, upstream.TTFBMs, nil, nil, upstream.AttemptCount, upstream.ReasoningLevel)
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		if upstream.AccountID != "" {
			w.Header().Set("X-Agent2API-Account", upstream.AccountID)
		}
		if provider := firstNonEmpty(upstream.Provider, providerFilter); provider != "" {
			w.Header().Set("X-Agent2API-Provider", provider)
		}
		w.Header().Set("X-Accel-Buffering", "no")
		// 压住状态，直到上游真正开始作答：见
		// stream_head.go。除非显式启用，否则关闭。
		var head *streamHead
		if streamHeadGateEnabled() {
			var headErr error
			head, headErr = readStreamHead(ctx, upstream.Response.Body, streamHeadTimeout())
			if headErr != nil {
				h.finishRequestLog(requestID, started, req, publicModel, upstream.AccountID, firstNonEmpty(upstream.Provider, providerFilter), upstream.Routing, accounts.RequestStatusError, upstream.TTFBMs, nil, headErr, upstream.AttemptCount, upstream.ReasoningLevel)
				writeErr(w, streamHeadGateStatus(headErr), "upstream_error", headErr.Error())
				return
			}
		}
		setReasoningDowngradeHeader(w, req, upstream.ReasoningLevel)
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		var body io.Reader = upstream.Response.Body
		if head != nil {
			body = io.MultiReader(bytes.NewReader(head.buffered), body)
		}
		var relay http.ResponseWriter = w
		if streamHeartbeatEnabled() {
			heartbeat := newStreamHeartbeat(w, streamHeartbeatInterval())
			heartbeat.start()
			defer heartbeat.stop()
			relay = heartbeat
		}
		stats, relayErr := RelayOpenAIStream(relay, body)
		status := streamLogStatus(relayErr, stats.SawDone, r.Context().Err() != nil)
		h.recordStreamDiagnostic(requestID, upstream.Response, started, stats, relayErr, r.Context().Err())
		ttfb := upstream.TTFBMs
		if stats.FirstTokenAt != nil {
			ttfb = int(stats.FirstTokenAt.Sub(started).Milliseconds())
			if ttfb < 1 {
				ttfb = 1
			}
		}
		logErr := relayErr
		switch status {
		case accounts.RequestStatusCanceled:
			logErr = context.Canceled
		case accounts.RequestStatusOK:
			logErr = nil
		}
		h.finishRequestLog(requestID, started, req, publicModel, upstream.AccountID, firstNonEmpty(upstream.Provider, providerFilter), upstream.Routing, status, ttfb, &stats, logErr, upstream.AttemptCount, upstream.ReasoningLevel)
		if relayErr == nil {
			h.Executor.CommitSession(ctx, req, upstream.Routing, upstream.AccountID)
		}
		if relayErr != nil {
			// 上游已返回 200 却在流内失败，因此 executor 的重试循环
			// 从未感知到它。把归类后的状态回馈给 pool，
			// 使下一次请求能够绕开配额耗尽的账号。
			// 使用 req.Model（已由 resolveProviderFilter 去掉前缀），
			// 使冷却 key 与 PickRoute 使用的 key 一致；
			// publicModel 可能仍带渠道前缀，会写入
			// 路由永远不会命中的冷却。
			if r.Context().Err() == nil && !errors.Is(relayErr, context.Canceled) && !errors.Is(relayErr, context.DeadlineExceeded) && !IsStreamClientDisconnect(relayErr) {
				h.Executor.ObserveStreamFailure(upstream.AccountID, relayErr, req.Model)
			}
			panic(http.ErrAbortHandler)
		}
		return
	}

	res, err := h.Executor.ChatNonStream(ctx, req, prefer, providerFilter)
	if err != nil {
		h.finishRequestLog(requestID, started, req, publicModel, res.AccountID, firstNonEmpty(res.Provider, providerFilter), res.Routing, accounts.RequestStatusError, 0, nil, err, res.AttemptCount, res.ReasoningLevel)
		WriteClassifiedErr(w, err)
		return
	}
	if publicModel != "" {
		res.Model = publicModel
	}
	h.finishRequestLog(requestID, started, req, publicModel, res.AccountID, firstNonEmpty(res.Provider, providerFilter), res.Routing, accounts.RequestStatusOK, 0, &StreamRelayStats{
		PromptTokens: ptrInt(res.PromptTokens), CompletionTokens: ptrInt(res.CompletionTokens),
		CacheReadTokens: res.CacheReadTokens, CacheWriteTokens: res.CacheWriteTokens,
		CachedTokens: res.CachedTokens, UsageSource: res.UsageSource, Credits: res.Credits,
		ConsumedCredits: res.ConsumedCredits, Model: res.Model,
	}, nil, res.AttemptCount, res.ReasoningLevel)
	message := map[string]any{
		"role":    "assistant",
		"content": res.Content,
	}
	if res.Reasoning != "" {
		message["reasoning_content"] = res.Reasoning
	}
	if len(res.ToolCalls) > 0 && string(res.ToolCalls) != "null" {
		message["tool_calls"] = json.RawMessage(res.ToolCalls)
		if res.Content == "" {
			message["content"] = nil
		}
	}
	finishReason := res.FinishReason
	if finishReason == "" {
		if len(res.ToolCalls) > 0 && string(res.ToolCalls) != "null" {
			finishReason = "tool_calls"
		} else {
			finishReason = "stop"
		}
	}
	if res.AccountID != "" {
		w.Header().Set("X-Agent2API-Account", res.AccountID)
	}
	if provider := firstNonEmpty(res.Provider, providerFilter); provider != "" {
		w.Header().Set("X-Agent2API-Provider", provider)
	}
	setReasoningDowngradeHeader(w, req, res.ReasoningLevel)
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      "chatcmpl-" + requestID,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   res.Model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       message,
			"finish_reason": finishReason,
		}},
		"usage": BuildChatUsage(res),
	})
}

func (h *Handler) finishRequestLog(requestID string, started time.Time, req translate.ChatRequest, publicModel, accountID, provider, routing, status string, ttfb int, stats *StreamRelayStats, err error, attemptCount int, resolvedReasoning string) {
	// 免费/付费学习（护栏 ③）：把实测的 usage.credit 回馈给
	// pool，使在某一区域被观测为免费的模型能赢得
	// 免费优先的倾斜。放在 recorder 判断之前，因此学习
	// 绝不依赖日志是否启用。
	h.learnModelCredit(accountID, req.Model, stats)
	// 每日护栏计费复用同一份实测用量。即便跳过积分判定，
	// 它也必须执行（积分未知同样会消耗 token）。
	h.noteDailyUsage(accountID, firstNonEmpty(publicModel, req.Model), status, stats)
	if h.Recorder == nil || requestID == "" {
		return
	}
	entry := accounts.RequestLog{
		ID:                 requestID,
		CreatedAt:          started,
		Stream:             req.Stream,
		Status:             status,
		RequestedModel:     firstNonEmpty(publicModel, req.Model),
		RequestedReasoning: executor.RequestedReasoningLevel(req),
		ResolvedReasoning:  resolvedReasoning,
		AccountID:          accountID,
		Provider:           provider,
		Routing:            routing,
		AttemptCount:       attemptCount,
	}
	if status != accounts.RequestStatusStarted && status != accounts.RequestStatusStreaming {
		finished := time.Now().UTC()
		latency := int(finished.Sub(started).Milliseconds())
		entry.FinishedAt = &finished
		entry.LatencyMs = &latency
	}
	if ttfb > 0 {
		entry.TTFBMs = &ttfb
	} else if !req.Stream && entry.LatencyMs != nil {
		entry.TTFBMs = entry.LatencyMs
	}
	if stats != nil {
		entry.PromptTokens = stats.PromptTokens
		entry.CompletionTokens = stats.CompletionTokens
		entry.CacheReadTokens = stats.CacheReadTokens
		entry.CacheWriteTokens = stats.CacheWriteTokens
		entry.UsageSource = stats.UsageSource
		consumed := stats.ConsumedCredits
		if consumed == nil {
			consumed = stats.Credits
		}
		entry.Credits = consumed
		if stats.Model != "" {
			entry.MappedModel = stats.Model
		}
	}
	if err != nil {
		classified := ClassifyAPIError(err)
		entry.ErrorKind = classified.Kind
		entry.ErrorCode = classified.Code
		entry.ErrorMessage = classified.Message
	}
	h.Recorder.Finish(entry)
	if stats != nil && entry.Credits != nil {
		h.Recorder.UsageDetail(accounts.RequestUsageDetail{
			RequestID: requestID,
			CreatedAt: started,
			Provider:  provider,
			Credit:    entry.Credits,
			Unit:      "credits",
		})
	}
}

// learnModelCredit 把一次 relay 的实测用量回馈给 pool，用于
// 免费/付费学习护栏。credits 是上游上报的单次调用消耗
// （优先 ConsumedCredits，其次 Credits）；totalTokens 是
// 使零积分样本可信的响应规模。
func (h *Handler) learnModelCredit(accountID, model string, stats *StreamRelayStats) {
	if h == nil || h.Pool == nil || stats == nil || accountID == "" {
		return
	}
	credits := stats.ConsumedCredits
	if credits == nil {
		credits = stats.Credits
	}
	if credits == nil {
		return
	}
	total := 0
	if stats.PromptTokens != nil {
		total += *stats.PromptTokens
	}
	if stats.CompletionTokens != nil {
		total += *stats.CompletionTokens
	}
	h.Pool.LearnModelCredit(accountID, model, credits, total)
}

// noteDailyUsage 把一次已完成请求的实测用量并入账号的
// 每日护栏计数器。被客户端中止的请求整体跳过：取消
// 不算一次已消耗的请求，且其 usage 块不完整
// （流在传输中途被切断），计入它会使当日
// 账目因中止发生的方向而失真。即便上游未上报积分，
// token 仍会计数；两者皆无的请求则完全不计。
func (h *Handler) noteDailyUsage(accountID, model, status string, stats *StreamRelayStats) {
	if h == nil || h.Pool == nil || accountID == "" {
		return
	}
	if status == accounts.RequestStatusCanceled {
		return
	}
	if stats == nil {
		return
	}
	var tokens int64
	if stats.PromptTokens != nil {
		tokens += int64(*stats.PromptTokens)
	}
	if stats.CompletionTokens != nil {
		tokens += int64(*stats.CompletionTokens)
	}
	var credits float64
	if stats.ConsumedCredits != nil {
		credits = *stats.ConsumedCredits
	} else if stats.Credits != nil {
		credits = *stats.Credits
	}
	if tokens <= 0 && credits <= 0 {
		return
	}
	h.Pool.NoteDailyUsage(accountID, model, tokens, credits)
}

func (h *Handler) recordStreamDiagnostic(requestID string, response *http.Response, started time.Time, stats StreamRelayStats, relayErr, contextErr error) {
	if h.Recorder == nil || requestID == "" {
		return
	}
	finished := time.Now().UTC()
	diagnostic := accounts.RequestStreamDiagnostic{
		RequestID:     requestID,
		CreatedAt:     started,
		FinishedAt:    &finished,
		SSEEventCount: stats.SSEEventCount,
		BytesRead:     stats.BytesRead,
		LastEvent:     stats.LastEvent,
		SawDone:       stats.SawDone,
	}
	if response != nil {
		status := response.StatusCode
		diagnostic.UpstreamStatus = &status
		if response.ContentLength >= 0 {
			contentLength := int(response.ContentLength)
			diagnostic.ContentLength = contentLength
		}
		diagnostic.UpstreamRequestID = firstNonEmpty(
			response.Header.Get("X-Request-ID"),
			response.Header.Get("X-Request-Id"),
			response.Header.Get("X-Upstream-Request-ID"),
		)
	}
	if contextErr != nil {
		diagnostic.ContextErr = contextErr.Error()
	}
	if relayErr != nil {
		diagnostic.RelayError = relayErr.Error()
	}
	switch {
	case IsStreamClientDisconnect(relayErr):
		diagnostic.CancellationSource = "client_disconnect"
	case contextErr != nil:
		diagnostic.CancellationSource = "request_context_canceled"
	case errors.Is(relayErr, context.DeadlineExceeded):
		diagnostic.CancellationSource = "request_timeout"
	case errors.Is(relayErr, context.Canceled):
		diagnostic.CancellationSource = "upstream_context_canceled"
	case relayErr != nil:
		diagnostic.CancellationSource = "upstream_stream_error"
	default:
		diagnostic.CancellationSource = "completed"
	}
	h.Recorder.StreamDiagnostic(diagnostic)
}

func ptrInt(value int) *int { return &value }
