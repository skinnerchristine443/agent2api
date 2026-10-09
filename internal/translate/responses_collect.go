package translate

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// MaxCollectedResponseBytes 限制由上游 SSE 组装出的非流式原生响应的大小。
// 终态 response 对象会重复每一个 output item，
// 因此这个上限覆盖整条流，而非单个事件。
const MaxCollectedResponseBytes = 64 << 20

// ErrResponsesIncomplete 表示上游流在任何终态事件之前就结束了。
// 此时还没有中继任何内容，因此调用方可以将其视为可重试。
var ErrResponsesIncomplete = errors.New("upstream responses stream ended before a terminal event")

// ResponsesStreamFailure 是一个终态的 response.failed / error 事件。
// Body 是供调用方错误分类器使用的事件载荷。
type ResponsesStreamFailure struct {
	Body string
}

func (e *ResponsesStreamFailure) Error() string {
	return "upstream responses stream failed: " + truncateForError(e.Body, 300)
}

// CollectedResponse 是由 SSE 组装出的非流式 Responses 结果。
type CollectedResponse struct {
	// Response 是终态事件的 response 对象，逐字保留：每个
	// output item 都保留其 id、type 和成员。
	Response     json.RawMessage
	FinishReason string
	InputTokens  *int
	OutputTokens *int
	CachedTokens *int
}

// CollectResponses 读取上游 Responses SSE 正文直到其终态事件，
// 并返回完整的 response 对象。它是网关原生中继的非流式孪生版本，
// 与后者共用 ParseResponsesEvent。
func CollectResponses(body io.Reader) (CollectedResponse, error) {
	reader := bufio.NewReaderSize(io.LimitReader(body, MaxCollectedResponseBytes+1), 64<<10)
	var total int
	var data []string
	finish := func() (CollectedResponse, bool, error) {
		if len(data) == 0 {
			return CollectedResponse{}, false, nil
		}
		payload := strings.Join(data, "\n")
		data = data[:0]
		event, ok := ParseResponsesEvent(payload)
		if !ok || !event.Terminal {
			return CollectedResponse{}, false, nil
		}
		if event.FinishReason == "error" {
			return CollectedResponse{}, true, &ResponsesStreamFailure{Body: payload}
		}
		if len(event.Response) == 0 || event.Response[0] != '{' {
			return CollectedResponse{}, true, fmt.Errorf("upstream %s carried no response object", event.Type)
		}
		return CollectedResponse{
			Response: event.Response, FinishReason: event.FinishReason,
			InputTokens: event.InputTokens, OutputTokens: event.OutputTokens, CachedTokens: event.CachedTokens,
		}, true, nil
	}
	for {
		line, err := reader.ReadString('\n')
		total += len(line)
		if total > MaxCollectedResponseBytes {
			return CollectedResponse{}, fmt.Errorf("upstream responses stream exceeds %d bytes", MaxCollectedResponseBytes)
		}
		trimmed := strings.TrimRight(line, "\r\n")
		switch {
		case trimmed == "" && line != "":
			if result, done, ferr := finish(); done {
				return result, ferr
			}
		case strings.HasPrefix(trimmed, "data:"):
			value := strings.TrimPrefix(trimmed, "data:")
			data = append(data, strings.TrimPrefix(value, " "))
		}
		if err == io.EOF {
			if result, done, ferr := finish(); done {
				return result, ferr
			}
			return CollectedResponse{}, ErrResponsesIncomplete
		}
		if err != nil {
			return CollectedResponse{}, err
		}
	}
}

func truncateForError(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
