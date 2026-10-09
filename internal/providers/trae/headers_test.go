package trae

import (
	"net/http"
	"testing"
)

// UG 签到后端把其按设备的每日限额挂在 X-Device-Id 上，并对裸十六进制 id
// 返回 code 9074，因此每个已存储的 id 都必须以 Trae 自家客户端所用的
// aha-<hex> 形态发出。
func TestSetUgHeadersNormalizesDeviceID(t *testing.T) {
	cases := []struct {
		name     string
		deviceID string
		want     string
	}{
		{"bare hex gets the aha prefix", "3ee0250fe3e0e6e03cdd8054e3a01a7b", "aha-3ee0250fe3e0e6e03cdd8054e3a01a7b"},
		{"prefixed id is left alone", "aha-3ee0250fe3e0e6e03cdd8054e3a01a7b", "aha-3ee0250fe3e0e6e03cdd8054e3a01a7b"},
		{"whitespace is trimmed", "  cfac6e546e2386ec29e13335393d79dc \n", "aha-cfac6e546e2386ec29e13335393d79dc"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			header := http.Header{}
			SetUgHeaders(header, Credential{AccessToken: "at", DeviceID: testCase.deviceID})
			if got := header.Get("X-Device-Id"); got != testCase.want {
				t.Fatalf("X-Device-Id=%q want %q", got, testCase.want)
			}
			if got := header.Get("Authorization"); got != "Cloud-IDE-JWT at" {
				t.Fatalf("Authorization=%q", got)
			}
		})
	}

	header := http.Header{}
	SetUgHeaders(header, Credential{AccessToken: "at"})
	if got := header.Get("X-Device-Id"); got != "" {
		t.Fatalf("missing device id must not be sent: %q", got)
	}
}
