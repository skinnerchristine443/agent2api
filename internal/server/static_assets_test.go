package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"agent2api/internal/auth"
)

// 静态资源缓存与压缩（审查 T48）：哈希资源不可变长缓存 + 入口 HTML
// 每次协商 + 按需 gzip（含 Vary）。测试从真实 index.html 解析资源名，
// 不硬编码哈希。

func staticServer() *Server {
	return New(Server{Auth: auth.NewVerifier("console-secret", "console-secret")})
}

func serveStatic(t *testing.T, s *Server, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

var assetRefPattern = regexp.MustCompile(`/assets/[A-Za-z0-9_\-]+\.js`)

func TestStaticAssetsCacheTiersAndGzip(t *testing.T) {
	s := staticServer()

	// 入口 HTML：no-cache（不得被硬缓存，否则新构建会被旧 HTML 指向已删除资源）。
	index := serveStatic(t, s, "/", nil)
	if index.Code != http.StatusOK {
		t.Fatalf("GET / = %d", index.Code)
	}
	if cc := index.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("入口 HTML Cache-Control = %q，want no-cache", cc)
	}
	match := assetRefPattern.FindString(index.Body.String())
	if match == "" {
		t.Fatalf("index.html 未引用任何 hashed asset：%s", index.Body.String()[:min(200, index.Body.Len())])
	}

	// 哈希资源：不可变长缓存；未协商时不得压缩。
	identity := serveStatic(t, s, match, nil)
	if identity.Code != http.StatusOK {
		t.Fatalf("GET %s = %d", match, identity.Code)
	}
	if cc := identity.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Fatalf("哈希资源 Cache-Control = %q", cc)
	}
	if identity.Header().Get("Content-Encoding") != "" {
		t.Fatal("未声明 Accept-Encoding 时不得压缩")
	}
	if !strings.Contains(identity.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatal("协商维度的响应必须声明 Vary: Accept-Encoding")
	}

	// 协商 gzip：解码后必须与原始字节完全一致；不得保留错误的 Content-Length。
	zipped := serveStatic(t, s, match, map[string]string{"Accept-Encoding": "gzip, deflate"})
	if zipped.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q", zipped.Header().Get("Content-Encoding"))
	}
	if zipped.Header().Get("Content-Length") != "" {
		t.Fatal("gzip 响应不得保留 Content-Length")
	}
	reader, err := gzip.NewReader(bytes.NewReader(zipped.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip 解码失败：%v", err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, identity.Body.Bytes()) {
		t.Fatal("gzip 解码结果必须与未压缩响应逐字节一致")
	}

	// SPA 回退路由（/accounts）同样是入口 HTML：可压缩且 no-cache。
	spa := serveStatic(t, s, "/accounts", map[string]string{"Accept-Encoding": "gzip"})
	if spa.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("SPA 回退也应压缩")
	}
	spaReader, err := gzip.NewReader(bytes.NewReader(spa.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	spaBody, _ := io.ReadAll(spaReader)
	if !bytes.Contains(bytes.ToLower(spaBody), []byte("<!doctype html")) {
		t.Fatal("SPA 回退解码后应为 HTML")
	}
	if cc := spa.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("SPA 回退 Cache-Control = %q", cc)
	}

	// Range 请求必须绕过压缩（206 分段与整体编码冲突）。
	ranged := serveStatic(t, s, match, map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=0-99"})
	if ranged.Header().Get("Content-Encoding") == "gzip" {
		t.Fatal("Range 请求不得压缩")
	}
	if ranged.Code != http.StatusPartialContent {
		t.Fatalf("Range 请求应得到 206，got %d", ranged.Code)
	}
}

// TestSPAFallbackCoverage 验证「受控通配」兜底（重构方案终版 §4.2；
// 修复 /usage、/checkins 直访 404 的 D1/D2 缺陷并根治白名单漏项模式 D5）：
// 全量前端路由直访均返回入口 HTML；未知路径同样回落入口（由前端 404 页
// 承接）；API/健康检查/调试前缀的未注册路径与非常规方法仍为 404。
// 路由清单与前端 src/nav/nav.ts + App.tsx 兼容重定向保持同步（前端侧的
// 清单一致性由约定脚本在阶段 2 接管）。
func TestSPAFallbackCoverage(t *testing.T) {
	s := staticServer()

	frontendRoutes := []string{
		"/", "/login",
		"/accounts", "/accounts/expiry", "/accounts/growth",
		"/tasks", "/quota", "/settings",
		"/models", "/access", "/usage",
		"/logs", "/logs/requests", "/logs/runtime",
		"/system", "/system/update", "/system/keys",
		"/providers", "/auth", "/checkins", "/welfare", // 兼容重定向入口
	}
	for _, route := range frontendRoutes {
		rec := serveStatic(t, s, route, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d，want 200（SPA 兜底）", route, rec.Code)
		}
		if !strings.Contains(strings.ToLower(rec.Body.String()), "<!doctype html") {
			t.Fatalf("GET %s 未返回入口 HTML", route)
		}
	}

	// 未知路径同样回落入口（坏链接由前端 404 页承接，不再是空 404）。
	for _, unknown := range []string{"/no-such-page", "/deep/unknown/path", "/accounts/unknown-sub"} {
		rec := serveStatic(t, s, unknown, nil)
		if rec.Code != http.StatusOK || !strings.Contains(strings.ToLower(rec.Body.String()), "<!doctype html") {
			t.Fatalf("GET %s = %d（want 200 + HTML）", unknown, rec.Code)
		}
	}

	// 排除集：未注册的 API / 调试路径不得回落到入口 HTML。
	for _, apiPath := range []string{"/api", "/api/unknown", "/v1/unknown", "/health/extra", "/debug/pprof", "/debug/unknown"} {
		rec := serveStatic(t, s, apiPath, nil)
		if rec.Code == http.StatusOK && strings.Contains(strings.ToLower(rec.Body.String()), "<!doctype html") {
			t.Fatalf("GET %s 不应回落到前端入口，got %d + HTML", apiPath, rec.Code)
		}
	}

	// 非 GET/HEAD 方法：未知路径保持 404。
	req := httptest.NewRequest(http.MethodPost, "/no-such-page", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /no-such-page = %d，want 404", rec.Code)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
