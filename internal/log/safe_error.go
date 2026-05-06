// SafeErrorMessage 提供 HTTP 错误响应去敏 helper：所有 handler 在用 err.Error()
// 直接回包前应该走它，避免把 stack trace / SQL 片段 / 文件路径 / 密钥泄漏到客户端。
package log

import (
	"regexp"
)

// 文件路径片段：去掉绝对路径暴露的目录结构，例如
// "/home/hujing/.../service.go:120" → "<path>"。
var filePathPattern = regexp.MustCompile(`/[A-Za-z0-9_./\-]+\.go(?::\d+)?`)

// SQL 片段：常见 sqlc / pgx 报错把整段 SQL 嵌进来，直接对外暴露表 / 列名。
// 这里用粗略匹配剔除 ` SELECT|UPDATE|INSERT|DELETE ` 后到行尾的内容。
var sqlPattern = regexp.MustCompile(`(?i)\b(SELECT|UPDATE|INSERT INTO|DELETE FROM)\s.+?(?:;|$)`)

// safeMsgMax 是对外展示的最大长度，超过时尾部加省略号。
const safeMsgMax = 200

// SafeErrorMessage 把 error 转成可对外展示的字符串。
//
// 规则：
//  1. nil → 空字符串；
//  2. 走日志脱敏：去掉 password / token / Bearer / sk- 等字段；
//  3. 替换 .go 文件路径片段为 <path>，避免暴露内部目录结构；
//  4. 替换 SQL 片段为 <sql>；
//  5. 截断到 200 字符，避免 stack trace 把大段内部状态推给前端。
func SafeErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	msg = RedactSecrets(msg)
	msg = filePathPattern.ReplaceAllString(msg, "<path>")
	msg = sqlPattern.ReplaceAllString(msg, "<sql>")
	if len(msg) > safeMsgMax {
		msg = msg[:safeMsgMax] + "..."
	}
	return msg
}
