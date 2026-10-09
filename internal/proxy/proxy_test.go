package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestParseProxyModes(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		mode Mode
	}{
		{name: "inherit", raw: "", mode: ModeInherit},
		{name: "direct", raw: "direct", mode: ModeDirect},
		{name: "http", raw: "http://proxy.example:8080", mode: ModeProxy},
		{name: "socks", raw: "socks5://proxy.example:1080", mode: ModeProxy},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setting, err := Parse(test.raw)
			if err != nil || setting.Mode != test.mode {
				t.Fatalf("Parse(%q) = mode=%d err=%v, want mode=%d", test.raw, setting.Mode, err, test.mode)
			}
		})
	}
}

func TestParseRejectsUnsupportedProxy(t *testing.T) {
	for _, raw := range []string{"proxy.example:8080", "ftp://proxy.example:21", "http://"} {
		if _, err := Parse(raw); err == nil {
			t.Fatalf("Parse(%q) unexpectedly succeeded", raw)
		}
	}
}

func TestPreserve(t *testing.T) {
	existing := "http://user:secret@proxy.example:8080"
	masked := Redact(existing)
	if masked != "http://user:%2A%2A%2A%2A%2A%2A@proxy.example:8080" {
		t.Fatalf("redacted proxy = %q", masked)
	}
	if got := Preserve(existing, masked); got != existing {
		t.Fatalf("preserved proxy = %q", got)
	}
	if got := Preserve(existing, "direct"); got != "direct" {
		t.Fatalf("replacement proxy = %q", got)
	}
}

