package providers

import "testing"

// Intl realm 必须能解析，否则每个携带它的账号都会在进程管理器的
// 区域查找中失败。
func TestWorkBuddyIntlRegionResolves(t *testing.T) {
	descriptor, region, err := Resolve("workbuddy", "intl")
	if err != nil {
		t.Fatalf("Resolve(workbuddy, intl): %v", err)
	}
	if descriptor.ID != "workbuddy" || region.ID != "intl" {
		t.Fatalf("resolved %q/%q", descriptor.ID, region.ID)
	}
	if region.DefaultDomain != "codebuddy.ai" {
		t.Fatalf("DefaultDomain = %q, want codebuddy.ai", region.DefaultDomain)
	}
}

// Intl 没有实现任何签到契约，因此运行时必须报告其为 unsupported，
// 而不是用 Intl token 调用 CN 端点。
func TestWorkBuddyIntlHasNoCheckinPolicy(t *testing.T) {
	if _, supported := CheckinFor("workbuddy", "intl"); supported {
		t.Fatal("Intl must not advertise a check-in policy")
	}
	if _, supported := CheckinFor("workbuddy", "cn"); !supported {
		t.Fatal("CN must keep its check-in policy")
	}
	if _, supported := CheckinFor("workbuddy", "global"); !supported {
		t.Fatal("Global must keep its check-in policy")
	}
}
