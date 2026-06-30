// Package dnsprovider 定义统一的 DNS provider 适配层，供 web-publish 能力使用。
//
// 该层有两类能力，对应设计 §6：
//  1. ACME DNS-01 挑战：写/删 _acme-challenge TXT 记录，供 lego 签发回调。
//     Provider 内嵌 lego 的 challenge.Provider（Present/CleanUp），本身即可直接
//     作为 lego DNS-01 挑战器使用，无需额外包装方法。
//  2. 通配解析记录：写/删 *.base_domain → ingressIP 的 A 记录。
//     lego 原生 provider 不覆盖此能力，故各实现用云厂商 DNS SDK 直接 CRUD。
//
// 四家实现：alidns / huaweicloud / tencentcloud 复用 lego 原生 DNS-01 provider；
// cmcccloud（移动云）vendor certimate 的实现（lego 无原生移动云）。
package dnsprovider

import (
	"context"

	"github.com/go-acme/lego/v5/challenge"
)

// ProviderType 是受支持的 DNS provider 枚举（与 org_web_publish_config.dns_provider 取值一致）。
type ProviderType string

const (
	// ProviderAlidns 阿里云 DNS（lego 原生 provider: providers/dns/alidns）。
	ProviderAlidns ProviderType = "alidns"
	// ProviderHuaweicloud 华为云 DNS（lego 原生 provider: providers/dns/huaweicloud）。
	ProviderHuaweicloud ProviderType = "huaweicloud"
	// ProviderTencentcloud 腾讯云 DNS（lego 原生 provider: providers/dns/tencentcloud）。
	ProviderTencentcloud ProviderType = "tencentcloud"
	// ProviderCmcccloud 中国移动云 DNS（lego 无原生，vendor certimate 实现）。
	ProviderCmcccloud ProviderType = "cmcccloud"
	// ProviderLocal 本地调试占位 provider：仅当平台开启 dev_self_signed_cert 时可选用，
	// 配合自签证书 provisioner 走完整开通流程（不真实调用任何云 DNS API）。
	// 是否允许选用由 service 层结合 dev 开关二次校验；factory.New 不构造它（dev 走自签 provisioner 旁路）。
	ProviderLocal ProviderType = "local"
)

// Valid 报告 pt 是否为受支持的 provider，用于落库前与签发前校验，挡住脏数据。
// ProviderLocal 也视为合法取值（已知枚举），但其「仅 dev 模式可用」由 service 层另行 gate。
func (pt ProviderType) Valid() bool {
	switch pt {
	case ProviderAlidns, ProviderHuaweicloud, ProviderTencentcloud, ProviderCmcccloud, ProviderLocal:
		return true
	default:
		return false
	}
}

// Credentials 是 provider 凭证的中立载体（key→value）。
// 不同 provider 需要的 key 不同；各实现的 New 自行取用并校验。
// 该结构 JSON 序列化后由 auth.Cipher 加密落库，本层只负责取值。
type Credentials map[string]string

// Provider 是统一的 DNS provider 适配接口。一个实例绑定一个基础域名与一组凭证。
//
// 内嵌 lego 的 challenge.Provider（Present/CleanUp）：本接口本身就是一个 lego DNS-01
// 挑战器，acme.Issuer 可直接 client.Challenge.SetDNS01Provider(provider)，省去包装方法
// 与中间一跳。ali/huawei/tencent 的实现内嵌 lego 原生 provider 即白得 Present/CleanUp；
// cmcccloud 内嵌 vendored certimate challenger。下面两个 A 记录方法是 lego 不提供、
// 需各实现用云厂商 SDK 自补的能力。
type Provider interface {
	challenge.Provider

	// EnsureWildcardA 幂等确保存在一条 *.baseDomain → ip 的 A 记录（已存在且值相同则不动）。
	// baseDomain 不含通配前缀（如 "apps.example.com"），由实现自行拼 "*" 子域。
	EnsureWildcardA(ctx context.Context, baseDomain, ip string) error

	// DeleteWildcardA 删除 *.baseDomain 的通配 A 记录（不存在视为成功，幂等）。
	DeleteWildcardA(ctx context.Context, baseDomain string) error
}
