package config

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// 环境变量 ↔ 样例双向对账（审查 T48）：代码里以 ("AGENT2API_*") 形式
// 读取的每个键都必须出现在两个 .env.example；样例里出现的键也必须
// 确实被代码读取（编排专用键除外）。防止「照样例配置却无效」与
// 「代码可配但样例缺位」两类漂移。
var (
	// 引号内出现的键 = 被代码消费（含 Getenv 直读与「常量先声明、
	// 后 Getenv(name) 间接读取」两种形态；注释里的键不带引号，不会误收）。
	envReadPattern  = regexp.MustCompile(`"AGENT2API_[A-Z0-9_]+"`)
	envTokenPattern = regexp.MustCompile(`AGENT2API_[A-Z0-9_]+`)
)

func TestEnvExamplesCoverProductionKeys(t *testing.T) {
	root := filepath.Join("..", "..")

	// —— 代码侧：扫描全部非测试 Go 文件的 env 读取调用。
	codeKeys := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "frontend", ".agent-runs", "vendor", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for _, match := range envReadPattern.FindAllString(string(raw), -1) {
			if name := envTokenPattern.FindString(match); name != "" {
				codeKeys[name] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// live 测试专用键不进样例。
	for key := range codeKeys {
		if strings.Contains(key, "_LIVE_") {
			delete(codeKeys, key)
		}
	}

	// 编排/镜像专用键：不出现在 Go 代码，但允许出现在 compose 侧样例。
	exempt := map[string]bool{
		"AGENT2API_IMAGE":              true, // deploy compose 的镜像引用
		"AGENT2API_UPDATER_SOCKET_DIR": true, // compose 卷替换变量
		"AGENT2API_UPDATER_GID":        true, // compose group_add 变量
	}
	// 部署样例的用户可配面之外的键：在容器语境下不可配置（由编排/镜像固定），
	// 因此只要求根样例覆盖；逐键给出理由。
	deployFixed := map[string]string{
		"AGENT2API_DATA_DIR":           "compose 固定为 /data（镜像内路径）",
		"AGENT2API_RUNTIME_DIR":        "compose/Dockerfile 固定为 /run/agent2api",
		"AGENT2API_UPDATE_SOCKET_PATH": "compose 固定为 /run/agent2api-updater/updater.sock",
		"AGENT2API_HOME":               "容器内不适用（数据/运行目录已显式固定；deploy 样例有说明）",
	}

	// —— 样例侧。
	exampleNames := []string{".env.example", filepath.Join("deploy", ".env.example")}
	examples := map[string]string{}
	exampleKeys := map[string]bool{}
	for _, name := range exampleNames {
		raw, readErr := os.ReadFile(filepath.Join(root, name))
		if readErr != nil {
			t.Fatalf("读取 %s：%v", name, readErr)
		}
		examples[name] = string(raw)
		for _, key := range envTokenPattern.FindAllString(string(raw), -1) {
			exampleKeys[key] = true
		}
	}

	// 方向一：根样例必须覆盖全部代码读取的键；部署样例覆盖用户可配面
	//（deployFixed 中的键在容器语境下不可配置，仅根样例覆盖）。
	var missing []string
	for key := range codeKeys {
		if exempt[key] {
			continue
		}
		if !strings.Contains(examples[".env.example"], key+"=") {
			missing = append(missing, key+" ← .env.example")
		}
		if _, fixed := deployFixed[key]; !fixed {
			if !strings.Contains(examples[filepath.Join("deploy", ".env.example")], key+"=") {
				missing = append(missing, key+" ← deploy/.env.example")
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("以下代码读取的键未在预期样例中登记：\n  %s", strings.Join(missing, "\n  "))
	}
	_ = deployFixed

	// 方向二：样例键必须被代码读取（编排专用除外）——防「死配置」。
	var dead []string
	for key := range exampleKeys {
		if !codeKeys[key] && !exempt[key] {
			dead = append(dead, key)
		}
	}
	sort.Strings(dead)
	if len(dead) > 0 {
		t.Fatalf("样例中出现但代码不读取的键（死配置或漏登豁免）：%s", strings.Join(dead, ", "))
	}
}
