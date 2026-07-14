package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	managerlog "oc-manager/internal/log"
)

// AICCDispatchObservation 是异步客服消息的安全观测载荷。
// 仅保留智能体、企业、上游和结果四类标签；不允许承载访客原文、会话标识或任何令牌。
type AICCDispatchObservation struct {
	// Event 表示固定生命周期阶段，如 queued、retry、completed 或 circuit_open。
	Event string
	// AgentID 是智能体归属标签，用于按客服配置聚合任务健康度。
	AgentID string
	// OrgID 是企业归属标签，用于在多租户之间定位排队或故障范围。
	OrgID string
	// Upstream 是稳定上游名称；当前异步回复统一为 hermes。
	Upstream string
	// Result 是固定结果枚举，不承载动态错误文本。
	Result string
	// QueueWaitMS 是任务从创建到本轮扫描的等待时长，仅作为数值观测值输出。
	QueueWaitMS int64
	// Inflight 是当前循环已占用的并发槽位数，仅作为数值观测值输出。
	Inflight int
}

// AICCDispatchMetricSnapshot 是可由现有日志/监控桥接层读取的安全指标快照。
// key 由固定指标名和低基数标签组成，不包含访客内容、会话或消息原文。
type AICCDispatchMetricSnapshot struct {
	Counters    map[string]uint64
	QueueWaitMS int64
	Inflight    int64
}

// AICCDispatchMetrics 是最小的指标出口；项目尚未部署 Prometheus，因此保持为可替换接口。
type AICCDispatchMetrics interface {
	Observe(AICCDispatchObservation)
	RecordQueue(depth int, queueWaitMS int64)
	RecordInflight(inflight int)
	Snapshot() AICCDispatchMetricSnapshot
}

// InMemoryAICCDispatchMetrics 保存进程内可读取指标，供日志桥接或未来监控适配器安全导出。
type InMemoryAICCDispatchMetrics struct {
	mu          sync.Mutex
	counters    map[string]uint64
	queueWaitMS int64
	inflight    int64
}

// NewInMemoryAICCDispatchMetrics 创建零依赖的指标注册表。
func NewInMemoryAICCDispatchMetrics() *InMemoryAICCDispatchMetrics {
	return &InMemoryAICCDispatchMetrics{counters: make(map[string]uint64)}
}

// Observe 记录生命周期计数、排队等待累计值和当前在飞值。
func (m *InMemoryAICCDispatchMetrics) Observe(event AICCDispatchObservation) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	name := "aicc_message_transitions_total"
	switch event.Event {
	case "retry":
		name = "aicc_message_retries_total"
	case "failed":
		name = "aicc_message_failures_total"
	case "circuit_open":
		name = "aicc_message_circuit_open_total"
	case "lease_recovered":
		name = "aicc_message_lease_recoveries_total"
	}
	m.counters[fmt.Sprintf("%s{org=%q,agent=%q,upstream=%q,result=%q}", name, event.OrgID, event.AgentID, event.Upstream, event.Result)]++
	m.queueWaitMS += event.QueueWaitMS
	if int64(event.Inflight) > m.inflight {
		m.inflight = int64(event.Inflight)
	}
}

// RecordQueue 记录本轮扫描的就绪任务深度和累计等待时长，不将扫描本身视作生命周期转换。
func (m *InMemoryAICCDispatchMetrics) RecordQueue(depth int, queueWaitMS int64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters["aicc_message_queue_depth"] = uint64(depth)
	m.queueWaitMS += queueWaitMS
}

// RecordInflight 更新当前在飞调用数，使用 gauge 语义而非累计计数。
func (m *InMemoryAICCDispatchMetrics) RecordInflight(inflight int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inflight = int64(inflight)
}

// Snapshot 返回独立副本，调用方不能修改注册表内部状态。
func (m *InMemoryAICCDispatchMetrics) Snapshot() AICCDispatchMetricSnapshot {
	if m == nil {
		return AICCDispatchMetricSnapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	counters := make(map[string]uint64, len(m.counters))
	for key, value := range m.counters {
		counters[key] = value
	}
	return AICCDispatchMetricSnapshot{Counters: counters, QueueWaitMS: m.queueWaitMS, Inflight: m.inflight}
}

// AICCDispatchObserver 接收 dispatcher 生命周期事件，便于接入项目既有 slog 或测试接收端。
type AICCDispatchObserver interface {
	Observe(context.Context, AICCDispatchObservation)
}

// SlogAICCDispatchObserver 把 dispatcher 事件输出为结构化日志，不额外引入监控依赖。
type SlogAICCDispatchObserver struct {
	// logger 复用服务已配置的脱敏结构化日志出口。
	logger  *slog.Logger
	metrics AICCDispatchMetrics
}

// NewSlogAICCDispatchObserver 创建复用服务顶层 logger 的安全观测器。
func NewSlogAICCDispatchObserver(logger *slog.Logger) *SlogAICCDispatchObserver {
	if logger == nil {
		logger = slog.Default()
	}
	return &SlogAICCDispatchObserver{logger: logger, metrics: NewInMemoryAICCDispatchMetrics()}
}

// Metrics 返回可替换的进程内指标快照，用于告警桥接而不要求引入新的监控平台。
func (o *SlogAICCDispatchObserver) Metrics() AICCDispatchMetricSnapshot {
	if o == nil {
		return AICCDispatchMetricSnapshot{}
	}
	return o.metrics.Snapshot()
}

// RecordQueueMetrics 将扫描值写入指标，不生成 queued 日志事件，避免 backlog 时每秒刷屏。
func (o *SlogAICCDispatchObserver) RecordQueueMetrics(depth int, queueWaitMS int64) {
	if o != nil && o.metrics != nil {
		o.metrics.RecordQueue(depth, queueWaitMS)
	}
}

// RecordInflightMetrics 将并发槽位写入 gauge，不伪造新的任务状态转换。
func (o *SlogAICCDispatchObserver) RecordInflightMetrics(inflight int) {
	if o != nil && o.metrics != nil {
		o.metrics.RecordInflight(inflight)
	}
}

// Observe 输出固定字段集合，避免调用方意外把请求内容等敏感信息作为日志标签传入。
func (o *SlogAICCDispatchObserver) Observe(ctx context.Context, event AICCDispatchObservation) {
	if o == nil || o.logger == nil {
		return
	}
	o.metrics.Observe(event)
	o.logger.InfoContext(ctx, "AICC 异步消息任务事件",
		slog.String("aicc_event", event.Event),
		slog.String("agent_id", event.AgentID),
		slog.String(managerlog.KeyOrgID, event.OrgID),
		slog.String("upstream", event.Upstream),
		slog.String("result", event.Result),
		slog.Int64("queue_wait_ms", event.QueueWaitMS),
		slog.Int("inflight", event.Inflight),
	)
}

// AICCSafeDispatchResult 将可能含有请求上下文的错误归并为低基数结果码，供 worker 安全记录。
func AICCSafeDispatchResult(err error) string {
	var status *AICCUpstreamStatusError
	if errors.As(err, &status) {
		return fmt.Sprintf("dispatch_http_%d", status.StatusCode)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "dispatch_timeout"
	}
	return "dispatch_runtime_error"
}
