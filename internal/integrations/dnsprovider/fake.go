package dnsprovider

import (
	"context"
)

// FakeProvider 是 Provider 的内存实现，仅供单测：A 记录与 DNS-01 TXT 记录都存 map，
// 直接实现 lego challenge.Provider 的 Present/CleanUp（接口内嵌后无需独立挑战类型）。
// 可注入错误模拟失败路径。
type FakeProvider struct {
	ARecords  map[string]string // key 为完整通配域名 "*.<baseDomain>"，value 为 IP
	TXTed     map[string]string // 记录 lego DNS-01 写入的 domain→keyAuth，供断言
	EnsureErr error             // 非 nil 时 EnsureWildcardA 直接返回它
	DeleteErr error             // 非 nil 时 DeleteWildcardA 直接返回它
}

// NewFakeProvider 构造一个空的内存 provider。
func NewFakeProvider() *FakeProvider {
	return &FakeProvider{ARecords: map[string]string{}, TXTed: map[string]string{}}
}

// 编译期断言：FakeProvider 必须满足 Provider（含内嵌的 challenge.Provider）。
var _ Provider = (*FakeProvider)(nil)

// Present 记录 DNS-01 挑战 TXT（lego 在签发时调用）；实现 challenge.Provider。
func (f *FakeProvider) Present(_ context.Context, domain, token, keyAuth string) error {
	f.TXTed[domain] = keyAuth
	return nil
}

// CleanUp 清理挑战 TXT（lego 在签发结束后调用）；实现 challenge.Provider。
func (f *FakeProvider) CleanUp(_ context.Context, domain, token, keyAuth string) error {
	delete(f.TXTed, domain)
	return nil
}

// EnsureWildcardA 写入/覆盖 *.baseDomain 的 A 记录；EnsureErr 非 nil 时返回它。
func (f *FakeProvider) EnsureWildcardA(_ context.Context, baseDomain, ip string) error {
	if f.EnsureErr != nil {
		return f.EnsureErr
	}
	f.ARecords["*."+baseDomain] = ip
	return nil
}

// DeleteWildcardA 删除 *.baseDomain 的 A 记录；DeleteErr 非 nil 时返回它。
func (f *FakeProvider) DeleteWildcardA(_ context.Context, baseDomain string) error {
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	delete(f.ARecords, "*."+baseDomain)
	return nil
}
