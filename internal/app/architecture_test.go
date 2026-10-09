package app

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

const modulePath = "agent2api"

type goPackage struct {
	ImportPath string
	Dir        string
	Name       string
	Imports    []string
}

// 生产代码导入豁免清单。每一项都是一处经评审的例外，并注明理由；
// 当前为空——历史唯一豁免（api→app）已随 facade 全部降级为测试包而消除。
var importAllowlist = map[string]map[string]string{}

func TestImportConstraints(t *testing.T) {
	root := moduleRoot(t)
	pkgs := listProductionPackages(t, root)
	var violations []string

	for _, pkg := range pkgs {
		rel := strings.TrimPrefix(pkg.ImportPath, modulePath+"/")
		imports := pkg.Imports
		allow := importAllowlist[pkg.ImportPath]

		for _, imp := range imports {
			if allowed, ok := allow[imp]; ok && allowed != "" {
				continue
			}
			if bad, reason := forbiddenImport(rel, imp); bad {
				violations = append(violations, pkg.ImportPath+" imports "+imp+": "+reason)
			}
		}

		if rel == "internal/api" {
			// 该包是测试专用包（facade 文件已全部降级为 _test）：
			// 出现任何生产 .go 文件都视为回归——生产构建必须忽略此包。
			entries, err := os.ReadDir(pkg.Dir)
			if err != nil {
				t.Fatalf("read api dir: %v", err)
			}
			for _, entry := range entries {
				name := entry.Name()
				if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
					continue
				}
				violations = append(violations, "internal/api must stay test-only; production file found: "+name)
			}
		}
	}

	if len(violations) > 0 {
		t.Fatalf("import constraints failed:\n%s", strings.Join(violations, "\n"))
	}
}

func forbiddenImport(importer, imp string) (bool, string) {
	if !strings.HasPrefix(importer, "internal/") && !strings.HasPrefix(importer, "cmd/") {
		return false, ""
	}

	switch {
	case importer == "internal/gateway" || strings.HasPrefix(importer, "internal/gateway/"):
		if isAny(imp, "internal/store", "database/sql", "modernc.org/sqlite", "internal/runtime", "internal/api") {
			return true, "gateway must not import store, SQL, runtime Manager, or api"
		}
		if isConcreteProvider(imp) {
			return true, "gateway must not import a concrete provider"
		}
	case importer == "internal/console" || strings.HasPrefix(importer, "internal/console/"):
		if isAny(imp, "internal/store", "database/sql", "modernc.org/sqlite", "internal/runtime", "internal/api") {
			return true, "console must not import store, SQL, runtime Manager, or api"
		}
		if isConcreteProvider(imp) {
			return true, "console must not import a concrete provider"
		}
	case importer == "internal/server" || strings.HasPrefix(importer, "internal/server/"):
		if isAny(imp, "internal/store", "database/sql", "modernc.org/sqlite", "internal/runtime", "internal/api") {
			return true, "server must not import store, SQL, runtime Manager, or api"
		}
		if isConcreteProvider(imp) {
			return true, "server must not import a concrete provider"
		}
	case importer == "internal/executor" || strings.HasPrefix(importer, "internal/executor/"):
		if isAny(imp, "internal/store", "internal/app", "internal/server", "internal/gateway", "internal/console", "internal/api") {
			return true, "executor must not import store or HTTP/app packages"
		}
		if isConcreteProvider(imp) {
			return true, "executor must not import a concrete provider"
		}
	case importer == "internal/control" || strings.HasPrefix(importer, "internal/control/"):
		if isAny(imp, "internal/store", "database/sql", "modernc.org/sqlite", "internal/runtime", "internal/app", "internal/server", "internal/gateway", "internal/console", "internal/api") {
			return true, "control must not import SQL, runtime, app, or HTTP packages"
		}
		if isConcreteProvider(imp) {
			return true, "control must not import a concrete provider"
		}
	case importer == "internal/runtime" || strings.HasPrefix(importer, "internal/runtime/"):
		if isAny(imp, "internal/control", "internal/app", "internal/server", "internal/gateway", "internal/console", "internal/api") {
			return true, "runtime must not import control, app, or HTTP packages"
		}
		if isConcreteProvider(imp) {
			return true, "runtime must not import a concrete provider"
		}
	case strings.HasPrefix(importer, "internal/providers/") && importer != "internal/providers":
		if isAny(imp, "internal/executor", "internal/runtime", "internal/gateway", "internal/console", "internal/server", "internal/store", "internal/app", "internal/api") {
			return true, "provider packages must not import executor, runtime Manager, HTTP, store, or api"
		}
	case importer == "internal/store" || strings.HasPrefix(importer, "internal/store/"):
		if isAny(imp, "internal/control", "internal/runtime", "internal/app", "internal/server", "internal/gateway", "internal/console", "internal/api") {
			return true, "store must not import control, runtime, app, or HTTP packages"
		}
		if isConcreteProvider(imp) {
			return true, "store must not import a concrete provider"
		}
	case importer == "internal/logs" || strings.HasPrefix(importer, "internal/logs/"):
		if isAny(imp, "internal/store", "internal/gateway", "internal/console", "internal/app", "internal/api") {
			return true, "logs must not import store, HTTP, or app"
		}
	case importer == "internal/accounts" || strings.HasPrefix(importer, "internal/accounts/"):
		// 注：accounts → providers（渠道元数据只读校验，两处）是经评审允许的方向，
		// 评估记录见 docs/02 §6.1——此处刻意不设禁止边。
		if isAny(imp, "internal/runtime", "internal/executor", "internal/store", "internal/app", "internal/api") {
			return true, "accounts must not import runtime, executor, store, or app"
		}
	case importer == "cmd/server":
		if isAny(imp, "internal/api") {
			return true, "cmd/server must construct app.New, not the api facade"
		}
	}

	if isAny(imp, "internal/app") && importer != "cmd/server" && importer != "internal/api" && !strings.HasPrefix(importer, "internal/app") {
		return true, "app must not be imported by lower packages"
	}
	if isAny(imp, "internal/api") && importer != "internal/api" {
		return true, "only the api facade package may import itself; production packages must not import api"
	}
	return false, ""
}

