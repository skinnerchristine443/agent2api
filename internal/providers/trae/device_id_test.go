package trae

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
)

// 上游 UG 签到后端对 x-device-id 的取值挑剔：数值 >= 2^52 一律被拒（9074）。
// 本文件的护栏锁定「生成必定在范围内」与「存量超界号被确定性归一」两条。

// 两个取自生产库的真实号：前者（首位 3）一直正常，后者（首位 5）持续 9074。
const (
	deviceIDInRange   = "3900821446190185"
	deviceIDOutOfRang = "5070723998239450"
)

func deviceIDValue(t *testing.T, id string) uint64 {
	t.Helper()
	value, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		t.Fatalf("device id %q 不是纯数字: %v", id, err)
	}
	return value
}

// 生成的号必须恒小于上限：一旦有人把取模去掉，本测试会因超界样本失败。
func TestRandomNumericDeviceIDStaysWithinUpstreamLimit(t *testing.T) {
	for i := 0; i < 1000; i++ {
		id := randomNumericDeviceID()
		if len(id) != 16 {
			t.Fatalf("第 %d 个号 %q 不是 16 位（IDE 客户端形态）", i, id)
		}
		if !isAllDigits(id) {
			t.Fatalf("第 %d 个号 %q 不是纯数字", i, id)
		}
		if value := deviceIDValue(t, id); value >= deviceIDCeiling {
			t.Fatalf("第 %d 个号 %q 数值 %d >= 上限 %d，会被上游判 9074", i, id, value, deviceIDCeiling)
		}
	}
}

// 归一必须只碰超界的纯数字号，其余一律原样返回。
func TestNormalizeDeviceIDOnlyRepairsOutOfRangeNumbers(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantExact string // 非空 = 必须逐字节不变
	}{
		{name: "范围内 16 位原样", in: deviceIDInRange, wantExact: deviceIDInRange},
		{name: "前导零的范围内号原样", in: "0924807388410591", wantExact: "0924807388410591"},
		{name: "15 位恒在范围内", in: "599766458326841", wantExact: "599766458326841"},
		{name: "短号原样", in: "123", wantExact: "123"},
		{name: "非数字原样", in: "d1", wantExact: "d1"},
		{name: "aha 前缀原样", in: "aha-2187341293967291", wantExact: "aha-2187341293967291"},
		{name: "空值原样", in: "", wantExact: ""},
		{name: "超界 16 位（生产实际号）", in: deviceIDOutOfRang},
		{name: "超界满位", in: "9999999999999999"},
		{name: "超界且带前导零", in: "0005070723998239450"},
		{name: "超界且超过 16 位", in: "12345678901234567890"},
		// 分界之下但高于已证安全区：不在拒绝侧，一律不动（宁可保留可疑的旧值，
		// 也不去改动可能正在生效的号）。
		{name: "分界之下未验证区原样", in: "4400000000000000", wantExact: "4400000000000000"},
		// 分界两侧逐位钉住。
		{name: "分界前一位原样", in: "4503599627370495", wantExact: "4503599627370495"},
		{name: "分界本身即超界", in: "4503599627370496"},
	}
	for _, tc := range cases {
		got := normalizeDeviceID(tc.in)
		if tc.wantExact != "" || tc.in == "" {
			if got != tc.wantExact || (tc.in == "" && got != "") {
				t.Fatalf("%s：normalize(%q) = %q，期望原样", tc.name, tc.in, got)
			}
			continue
		}
		if got == tc.in {
			t.Fatalf("%s：normalize(%q) 未修正超界号", tc.name, tc.in)
		}
		if len(got) != 16 || !isAllDigits(got) {
			t.Fatalf("%s：修正后的 %q 形态不合法", tc.name, got)
		}
		if value := deviceIDValue(t, got); value >= deviceIDCeiling {
			t.Fatalf("%s：修正后的 %q 仍超界（%d）", tc.name, got, value)
		}
	}
}

// 同一输入恒得同一输出，且二次归一是幂等的——设备号反复变化本身是风控信号。
func TestNormalizeDeviceIDIsStableAndIdempotent(t *testing.T) {
	first := normalizeDeviceID(deviceIDOutOfRang)
	if again := normalizeDeviceID(deviceIDOutOfRang); again != first {
		t.Fatalf("同一输入两次归一结果不同: %q vs %q", first, again)
	}
	if thrice := normalizeDeviceID(first); thrice != first {
		t.Fatalf("归一无幂等: %q -> %q", first, thrice)
	}
}

