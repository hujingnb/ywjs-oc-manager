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

	// 必填配置校验（fail-fast）：缺这三项会让服务"看似启动"却永远不可用——
	// 空 S3 端点/桶无法读取任何对象，空同步 URL 会让每轮同步静默失败、注册表恒空、
	// 所有请求 404，线上极难诊断。故启动即退出，逼运维补齐配置。
	if syncURL == "" || s3cfg.Endpoint == "" || s3cfg.Bucket == "" {
		logger.Error("缺少必填配置",
			"MANAGER_SYNC_URL", syncURL,
			"S3_ENDPOINT", s3cfg.Endpoint,
			"S3_BUCKET", s3cfg.Bucket)
		os.Exit(1)
	}
	// 同步 token 缺失只告警不退出：本地联调的 manager 端点可能未启用鉴权。
	if syncToken == "" {
		logger.Warn("MANAGER_SYNC_TOKEN 为空，同步请求将不带鉴权头（仅限无鉴权环境）")
	}

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
