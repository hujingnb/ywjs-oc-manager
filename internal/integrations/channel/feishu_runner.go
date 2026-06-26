package channel

import (
	"context"

	"oc-manager/internal/integrations/ocops"
)

// ocopsFeishuClient 窄接口：飞书注册 SSE。
// 仅声明 runner 真正用到的方法，便于在 main 包用 *ocops.Client 直接满足，
// 同时让 channel 包不依赖整个 ocops.Client 的方法集合。
// 手填凭证的 probe 校验已挪到 worker 阶段1（彼时有 per-app oc-ops 坐标），不再由本包发起。
type ocopsFeishuClient interface {
	FeishuRegister(ctx context.Context, ep ocops.Endpoint, domain string) (<-chan ocops.FeishuRegisterEvent, error)
}

// OcOpsFeishuRunner 用 input.Endpoint 把注册 SSE 路由到目标 app 实例。
type OcOpsFeishuRunner struct{ ops ocopsFeishuClient }

// NewOcOpsFeishuRunner 创建扫码注册 runner。
func NewOcOpsFeishuRunner(ops ocopsFeishuClient) *OcOpsFeishuRunner { return &OcOpsFeishuRunner{ops: ops} }

// StreamFeishuRegister 按 input.Endpoint 寻址目标实例并触发飞书扫码注册 SSE。
func (r *OcOpsFeishuRunner) StreamFeishuRegister(ctx context.Context, input AuthInput, domain string) (<-chan ocops.FeishuRegisterEvent, error) {
	return r.ops.FeishuRegister(ctx, input.Endpoint, domain)
}
