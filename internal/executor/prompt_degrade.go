package executor

// Prompt 策略降级门控。
//
// 有些上游拒绝是指纹误报，而非真实的内容问题：
// 缺失 system prompt 这一类以及部分内容
// 筛查，会在一套更钝的策略本可完全避开的 prompt 形态上触发。
// 当观察到这类拒绝时，executor 会切换到它最
// 中性的 prompt 策略——即使是通常会保留 system prompt 的
// 账号也丢弃调用方的 system prompt——直到下一个本地午夜，使
// 同一指纹无法整日消耗尝试次数。
//
// 该切换是刻意且有界的：每日重置（届时客户端可能已
// 改变），激活期间从不延长，且可通过
// AGENT2API_WORKBUDDY_DEGRADE_GATE=0 禁用。
//
// 结构化的 tool 序列拒绝被刻意排除在这一
// 类别之外：丢弃 prompt 无法修复格式错误的 tool 往返。

import (
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"agent2api/internal/accounts"
)

const promptDegradeEnv = "AGENT2API_WORKBUDDY_DEGRADE_GATE"

// promptFingerprintCode 是上游自身的反探测/缺失 system 错误
// 编号；在请求级拒绝中看到它即是典型触发条件。
const promptFingerprintCode = "11128"

// promptDegradeEnabled 报告该门控是否可触发。默认开启；只有
// 显式的否定值才会禁用它。
func promptDegradeEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(promptDegradeEnv))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

type promptDegradeGate struct {
	mu      sync.Mutex
	enabled bool
	until   time.Time
}

func newPromptDegradeGate() *promptDegradeGate {
	return &promptDegradeGate{enabled: promptDegradeEnabled()}
}

// active 报告中性策略当前是否生效。
func (g *promptDegradeGate) active() bool {
	return g.activeAt(time.Now())
}

func (g *promptDegradeGate) activeAt(now time.Time) bool {
	if g == nil || !g.enabled {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return now.Before(g.until)
}

// trigger 将门控激活至下一个本地午夜。激活期间它从不
// 延长：复位始终绑定当天的首次触发，因此该
// 策略每日会被重新探测。
func (g *promptDegradeGate) trigger(now time.Time) {
	if g == nil || !g.enabled {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if now.Before(g.until) {
		return
	}
	g.until = now.Add(NextLocalMidnightCooldownAt(now))
	log.Printf("workbuddy prompt strategy degraded until %s after a prompt/content rejection", g.until.Format(time.RFC3339))
}

// promptDegradeTrigger 报告某个分类后的失败是否属于该
// 门控会响应的易误报类别：缺失 system prompt
// 这一类以及内容筛查拒绝。
func promptDegradeTrigger(c Classified) bool {
	if c.Kind != accounts.KindInvalidRequest {
		return false
	}
	searchable := strings.ToLower(c.Code + " " + c.Message)
	if strings.Contains(searchable, promptFingerprintCode) ||
		strings.Contains(searchable, "first message is not system") {
		return true
	}
	return accounts.IsInvalidRequestText(c.Message)
}

// observePromptDegrade 在某个 WorkBuddy 尝试因该类别的
// 拒绝而失败时，激活降级门控。
func (e ChatExecutor) observePromptDegrade(item Item, classified Classified) {
	if e.promptDegrade == nil || itemProvider(item) != "workbuddy" {
		return
	}
	if !promptDegradeTrigger(classified) {
		return
	}
	e.promptDegrade.trigger(time.Now())
}
