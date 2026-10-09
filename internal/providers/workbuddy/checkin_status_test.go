package workbuddy

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func saveCNCredential(t *testing.T, store *memStore, accountID string) {
	t.Helper()
	payload, err := json.Marshal(Credential{
		AccessToken: "at", RefreshToken: "rt", ExpiresAt: 4102444800, Domain: DomainCN, UID: "u1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCredentialPayload(context.Background(), accountID, CredentialFormat, payload); err != nil {
		t.Fatal(err)
	}
}

// 已领取的一天必须只需一次读取，而非一次盲写，且结构化计数器必须进入记录。
func TestCheckinCNSkipsClaimWhenAlreadyCheckedIn(t *testing.T) {
	checkinCalls := 0
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, pathCheckinStatus):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"active": true, "today_checked_in": true, "today_credit": 5, "streak_days": 9, "total_credits": 321,
			}})
		case strings.HasSuffix(r.URL.Path, pathDailyCheckin):
			checkinCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "签到成功"})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	store.region = "cn"
	saveCNCredential(t, store, "acc1")

	result, err := client.Checkin(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "already" {
		t.Fatalf("status=%q, want already", result.Status)
	}
	if checkinCalls != 0 {
		t.Fatalf("the claim must be skipped when already checked in, calls=%d", checkinCalls)
	}
	if !strings.Contains(result.Message, "连续 9 天") || !strings.Contains(result.Message, "累计 321 积分") {
		t.Fatalf("message must carry the counters: %q", result.Message)
	}
}

// 已关闭的活动报告为 skip，而不是去驱动一次注定失败的领取。
func TestCheckinCNSkipsWhenActivityInactive(t *testing.T) {
	checkinCalls := 0
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, pathCheckinStatus):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"active": false, "activity_name": "每日签到", "today_checked_in": false,
			}})
		case strings.HasSuffix(r.URL.Path, pathDailyCheckin):
			checkinCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "签到成功"})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	store.region = "cn"
	saveCNCredential(t, store, "acc1")

	result, err := client.Checkin(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "skipped" {
		t.Fatalf("status=%q, want skipped", result.Status)
	}
	if checkinCalls != 0 {
		t.Fatalf("an inactive activity must not be claimed, calls=%d", checkinCalls)
	}
	if !strings.Contains(result.Message, "每日签到") {
		t.Fatalf("message should keep the activity name: %q", result.Message)
	}
}

// 状态读取是建议性的：当它失败时，领取必须与以往完全相同地继续，
// 因此状态服务故障绝不会让我们损失一天。
func TestCheckinCNClaimsWhenStatusReadFails(t *testing.T) {
	checkinCalls := 0
	client, store := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, pathCheckinStatus):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":500,"msg":"boom"}`))
		case strings.HasSuffix(r.URL.Path, pathDailyCheckin):
			checkinCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "签到成功"})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	store.region = "cn"
	saveCNCredential(t, store, "acc1")

	result, err := client.Checkin(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Fatalf("status=%q, want success", result.Status)
	}
	if checkinCalls != 1 {
		t.Fatalf("the claim must still run, calls=%d", checkinCalls)
	}
}

// 状态载荷以宽松类型和两种形态到达；两者都必须能解码，
// 且缺失字段必须保持未知而不是默认为零。
func TestParseCheckinStatusToleratesLooseTypes(t *testing.T) {
	top, err := parseCheckinStatus([]byte(`{"active":1,"today_checked_in":1,"streak_days":"12","total_credits":"1.5","today_credit":"3"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !top.TodayCheckedIn || top.Active == nil || !*top.Active {
		t.Fatalf("loose booleans must decode: %+v", top)
	}
	if !top.HasStreakDays || top.StreakDays != 12 {
		t.Fatalf("numeric string streak must decode: %+v", top)
	}
	if !top.HasTotal || top.TotalCredits != 1.5 || !top.HasTodayCredit || top.TodayCredit != 3 {
		t.Fatalf("numeric string credits must decode: %+v", top)
	}

	wrapped, err := parseCheckinStatus([]byte(`{"code":0,"msg":"","data":{"today_checked_in":false,"total_credits":2049}}`))
	if err != nil {
		t.Fatal(err)
	}
	if wrapped.TodayCheckedIn || wrapped.HasStreakDays || wrapped.Active != nil {
		t.Fatalf("absent fields must stay unknown: %+v", wrapped)
	}
	if !wrapped.HasTotal || wrapped.TotalCredits != 2049 {
		t.Fatalf("wrapped payload must decode: %+v", wrapped)
	}
}
