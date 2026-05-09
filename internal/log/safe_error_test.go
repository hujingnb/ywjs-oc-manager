package log

import (
	"errors"
	"strings"
	"testing"
	"github.com/stretchr/testify/require"
)

func TestSafeErrorMessage_Nil(t *testing.T) {
	require.Empty(t, SafeErrorMessage(nil))
}

func TestSafeErrorMessage_RedactsToken(t *testing.T) {
	got := SafeErrorMessage(errors.New(`failed: Bearer eyJabc123 is invalid`))
	require.NotContains(t, got, "eyJabc123")
	require.Contains(t, got, "Bearer ***")
}

func TestSafeErrorMessage_StripsFilePath(t *testing.T) {
	got := SafeErrorMessage(errors.New(`scan: /home/hujing/oc-manager/internal/service/foo.go:42 unexpected EOF`))
	require.NotContains(t, got, "/home/hujing/")
	require.Contains(t, got, "<path>")
}

func TestSafeErrorMessage_StripsSQL(t *testing.T) {
	got := SafeErrorMessage(errors.New(`pq: SELECT id, password_hash FROM users WHERE username='admin';`))
	require.NotContains(t, got, "password_hash")
	require.Contains(t, got, "<sql>")
}

func TestSafeErrorMessage_Truncates(t *testing.T) {
	long := strings.Repeat("x", 500)
	got := SafeErrorMessage(errors.New(long))
	if len(got) > safeMsgMax+5 {
		t.Fatalf("got len = %d, 应截到 %d 加省略号", len(got), safeMsgMax)
	}
	require.True(t, strings.HasSuffix(got, "..."))
}