func isConcreteProvider(imp string) bool {
	prefix := modulePath + "/internal/providers/"
	if !strings.HasPrefix(imp, prefix) {
		return false
	}
	rest := strings.TrimPrefix(imp, prefix)
	root, _, _ := strings.Cut(rest, "/")
	switch root {
	case "workbuddy", "trae":
		return true
	default:
		return false
	}
}

func isAny(imp string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if imp == suffix || imp == modulePath+"/"+suffix || strings.HasSuffix(imp, "/"+suffix) {
			if strings.Contains(suffix, "/") || strings.Contains(suffix, ".") {
				if imp == suffix || imp == modulePath+"/"+suffix {
					return true
				}
				if strings.HasPrefix(imp, modulePath+"/"+suffix+"/") {
					return true
				}
			} else if imp == suffix || imp == modulePath+"/"+suffix {
				return true
			}
		}
		if strings.HasPrefix(suffix, "internal/") && (imp == modulePath+"/"+suffix || strings.HasPrefix(imp, modulePath+"/"+suffix+"/")) {
			return true
		}
		if suffix == "database/sql" && imp == "database/sql" {
			return true
		}
		if suffix == "modernc.org/sqlite" && (imp == "modernc.org/sqlite" || strings.HasPrefix(imp, "modernc.org/sqlite/")) {
			return true
		}
	}
	return false
}

