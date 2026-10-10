package trae

import (
	"os"
	"strings"
)

// 签到设备身份。
//
// UG 签到后端把按设备的限额挂在 X-Device-Id 上，并对取值挑剔：数值 >= 2^52
// 的号一律被拒（9074）。存量凭据里可能带着历史生成器产出的超界号，因此签到
// 前统一做一次确定性归一（normalizeDeviceID），使旧账号无需数据迁移即可通过
// 设备检查。
//
// 另有一个默认关闭的开关：开启后每次签到流程铸造一个全新数字 id。它会改变
// 上游所见内容，而且设备号反复变化本身就是风控信号，所以要刻意启用。

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

// checkinCredential 返回用于单次签到流程的凭据。开关关闭时复用已存号
// （超界的就地归一，其余逐字节不变）；开关打开时为该流程铸造一个全新
// 的合法数字 id。
func checkinCredential(credential Credential) Credential {
	if !checkinDeviceEnabled() {
		credential.DeviceID = normalizeDeviceID(credential.DeviceID)
		return credential
	}
	credential.DeviceID = randomNumericDeviceID()
	return credential
}
