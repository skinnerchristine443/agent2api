package api

import (
	"io"
	"net/http"

	"agent2api/internal/executor"
	apigateway "agent2api/internal/gateway"
)

type streamRelayStats = apigateway.StreamRelayStats
type streamFlushWriter = apigateway.StreamFlushWriter

func relayOpenAIStream(w http.ResponseWriter, body io.Reader) (streamRelayStats, error) {
	return apigateway.RelayOpenAIStream(w, body)
}

func relayAnthropicStream(writer io.Writer, body io.Reader, requestID, model string) (streamRelayStats, error) {
	return apigateway.RelayAnthropicStream(writer, body, requestID, model)
}

// relayResponsesStream 是仅供测试的辅助函数：它刻意丢弃
// RelayResponsesStream 的 assistant 轮次返回值。若它将来成为
// 真正的流式路径，必须捕获该值并交给
// 续接缓存，否则流式续接会静默停止缓存。
func relayResponsesStream(writer io.Writer, body io.Reader, requestID, model string) (streamRelayStats, error) {
	stats, _, err := apigateway.RelayResponsesStream(writer, body, requestID, model, nil)
	return stats, err
}

func sseDeltaHasToken(line string) bool {
	return apigateway.SSEDeltaHasToken(line)
}

func isStreamClientDisconnect(err error) bool {
	return apigateway.IsStreamClientDisconnect(err)
}

func classifyAPIError(err error) executor.Classified {
	return apigateway.ClassifyAPIError(err)
}

func writeClassifiedErr(w http.ResponseWriter, err error) {
	apigateway.WriteClassifiedErr(w, err)
}

func buildChatUsage(res executor.ChatResult) map[string]any {
	return apigateway.BuildChatUsage(res)
}

func parseStreamUsageLine(line string) (streamRelayStats, bool) {
	return apigateway.ParseStreamUsageLine(line)
}

type streamRelayWriteError = apigateway.StreamRelayWriteError
