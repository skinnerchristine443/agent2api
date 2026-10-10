package proxy

import (
	"container/list"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	socksproxy "golang.org/x/net/proxy"
)

type Mode int

const (
	ModeInherit Mode = iota
	ModeDirect
	ModeProxy
)

// socksHandshakeTimeout 限定到 SOCKS 代理的 TCP 连接加上 SOCKS5 协商的耗时。
// 没有它，一个停滞的代理在调用方无 deadline 时会让拨号
// （以及标准库的兜底 goroutine）一直阻塞。
const socksHandshakeTimeout = 30 * time.Second

// socksDialContext 用 context 经由 SOCKS dialer 拨号。实现了
// socksproxy.ContextDialer 的 dialer 会直接响应取消；兜底路径会关闭
// 在 context 取消之后才到达的连接。
func socksDialContext(ctx context.Context, dialer socksproxy.Dialer, network, address string) (net.Conn, error) {
	if contextDialer, ok := dialer.(socksproxy.ContextDialer); ok {
		return contextDialer.DialContext(ctx, network, address)
	}
	type result struct {
		conn net.Conn
		err  error
	}
	done := make(chan result, 1)
	go func() {
		conn, err := dialer.Dial(network, address)
		done <- result{conn: conn, err: err}
	}()
	select {
	case <-ctx.Done():
		go func() {
			if r := <-done; r.conn != nil {
				_ = r.conn.Close()
			}
		}()
		return nil, ctx.Err()
	case r := <-done:
		if r.err == nil && ctx.Err() != nil {
			_ = r.conn.Close()
			return nil, ctx.Err()
		}
		return r.conn, r.err
	}
}

type Setting struct {
	Raw  string
	Mode Mode
	URL  *url.URL
}

func Parse(raw string) (Setting, error) {
	raw = strings.TrimSpace(raw)
	setting := Setting{Raw: raw}
	if raw == "" {
		return setting, nil
	}
	if strings.EqualFold(raw, "direct") || strings.EqualFold(raw, "none") {
		setting.Mode = ModeDirect
		return setting, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return Setting{Raw: raw}, fmt.Errorf("proxy URL must include a supported scheme and host")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks5", "socks5h":
		setting.Mode = ModeProxy
		setting.URL = parsed
		return setting, nil
	default:
		return Setting{Raw: raw}, fmt.Errorf("unsupported proxy scheme %q", parsed.Scheme)
	}
}

func Preserve(existing, replacement string) string {
	existing = strings.TrimSpace(existing)
	replacement = strings.TrimSpace(replacement)
	if existing != "" && Redact(existing) == replacement {
		return existing
	}
	return replacement
}

func Redact(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "direct") || strings.EqualFold(raw, "none") {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User == nil {
		return raw
	}
	parsed.User = url.UserPassword(parsed.User.Username(), "******")
	return parsed.String()
}

// ValidateHTTPOnly 拒绝 SOCKS 代理，同时仍接受 http(s)、direct、none 以及
// 空值（inherit）。它约束那些必须停留在 http(s) 边界内的设置——即 GLOBAL 出站代理，
// 每个 provider 都会继承它，包括那个其账号级代理被文档标注为仅支持 http(s) 的 provider。
func ValidateHTTPOnly(raw string) error {
	setting, err := Parse(raw)
	if err != nil {
		return err
	}
	if setting.Mode != ModeProxy {
		return nil
	}
	switch strings.ToLower(setting.URL.Scheme) {
	case "http", "https":
		return nil
	default:
		return fmt.Errorf("this proxy setting only supports http(s), direct, or none")
	}
}

// 每个出站传输的连接池设置。Go 的默认值（MaxIdleConnsPerHost = 2）是按浏览器
// 规模设定的，不适合把同一个上游主机扇出到多条并发流的网关：负载下连接会被
// 反复拆除并重新拨号（并重跑代理握手）。
const (
	maxIdleConns          = 100
	maxIdleConnsPerHost   = 20
	idleConnTimeout       = 90 * time.Second
	responseHeaderTimeout = 120 * time.Second
)

// NewTransport 为一项代理设置构建出站传输。它克隆 http.DefaultTransport
// （保留 HTTP/2 与标准 dialer），然后显式固定连接池上限。
func NewTransport(raw string) (*http.Transport, error) {
	setting, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	if setting.Mode == ModeInherit {
		return nil, nil
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok || transport == nil {
		transport = &http.Transport{}
	} else {
		transport = transport.Clone()
	}
	tunePoolSettings(transport)
	switch setting.Mode {
	case ModeDirect:
		transport.Proxy = nil
	case ModeProxy:
		if setting.URL.Scheme == "socks5" || setting.URL.Scheme == "socks5h" {
			var auth *socksproxy.Auth
			if setting.URL.User != nil {
				password, _ := setting.URL.User.Password()
				auth = &socksproxy.Auth{User: setting.URL.User.Username(), Password: password}
			}
			// 前置 dialer 自己负责连到 SOCKS 代理；用感知 context 的 dialer，
			// 这样代理失效时会在取消时中止，而不是卡在标准库的兜底 goroutine 里。
			dialer, err := socksproxy.SOCKS5("tcp", setting.URL.Host, auth, &net.Dialer{})
			if err != nil {
				return nil, fmt.Errorf("create SOCKS5 proxy: %w", err)
			}
			transport.Proxy = nil
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				// 即使调用方传入的 context 没有 deadline，也限定 TCP 连接 + SOCKS
				// 握手的耗时，这样停滞的代理无法永远占住拨号（及其 goroutine）。
				handshakeCtx, cancel := context.WithTimeout(ctx, socksHandshakeTimeout)
				defer cancel()
				return socksDialContext(handshakeCtx, dialer, network, address)
			}
		} else {
			transport.Proxy = http.ProxyURL(setting.URL)
		}
	}
	return transport, nil
}

