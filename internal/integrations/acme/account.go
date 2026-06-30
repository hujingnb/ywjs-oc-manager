// Package acme 用 go-acme/lego 编排 ACME DNS-01 通配证书的签发与续签。
//
// 设计要点：
//   - 每企业一张 *.base_domain 通配证书，manager 全权托管、自动续期。
//   - 通配证书必须走 DNS-01 挑战，挑战 provider 由 dnsprovider 适配层提供。
//   - 本包只产出 PEM（证书链 + 私钥），写 k8s TLS Secret 的事交给 k8sorch。
//
// 账户私钥的持久化策略：本期账户私钥进程内生成、不落库（ACME 账户无状态副作用，
// 每次用新账户也能成功注册并签发）。若未来要稳定账户身份，再扩展为从 Secret 读取。
package acme

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"

	legoacme "github.com/go-acme/lego/v5/acme"
	"github.com/go-acme/lego/v5/registration"
)

// account 实现 lego 的 registration.User：携带邮箱、ACME 注册资源与账户私钥。
//
// lego v5 中 registration.User 要求：
//   - GetEmail() string
//   - GetRegistration() *acme.ExtendedAccount（v4 为 *registration.Resource）
//   - GetPrivateKey() crypto.Signer（v4 为 crypto.PrivateKey）
type account struct {
	// email 是注册到 CA 的联系邮箱，用于到期提醒等通知。
	email string
	// registration 是向 CA 成功注册后返回的账户资源；注册前为 nil。
	registration *legoacme.ExtendedAccount
	// privateKey 是账户的 P-256 ECDSA 私钥，用于对 ACME 请求签名。
	privateKey crypto.Signer
}

// newAccount 生成一个带新 P-256 私钥的 ACME 账户（尚未向 CA 注册）。
func newAccount(email string) (*account, error) {
	// ACME 账户私钥用 P-256 ECDSA，足够且签发快；与证书私钥无关。
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &account{email: email, privateKey: key}, nil
}

// newAccountWithKey 用给定（已持久化、稳定）的私钥构造 ACME 账户。
// 复用同一账户私钥可使 lego 注册时返回「已存在账户」而非新建，从而不触发 Let's Encrypt
// 「每 IP 每 3h 新注册数」限流——这是反复签发/重试时避免 429 rateLimited 的关键。
func newAccountWithKey(email string, key crypto.Signer) *account {
	return &account{email: email, privateKey: key}
}

// GetEmail 返回账户邮箱（registration.User 接口）。
func (a *account) GetEmail() string { return a.email }

// GetRegistration 返回 ACME 注册资源（registration.User 接口）；注册前为 nil。
//
// lego v5 将返回类型从 *registration.Resource 改为 *acme.ExtendedAccount。
func (a *account) GetRegistration() *legoacme.ExtendedAccount { return a.registration }

// GetPrivateKey 返回账户私钥（registration.User 接口）。
//
// lego v5 将返回类型从 crypto.PrivateKey 改为 crypto.Signer，以明确要求私钥
// 必须具备签名能力（ecdsa.PrivateKey 同时满足两者）。
func (a *account) GetPrivateKey() crypto.Signer { return a.privateKey }

// 编译期静态断言：确保 *account 满足 registration.User 接口。
// 若接口方法集发生变化，此处会立即报错，而非在运行时才暴露。
var _ registration.User = (*account)(nil)
