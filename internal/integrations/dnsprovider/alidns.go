package dnsprovider

import (
	"context"
	"fmt"
	"strings"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	"github.com/alibabacloud-go/tea/dara"
	alidns "github.com/go-acme/alidns-20150109/v5/client"
	"github.com/go-acme/lego/v5/challenge"
	legoalidns "github.com/go-acme/lego/v5/providers/dns/alidns"
)

// alidnsProvider 适配阿里云 DNS：
//   - DNS-01 挑战复用 lego v5 原生 provider（内嵌满足 challenge.Provider，白得 Present/CleanUp）；
//   - 通配 A 记录 CRUD lego 不覆盖，用自建的阿里云 alidns OpenAPI client 直接操作
//     （lego 内部 client 私有无法复用，故另建一个；二者同款 SDK，不引入新依赖）。
type alidnsProvider struct {
	challenge.Provider // 内嵌 lego 原生 alidns provider（DNS-01 TXT）
	client *alidns.Client
}

// 编译期断言：alidnsProvider 满足 Provider 接口。
var _ Provider = (*alidnsProvider)(nil)

// wildcardARecordTTL 是通配 A 记录的 TTL（秒）。600s 与 lego 默认一致，足够低以便变更快速生效。
const wildcardARecordTTL int64 = 600

// newAlidns 校验必填凭证并装配 lego 原生 DNS-01 provider + 自建 A 记录 client。
// 凭证字段：access_key_id（AccessKey ID）、access_key_secret（AccessKey Secret）。
func newAlidns(creds Credentials) (Provider, error) {
	akID := creds["access_key_id"]
	akSecret := creds["access_key_secret"]
	if akID == "" || akSecret == "" {
		return nil, fmt.Errorf("alidns 凭证缺少 access_key_id 或 access_key_secret")
	}

	// 1) lego 原生 alidns provider：负责 DNS-01 TXT 挑战。
	cfg := legoalidns.NewDefaultConfig()
	cfg.APIKey = akID        // Config.APIKey 对应 AccessKey ID
	cfg.SecretKey = akSecret // Config.SecretKey 对应 AccessKey Secret
	p, err := legoalidns.NewDNSProviderConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("装配 alidns DNS-01 provider 失败: %w", err)
	}

	// 2) 自建 alidns OpenAPI client：负责通配 A 记录 CRUD（lego 不提供该能力）。
	// region 取 cn-hangzhou（与 lego 默认一致；DNS 为全局服务，region 仅影响接入点）。
	openCfg := new(openapi.Config).
		SetAccessKeyId(akID).
		SetAccessKeySecret(akSecret).
		SetRegionId("cn-hangzhou")
	client, err := alidns.NewClient(openCfg)
	if err != nil {
		return nil, fmt.Errorf("装配 alidns A 记录 client 失败: %w", err)
	}

	return &alidnsProvider{Provider: p, client: client}, nil
}

// EnsureWildcardA 幂等确保存在一条 *.baseDomain → ip 的 A 记录。
// 已存在且值相同则不动；值不同则更新；不存在则新增。
func (a *alidnsProvider) EnsureWildcardA(ctx context.Context, baseDomain, ip string) error {
	fullRecord := "*." + baseDomain // 如 *.aisite.ywjs.com
	zone, rr, err := a.splitZoneRR(ctx, fullRecord)
	if err != nil {
		return err
	}

	records, err := a.findARecords(ctx, zone, rr)
	if err != nil {
		return err
	}

	if len(records) > 0 {
		// 已有记录：值相同则幂等返回；否则更新第一条（多条理论上不应出现，取第一条对齐）。
		rec := records[0]
		if derefStr(rec.Value) == ip {
			return nil
		}
		req := new(alidns.UpdateDomainRecordRequest).
			SetRecordId(derefStr(rec.RecordId)).
			SetRR(rr).
			SetType("A").
			SetValue(ip).
			SetTTL(wildcardARecordTTL)
		if _, err := alidns.UpdateDomainRecordWithContext(ctx, a.client, req, &dara.RuntimeOptions{}); err != nil {
			return fmt.Errorf("更新通配 A 记录失败（zone=%s rr=%s ip=%s）: %w", zone, rr, ip, err)
		}
		return nil
	}

	// 不存在：新增。
	req := new(alidns.AddDomainRecordRequest).
		SetDomainName(zone).
		SetRR(rr).
		SetType("A").
		SetValue(ip).
		SetTTL(wildcardARecordTTL)
	if _, err := alidns.AddDomainRecordWithContext(ctx, a.client, req, &dara.RuntimeOptions{}); err != nil {
		return fmt.Errorf("新增通配 A 记录失败（zone=%s rr=%s ip=%s）: %w", zone, rr, ip, err)
	}
	return nil
}