func listProductionPackages(t *testing.T, root string) []goPackage {
	t.Helper()
	cmd := exec.Command("go", "list", "-json", "./internal/...", "./cmd/...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		t.Fatalf("go list: %v\n%s", err, stderr)
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	var pkgs []goPackage
	for dec.More() {
		var pkg goPackage
		if err := dec.Decode(&pkg); err != nil {
			t.Fatalf("decode go list: %v", err)
		}
		if !strings.HasPrefix(pkg.ImportPath, modulePath+"/") {
			continue
		}
		pkgs = append(pkgs, pkg)
	}
	if len(pkgs) == 0 {
		t.Fatal("go list returned no module packages")
	}
	return pkgs
}

func TestDutyBoundaries(t *testing.T) {
	root := moduleRoot(t)
	var violations []string
	walkProductionGoFiles(t, root, func(rel, src string) {
		switch {
		case strings.HasPrefix(rel, "internal/app/"):
			for _, needle := range []string{
				"func FilterModelsForIdentity",
				"func DecorateModelsWithContext",
				"func decorateProviderSettings",
				"func MergeModelEntryCapabilities",
				"func NextLocalMidnightCooldown",
			} {
				if strings.Contains(src, needle) {
					violations = append(violations, rel+": app must not implement "+needle)
				}
			}
		case strings.HasPrefix(rel, "internal/control/"):
			for _, needle := range []string{
				"/admin/login/",
				"oauth_if_complete",
			} {
				if strings.Contains(src, needle) {
					violations = append(violations, rel+": control must not hard-code provider login protocol paths")
				}
			}
		case strings.HasPrefix(rel, "internal/console/"):
			for _, needle := range []string{
				".SetSecret(",
				".SetSecretOrEmpty(",
				".SetConsoleSecret(",
				".ReplaceProxyAPIKey(",
				"workbuddy has no max-mode switch",
				"/admin/login/",
				"oauth_if_complete",
				"statsCache",
			} {
				if strings.Contains(src, needle) {
					violations = append(violations, rel+": console must not persist settings/keys, own model-setting rules, login protocol, or stats cache; use control / logs")
				}
			}
		case strings.HasPrefix(rel, "internal/gateway/"):
			for _, needle := range []string{
				"func Classify(",
				"executor.Classify(",
				"NextLocalMidnightCooldown",
				"minRateLimitCooldown",
			} {
				if strings.Contains(src, needle) {
					violations = append(violations, rel+": gateway must format classified errors, not own cooldown policy")
				}
			}
		case strings.HasPrefix(rel, "internal/accounts/"):
			if strings.Contains(src, "func NextLocalMidnightCooldown") {
				violations = append(violations, rel+": accounts must not compute cooldown durations")
			}
		case strings.HasPrefix(rel, "internal/runtime/"):
			if strings.Contains(src, "/admin/login/") {
				violations = append(violations, rel+": runtime must not hard-code provider login protocol paths")
			}
		case strings.HasPrefix(rel, "internal/store/"):
			for _, needle := range []string{
				"GenerateAPIKeySecret",
				"SecretOnce",
			} {
				if strings.Contains(src, needle) {
					violations = append(violations, rel+": store must persist prepared API keys, not generate secrets")
				}
			}
		case strings.HasPrefix(rel, "internal/providers/"):
			if writerParamInFile(t, filepath.Join(root, rel)) {
				violations = append(violations, rel+": provider packages must not receive http.ResponseWriter")
			}
			for _, needle := range []string{
				"func classifiedCooldown",
				"Failover: &failover",
				"hardRateCooldown",
			} {
				if strings.Contains(src, needle) {
					violations = append(violations, rel+": provider packages must not set failover or compute cooldown policy")
				}
			}
		}
	})
	if !writerParamInFile(t, filepath.Join(root, "internal/auth/loopback.go")) {
		violations = append(violations, "internal/auth/loopback.go must own the loopback ResponseWriter")
	}
	if len(violations) > 0 {
		t.Fatalf("duty boundaries failed:\n%s", strings.Join(violations, "\n"))
	}
}

func walkProductionGoFiles(t *testing.T, root string, fn func(rel, src string)) {
	t.Helper()
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fn(filepath.ToSlash(rel), string(src))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func writerParamInFile(t *testing.T, path string) bool {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncType)
		if !ok || fn.Params == nil {
			return true
		}
		for _, field := range fn.Params.List {
			if isHTTPResponseWriter(field.Type) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func isHTTPResponseWriter(expr ast.Expr) bool {
	switch typed := expr.(type) {
	case *ast.StarExpr:
		return isHTTPResponseWriter(typed.X)
	case *ast.SelectorExpr:
		ident, ok := typed.X.(*ast.Ident)
		return ok && ident.Name == "http" && typed.Sel != nil && typed.Sel.Name == "ResponseWriter"
	default:
		return false
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// upstreamJSONTagAllowlist 列出允许 json tag 原样照搬外部 API 自身字段名
// （camelCase / PascalCase）的位置。其他任何地方，tag 都归属于本仓库自身的契约，
// 必须是 snake_case。
//
// 目录条目以 "/" 结尾，覆盖其下所有内容。新增一行是一次刻意的评审决定：
// 它扩大了允许偏离我们自身命名契约的位置集合。
var upstreamJSONTagAllowlist = map[string]string{
	"internal/providers/":          "provider wire structs mirror each upstream's field names.",
	"internal/updater/executor.go": "mirrors the Docker Engine API payload (Image, Mounts, Networks, NetworkSettings).",
}

// TestUpstreamJSONTagBoundary 让「自身契约 vs 上游镜像」的划分保持可强制。
// 白名单之外出现上游风格的 json tag，意味着要么该 struct 应归入 internal/providers，
// 要么我们的 tag 偏离了 snake_case——两者都值得让构建失败。
func TestUpstreamJSONTagBoundary(t *testing.T) {
	root := moduleRoot(t)
	var violations []string
	matched := map[string]int{}

	for _, top := range []string{"internal", "cmd"} {
		base := filepath.Join(root, top)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			for _, name := range upstreamJSONTagNames(string(src)) {
				if !hasUpperASCII(name) {
					continue
				}
				if _, ok := upstreamTagAllowed(rel); ok {
					matched[rel]++
					continue
				}
				violations = append(violations,
					rel+`: json:"`+name+`" carries an upstream field name outside the allowlist`)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	if len(violations) > 0 {
		t.Fatalf("upstream json tag boundary failed:\n%s\n\nAllowed locations:\n%s",
			strings.Join(violations, "\n"), strings.Join(upstreamAllowlistLines(), "\n"))
	}

	// 过期的文件条目会继续放行一个已不再需要它的文件。
	for entry := range upstreamJSONTagAllowlist {
		if !strings.HasSuffix(entry, "/") && matched[entry] == 0 {
			t.Logf("note: allowlist row %q matched no upstream json tag; consider removing it", entry)
		}
	}
}

// upstreamJSONTagNames 返回 src 中每个 `json:"..."` struct tag 的字段名。
// 它以文本扫描实现，从而不依赖 AST 版本。
func upstreamJSONTagNames(src string) []string {
	var names []string
	for _, line := range strings.Split(src, "\n") {
		rest := line
		for {
			i := strings.Index(rest, `json:"`)
			if i < 0 {
				break
			}
			rest = rest[i+len(`json:"`):]
			j := strings.IndexByte(rest, '"')
			if j < 0 {
				break
			}
			names = append(names, rest[:j])
			rest = rest[j:]
		}
	}
	return names
}

// hasUpperASCII 报告 s 是否含大写 ASCII 字母——这正是上游字段名
// 与我们 snake_case 契约的区别所在。
func hasUpperASCII(s string) bool {
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}

// upstreamTagAllowed 报告 rel 是否允许携带上游 json tag。
func upstreamTagAllowed(rel string) (string, bool) {
	if reason, ok := upstreamJSONTagAllowlist[rel]; ok {
		return reason, true
	}
	for entry, reason := range upstreamJSONTagAllowlist {
		if strings.HasSuffix(entry, "/") && strings.HasPrefix(rel, entry) {
			return reason, true
		}
	}
	return "", false
}

func upstreamAllowlistLines() []string {
	lines := make([]string, 0, len(upstreamJSONTagAllowlist))
	for entry, reason := range upstreamJSONTagAllowlist {
		lines = append(lines, "  "+entry+" — "+reason)
	}
	sort.Strings(lines)
	return lines
}
