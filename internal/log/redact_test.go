package log

import (
	"bytes"
	"strings"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // 期望子串
		bad  string // 不应出现的子串
	}{
		{name: "json password", in: `{"username":"u","password":"p@ssw0rd!"}`, want: `"password":"***"`, bad: "p@ssw0rd"},
		{name: "form password", in: `username=admin&password=hunter2`, want: `password=***`, bad: "hunter2"},
		{name: "api_key json", in: `{"api_key":"sk-abcdef1234"}`, want: `"api_key":"***"`, bad: "abcdef1234"},
		{name: "bootstrap_token", in: `{"bootstrap_token":"abcdef0123"}`, want: `"bootstrap_token":"***"`, bad: "abcdef0123"},
		{name: "agent_token", in: `{"agent_token":"xyz123"}`, want: `"agent_token":"***"`, bad: "xyz123"},
		{name: "refresh_token", in: `{"refresh_token":"rt-xyz"}`, want: `"refresh_token":"***"`, bad: "rt-xyz"},
		{name: "access_token", in: `{"access_token":"eyJabc"}`, want: `"access_token":"***"`, bad: "eyJabc"},
		{name: "master_key", in: `{"master_key":"AAAA="}`, want: `"master_key":"***"`, bad: "AAAA="},
		{name: "Bearer header", in: `Authorization: Bearer eyJhbGciOiJI`, want: `Bearer ***`, bad: "eyJhbGciOiJI"},
		{name: "sk- token", in: `OPENAI_API_KEY=sk-PWcprXYZ`, want: `sk-***`, bad: "PWcprXYZ"},
		{name: "no field untouched", in: `username=alice`, want: `username=alice`, bad: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactSecrets(tc.in)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("RedactSecrets(%q) = %q, want contain %q", tc.in, got, tc.want)
			}
			if tc.bad != "" && strings.Contains(got, tc.bad) {
				t.Fatalf("RedactSecrets(%q) = %q, 不应包含 %q", tc.in, got, tc.bad)
			}
		})
	}
}

func TestRedactingWriter(t *testing.T) {
	var buf bytes.Buffer
	w := NewRedactingWriter(&buf)
	original := `{"password":"secret","note":"hi"}`
	n, err := w.Write([]byte(original))
	if err != nil {
		t.Fatalf("Write err = %v", err)
	}
	if n != len(original) {
		t.Fatalf("Write n = %d, want %d (must report original length)", n, len(original))
	}
	if strings.Contains(buf.String(), "secret") {
		t.Fatalf("buffer 仍包含明文密码: %q", buf.String())
	}
	if !strings.Contains(buf.String(), `"password":"***"`) {
		t.Fatalf("buffer 缺脱敏标记: %q", buf.String())
	}
}
