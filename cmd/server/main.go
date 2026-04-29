package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"oc-manager/internal/api"
	"oc-manager/internal/auth"
	"oc-manager/internal/config"
	"oc-manager/internal/service"
	"oc-manager/internal/store"
)

func main() {
	configPath := os.Getenv("OCM_CONFIG")
	if configPath == "" {
		configPath = "config/config.yaml"
	}

	cfg, err := config.LoadFile(configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	ctx := context.Background()
	dbStore, err := store.Open(ctx, cfg.Database.URL)
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	defer dbStore.Close()

	tokenManager, err := auth.NewTokenManager(
		cfg.Auth.JWTAccessSecret,
		cfg.Auth.JWTRefreshSecret,
		cfg.Auth.AccessTokenTTL.Duration,
		cfg.Auth.RefreshTokenTTL.Duration,
	)
	if err != nil {
		log.Fatalf("初始化认证令牌管理器失败: %v", err)
	}
	authService := service.NewAuthService(dbStore.Queries, tokenManager)

	server := &http.Server{
		Addr:              cfg.App.HTTPAddr,
		Handler:           api.NewRouter(api.Dependencies{AuthService: authService, TokenManager: tokenManager}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("manager api listening on %s", cfg.App.HTTPAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("启动 HTTP 服务失败: %v", err)
	}
}
