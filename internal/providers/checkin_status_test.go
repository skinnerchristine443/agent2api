package providers

import "testing"

func TestCheckinStatusSettled(t *testing.T) {
	for _, status := range []string{"success", "already", " already "} {
		if !CheckinStatusSettled(status) {
			t.Fatalf("%q must count as settled for the day", status)
		}
	}
	for _, status := range []string{"skipped", "partial", "error", ""} {
		if CheckinStatusSettled(status) {
			t.Fatalf("%q must NOT count as settled", status)
		}
	}
}

// 封闭状态集：Valid 恰好接受适配器可以报告的结果；运行时在失败时构建的
// 合成 "error" 记录不是适配器结果，必须被拒绝。
func TestCheckinResultValidEnumeratesTheClosedSet(t *testing.T) {
	for _, status := range []CheckinStatus{
		CheckinStatusSuccess, CheckinStatusAlready, CheckinStatusSkipped, CheckinStatusPartial,
	} {
		if !(CheckinResult{Status: status}).Valid() {
			t.Fatalf("%q must be valid", status)
		}
	}
	for _, status := range []CheckinStatus{"error", "", "Success"} {
		if (CheckinResult{Status: status}).Valid() {
			t.Fatalf("%q must be invalid", status)
		}
	}
}
