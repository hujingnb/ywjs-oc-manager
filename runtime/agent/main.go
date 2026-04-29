package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// HealthResponse 是 runtime agent 健康检查响应。
// 后续注册、心跳和文件 API 会复用该服务入口，因此这里先固定最小可观测字段。
type HealthResponse struct {
	Status    string `json:"status"`
	Role      string `json:"role"`
	DataRoot  string `json:"dataRoot"`
	Timestamp string `json:"timestamp"`
}

func main() {
	dataRoot := getenv("DATA_ROOT", "/var/lib/oc-agent")
	handler := newHandler(dataRoot)

	fileServer := &http.Server{
		Addr:              ":7002",
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	dockerProxyServer := &http.Server{
		Addr:              ":7001",
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go serve("file-api", fileServer)
	go serve("docker-proxy", dockerProxyServer)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = fileServer.Shutdown(ctx)
	_ = dockerProxyServer.Shutdown(ctx)
}

func newHandler(dataRoot string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, HealthResponse{
			Status:    "ok",
			Role:      "runtime-agent",
			DataRoot:  dataRoot,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/v1/files/ping", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})
	return mux
}

func serve(name string, server *http.Server) {
	log.Printf("%s listening on %s", name, server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("%s stopped unexpectedly: %v", name, err)
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("写入 JSON 响应失败: %v", err)
	}
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
