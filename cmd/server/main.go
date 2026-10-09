package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"agent2api/internal/app"
	"agent2api/internal/config"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	application, err := app.New(cfg)
	if err != nil {
		log.Fatalf("startup failed: %v", err)
	}
	defer application.Close()
	// 拒绝在非 loopback 地址上暴露未鉴权的服务。
	// 非 loopback 绑定实际暴露的是数据面（/v1/*），
	// 它由 proxy key 守卫——因此该 key 就是此处的判据。
	// 该检查发生在 app.New 解析出密钥之后、开始服务之前。
	// 注：生产路径上 EnsureProxyAPIKey 保证密钥恒非空（生成或读库），
	// 故第二项实参当前恒为 true——本守卫是纵深防御：任何「空密钥 +
	// 非环回绑定」的回归都会在这里被拦下。
	if err := config.EnsureBindAuthPolicy(cfg.Host, application.Auth.ProxyKey() != ""); err != nil {
		log.Fatal(err)
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           application.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		log.Printf("agent2api listening on http://%s", addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http server failed: %v", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown failed: %v", err)
	}
}
