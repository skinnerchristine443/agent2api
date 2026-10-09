package proxy

import "testing"

// Go 的默认值（MaxIdleConnsPerHost = 2）是按浏览器规模设定的，不适合
// 把同一个上游主机扇出到多条并发流的网关：连接会被反复拆除并重新拨号
// （并重跑代理握手）。因此这些上限必须被显式固定。
func TestNewTransportPinsConnectionPoolLimits(t *testing.T) {
	transport, err := NewTransport("direct")
	if err != nil {
		t.Fatal(err)
	}
	if transport.MaxIdleConns != maxIdleConns {
		t.Fatalf("MaxIdleConns = %d, want %d", transport.MaxIdleConns, maxIdleConns)
	}
	if transport.MaxIdleConnsPerHost != maxIdleConnsPerHost {
		t.Fatalf("MaxIdleConnsPerHost = %d, want %d", transport.MaxIdleConnsPerHost, maxIdleConnsPerHost)
	}
	if transport.IdleConnTimeout != idleConnTimeout {
		t.Fatalf("IdleConnTimeout = %v, want %v", transport.IdleConnTimeout, idleConnTimeout)
	}
	if transport.ResponseHeaderTimeout != responseHeaderTimeout {
		t.Fatalf("ResponseHeaderTimeout = %v, want %v", transport.ResponseHeaderTimeout, responseHeaderTimeout)
	}
}

// inherit 设置意为「沿用调用方自己的 transport」：它必须保持 nil，
// 这样调用方不会悄悄采用一个运维从未要求的连接池。
func TestInheritTransportStaysNil(t *testing.T) {
	transport, err := NewTransport("")
	if err != nil {
		t.Fatal(err)
	}
	if transport != nil {
		t.Fatalf("inherit returned %+v, want nil", transport)
	}
}