// 解码是导入 / 登录 / 刷新 / 聊天 / 签到的共同入口，存量超界号在此被修正，
// 且修正结果可稳定往返（导出再导入不会退回坏号）。
func TestDecodeCredentialRepairsOutOfRangeStoredDeviceID(t *testing.T) {
	bad, err := json.Marshal(Credential{
		AccessToken: "at", RefreshToken: "rt", ExpiresAt: 4102444800,
		Domain: DomainCN, UID: "u1", DeviceID: deviceIDOutOfRang,
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCredential(bad)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.DeviceID == deviceIDOutOfRang {
		t.Fatalf("解码未修正超界号 %q", decoded.DeviceID)
	}
	if value := deviceIDValue(t, decoded.DeviceID); value >= deviceIDCeiling {
		t.Fatalf("解码修正后仍超界: %q", decoded.DeviceID)
	}

	roundTrip, err := decoded.Encode()
	if err != nil {
		t.Fatal(err)
	}
	again, err := DecodeCredential(roundTrip)
	if err != nil {
		t.Fatal(err)
	}
	if again.DeviceID != decoded.DeviceID {
		t.Fatalf("往返后设备号漂移: %q -> %q", decoded.DeviceID, again.DeviceID)
	}

	good, err := json.Marshal(Credential{
		AccessToken: "at", RefreshToken: "rt", ExpiresAt: 4102444800,
		Domain: DomainCN, UID: "u1", DeviceID: deviceIDInRange,
	})
	if err != nil {
		t.Fatal(err)
	}
	decodedGood, err := DecodeCredential(good)
	if err != nil {
		t.Fatal(err)
	}
	if decodedGood.DeviceID != deviceIDInRange {
		t.Fatalf("范围内号被改动: %q -> %q", deviceIDInRange, decodedGood.DeviceID)
	}
}

// 端到端：存量带超界号的账号签到，必须用归一后的号，且整条流程统一使用它。
func TestCheckinUsesRepairedDeviceIDForOutOfRangeStoredValue(t *testing.T) {
	t.Setenv(checkinDeviceEnv, "")

	var deviceIDs []string
	client, store := newTestClient(t, checkinStub(t, &deviceIDs))
	seedCheckinCredentialWithRawDevice(t, store, deviceIDOutOfRang)

	result, err := client.Checkin(context.Background(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Fatalf("result=%+v", result)
	}
	if len(deviceIDs) != 3 {
		t.Fatalf("设备号记录数 = %v，期望 status/claim/复查 各一次", deviceIDs)
	}
	if deviceIDs[0] == deviceIDOutOfRang {
		t.Fatalf("签到仍发送超界号 %q", deviceIDs[0])
	}
	for i, id := range deviceIDs {
		if id != deviceIDs[0] {
			t.Fatalf("第 %d 次用了 %q，与首次 %q 不一致", i, id, deviceIDs[0])
		}
		if len(id) != 16 || !isAllDigits(id) {
			t.Fatalf("发送的 %q 形态不合法", id)
		}
		if value := deviceIDValue(t, id); value >= deviceIDCeiling {
			t.Fatalf("发送的 %q 仍超界（%d）", id, value)
		}
	}
}

// 签到取号函数自身的契约：开关关闭时复用已存号并就地归一（覆盖
// 未经解码入口的调用方），开关打开时铸造合法新号。
func TestCheckinCredentialRepairsOutOfRangeStoredID(t *testing.T) {
	t.Setenv(checkinDeviceEnv, "")
	repaired := checkinCredential(Credential{DeviceID: deviceIDOutOfRang}).DeviceID
	if repaired == deviceIDOutOfRang {
		t.Fatalf("开关关闭时未归一超界号 %q", deviceIDOutOfRang)
	}
	if value := deviceIDValue(t, repaired); value >= deviceIDCeiling {
		t.Fatalf("开关关闭时归一出界: %q", repaired)
	}
	if again := checkinCredential(Credential{DeviceID: deviceIDOutOfRang}).DeviceID; again != repaired {
		t.Fatalf("开关关闭时归一不稳定: %q vs %q", repaired, again)
	}
	if keep := checkinCredential(Credential{DeviceID: deviceIDInRange}).DeviceID; keep != deviceIDInRange {
		t.Fatalf("开关关闭时改动了范围内号: %q", keep)
	}

	t.Setenv(checkinDeviceEnv, "1")
	fresh := checkinCredential(Credential{DeviceID: deviceIDInRange}).DeviceID
	if fresh == deviceIDInRange {
		t.Fatalf("开关打开时未铸造新号")
	}
	if value := deviceIDValue(t, fresh); value >= deviceIDCeiling {
		t.Fatalf("开关打开时铸造出超界号: %q", fresh)
	}
}

// 导入与登录共用 EnsureDevice：超界号被归一，空值补新号，合法号不动。
func TestEnsureDeviceRepairsOutOfRangeAndFillsEmpty(t *testing.T) {
	filled := EnsureDevice(Credential{})
	if len(filled.DeviceID) != 16 || !isAllDigits(filled.DeviceID) {
		t.Fatalf("空号未补出合法形态: %q", filled.DeviceID)
	}
	if value := deviceIDValue(t, filled.DeviceID); value >= deviceIDCeiling {
		t.Fatalf("补出的号超界: %q", filled.DeviceID)
	}
	if filled.MachineID == "" {
		t.Fatal("machine id 未补齐")
	}

	repaired := EnsureDevice(Credential{DeviceID: deviceIDOutOfRang, MachineID: "m1", UID: "u1"})
	if repaired.DeviceID == deviceIDOutOfRang {
		t.Fatalf("EnsureDevice 未归一超界号 %q", deviceIDOutOfRang)
	}
	if value := deviceIDValue(t, repaired.DeviceID); value >= deviceIDCeiling {
		t.Fatalf("EnsureDevice 归一出界: %q", repaired.DeviceID)
	}
	if repaired.MachineID != "m1" {
		t.Fatalf("EnsureDevice 改动已有 machine id: %q", repaired.MachineID)
	}

	kept := EnsureDevice(Credential{DeviceID: deviceIDInRange, MachineID: "m1"})
	if kept.DeviceID != deviceIDInRange {
		t.Fatalf("EnsureDevice 改动了范围内号: %q", kept.DeviceID)
	}
}
