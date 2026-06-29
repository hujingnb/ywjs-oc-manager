package dnsprovider

import (
	"context"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	legoalidns "github.com/go-acme/lego/v5/providers/dns/alidns"
)

// alidnsProvider 适配阿里云 DNS：DNS-01 挑战复用 lego v5 原生 provider（内嵌满足 challenge.Provider），
// 通配 A 记录 CRUD 需阿里云 alidns SDK（AddDomainRecord/DescribeDomainRecords），本期未接入（见 EnsureWildcardA 注释）。
type alidnsProvider struct {
	challenge.Provider // 内嵌 lego 原生 alidns provider，白得 Present/CleanUp
}

// 编译期断言：alidnsProvider 满足 Provider 接口。
var _ Provider = (*alidnsProvider)(nil)

// newAlidns 校验必填凭证并装配 lego 原生 alidns DNS-01 provider。
// 凭证字段：access_key_id（APIKey）、access_key_secret（SecretKey）。
func newAlidns(creds Credentials) (Provider, error) {
	akID := creds["access_key_id"]
	akSecret := creds["access_key_secret"]
	if akID == "" || akSecret == "" {
		return nil, fmt.Errorf("alidns 凭证缺少 access_key_id 或 access_key_secret")
	}
	cfg := legoalidns.NewDefaultConfig()
	// Config.APIKey 对应阿里云 AccessKey ID，Config.SecretKey 对应 AccessKey Secret。
	cfg.APIKey = akID
	cfg.SecretKey = akSecret
	p, err := legoalidns.NewDNSProviderConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("装配 alidns DNS-01 provider 失败: %w", err)
	}
	return &alidnsProvider{Provider: p}, nil
}

// EnsureWildcardA 通配 A 记录管理需阿里云 alidns SDK（AddDomainRecord/DescribeDomainRecords），
// 本期未接入；真实装配与本地联调见 Plan 1 §10 计划阶段细化点。
func (a *alidnsProvider) EnsureWildcardA(_ context.Context, baseDomain, ip string) error {
	return fmt.Errorf("alidns 通配 A 记录管理待实现（base=%s ip=%s）", baseDomain, ip)
}

// DeleteWildcardA 同 EnsureWildcardA，待接入阿里云 alidns SDK。
func (a *alidnsProvider) DeleteWildcardA(_ context.Context, baseDomain string) error {
	return fmt.Errorf("alidns 通配 A 记录删除待实现（base=%s）", baseDomain)
}
