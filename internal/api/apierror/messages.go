package apierror

import "fmt"

// MsgKey 是用户可见错误文案的稳定标识（err.<domain>.<slug>），catalog 据此存中/英译文。
type MsgKey string

// catalog 是 MsgKey → locale → 文案 的中心表；由各 domain 文件 init() 调 Register 填充。
var catalog = map[MsgKey]map[string]string{}

// Register 把一组译文并入 catalog；同 key 重复注册即 panic（编译期布局错误，尽早暴露）。
func Register(entries map[MsgKey]map[string]string) {
	for key, langs := range entries {
		if _, dup := catalog[key]; dup {
			panic("apierror: 重复注册 MsgKey " + string(key))
		}
		catalog[key] = langs
	}
}

// Localize 把 key 按 loc 解析为文案：缺该语言回落 en，再缺回落 key 本身（永不 panic）；
// 有 args 时按 fmt.Sprintf 格式化（catalog 串里用 %s/%d 占位符）。
func Localize(key MsgKey, loc string, args ...any) string {
	langs := catalog[key]
	msg := ""
	if langs != nil {
		if m, ok := langs[loc]; ok {
			msg = m
		} else {
			msg = langs["en"]
		}
	}
	if msg == "" {
		msg = string(key)
	}
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}
