package dnsprovider

import (
	"context"
	"fmt"
)

// cmcccloudProvider 是移动云 provider 的占位实现：lego 无原生移动云支持，需 vendor certimate 的
// cmcccloud challenger + fork 的 ecloud SDK（spec §2.1/§6/§10）。该 vendoring 是独立的大体量
// 外部代码搬运，本期未实现，仅保留骨架使工厂分发完整。
type cmcccloudProvider struct{}

// 编译期断言：cmcccloudProvider 满足 Provider 接口。
var _ Provider = (*cmcccloudProvider)(nil)

// newCmcccloud 校验凭证并返回占位 provider；真实 DNS-01 与 A 记录待 vendor certimate 后接入。
// 凭证字段（预留）：access_key、secret_key。
func newCmcccloud(creds Credentials) (Provider, error) {
	if creds["access_key"] == "" || creds["secret_key"] == "" {
		return nil, fmt.Errorf("cmcccloud 凭证缺少 access_key 或 secret_key")
	}
	return &cmcccloudProvider{}, nil
}

// Present 占位：lego 无原生移动云，DNS-01 挑战待 vendor certimate 实现后接入。
func (c *cmcccloudProvider) Present(_ context.Context, _, _, _ string) error {
	return fmt.Errorf("cmcccloud DNS-01 Present 待 vendor certimate 实现")
}

// CleanUp 占位：同 Present，待 vendor certimate 实现后接入。
func (c *cmcccloudProvider) CleanUp(_ context.Context, _, _, _ string) error {
	return fmt.Errorf("cmcccloud DNS-01 CleanUp 待 vendor certimate 实现")
}

// EnsureWildcardA 占位：移动云 A 记录管理依赖 ecloud SDK（fork），本期未 vendor，待接入。
func (c *cmcccloudProvider) EnsureWildcardA(_ context.Context, _, _ string) error {
	return fmt.Errorf("cmcccloud 通配 A 记录管理待 vendor certimate 实现")
}

// DeleteWildcardA 占位：同 EnsureWildcardA，待 vendor certimate 实现后接入。
func (c *cmcccloudProvider) DeleteWildcardA(_ context.Context, _ string) error {
	return fmt.Errorf("cmcccloud 通配 A 记录删除待 vendor certimate 实现")
}
