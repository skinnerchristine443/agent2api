package trae

import (
	"net/http"
	"strings"
)

func clientUA() string { return UserAgent }

func SetOAuthHeaders(header http.Header) {
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/json")
	header.Set("User-Agent", clientUA())
}

func SetUgHeaders(header http.Header, credential Credential) {
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/json")
	header.Set("User-Agent", clientUA())
	if credential.AccessToken != "" {
		header.Set("Authorization", "Cloud-IDE-JWT "+credential.AccessToken)
	}
	header.Set("X-User-Region", "CN")
	if deviceID := ugDeviceID(credential.DeviceID); deviceID != "" {
		header.Set("X-Device-Id", deviceID)
	}
}

// ugDeviceID 为 UG 端点规范化存储的 device id。签到后端把其按设备的
// 每日限额挂在 X-Device-Id 上，并对形态很挑剔：裸十六进制 id 会以 9074
// 被拒绝，因此十六进制 id 会加上 IDE 客户端所用的 aha- 前缀；但*纯数字*
// id（当前登录所签发的形态）必须以裸形式发送——"aha-<digits>" 本身
// 也会以 9074 被拒绝。
func ugDeviceID(deviceID string) string {
	trimmed := strings.TrimSpace(deviceID)
	if trimmed == "" || strings.HasPrefix(trimmed, "aha-") {
		return trimmed
	}
	if isAllDigits(trimmed) {
		return trimmed
	}
	return "aha-" + trimmed
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func SetSOLOHeaders(header http.Header, credential Credential, stream bool) {
	header.Set("Content-Type", "application/json")
	if stream {
		header.Set("Accept", "text/event-stream")
	} else {
		header.Set("Accept", "application/json")
	}
	header.Set("User-Agent", clientUA())
	if credential.AccessToken != "" {
		header.Set("Authorization", "Cloud-IDE-JWT "+credential.AccessToken)
		header.Set("X-Cloudide-Token", credential.AccessToken)
		header.Set("X-Ide-Token", credential.AccessToken)
	}
	if credential.UID != "" {
		header.Set("X-Uid", credential.UID)
	}
	header.Set("X-App-Id", AppID)
	header.Set("X-App-Version", "default")
	header.Set("X-Ide-Version", IDEVersion)
	header.Set("X-Ide-Version-Code", IDEVersionCode)
	header.Set("X-App-Version-Code", IDEVersionCode)
	header.Set("X-Ide-Version-Type", "stable")
	header.Set("X-Device-Type", "windows")
	header.Set("X-OS-Version", OSVersion)
	header.Set("X-Device-Brand", DeviceBrand)
	header.Set("Request-Traffic-Type", "prod")
	if credential.MachineID != "" {
		header.Set("X-Machine-Id", credential.MachineID)
	}
	if credential.DeviceID != "" {
		header.Set("X-Device-Id", credential.DeviceID)
	}
}

func SetChatHeaders(header http.Header, credential Credential) {
	SetSOLOHeaders(header, credential, true)
}

func SetCatalogHeaders(header http.Header, credential Credential) {
	SetSOLOHeaders(header, credential, false)
}
