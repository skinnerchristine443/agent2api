package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agent2api/internal/auth"
)

func newTestThrottle(now *time.Time) *consoleThrottle {
	return &consoleThrottle{buckets: map[string]*consoleBucket{}, now: func() time.Time { return *now }}
}

func TestConsoleThrottleBacksOffAfterCapacity(t *testing.T) {
	now := time.Unix(0, 0)
	throttle := newTestThrottle(&now)

	for attempt := 1; attempt <= consoleThrottleCapacity; attempt++ {
		if _, limited := throttle.allow("10.0.0.1"); limited {
			t.Fatalf("attempt %d was limited too early", attempt)
		}
	}
	wait, limited := throttle.allow("10.0.0.1")
	if !limited || wait <= 0 {
		t.Fatalf("attempt %d: limited=%v wait=%v, want a back-off", consoleThrottleCapacity+1, limited, wait)
	}

	// 另一个地址保有各自的 bucket。
	if _, limited := throttle.allow("10.0.0.2"); limited {
		t.Fatal("a different address must not be affected")
	}

	// token 随时间回填。
	now = now.Add(consoleThrottleRefill)
	if _, limited := throttle.allow("10.0.0.1"); limited {
		t.Fatal("a refilled bucket must allow again")
	}
}

// 轮换地址的攻击者不得让 map 无限增长。
func TestConsoleThrottleEvictsIdleBuckets(t *testing.T) {
	now := time.Unix(0, 0)
	throttle := newTestThrottle(&now)
	throttle.allow("10.0.0.1")

	now = now.Add(consoleBucketTTL + time.Minute)
	throttle.allow("10.0.0.2")

	throttle.mu.Lock()
	defer throttle.mu.Unlock()
	if _, ok := throttle.buckets["10.0.0.1"]; ok {
		t.Fatal("an idle bucket must be evicted")
	}
}

// nil 限流器放行一切：未构建限流器的 Server 仍可工作。
func TestNilThrottleAllows(t *testing.T) {
	var throttle *consoleThrottle
	if _, limited := throttle.allow("10.0.0.1"); limited {
		t.Fatal("a nil throttle must never limit")
	}
}

func consoleRequest(path, remote, key string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = remote
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	return request
}

// 核心要点：成功的 console 流量从不限流，失败的 console
// 认证才限流，且数据面不受影响。
func TestConsoleKeyThrottlesOnlyFailedAttempts(t *testing.T) {
	s := New(Server{Auth: auth.NewVerifier("console-secret", "console-secret")})
	reached := 0
	handler := s.withConsoleKey(func(w http.ResponseWriter, r *http.Request) {
		reached++
		w.WriteHeader(http.StatusOK)
	})
	call := func(request *http.Request) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler(recorder, request)
		return recorder
	}

	const authorized = consoleThrottleCapacity * 4
	for i := 0; i < authorized; i++ {
		if recorder := call(consoleRequest("/api/overview", "127.0.0.1:1234", "console-secret")); recorder.Code != http.StatusOK {
			t.Fatalf("authorized request %d = %d, want 200", i, recorder.Code)
		}
	}
	if reached != authorized {
		t.Fatalf("handler reached %d times, want %d", reached, authorized)
	}

	for attempt := 1; attempt <= consoleThrottleCapacity; attempt++ {
		if code := call(consoleRequest("/api/overview", "127.0.0.1:1234", "")).Code; code != http.StatusUnauthorized {
			t.Fatalf("failed attempt %d = %d, want 401", attempt, code)
		}
	}
	limited := call(consoleRequest("/api/overview", "127.0.0.1:1234", ""))
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt after the capacity = %d, want 429", limited.Code)
	}
	if limited.Header().Get("Retry-After") == "" {
		t.Fatal("a throttled response must advertise Retry-After")
	}
}

// 过期 key 绝不能把客户端锁在 API 之外：无论到达多少
// 次失败，数据面完全没有限流。
func TestDataPlaneIsNeverThrottled(t *testing.T) {
	s := New(Server{Auth: auth.NewVerifier("console-secret", "console-secret")})
	handler := s.withAPIKey(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	for i := 0; i < consoleThrottleCapacity*5; i++ {
		recorder := httptest.NewRecorder()
		handler(recorder, consoleRequest("/v1/models", "10.0.0.9:1234", ""))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401 (never 429)", i, recorder.Code)
		}
	}
}
