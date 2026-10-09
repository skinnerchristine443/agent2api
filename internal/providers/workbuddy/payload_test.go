package workbuddy

import (
	"encoding/json"
	"testing"
)

// OSS 缺口回归：真实第三方客户端（dsh、Claude 系 agent）会发送一些
// WorkBuddy 上游以 11-128 / 11148 拒绝的形态。

// dsh 0.1.7+ 把每个 tool result 作为它自己的 role:"tool" 消息发送（而不是一个
// content block）。并行的一轮必须逐字节保留。
func TestPrepareBodyKeepsDshStyleParallelToolResults(t *testing.T) {
	out := PrepareBody([]byte(`{"model":"m","messages":[
		{"role":"user","content":"q"},
		{"role":"assistant","content":"","tool_calls":[
			{"id":"call_a","type":"function","function":{"name":"t","arguments":"{}"}},
			{"id":"call_b","type":"function","function":{"name":"t","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"call_a","content":"ra"},
		{"role":"tool","tool_call_id":"call_b","content":"rb"},
		{"role":"user","content":"go on"}
	]}`))
	var body map[string]any
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatal(err)
	}
	roles := messageRoles(body)
	want := []string{"system", "user", "assistant", "tool", "tool", "user"}
	if len(roles) != len(want) {
		t.Fatalf("roles=%v want=%v", roles, want)
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("roles=%v want=%v", roles, want)
		}
	}
}

// 一个在两轮 assistant 间复用同一个 tool_call id 的客户端会让上游拒绝整个请求
// （code 11148）。后一轮必须被重新分配键，同时保持两次交互完整且 id 全局唯一。
func TestPrepareBodyRekeysDuplicateToolCallIDAcrossRounds(t *testing.T) {
	out := PrepareBody([]byte(`{"model":"m","messages":[
		{"role":"user","content":"q"},
		{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"t","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"call_1","content":"r1"},
		{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"t","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"call_1","content":"r2"}
	]}`))
	var body map[string]any
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatal(err)
	}
	roles := messageRoles(body)
	want := []string{"system", "user", "assistant", "tool", "assistant", "tool"}
	if len(roles) != len(want) {
		t.Fatalf("both rounds must be kept: roles=%v", roles)
	}

	callIDs := make([]string, 0, 2)
	resultIDs := make([]string, 0, 2)
	seen := map[string]bool{}
	for _, item := range body["messages"].([]any) {
		message, _ := item.(map[string]any)
		switch messageRole(message) {
		case "assistant":
			for _, raw := range message["tool_calls"].([]any) {
				call, _ := raw.(map[string]any)
				id := toolCallID(call)
				callIDs = append(callIDs, id)
				if seen[id] {
					t.Fatalf("tool_call id reused: %v", callIDs)
				}
				seen[id] = true
			}
		case "tool":
			resultIDs = append(resultIDs, toolResultID(message))
		}
	}
	if len(callIDs) != 2 || len(resultIDs) != 2 {
		t.Fatalf("calls=%v results=%v", callIDs, resultIDs)
	}
	// 每个 result 仍须匹配它自己那一轮（重新分配键后）的 call。
	for i := range callIDs {
		if callIDs[i] != resultIDs[i] {
			t.Fatalf("result %d does not match its call: calls=%v results=%v", i, callIDs, resultIDs)
		}
	}
	if callIDs[1] == "call_1" {
		t.Fatalf("duplicate id not re-keyed: %v", callIDs)
	}
}

// hub#56：tool_choice:"none" 不得剥离 tools 数组，否则模型会失去工具感知并退化
// 成文本循环。只丢弃 choice。
func TestPrepareBodyKeepsToolsWhenToolChoiceNone(t *testing.T) {
	out := PrepareBody([]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],
		"tools":[{"type":"function","function":{"name":"t","parameters":{"type":"object"}}}],
		"tool_choice":"none"}`))
	var body map[string]any
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["tool_choice"]; ok {
		t.Fatalf("tool_choice must be dropped: %v", body["tool_choice"])
	}
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools must survive tool_choice:none: %v", body["tools"])
	}
}
