package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"oc-manager/runtime/agent/config"
)

// captureHBLogger 累计三类日志调用次数，便于断言失败计数到阈值时打 ERROR。
type captureHBLogger struct {
	mu     sync.Mutex
	infos  int
	warns  int
	errors int
}

func (l *captureHBLogger) Infof(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infos++
}
func (l *captureHBLogger) Warnf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns++
}
func (l *captureHBLogger) Errorf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors++
}
func (l *captureHBLogger) errorCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.errors
}
func (l *captureHBLogger) warnCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.warns
}

func TestHeartbeat_NotStartedWhenManagerEmpty(t *testing.T) {
	cfg := config.Config{
		Heartbeat: config.HeartbeatConfig{IntervalSeconds: 30, FailureLogThreshold: 5},
	}
	hb := newHeartbeat(cfg)
	if hb.shouldStart() {
		t.Fatal("manager 三字段全空时不应启动 heartbeat")
	}
}

func TestHeartbeat_PeriodicPost(t *testing.T) {
	var hits atomic.Int32
	var lastBodyMu sync.Mutex
	var lastBody []byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agent/runtime-nodes/node-x/heartbeat" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok-x" {
			t.Errorf("auth header = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		lastBodyMu.Lock()
		lastBody = body
		lastBodyMu.Unlock()
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.Config{
		Manager: config.ManagerConfig{
			Endpoint:   srv.URL + "/api/v1",
			NodeID:     "node-x",
			AgentToken: "tok-x",
			SkipVerify: true,
		},
		Heartbeat: config.HeartbeatConfig{IntervalSeconds: 30, FailureLogThreshold: 5},
	}
	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		Timeout:   2 * time.Second,
	}
	hb := newHeartbeat(cfg, withTickInterval(40*time.Millisecond), withHTTPClient(client))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		hb.Run(ctx)
		close(done)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && hits.Load() < 3 {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	if got := hits.Load(); got < 3 {
		t.Fatalf("expected ≥3 heartbeats, got %d", got)
	}

	lastBodyMu.Lock()
	body := append([]byte(nil), lastBody...)
	lastBodyMu.Unlock()
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body json: %v; raw=%s", err, body)
	}
	if _, ok := decoded["agent_version"]; !ok {
		t.Errorf("body 缺少 agent_version 字段")
	}
	if _, ok := decoded["resource_snapshot"]; !ok {
		t.Errorf("body 缺少 resource_snapshot 字段")
	}
}

func TestHeartbeat_FailureLogThreshold(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := config.Config{
		Manager: config.ManagerConfig{
			Endpoint:   srv.URL + "/api/v1",
			NodeID:     "node-x",
			AgentToken: "tok-x",
			SkipVerify: true,
		},
		Heartbeat: config.HeartbeatConfig{IntervalSeconds: 30, FailureLogThreshold: 3},
	}
	logger := &captureHBLogger{}
	client := &http.Client{Timeout: 1 * time.Second}
	hb := newHeartbeat(cfg, withTickInterval(20*time.Millisecond), withHTTPClient(client), withLogger(logger))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		hb.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && logger.errorCount() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	if logger.errorCount() == 0 {
		t.Fatalf("连续失败到阈值应触发 ERROR 日志（warns=%d）", logger.warnCount())
	}
}

func TestHeartbeat_CancelStopsGoroutine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.Config{
		Manager: config.ManagerConfig{
			Endpoint:   srv.URL + "/api/v1",
			NodeID:     "node-x",
			AgentToken: "tok-x",
			SkipVerify: true,
		},
		Heartbeat: config.HeartbeatConfig{IntervalSeconds: 30, FailureLogThreshold: 5},
	}
	hb := newHeartbeat(cfg, withTickInterval(50*time.Millisecond), withHTTPClient(srv.Client()))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		hb.Run(ctx)
		close(done)
	}()
	time.Sleep(80 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancel ctx 后 goroutine 应在 1 秒内退出")
	}
}
