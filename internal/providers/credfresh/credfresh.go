// Package credfresh 合并单个账号的并发凭据刷新。
//
// 会 ROTATE refresh token 的凭据端点使未合并的刷新变得危险：N 个并发刷新
// 会持久化 N 个各不相同的 token，而后来的写入会使已经发出的那些失效——
// 从而损坏账号。一个 Group 对每个 key 保持仅一次刷新在途，并把在其运行
// 期间到达的每个调用方赋予同一值，或同一错误。
package credfresh

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Group 按 key 合并调用。零值即可直接使用。
//
// Group 首次使用后不得被复制。
type Group[V any] struct {
	mu      sync.Mutex
	flights map[string]*flight[V]
}

type flight[V any] struct {
	done  chan struct{}
	value V
	err   error
}

// Do 在有调用在途时对每个 key 只运行一次 fn。在调用运行期间到达的调用方
// 会等待它，而不是启动自己的调用。
//
// fn 在某个 goroutine 上运行，其 context 与调用方 DETACHED，并由 timeout
// 限定，因此某个调用方断开连接不会使共享该刷新的其他所有人作废刷新。
// 放弃等待的调用方返回其自身的 context 错误，且不影响共享调用。
// 发生 panic 的 fn 会作为错误报告给每个等待者，而不是永远卡住该 key。
func (g *Group[V]) Do(ctx context.Context, key string, timeout time.Duration, fn func(context.Context) (V, error)) (V, error) {
	var zero V

	g.mu.Lock()
	if g.flights == nil {
		g.flights = map[string]*flight[V]{}
	}
	if pending := g.flights[key]; pending != nil {
		g.mu.Unlock()
		select {
		case <-pending.done:
			return pending.value, pending.err
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}
	current := &flight[V]{done: make(chan struct{})}
	g.flights[key] = current
	g.mu.Unlock()

	go func() {
		var value V
		var err error
		// 无条件地发布、注销并关闭：发生 panic 的 leader
		// 绝不能泄漏该条目并永远卡住该 key。
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("credential refresh panicked: %v", r)
			}
			current.value, current.err = value, err
			g.mu.Lock()
			delete(g.flights, key)
			g.mu.Unlock()
			close(current.done)
		}()

		runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()
		value, err = fn(runCtx)
	}()

	select {
	case <-current.done:
		return current.value, current.err
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

// VersionedStore 是凭据存储的一个可选能力：读取 payload 及其标识版本的
// 戳记，并且仅在该版本仍为最新时才写回。未实现它的 store
// 回退为无条件保存。
type VersionedStore interface {
	LoadCredentialPayloadWithVersion(ctx context.Context, accountID string) (format string, payload []byte, updatedAt string, err error)
	SaveCredentialPayloadIfUnchanged(ctx context.Context, accountID, format string, payload []byte, expectedUpdatedAt string) (written bool, err error)
}
