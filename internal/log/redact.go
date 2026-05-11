// Package log 提供日志脱敏 helper，避免 manager 把敏感字段（密码 / token / 密钥）写进
// 进程标准输出和容器日志。脱敏逻辑作为 io.Writer 包装层注入 stdlib log.Logger，
// 不需要修改任何调用点。
package log

import (
	"io"
	"regexp"
)

// secretPatterns 列出需要脱敏的敏感字段。每条规则替换成 `<key>=***`，
// 既保留字段名便于排障，又不泄漏值。
//
// 注意：所有正则用 `\b` 边界匹配，避免误伤其他字段（如 "no_password=1"）。
// JSON 形式 `"key":"value"` 与 query/form 形式 `key=value` 都覆盖。
var secretPatterns = []struct {
	// re 匹配一种敏感值出现形式，必须尽量收窄，避免误删普通业务文本。
	re *regexp.Regexp
	// repl 保留字段名和必要前缀，隐藏实际 secret。
	repl string
}{
	{re: regexp.MustCompile(`(?i)("password"\s*:\s*)"[^"]*"`), repl: `$1"***"`},
	{re: regexp.MustCompile(`(?i)\bpassword=([^\s&"]+)`), repl: `password=***`},
	{re: regexp.MustCompile(`(?i)("api_key"\s*:\s*)"[^"]*"`), repl: `$1"***"`},
	{re: regexp.MustCompile(`(?i)("api_key_ciphertext"\s*:\s*)"[^"]*"`), repl: `$1"***"`},
	{re: regexp.MustCompile(`(?i)("bootstrap_token"\s*:\s*)"[^"]*"`), repl: `$1"***"`},
	{re: regexp.MustCompile(`(?i)("agent_token"\s*:\s*)"[^"]*"`), repl: `$1"***"`},
	{re: regexp.MustCompile(`(?i)("enrollment_secret"\s*:\s*)"[^"]*"`), repl: `$1"***"`},
	{re: regexp.MustCompile(`(?i)("refresh_token"\s*:\s*)"[^"]*"`), repl: `$1"***"`},
	{re: regexp.MustCompile(`(?i)("access_token"\s*:\s*)"[^"]*"`), repl: `$1"***"`},
	{re: regexp.MustCompile(`(?i)("master_key"\s*:\s*)"[^"]*"`), repl: `$1"***"`},
	{re: regexp.MustCompile(`Bearer\s+[A-Za-z0-9._\-+/=]{8,}`), repl: `Bearer ***`},
	// sk-... 形式 OpenAI 兼容 token：保留 sk- 前缀方便识别；只露前 6 字符后部分 ***。
	{re: regexp.MustCompile(`\bsk-[A-Za-z0-9]{4,}`), repl: `sk-***`},
}

// RedactSecrets 对单行字符串做脱敏，按 secretPatterns 顺序应用所有规则。
// 调用方可以在写日志前主动调用，也可以借助 NewRedactingWriter 包装 io.Writer。
func RedactSecrets(s string) string {
	for _, p := range secretPatterns {
		// 顺序应用所有规则，确保同一行里同时出现 password、Bearer 与 sk- token 时都能覆盖。
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}

// RedactingWriter 把 io.Writer 包装成"先脱敏再写"的形式。
// 配合 stdlib log.Logger.SetOutput 使用，可以在不改任何调用点的前提下覆盖全进程日志。
type RedactingWriter struct {
	// W 是最终写入目标；调用方通常传 os.Stderr 或测试用 bytes.Buffer。
	W io.Writer
}

// NewRedactingWriter 构造脱敏 writer。
func NewRedactingWriter(w io.Writer) *RedactingWriter {
	return &RedactingWriter{W: w}
}

// Write 对每次 Write 调用做脱敏；返回长度永远是原 len(p)，避免上游误以为写入失败。
func (r *RedactingWriter) Write(p []byte) (int, error) {
	cleaned := RedactSecrets(string(p))
	if _, err := r.W.Write([]byte(cleaned)); err != nil {
		return 0, err
	}
	return len(p), nil
}