// DeleteWildcardA 删除 *.baseDomain 的通配 A 记录（不存在视为成功，幂等）。
func (a *alidnsProvider) DeleteWildcardA(ctx context.Context, baseDomain string) error {
	fullRecord := "*." + baseDomain
	zone, rr, err := a.splitZoneRR(ctx, fullRecord)
	if err != nil {
		return err
	}
	records, err := a.findARecords(ctx, zone, rr)
	if err != nil {
		return err
	}
	for _, rec := range records {
		req := new(alidns.DeleteDomainRecordRequest).SetRecordId(derefStr(rec.RecordId))
		if _, err := alidns.DeleteDomainRecordWithContext(ctx, a.client, req, &dara.RuntimeOptions{}); err != nil {
			return fmt.Errorf("删除通配 A 记录失败（zone=%s rr=%s）: %w", zone, rr, err)
		}
	}
	return nil
}

// splitZoneRR 把完整记录名拆成 (托管 zone, RR 前缀)。
// 从阿里云账号下已托管的域名列表中选出 fullRecord 的最长后缀匹配作为 zone，
// 其余前缀即 RR（zone 本身即记录时 RR 为 "@"）。例如：
//
//	fullRecord=*.aisite.ywjs.com，账号托管 ywjs.com → zone=ywjs.com, rr=*.aisite
func (a *alidnsProvider) splitZoneRR(ctx context.Context, fullRecord string) (zone, rr string, err error) {
	domains, err := a.listDomains(ctx)
	if err != nil {
		return "", "", err
	}
	for _, d := range domains {
		// d 是 fullRecord 的后缀（完整相等，或作为父域以 "." 衔接）。取最长者为托管 zone。
		if (fullRecord == d || strings.HasSuffix(fullRecord, "."+d)) && len(d) > len(zone) {
			zone = d
		}
	}
	if zone == "" {
		return "", "", fmt.Errorf("在 alidns 账号下未找到 %s 对应的托管域名（base_domain 是否已托管到该阿里云账号？）", fullRecord)
	}
	if fullRecord == zone {
		return zone, "@", nil
	}
	return zone, strings.TrimSuffix(fullRecord, "."+zone), nil
}

// listDomains 翻页拉取账号下全部托管域名的名称。
func (a *alidnsProvider) listDomains(ctx context.Context) ([]string, error) {
	var out []string
	var page int64 = 1
	for {
		req := new(alidns.DescribeDomainsRequest).SetPageNumber(page).SetPageSize(100)
		resp, err := alidns.DescribeDomainsWithContext(ctx, a.client, req, &dara.RuntimeOptions{})
		if err != nil {
			return nil, fmt.Errorf("查询 alidns 托管域名列表失败: %w", err)
		}
		if resp == nil || resp.Body == nil || resp.Body.Domains == nil {
			break
		}
		for _, d := range resp.Body.Domains.Domain {
			if name := derefStr(d.DomainName); name != "" {
				out = append(out, name)
			}
		}
		total := derefInt64(resp.Body.TotalCount)
		pageSize := derefInt64(resp.Body.PageSize)
		pageNum := derefInt64(resp.Body.PageNumber)
		if pageSize == 0 || pageNum*pageSize >= total {
			break
		}
		page++
	}
	return out, nil
}

// findARecords 查 zone 下 RR 完全等于 rr 的 A 记录。
// 只按 Type=A 服务端过滤（A 记录通常很少），再在内存按 RR 精确匹配，
// 避免 RRKeyWord 对通配 "*" 子域的模糊匹配歧义。
func (a *alidnsProvider) findARecords(ctx context.Context, zone, rr string) ([]*alidns.DescribeDomainRecordsResponseBodyDomainRecordsRecord, error) {
	req := new(alidns.DescribeDomainRecordsRequest).
		SetDomainName(zone).
		SetType("A").
		SetPageSize(500)
	resp, err := alidns.DescribeDomainRecordsWithContext(ctx, a.client, req, &dara.RuntimeOptions{})
	if err != nil {
		return nil, fmt.Errorf("查询 alidns 解析记录失败（zone=%s）: %w", zone, err)
	}
	var out []*alidns.DescribeDomainRecordsResponseBodyDomainRecordsRecord
	if resp == nil || resp.Body == nil || resp.Body.DomainRecords == nil {
		return out, nil
	}
	for _, rec := range resp.Body.DomainRecords.Record {
		if derefStr(rec.RR) == rr && derefStr(rec.Type) == "A" {
			out = append(out, rec)
		}
	}
	return out, nil
}

// derefStr 安全解引用 *string（nil 返回空串）。SDK 字段均为指针，需逐个判空。
func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// derefInt64 安全解引用 *int64（nil 返回 0）。
func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
