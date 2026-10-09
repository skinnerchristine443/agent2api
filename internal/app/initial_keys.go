package app

import (
	"log"
	"os"
	"path/filepath"
)

// initialKeysFileName 是首启密钥的落盘文件名（仅非终端环境使用）。
// 文件位于数据目录内，与 SQLite 同属一个信任边界；权限固定为 0600。
const initialKeysFileName = "initial-keys.txt"

// isTerminal 报告 f 是否连接终端（字符设备）。
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// deliverInitialSecret 交付一枚「只应展示一次」的首启密钥：
// stderr 连接终端时按既有行为打印一次；非终端环境（容器 / 日志采集）
// 改为追加写入 dataDir/initial-keys.txt（0600），避免高权密钥滞留于
// 容器日志与日志聚合系统。写盘失败时只记录错误，绝不回退为打印。
func deliverInitialSecret(dataDir, printLine, fileLine string) {
	if isTerminal(os.Stderr) {
		log.Print(printLine)
		return
	}
	path := filepath.Join(dataDir, initialKeysFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		log.Printf("[security] failed to persist initial secret to %s: %v", path, err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(fileLine + "\n"); err != nil {
		log.Printf("[security] failed to persist initial secret to %s: %v", path, err)
		return
	}
	log.Printf("[security] initial secret persisted to %s (mode 0600)", path)
}
