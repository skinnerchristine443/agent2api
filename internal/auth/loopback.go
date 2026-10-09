package auth

import (
	"context"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"time"
)

// ServeLoopback 在一个已绑定的 loopback listener 上持有 HTTP 传输。
// Provider 回调只接收协议输入；错误保持 400 契约。
func ServeLoopback(ln net.Listener, path, title string, callback func(context.Context, string) error) {
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if err := callback(r.Context(), r.URL.String()); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, fmt.Sprintf(`<!doctype html><meta charset="utf-8"><title>%s login</title><p>Login complete. You can close this tab.</p>`, html.EscapeString(title)))
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(ln) }()
}