// tunePoolSettings 把连接池上限钉到网关规模。Go 默认的
// MaxIdleConnsPerHost = 2 是按浏览器规模设的：把同一上游主机扇出到多条并发流时，
// 连接会被反复拆除并重新拨号（并重跑 TLS 握手），正是「首个 token 迟迟不来」的
// 常见成因之一。
func tunePoolSettings(transport *http.Transport) {
	transport.MaxIdleConns = maxIdleConns
	transport.MaxIdleConnsPerHost = maxIdleConnsPerHost
	transport.IdleConnTimeout = idleConnTimeout
	// 仅当尚无其他限制时才限制响应头等待：默认是「无限制」，
	// 一个停滞的上游一直握着响应头会占住一个 worker 槽位。
	if transport.ResponseHeaderTimeout == 0 {
		transport.ResponseHeaderTimeout = responseHeaderTimeout
	}
}

// inheritTransport 是「未配置代理」时使用的出站传输：与显式代理路径同样钉好
// 连接池上限，但保留 http.DefaultTransport 的 Proxy（http.ProxyFromEnvironment），
// 因此仍尊重 HTTP_PROXY/NO_PROXY 等环境变量。
//
// 没有它时，空设置会让每个请求的客户端退回 http.DefaultTransport——
// MaxIdleConnsPerHost 只有 2，并发下连接反复重建。惰性构建一次并共享。
var (
	inheritTransportOnce sync.Once
	inheritTransport     *http.Transport
)

func SharedInheritTransport() *http.Transport {
	inheritTransportOnce.Do(func() {
		transport, ok := http.DefaultTransport.(*http.Transport)
		if !ok || transport == nil {
			transport = &http.Transport{}
		} else {
			transport = transport.Clone()
		}
		tunePoolSettings(transport)
		inheritTransport = transport
	})
	return inheritTransport
}

// maxCachedTransports 限定缓存规模。没有上限时，每个不同的代理 URL
// （全局或按账号）都会让一个 http.Transport 永久存活，即便运维已不再使用该代理，
// 仍会占住其空闲连接与代理凭据。32 足以覆盖现实的账号数量，
// 同时为工作集保留复用。
const maxCachedTransports = 32

// transportEntry 是一个缓存槽：key 与 transport 一同存储，
// 这样淘汰时无需反向查找即可删除 map 条目。
type transportEntry struct {
	raw       string
	transport *http.Transport
}

// TransportCache 按代理设置记忆化 transport，让重复请求复用同一个连接池
// （以及 HTTP CONNECT 隧道），而不必再次拨号与握手。http.Transport 可安全并发使用。
// 它是有界 LRU：一旦超过 maxCachedTransports，就淘汰最近最少使用的条目
// （并关闭其空闲连接）。
type TransportCache struct {
	mu         sync.Mutex
	transports map[string]*list.Element
	order      *list.List // 最近最多使用者位于队首
}

// Get 返回 raw 对应的缓存 transport，惰性构建。raw 为空时返回 (nil, nil)，
// 让调用方沿用其现有/默认 transport（客户端默认已是 SharedInheritTransport，
// 见各 provider 的 NewClient）。
func (c *TransportCache) Get(raw string) (*http.Transport, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.transports == nil {
		c.transports = make(map[string]*list.Element, maxCachedTransports)
	}
	if c.order == nil {
		c.order = list.New()
	}
	if element, ok := c.transports[raw]; ok {
		c.order.MoveToFront(element)
		return element.Value.(*transportEntry).transport, nil
	}

	transport, err := NewTransport(raw)
	if err != nil {
		return nil, err
	}
	if transport == nil {
		return nil, nil
	}
	c.transports[raw] = c.order.PushFront(&transportEntry{raw: raw, transport: transport})
	c.evictLocked()
	return transport, nil
}

// evictLocked 淘汰最近最少使用的条目直到缓存回到容量内，
// 并关闭其空闲连接，让旧的代理隧道不再滞留。
func (c *TransportCache) evictLocked() {
	for c.order.Len() > maxCachedTransports {
		back := c.order.Back()
		if back == nil {
			return
		}
		entry := back.Value.(*transportEntry)
		c.order.Remove(back)
		delete(c.transports, entry.raw)
		entry.transport.CloseIdleConnections()
	}
}

// CloseIdleConnections 关闭所有缓存 transport 上的空闲连接。
func (c *TransportCache) CloseIdleConnections() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.order == nil {
		return
	}
	for element := c.order.Front(); element != nil; element = element.Next() {
		element.Value.(*transportEntry).transport.CloseIdleConnections()
	}
}
