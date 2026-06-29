package dnsprovider

import (
	"context"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	legotencent "github.com/go-acme/lego/v5/providers/dns/tencentcloud"
)

// tencentcloudProvider 适配腾讯云 DNS：DNS-01 挑战复用 lego v5 原生 provider（内嵌满足 challenge.Provider），
// 通配 A 记录 CRUD 需腾讯云 DNS SDK，本期未接入（见 EnsureWildcardA 注释）。
type tencentcloudProvider struct {
	challenge.Provider // 内嵌 lego 原生 tencentcloud provider，白得 Present/CleanUp
}

// 编译期断言：tencentcloudProvider 满足 Provider 接口。
var _ Provider = (*tencentcloudProvider)(nil)

// newTencentcloud 校验必填凭证并装配 lego 原生 tencentcloud DNS-01 provider。
// 凭证字段：secret_id（SecretID）、secret_key（SecretKey）。
func newTencentcloud(creds Credentials) (Provider, error) {
	secretID := creds["secret_id"]
	secretKey := creds["secret_key"]
	if secretID == "" || secretKey == "" {
		return nil, fmt.Errorf("tencentcloud 凭证缺少 secret_id 或 secret_key")
	}
	cfg := legotencent.NewDefaultConfig()
	// Config.SecretID / SecretKey 均为 lego tencentcloud Config 字段。
	cfg.SecretID = secretID
	cfg.SecretKey = secretKey
	p, err := legotencent.NewDNSProviderConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("装配 tencentcloud DNS-01 provider 失败: %w", err)
	}
	return &tencentcloudProvider{Provider: p}, nil
}

// EnsureWildcardA 通配 A 记录管理需腾讯云 DNS SDK（CreateRecordBatch/DescribeRecordList），
// 本期未接入；真实装配与本地联调见 Plan 1 §10 计划阶段细化点。
func (t *tencentcloudProvider) EnsureWildcardA(_ context.Context, baseDomain, ip string) error {
	return fmt.Errorf("tencentcloud 通配 A 记录管理待实现（base=%s ip=%s）", baseDomain, ip)
}

// DeleteWildcardA 同 EnsureWildcardA，待接入腾讯云 DNS SDK。
func (t *tencentcloudProvider) DeleteWildcardA(_ context.Context, baseDomain string) error {
	return fmt.Errorf("tencentcloud 通配 A 记录删除待实现（base=%s）", baseDomain)
}
