package dnsprovider

import (
	"context"
	"errors"
	"fmt"
)

// ErrUnsupportedProvider 表示传入了不受支持的 provider 类型。
var ErrUnsupportedProvider = errors.New("dnsprovider: 不支持的 provider 类型")

// New 按 ProviderType 构造对应 Provider 实例：校验凭证、装配 DNS-01 provider。
// baseDomain 仅用于校验/日志，实际域名在每次调用时再传。
func New(_ context.Context, pt ProviderType, creds Credentials, _ string) (Provider, error) {
	switch pt {
	case ProviderAlidns:
		return newAlidns(creds)
	case ProviderHuaweicloud:
		return newHuaweicloud(creds)
	case ProviderTencentcloud:
		return newTencentcloud(creds)
	case ProviderCmcccloud:
		return newCmcccloud(creds)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedProvider, pt)
	}
}
