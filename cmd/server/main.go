package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"oc-manager/internal/api"
	"oc-manager/internal/config"
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

	server := &http.Server{
		Addr:              cfg.App.HTTPAddr,
		Handler:           api.NewRouter(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("manager api listening on %s", cfg.App.HTTPAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("启动 HTTP 服务失败: %v", err)
	}
}
