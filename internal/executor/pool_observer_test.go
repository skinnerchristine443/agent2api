package executor

import (
	"testing"
	"time"
)

func TestPoolObserverIsDataOnly(t *testing.T) {
	var seen Item
	pool := NewPool()
	pool.SetObserver(func(item Item) { seen = item })
	ready := true
	pool.Upsert(Item{ID: "acc_1", Ready: &ready})
	pool.MarkOK("acc_1", "glm-5")
	if seen.ID != "acc_1" {
		t.Fatalf("observer item = %+v", seen)
	}
}

// F1 无变化短路：健康账号上的重复成功不得推进状态版本，也不得再通知
// observer——否则每次成功都会产生一次内容相同的异步落盘写事务
// （单连接 SQLite 写放大）。首次记录「已证明」模型与真正的冷却清除
// 仍必须照旧通知。
func TestMarkOKIdleSuccessDoesNotNotify(t *testing.T) {
	pool := NewPool()
	ready := true
	notifies := 0
	pool.SetObserver(func(Item) { notifies++ })
	pool.Upsert(Item{ID: "a", Ready: &ready})

	// 首次成功：记录已证明模型是一次真实变化，必须通知。
	pool.MarkOK("a", "glm-5")
	if notifies != 1 {
		t.Fatalf("首次成功必须通知一次，got %d", notifies)
	}
	before, _ := pool.ByID("a")

	// 无变化的重复成功：不得通知、不得推进状态版本。
	pool.MarkOK("a", "glm-5")
	after, _ := pool.ByID("a")
	if notifies != 1 {
		t.Fatalf("无变化的成功不得再通知，got %d", notifies)
	}
	if after.StateVersion != before.StateVersion {
		t.Fatalf("无变化的成功不得推进状态版本：%d → %d", before.StateVersion, after.StateVersion)
	}

	// 真正清除冷却：必须照旧通知并推进版本。
	pool.MarkClassified("a", Classified{Kind: KindRateLimit, Cooldown: time.Minute, Failover: true, Model: "glm-5"})
	markNotifies := notifies
	pool.MarkOK("a", "glm-5")
	if notifies != markNotifies+1 {
		t.Fatalf("清除冷却的成功必须通知，got %d", notifies)
	}
	item, _ := pool.ByID("a")
	if _, cooling := item.ModelDownUntil["glm-5"]; cooling {
		t.Fatalf("冷却必须被清除：%+v", item.ModelDownUntil)
	}
	if item.StateVersion == after.StateVersion {
		t.Fatal("清除冷却必须推进状态版本")
	}
}
