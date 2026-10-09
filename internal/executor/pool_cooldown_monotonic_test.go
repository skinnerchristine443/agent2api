package executor

import (
	"testing"
	"time"
)

// 较晚、较温和的信号绝不能比较早、较严厉的信号
// 更早释放账号——否则退避阶梯会被噪声静默重置。
func TestCooldownNeverShortensAnEffectiveWindow(t *testing.T) {
	p := stubPool("a")

	p.MarkClassified("a", Classified{Kind: KindRateLimit, Cooldown: 2 * time.Hour, Failover: true, Model: "glm-5.3"})
	first, _ := p.ByID("a")
	modelWindow := first.ModelDownUntil["glm-5.3"]

	p.MarkClassified("a", Classified{Kind: KindRateLimit, Cooldown: time.Minute, Failover: true, Model: "glm-5.3"})
	second, _ := p.ByID("a")
	if got := second.ModelDownUntil["glm-5.3"]; !got.Equal(modelWindow) {
		t.Fatalf("model window shortened: %v -> %v", modelWindow, got)
	}

	p.MarkClassified("a", Classified{Kind: KindUnavailable, Cooldown: 2 * time.Hour, Failover: true})
	third, _ := p.ByID("a")
	accountWindow := third.DownUntil

	p.MarkClassified("a", Classified{Kind: KindUnavailable, Cooldown: time.Minute, Failover: true})
	fourth, _ := p.ByID("a")
	if fourth.DownUntil.Before(accountWindow) {
		t.Fatalf("account window shortened: %v -> %v", accountWindow, fourth.DownUntil)
	}
}
