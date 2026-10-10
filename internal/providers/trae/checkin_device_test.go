package trae

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// storedCheckinDevice 是签到后端所接受的形态的数字 id。
const storedCheckinDevice = "1111111111111111"

func seedCheckinCredentialWithDevice(t *testing.T, store *memStore) {
	t.Helper()
	seedCheckinCredentialWithRawDevice(t, store, storedCheckinDevice)
}

// seedCheckinCredentialWithRawDevice 存储一个带指定设备号的签到凭据，
// 供「存量异常号归一」类用例构造现场。
func seedCheckinCredentialWithRawDevice(t *testing.T, store *memStore, deviceID string) {
	t.Helper()
	payload, err := json.Marshal(Credential{
		AccessToken: "at", RefreshToken: "rt", ExpiresAt: 4102444800,
		Domain: DomainCN, UID: "u1", DeviceID: deviceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCredentialPayload(context.Background(), "acc1", CredentialFormat, payload); err != nil {
		t.Fatal(err)
	}
}

func checkinStub(t *testing.T, deviceIDs *[]string) http.HandlerFunc {
	t.Helper()
	statusCalls := 0
	return func(w http.ResponseWriter, r *http.Request) {
		*deviceIDs = append(*deviceIDs, r.Header.Get("X-Device-Id"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case pathCheckinStatus:
			statusCalls++
			// 首次探测：尚未签到。领取后重新探测：已翻转。
			if statusCalls == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{"checked_in": false, "credits": 0, "enable": true})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{"checked_in": true, "credits": 200})
			}
		case pathCheckinClaim:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}
}

// 被上游拒绝过的 device id 会保持被拒，因此一次签到流程必须在
// status / claim / re-check 之间使用同一个全新 id，而不是复用已存储的那个。
func TestCheckinPairsOneFreshDevicePerFlow(t *testing.T) {
	t.Setenv(checkinDeviceEnv, "1")

	var deviceIDs []string
	client, store := newTestClient(t, checkinStub(t, &deviceIDs))
	seedCheckinCredentialWithDevice(t, store)

	result, err := client.Checkin(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Fatalf("result=%+v", result)
	}

	if len(deviceIDs) != 3 {
		t.Fatalf("device ids recorded = %v, want one per call (status/claim/re-check)", deviceIDs)
	}
	for i, id := range deviceIDs {
		if id == storedCheckinDevice {
			t.Fatalf("call %d reused the stored device id %q", i, id)
		}
		if id != deviceIDs[0] {
			t.Fatalf("call %d used %q but call 0 used %q: one flow must pair one id", i, id, deviceIDs[0])
		}
		if len(id) != 16 {
			t.Fatalf("device id %q is not the 16-digit shape the backend accepts", id)
		}
		for _, ch := range id {
			if ch < '0' || ch > '9' {
				t.Fatalf("device id %q is not numeric", id)
			}
		}
	}
}

// 默认关闭：已存储的 device id 被原样使用。
func TestCheckinReusesTheStoredDeviceByDefault(t *testing.T) {
	t.Setenv(checkinDeviceEnv, "")

	var deviceIDs []string
	client, store := newTestClient(t, checkinStub(t, &deviceIDs))
	seedCheckinCredentialWithDevice(t, store)

	if _, err := client.Checkin(context.Background(), "acc1"); err != nil {
		t.Fatal(err)
	}
	for i, id := range deviceIDs {
		if id != storedCheckinDevice {
			t.Fatalf("call %d sent %q, want the stored id %q while the switch is off", i, id, storedCheckinDevice)
		}
	}
}
