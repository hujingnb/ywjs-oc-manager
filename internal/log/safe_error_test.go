package log

import (
	"errors"
	"strings"
	"testing"
)

func TestSafeErrorMessage_Nil(t *testing.T) {
	if got := SafeErrorMessage(nil); got != "" {
		t.Fatalf("got = %q, want empty", got)
	}
}

func TestSafeErrorMessage_RedactsToken(t *testing.T) {
	got := SafeErrorMessage(errors.New(`failed: Bearer eyJabc123 is invalid`))
	if strings.Contains(got, "eyJabc123") {
		t.Fatalf("got = %q, 不应暴露 Bearer 明文", got)
	}
	if !strings.Contains(got, "Bearer ***") {
		t.Fatalf("got = %q, 应保留 Bearer 标记", got)
	}
}

func TestSafeErrorMessage_StripsFilePath(t *testing.T) {
	got := SafeErrorMessage(errors.New(`scan: /home/hujing/oc-manager/internal/service/foo.go:42 unexpected EOF`))
	if strings.Contains(got, "/home/hujing/") {
		t.Fatalf("got = %q, 不应暴露文件绝对路径", got)
	}
	if !strings.Contains(got, "<path>") {
		t.Fatalf("got = %q, 应替换为 <path>", got)
	}
}

func TestSafeErrorMessage_StripsSQL(t *testing.T) {
	got := SafeErrorMessage(errors.New(`pq: SELECT id, password_hash FROM users WHERE username='admin';`))
	if strings.Contains(got, "password_hash") {
		t.Fatalf("got = %q, 不应暴露 SQL 列名", got)
	}
	if !strings.Contains(got, "<sql>") {
		t.Fatalf("got = %q, 应替换为 <sql>", got)
	}
}

func TestSafeErrorMessage_Truncates(t *testing.T) {
	long := strings.Repeat("x", 500)
	got := SafeErrorMessage(errors.New(long))
	if len(got) > safeMsgMax+5 {
		t.Fatalf("got len = %d, 应截到 %d 加省略号", len(got), safeMsgMax)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("got = %q, 截断时应有省略号", got)
	}
}
