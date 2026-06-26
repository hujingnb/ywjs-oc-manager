package channel

import (
	"context"

	"oc-manager/internal/integrations/ocops"
)

// ocopsFeishuClient 窄接口：飞书注册 SSE 与手填校验。
// 仅声明 adapter 真正用到的两个方法，便于在 main 包用 *ocops.Client 直接满足，
// 同时让 channel 包不依赖整个 ocops.Client 的方法集合。
type ocopsFeishuClient interface {
	FeishuRegister(ctx context.Context, ep ocops.Endpoint, domain string) (<-chan ocops.FeishuRegisterEvent, error)
	FeishuProbe(ctx context.Context, ep ocops.Endpoint, appID, appSecret, domain string) (ocops.FeishuProbeResult, error)
}

// OcOpsFeishuRunner 用 input.Endpoint 把注册 SSE 路由到目标 app 实例。
type OcOpsFeishuRunner struct{ ops ocopsFeishuClient }

// NewOcOpsFeishuRunner 创建扫码注册 runner。
func NewOcOpsFeishuRunner(ops ocopsFeishuClient) *OcOpsFeishuRunner { return &OcOpsFeishuRunner{ops: ops} }

// StreamFeishuRegister 按 input.Endpoint 寻址目标实例并触发飞书扫码注册 SSE。
func (r *OcOpsFeishuRunner) StreamFeishuRegister(ctx context.Context, input AuthInput, domain string) (<-chan ocops.FeishuRegisterEvent, error) {
	return r.ops.FeishuRegister(ctx, input.Endpoint, domain)
}

// OcOpsFeishuProber 经 oc-ops 手填校验。
type OcOpsFeishuProber struct{ ops ocopsFeishuClient }

// NewOcOpsFeishuProber 创建手填校验器。
func NewOcOpsFeishuProber(ops ocopsFeishuClient) *OcOpsFeishuProber { return &OcOpsFeishuProber{ops: ops} }

// ProbeFeishu 调用 oc-ops 手填校验端点，返回校验结果与机器人身份。
func (p *OcOpsFeishuProber) ProbeFeishu(ctx context.Context, input AuthInput, appID, appSecret, domain string) (bool, string, string, error) {
	res, err := p.ops.FeishuProbe(ctx, input.Endpoint, appID, appSecret, domain)
	if err != nil {
		return false, "", "", err
	}
	return res.OK, res.BotName, res.BotOpenID, nil
}
