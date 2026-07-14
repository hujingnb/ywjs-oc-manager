package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

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

// AICCDispatchObserver 接收 dispatcher 生命周期事件，便于接入项目既有 slog 或测试接收端。
type AICCDispatchObserver interface {
	Observe(context.Context, AICCDispatchObservation)
}

// SlogAICCDispatchObserver 把 dispatcher 事件输出为结构化日志，不额外引入监控依赖。
type SlogAICCDispatchObserver struct {
	// logger 复用服务已配置的脱敏结构化日志出口。
	logger *slog.Logger
}

// NewSlogAICCDispatchObserver 创建复用服务顶层 logger 的安全观测器。
func NewSlogAICCDispatchObserver(logger *slog.Logger) *SlogAICCDispatchObserver {
	if logger == nil {
		logger = slog.Default()
	}
	return &SlogAICCDispatchObserver{logger: logger}
}

// Observe 输出固定字段集合，避免调用方意外把请求内容等敏感信息作为日志标签传入。
func (o *SlogAICCDispatchObserver) Observe(ctx context.Context, event AICCDispatchObservation) {
	if o == nil || o.logger == nil {
		return
	}
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