func TestNewTransport(t *testing.T) {
	transport, err := NewTransport("direct")
	if err != nil || transport == nil || transport.Proxy != nil {
		t.Fatalf("direct transport = %#v err=%v", transport, err)
	}
	transport, err = NewTransport("http://proxy.example:8080")
	if err != nil || transport == nil {
		t.Fatalf("proxy transport = %#v err=%v", transport, err)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	proxyURL, err := transport.Proxy(req)
	if err != nil || proxyURL.String() != "http://proxy.example:8080" {
		t.Fatalf("transport proxy = %v err=%v", proxyURL, err)
	}
}

// GLOBAL 代理被每个 provider 继承，因此它停留在 http(s) 边界内；
// SOCKS5 改在账号级别提供。无论如何，不支持的 scheme 都会在解析时被拒绝。
func TestValidateHTTPOnly(t *testing.T) {
	for _, raw := range []string{
		"",
		"direct",
		"none",
		"http://proxy.example:8080",
		"https://proxy.example:8443",
	} {
		if err := ValidateHTTPOnly(raw); err != nil {
			t.Fatalf("ValidateHTTPOnly(%q): %v", raw, err)
		}
	}

	for _, raw := range []string{
		"socks5://proxy.example:1080",
		"socks5h://proxy.example:1080",
	} {
		if err := ValidateHTTPOnly(raw); err == nil {
			t.Fatalf("ValidateHTTPOnly(%q) unexpectedly succeeded", raw)
		}
	}

	// transport 本身能构建 SOCKS5；只是这项设置更为严格。
	if _, err := Parse("socks5://proxy.example:1080"); err != nil {
		t.Fatalf("Parse must still accept SOCKS5 for account-level proxies: %v", err)
	}

	if err := ValidateHTTPOnly("ftp://proxy.example:21"); err == nil {
		t.Fatal("ValidateHTTPOnly accepted an unsupported scheme it should reject at parse time")
	}
}

func TestTransportCache(t *testing.T) {
	var cache TransportCache

	first, err := cache.Get("http://proxy.example:8080")
	if err != nil || first == nil {
		t.Fatalf("first Get = %#v err=%v", first, err)
	}
	second, err := cache.Get("http://proxy.example:8080")
	if err != nil || second != first {
		t.Fatalf("same URL was not reused: %#v vs %#v err=%v", first, second, err)
	}
	// 在作为 key 之前会先归一化空白。
	third, err := cache.Get("  http://proxy.example:8080  ")
	if err != nil || third != first {
		t.Fatalf("trimmed URL was not reused: %#v vs %#v err=%v", first, third, err)
	}

	other, err := cache.Get("http://other.example:8080")
	if err != nil || other == nil || other == first {
		t.Fatalf("distinct URL shared a transport: %#v vs %#v err=%v", first, other, err)
	}

	// 空值意为 "inherit"：不生成 transport，且默认值保持可用。
	empty, err := cache.Get("   ")
	if err != nil || empty != nil {
		t.Fatalf("empty Get = %#v err=%v, want nil", empty, err)
	}
}

func TestTransportCacheEvictsOldestAtCapacity(t *testing.T) {
	var cache TransportCache

	first, err := cache.Get("http://proxy0.example:8080")
	if err != nil || first == nil {
		t.Fatalf("seed Get = %#v err=%v", first, err)
	}
	for i := 1; i <= maxCachedTransports; i++ {
		raw := fmt.Sprintf("http://proxy%d.example:8080", i)
		if _, err := cache.Get(raw); err != nil {
			t.Fatalf("Get(%q): %v", raw, err)
		}
	}

	// 缓存先填满到容量，然后又溢出一次：必须丢弃最老的条目
	//（proxy0）以保持 map 有界。
	if len(cache.transports) != maxCachedTransports {
		t.Fatalf("cache size = %d, want %d", len(cache.transports), maxCachedTransports)
	}
	if _, ok := cache.transports["http://proxy0.example:8080"]; ok {
		t.Fatal("least-recently-used transport was not evicted")
	}
	rebuilt, err := cache.Get("http://proxy0.example:8080")
	if err != nil || rebuilt == nil || rebuilt == first {
		t.Fatalf("evicted transport was not rebuilt: %#v vs %#v err=%v", rebuilt, first, err)
	}
}

func TestTransportCacheGetPromotesRecency(t *testing.T) {
	var cache TransportCache

	for i := 0; i < maxCachedTransports; i++ {
		raw := fmt.Sprintf("http://proxy%d.example:8080", i)
		if _, err := cache.Get(raw); err != nil {
			t.Fatalf("Get(%q): %v", raw, err)
		}
	}

	// 重新读取 proxy0 使它成为最近最多使用；下一次插入就必须淘汰
	// proxy1（新的 LRU）而非 proxy0。
	if _, err := cache.Get("http://proxy0.example:8080"); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Get("http://overflow.example:8080"); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.transports["http://proxy0.example:8080"]; !ok {
		t.Fatal("recently used transport was evicted")
	}
	if _, ok := cache.transports["http://proxy1.example:8080"]; ok {
		t.Fatal("least-recently-used transport was not evicted after promotion")
	}
}

func TestTransportCacheGetInvalidProxyDoesNotCache(t *testing.T) {
	var cache TransportCache
	if _, err := cache.Get("ftp://proxy.example:21"); err == nil {
		t.Fatal("Get accepted an unsupported proxy scheme")
	}
	if len(cache.transports) != 0 {
		t.Fatalf("failed Get polluted the cache: %#v", cache.transports)
	}
}

// 回归测试：零值缓存可经 Get 使用，因此在任何 Get 惰性初始化 order/transports
// 之前，CloseIdleConnections 不得 panic。
func TestTransportCacheCloseIdleConnectionsOnZeroValue(t *testing.T) {
	var cache TransportCache
	cache.CloseIdleConnections() // 不得 panic

	if _, err := cache.Get("http://proxy.example:8080"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	cache.CloseIdleConnections() // 填充后仍然安全
}

func TestTransportCacheConcurrentGet(t *testing.T) {
	var cache TransportCache

	const workers = 32
	transports := make([]*http.Transport, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			transport, err := cache.Get("http://proxy.example:8080")
			if err != nil {
				t.Errorf("Get: %v", err)
				return
			}
			transports[i] = transport
		}(i)
	}
	wg.Wait()

	for i, transport := range transports {
		if transport == nil || transport != transports[0] {
			t.Fatalf("concurrent Get[%d] did not reuse the single transport", i)
		}
	}
}

func TestSOCKSDialHonorsContextCancellation(t *testing.T) {
	// 一个接受 TCP 但永不完成 SOCKS5 握手的 listener。
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// 保持连接打开但不回复。
			_ = conn
		}
	}()

	transport, err := NewTransport("socks5://" + listener.Addr().String())
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	if transport == nil || transport.DialContext == nil {
		t.Fatal("SOCKS transport has no DialContext")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = transport.DialContext(ctx, "tcp", "example.com:443")
	if err == nil {
		t.Fatal("dial through a stalled SOCKS proxy unexpectedly succeeded")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("dial did not abort on context cancellation: took %v", elapsed)
	}

	// 已取消的 context 会快速失败，且不进行拨号。
	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if _, err := transport.DialContext(cancelled, "tcp", "example.com:443"); err == nil {
		t.Fatal("dial with a cancelled context unexpectedly succeeded")
	}
}
