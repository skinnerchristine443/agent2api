package runtime

import (
	"testing"

	"agent2api/internal/providers"
)

// 目录费率必须能通过目录暴露的每一种拼写访问到，且 provider 从未上报费率的模型
// 必须被完全省略，使调度器把它视为 "未知"（排在最后）而非免费。
func TestModelRateTableFansOutAliases(t *testing.T) {
	table := modelRateTable([]providers.ModelInfo{
		{PublicModel: "glm-5.2", NativeModel: "GLM-5.2-Native", DisplayName: "GLM-5.2", Rate: 0.5, RateKnown: true},
		{PublicModel: "no-rate", NativeModel: "no-rate"},
		{PublicModel: "free", NativeModel: "free", Rate: 0, RateKnown: true},
	})

	for _, alias := range []string{"glm-5.2", "GLM-5.2-Native"} {
		if got, ok := table[alias]; !ok || got != 0.5 {
			t.Fatalf("alias %q = %v ok=%v, want 0.5", alias, got, ok)
		}
	}
	if got, ok := table["free"]; !ok || got != 0 {
		t.Fatalf("known-free rate = %v ok=%v, want 0 present", got, ok)
	}
	if _, ok := table["no-rate"]; ok {
		t.Fatal("a model with no declared rate must not appear in the table")
	}
}
