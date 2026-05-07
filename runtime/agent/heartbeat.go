package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strings"
	"time"

	"oc-manager/runtime/agent/config"
)

// agentVersion 由 build 时通过 -ldflags 注入；未注入时记录为 "dev"，便于 manager 端识别开发态。
var agentVersion = "dev"

// hbLogger 抽象心跳日志输出，便于测试中捕获不同级别的日志条数。
type hbLogger interface {
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}

// stdHBLogger 把心跳日志写到默认 log 包，符合现有 agent 进程日志风格。
type stdHBLogger struct{}

func (stdHBLogger) Infof(format string, args ...any)  { log.Printf("INFO heartbeat "+format, args...) }
func (stdHBLogger) Warnf(format string, args ...any)  { log.Printf("WARN heartbeat "+format, args...) }
func (stdHBLogger) Errorf(format string, args ...any) { log.Printf("ERROR heartbeat "+format, args...) }

// heartbeat 在 agent 进程内周期主动 POST 到 manager，触发节点 unreachable→active 自愈。
// shouldStart 不满足时（manager 三字段全空）Run 立即返回，避免空跑。
type heartbeat struct {
	cfg          config.Config
	client       *http.Client
	tickInterval time.Duration
	logger       hbLogger
	failures     int
}

type heartbeatOption func(*heartbeat)

// withTickInterval 仅供测试覆盖时间间隔；生产路径用 cfg.Heartbeat.IntervalSeconds。
func withTickInterval(d time.Duration) heartbeatOption {
	return func(h *heartbeat) { h.tickInterval = d }
}

// withHTTPClient 注入自定义 http.Client，让测试用 httptest.NewTLSServer 的 client 跑。
func withHTTPClient(c *http.Client) heartbeatOption {
	return func(h *heartbeat) { h.client = c }
}

// withLogger 替换默认 logger，便于测试断言 ERROR 触发次数。
func withLogger(l hbLogger) heartbeatOption {
	return func(h *heartbeat) { h.logger = l }
}

// newHeartbeat 用配置 + 选项构造心跳器。
// 默认 tickInterval = cfg.Heartbeat.IntervalSeconds 秒；测试可用 option 缩短到毫秒级。
func newHeartbeat(cfg config.Config, opts ...heartbeatOption) *heartbeat {
	hb := &heartbeat{
		cfg:          cfg,
		tickInterval: time.Duration(cfg.Heartbeat.IntervalSeconds) * time.Second,
		logger:       stdHBLogger{},
	}
	for _, o := range opts {
		o(hb)
	}
	if hb.client == nil {
		hb.client = buildHeartbeatClient(cfg.Manager)
	}
	if hb.tickInterval <= 0 {
		hb.tickInterval = 30 * time.Second
	}
	return hb
}

// shouldStart 决定 Run 是否进入主循环。三字段全空表示 ops 还未把 register 响应回填到 yaml。
func (h *heartbeat) shouldStart() bool {
	m := h.cfg.Manager
	return m.Endpoint != "" && m.NodeID != "" && m.AgentToken != ""
}

// Run 阻塞执行心跳主循环；ctx 取消即退出。启动后立刻发一次，避免等满间隔。
func (h *heartbeat) Run(ctx context.Context) {
	if !h.shouldStart() {
		h.logger.Infof("manager 段未配置完整，跳过心跳；待 ops 在 yaml 填齐 endpoint/node_id/agent_token 后重启 agent")
		return
	}
	t := time.NewTicker(h.tickInterval)
	defer t.Stop()
	h.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.tick(ctx)
		}
	}
}

// tick 发起一次心跳；HTTP 状态非 2xx 视为失败并累加 failures。
func (h *heartbeat) tick(ctx context.Context) {
	url := strings.TrimRight(h.cfg.Manager.Endpoint, "/") + "/agent/runtime-nodes/" + h.cfg.Manager.NodeID + "/heartbeat"
	body := map[string]any{
		"agent_version":     agentVersion,
		"resource_snapshot": collectSnapshot(),
	}
	buf, err := json.Marshal(body)
	if err != nil {
		h.recordFailure(fmt.Errorf("序列化 body 失败: %w", err))
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		h.recordFailure(fmt.Errorf("构造请求失败: %w", err))
		return
	}
	req.Header.Set("Authorization", "Bearer "+h.cfg.Manager.AgentToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		h.recordFailure(fmt.Errorf("HTTP 请求失败: %w", err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		h.recordFailure(fmt.Errorf("manager 返回非 2xx: %d", resp.StatusCode))
		return
	}
	if h.failures > 0 {
		h.logger.Infof("心跳已恢复，连续失败计数清零（之前 %d 次）", h.failures)
	}
	h.failures = 0
}

// recordFailure 累加失败次数并按阈值升级日志级别。
func (h *heartbeat) recordFailure(err error) {
	h.failures++
	h.logger.Warnf("心跳失败: %v（连续 %d 次）", err, h.failures)
	if h.failures == h.cfg.Heartbeat.FailureLogThreshold {
		h.logger.Errorf("心跳连续失败已达 %d 次阈值，请检查 manager 可达性", h.failures)
	}
}

// collectSnapshot 收集本进程粗粒度资源信息。
// 第一版只放 goroutine 数与堆分配字节，避免引入 cgo 依赖；后续可扩展容器数量等节点级指标。
func collectSnapshot() map[string]any {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return map[string]any{
		"goroutines":      runtime.NumGoroutine(),
		"mem_alloc_bytes": ms.Alloc,
		"now":             time.Now().UTC().Format(time.RFC3339),
	}
}

// buildHeartbeatClient 按 manager TLS 配置构造默认 http.Client。
// CABundle 非空时只信任该 CA；SkipVerify 仅本地调试用。
func buildHeartbeatClient(mgr config.ManagerConfig) *http.Client {
	tlsCfg := &tls.Config{InsecureSkipVerify: mgr.SkipVerify}
	if mgr.CABundle != "" {
		pool := x509.NewCertPool()
		if pool.AppendCertsFromPEM([]byte(mgr.CABundle)) {
			tlsCfg.RootCAs = pool
		}
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}
}
