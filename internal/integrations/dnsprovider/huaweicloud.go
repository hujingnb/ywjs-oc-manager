package dnsprovider

import (
	"context"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	legohuawei "github.com/go-acme/lego/v5/providers/dns/huaweicloud"
)

// huaweicloudProvider 适配华为云 DNS：DNS-01 挑战复用 lego v5 原生 provider（内嵌满足 challenge.Provider），
// 通配 A 记录 CRUD 需华为云 DNS SDK，本期未接入（见 EnsureWildcardA 注释）。
type huaweicloudProvider struct {
	challenge.Provider // 内嵌 lego 原生 huaweicloud provider，白得 Present/CleanUp
}

// 编译期断言：huaweicloudProvider 满足 Provider 接口。
var _ Provider = (*huaweicloudProvider)(nil)

// newHuaweicloud 校验必填凭证并装配 lego 原生 huaweicloud DNS-01 provider。
// 凭证字段：access_key_id（AccessKeyID）、secret_access_key（SecretAccessKey）、region（Region）。
func newHuaweicloud(creds Credentials) (Provider, error) {
	akID := creds["access_key_id"]
	akSecret := creds["secret_access_key"]
	region := creds["region"]
	if akID == "" || akSecret == "" {
		return nil, fmt.Errorf("huaweicloud 凭证缺少 access_key_id 或 secret_access_key")
	}
	cfg := legohuawei.NewDefaultConfig()
	// Config.AccessKeyID / SecretAccessKey / Region 均为 lego huaweicloud Config 字段。
	cfg.AccessKeyID = akID
	cfg.SecretAccessKey = akSecret
	if region != "" {
		cfg.Region = region
	}
	p, err := legohuawei.NewDNSProviderConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("装配 huaweicloud DNS-01 provider 失败: %w", err)
	}
	return &huaweicloudProvider{Provider: p}, nil
}

// EnsureWildcardA 通配 A 记录管理需华为云 DNS SDK，本期未接入；
// 真实装配与本地联调见 Plan 1 §10 计划阶段细化点。
func (h *huaweicloudProvider) EnsureWildcardA(_ context.Context, baseDomain, ip string) error {
	return fmt.Errorf("huaweicloud 通配 A 记录管理待实现（base=%s ip=%s）", baseDomain, ip)
}

// DeleteWildcardA 同 EnsureWildcardA，待接入华为云 DNS SDK。
func (h *huaweicloudProvider) DeleteWildcardA(_ context.Context, baseDomain string) error {
	return fmt.Errorf("huaweicloud 通配 A 记录删除待实现（base=%s）", baseDomain)
}
