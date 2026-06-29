// Command site-server 是公网静态站点服务：按 Host 路由到对象存储前缀流式返回文件。
// 无状态、只读单一 bucket/前缀；注册表通过轮询 manager 内部端点刷新。
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"oc-manager/internal/integrations/storage"
	"oc-manager/internal/siteserver"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	listenAddr := envOr("SITE_SERVER_LISTEN", ":80")
	s3cfg := storage.S3Config{
		Endpoint:        os.Getenv("S3_ENDPOINT"),
		Region:          envOr("S3_REGION", "us-east-1"),
		Bucket:          os.Getenv("S3_BUCKET"),
		AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
		UsePathStyle:    os.Getenv("S3_USE_PATH_STYLE") == "true",
	}
	syncURL := os.Getenv("MANAGER_SYNC_URL")
	syncToken := os.Getenv("MANAGER_SYNC_TOKEN")
	interval := 5 * time.Second
	if v := os.Getenv("SYNC_INTERVAL_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			interval = time.Duration(n) * time.Second
		}
	}

	store := storage.NewS3ObjectStore(s3cfg)
	registry := siteserver.NewRegistry()
	handler := siteserver.NewHandler(registry, store)
	syncer := siteserver.NewSyncer(siteserver.NewHTTPSiteListClient(syncURL, syncToken), registry, interval)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go syncer.Run(ctx, func(err error) { logger.Warn("站点注册表同步失败，保留旧快照", "err", err) })

	srv := &http.Server{Addr: listenAddr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	logger.Info("site-server 启动", "addr", listenAddr, "sync_url", syncURL, "interval", interval.String())
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("site-server 退出", "err", err)
		os.Exit(1)
	}
}

// envOr 读取环境变量，空则返回默认值。
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
