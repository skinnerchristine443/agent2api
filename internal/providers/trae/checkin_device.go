package trae

import (
	"os"
	"strings"
)

// 签到设备身份。
//
// UG 签到后端把其按设备的限额挂在 X-Device-Id 头部上，并会记住它已拒绝的
// id：一旦被拒绝，复用该 id 就会持续失败，因此设备 id 被拒的已存凭据
// 永远无法恢复。因此一个全新的数字 id 属于同一个签到流程——在 status、
// claim 和 re-check 之间成对使用，使该流程内部保持一致，同时下一次运行
// 从干净状态开始。
//
// 默认关闭：它会改变上游所见内容，所以需刻意启用。
// 无论哪种情况，chat 面都继续使用已存储的 id。

// checkinDeviceEnv 启用每次签到流程使用全新的设备身份。
const checkinDeviceEnv = "AGENT2API_TRAE_FRESH_CHECKIN_DEVICE"

// checkinDeviceEnabled 报告每个流程是否铸造自己的 device id。
// 只有显式的肯定值才会将其打开。
func checkinDeviceEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(checkinDeviceEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// checkinCredential 返回用于单次签到流程的凭据。开关关闭时，
// 它就是逐字节相同的已存凭据。
func checkinCredential(credential Credential) Credential {
	if !checkinDeviceEnabled() {
		return credential
	}
	credential.DeviceID = randomNumericDeviceID()
	return credential
}
