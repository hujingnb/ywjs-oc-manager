package k8sorch

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

// AICCExternalMetricConfig 描述 external.metrics.k8s.io 中的一项 AICC 业务 gauge。
// 该指标必须由集群已安装的 adapter 按隐藏应用标签返回；manager 不把受保护的管理 JSON
// 指标端点直接暴露给 HPA，避免 Kubernetes 控制器依赖用户令牌或非标准 API。
type AICCExternalMetricConfig struct {
	// Name 是 external metrics API 已注册的指标名。
	Name string
	// TargetAverageValue 是每个 AICC Pod 可承担的平均业务负载阈值。
	TargetAverageValue resource.Quantity
}

// AICCBusinessMetricsConfig 是 AICC HPA 可选的外部业务指标合同。
// Provider 仅记录部署者配置的 adapter 身份，实际查询仍由 Kubernetes HPA 经
// external.metrics.k8s.io 完成；AppLabel 将每个 HPA 限定到自己的隐藏应用。
type AICCBusinessMetricsConfig struct {
	Provider   string
	AppLabel   string
	QueueDepth AICCExternalMetricConfig
	Inflight   AICCExternalMetricConfig
}

// Enabled 仅在队列深度和在飞 gauge 都完整时启用，避免半配置导致 HPA 只依据不完整信号扩缩。
func (c AICCBusinessMetricsConfig) Enabled() bool {
	return strings.TrimSpace(c.Provider) != "" && strings.TrimSpace(c.AppLabel) != "" &&
		strings.TrimSpace(c.QueueDepth.Name) != "" && c.QueueDepth.TargetAverageValue.Sign() > 0 &&
		strings.TrimSpace(c.Inflight.Name) != "" && c.Inflight.TargetAverageValue.Sign() > 0
}

// NewAICCBusinessMetricsConfig 把已通过配置层校验的字符串阈值转换为 Kubernetes quantity。
// 仍返回错误，确保其他装配入口无法把非法 quantity 传进 HPA 渲染逻辑而触发 panic。
func NewAICCBusinessMetricsConfig(provider, appLabel, queueName, queueTarget, inflightName, inflightTarget string) (AICCBusinessMetricsConfig, error) {
	queueValue, err := resource.ParseQuantity(queueTarget)
	if err != nil {
		return AICCBusinessMetricsConfig{}, fmt.Errorf("解析 AICC 队列深度 HPA 阈值: %w", err)
	}
	inflightValue, err := resource.ParseQuantity(inflightTarget)
	if err != nil {
		return AICCBusinessMetricsConfig{}, fmt.Errorf("解析 AICC 在飞 HPA 阈值: %w", err)
	}
	return AICCBusinessMetricsConfig{
		Provider:   provider,
		AppLabel:   appLabel,
		QueueDepth: AICCExternalMetricConfig{Name: queueName, TargetAverageValue: queueValue},
		Inflight:   AICCExternalMetricConfig{Name: inflightName, TargetAverageValue: inflightValue},
	}, nil
}
