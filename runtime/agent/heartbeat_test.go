package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/runtime/agent/config"
)

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

// TestHeartbeat_NotStartedWhenTokenEmpty 验证心跳未Started当令牌空值的边界条件场景。
func TestHeartbeat_NotStartedWhenTokenEmpty(t *testing.T) {
	cfg := config.Config{Heartbeat: config.HeartbeatConfig{IntervalSeconds: 30, FailureLogThreshold: 5}}
	hb := newHeartbeat(cfg, "agent-1", func() string { return "" }, t.TempDir(), "host", "", t.TempDir(), ":7001", ":7002")
	require.False(t, hb.shouldStart())
}

// TestHeartbeat_PeriodicPost 验证心跳Periodic提交的预期行为场景。
func TestHeartbeat_PeriodicPost(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/agent/heartbeat", r.URL.Path)
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.Config{
		Manager: config.ManagerConfig{
			Endpoint:         srv.URL + "/api/v1",
			EnrollmentSecret: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			SkipVerify:       true,
		},
		Heartbeat: config.HeartbeatConfig{IntervalSeconds: 30, FailureLogThreshold: 5},
	}
	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		Timeout:   2 * time.Second,
	}
	token := "tok-x"
	hb := newHeartbeat(cfg, "agent-1", func() string { return token }, t.TempDir(), "host", "", t.TempDir(), ":7001", ":7002", withTickInterval(40*time.Millisecond), withHTTPClient(client))

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

	require.GreaterOrEqual(t, hits.Load(), int32(3))
}

// TestHeartbeat_FailureLogThreshold 验证心跳失败LogThreshold的预期行为场景。
func TestHeartbeat_FailureLogThreshold(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := config.Config{
		Manager: config.ManagerConfig{
			Endpoint:         srv.URL + "/api/v1",
			EnrollmentSecret: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		},
		Heartbeat: config.HeartbeatConfig{IntervalSeconds: 30, FailureLogThreshold: 3},
	}
	logger := &captureHBLogger{}
	token := "tok-x"
	hb := newHeartbeat(cfg, "agent-1", func() string { return token }, t.TempDir(), "host", "", t.TempDir(), ":7001", ":7002", withTickInterval(20*time.Millisecond), withHTTPClient(srv.Client()), withLogger(logger))

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

	require.NotZero(t, logger.errorCount())
}

// TestEnrollAgentUsesConfiguredName 验证注册agent使用配置的名称的预期行为场景。
func TestEnrollAgentUsesConfiguredName(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/agent/enroll", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		writeJSON(w, map[string]any{
			"node_id":                    "00000000-0000-0000-0000-000000000001",
			"agent_token":                "token-1",
			"heartbeat_interval_seconds": 30,
		})
	}))
	defer srv.Close()

	stateDir := t.TempDir()
	cfg := config.Config{
		Agent: config.AgentConfig{
			Name:          "local-agent-1",
			AdvertiseHost: "runtime-agent.local",
			MaxApps:       int32Ptr(3),
		},
		Manager: config.ManagerConfig{
			Endpoint:         srv.URL + "/api/v1",
			EnrollmentSecret: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		},
	}
	_, _, err := enrollAgent(context.Background(), cfg, "00000000-0000-0000-0000-00000000a001", cfg.Agent.Name, "container-host", "/data", stateDir, ":7001", ":7002", "test", "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n")
	require.NoError(t, err)
	require.Equal(t, "local-agent-1", got["name"])
	require.Equal(t, float64(3), got["max_apps"])
}

func int32Ptr(v int32) *int32 { return &v }
