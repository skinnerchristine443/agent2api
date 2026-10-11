package accounts

import "testing"

// NormalizeActivityReportTime 归一 HH:MM：修剪空白、拒绝非定宽与非法时刻。
func TestNormalizeActivityReportTime(t *testing.T) {
	if got, err := NormalizeActivityReportTime(" 07:30 "); err != nil || got != "07:30" {
		t.Fatalf("normalize = %q err=%v", got, err)
	}
	if _, err := NormalizeActivityReportTime("25:00"); err == nil {
		t.Fatal("25:00 应被拒绝")
	}
	if _, err := NormalizeActivityReportTime("7:30"); err == nil {
		t.Fatal("非定宽应被拒绝")
	}
	if _, err := NormalizeActivityReportTime(""); err == nil {
		t.Fatal("空值应被拒绝")
	}
}

// errActivity 的 Error() 返回其字面消息（错误串可读）。
func TestErrActivityReportTimeMessage(t *testing.T) {
	var err error = errActivityReportTime
	if got := err.Error(); got != "activity_report_time must use HH:mm" {
		t.Fatalf("Error() = %q", got)
	}
}

// ActivityReportEnabled 只认 "1"；缺省（空）与其它取值一律视为关闭。
// 这是「默认关」契约的底层保证——写错值不会意外开启上报。
func TestActivityReportEnabled(t *testing.T) {
	if !ActivityReportEnabled("1") {
		t.Fatal("1 应为开启")
	}
	for _, value := range []string{"", "0", "true", "yes", "on", " 1 "} {
		// 注意：" 1 " 带空格：TrimSpace 后为 "1"，应视为开启——单独断言。
		if value == " 1 " {
			if !ActivityReportEnabled(value) {
				t.Fatalf("%q 修剪后应为开启", value)
			}
			continue
		}
		if ActivityReportEnabled(value) {
			t.Fatalf("%q 应为关闭", value)
		}
	}
}

// 缺省常量值必须稳定（排程与设置层都依赖它；改动会影响「何时跑」）。
func TestActivityReportDefaults(t *testing.T) {
	if DefaultActivityReportTime != "09:00" {
		t.Fatalf("DefaultActivityReportTime = %q, want 09:00", DefaultActivityReportTime)
	}
	if ActivityReportEnabledSecret != "activity_report_enabled" {
		t.Fatalf("ActivityReportEnabledSecret = %q", ActivityReportEnabledSecret)
	}
	if ActivityReportTimeSecret != "activity_report_time" {
		t.Fatalf("ActivityReportTimeSecret = %q", ActivityReportTimeSecret)
	}
}
