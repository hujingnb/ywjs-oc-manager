// Package i18n 提供 manager 后端的 locale 归一公共逻辑，供中间件与服务层复用，
// 避免 Accept-Language 解析与受支持语言集合在多处分叉。
package i18n

import "strings"

// SupportedLocales 是后端受支持的界面语言集合；与前端 LocaleSwitcher、
// service.SupportedLocales 保持一致，新增语言时同步扩展。
var SupportedLocales = []string{"en", "zh"}

// isSupported 判断给定 locale 字符串是否在受支持集合中。
func isSupported(loc string) bool {
	for _, l := range SupportedLocales {
		if l == loc {
			return true
		}
	}
	return false
}

// NormalizeLocale 把任意语言串归一到受支持 locale：精确命中直接返回；否则剥掉
// 区域后缀（zh-CN→zh）再判；仍不支持则回落 def。
func NormalizeLocale(raw, def string) string {
	key := strings.ToLower(strings.TrimSpace(raw))
	if key == "" {
		return def
	}
	if isSupported(key) {
		return key
	}
	if base := strings.SplitN(key, "-", 2)[0]; isSupported(base) {
		return base
	}
	return def
}

// ParseAcceptLanguage 取 Accept-Language 首选标签并归一；空或无法解析回落 def。
func ParseAcceptLanguage(header, def string) string {
	if strings.TrimSpace(header) == "" {
		return def
	}
	first := strings.SplitN(header, ",", 2)[0]
	tag := strings.SplitN(first, ";", 2)[0]
	return NormalizeLocale(tag, def)
}
