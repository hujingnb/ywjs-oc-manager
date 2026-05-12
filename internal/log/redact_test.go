package log

import (
	"bytes"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

// TestRedactSecrets 验证Redact密钥的预期行为场景。
func TestRedactSecrets(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // 期望子串
		bad  string // 不应出现的子串
	}{
		{name: "json password", in: `{"username":"u","password":"p@ssw0rd!"}`, want: `"password":"***"`, bad: "p@ssw0rd"},     // 场景：JSON password 字段应被脱敏
		{name: "form password", in: `username=admin&password=hunter2`, want: `password=***`, bad: "hunter2"},                  // 场景：表单 password 字段应被脱敏
		{name: "api_key json", in: `{"api_key":"sk-abcdef1234"}`, want: `"api_key":"***"`, bad: "abcdef1234"},                 // 场景：JSON api_key 字段应被脱敏
		{name: "bootstrap_token", in: `{"bootstrap_token":"abcdef0123"}`, want: `"bootstrap_token":"***"`, bad: "abcdef0123"}, // 场景：bootstrap_token 字段应被脱敏
		{name: "agent_token", in: `{"agent_token":"xyz123"}`, want: `"agent_token":"***"`, bad: "xyz123"},                     // 场景：agent_token 字段应被脱敏
		{name: "refresh_token", in: `{"refresh_token":"rt-xyz"}`, want: `"refresh_token":"***"`, bad: "rt-xyz"},               // 场景：refresh_token 字段应被脱敏
		{name: "access_token", in: `{"access_token":"eyJabc"}`, want: `"access_token":"***"`, bad: "eyJabc"},                  // 场景：access_token 字段应被脱敏
		{name: "master_key", in: `{"master_key":"AAAA="}`, want: `"master_key":"***"`, bad: "AAAA="},                          // 场景：master_key 字段应被脱敏
		{name: "Bearer header", in: `Authorization: Bearer eyJhbGciOiJI`, want: `Bearer ***`, bad: "eyJhbGciOiJI"},            // 场景：Authorization Bearer 令牌应被脱敏
		{name: "sk- token", in: `OPENAI_API_KEY=sk-PWcprXYZ`, want: `sk-***`, bad: "PWcprXYZ"},                                // 场景：sk- 前缀 API key 应被脱敏
		{name: "no field untouched", in: `username=alice`, want: `username=alice`, bad: ""},                                   // 场景：不含敏感字段的内容应保持原样
	}
	for _, tc := range cases {
		// 当前子测试覆盖表格用例中该名称对应的输入组合、边界条件和期望结果。
		t.Run(tc.name, func(t *testing.T) {
			got := RedactSecrets(tc.in)
			require.Contains(t, got, tc.want)
			if tc.bad != "" && strings.Contains(got, tc.bad) {
				t.Fatalf("RedactSecrets(%q) = %q, 不应包含 %q", tc.in, got, tc.bad)
			}
		})
	}
}

// TestRedactingWriter 验证RedactingWriter的预期行为场景。
func TestRedactingWriter(t *testing.T) {
	var buf bytes.Buffer
	w := NewRedactingWriter(&buf)
	original := `{"password":"secret","note":"hi"}`
	n, err := w.Write([]byte(original))
	require.NoError(t, err)
	require.Equal(t, len(original), n)
	require.False(t, strings.Contains(buf.String(), "secret"))
	require.True(t, strings.Contains(buf.String(), `"password":"***"`))
}
