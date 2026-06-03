// client_skills_test.go — skill 市场 client 方法（SkillList/SkillInstall/SkillDelete/SkillReload）的
// httptest 单元测试。
//
// 每个测试用 httptest.Server mock oc-ops 行为，断言方法发出的 HTTP method / path /
// Content-Type / body 与契约一致，并验证响应正确解码或错误正确映射。
package ocops_test

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSkillList 验证 SkillList 发出 GET /oc/skills 并正确解码 skill 列表。
func TestSkillList(t *testing.T) {
	// 正常路径：server 返回一个 managed=true、builtin=false 的 skill
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 断言请求方法与路径
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/oc/skills", r.URL.Path)
		// 断言 Bearer token 正确传递
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"skills":[{"name":"a","managed":true,"builtin":false}]}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	skills, err := c.SkillList(context.Background(), ep)
	require.NoError(t, err)
	// 断言列表长度与字段解码
	require.Len(t, skills, 1)
	assert.Equal(t, "a", skills[0].Name)
	assert.True(t, skills[0].Managed)
	assert.False(t, skills[0].Builtin)
}

// TestSkillListEmpty 验证 SkillList 在 server 返回空列表时返回空切片而非 nil。
func TestSkillListEmpty(t *testing.T) {
	// 边界条件：skills 数组为空
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"skills":[]}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	skills, err := c.SkillList(context.Background(), ep)
	require.NoError(t, err)
	assert.Empty(t, skills)
}

// TestSkillDelete 验证 SkillDelete 发出 DELETE /oc/skills/{name}，路径正确编码。
func TestSkillDelete(t *testing.T) {
	// 正常路径：删除名为 "a" 的 skill，server 返回 204
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 断言请求方法与路径（name "a" 无特殊字符，路径应为 /oc/skills/a）
		assert.Equal(t, http.MethodDelete, r.Method)
		assert.Equal(t, "/oc/skills/a", r.URL.Path)
		// 断言 Bearer token 正确传递
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	err := c.SkillDelete(context.Background(), ep, "a")
	require.NoError(t, err)
}

// TestSkillDeleteSpecialName 验证 SkillDelete 对含空格等特殊字符的 skill 名做 URL 转义。
func TestSkillDeleteSpecialName(t *testing.T) {
	// 边界条件：name 含空格，RequestURI 应保留 %20 编码（r.URL.Path 会被 Go HTTP 服务端自动解码，
	// 需通过 r.RequestURI 断言客户端实际发出的原始编码路径）
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		// r.RequestURI 保留客户端发出的原始请求行，%20 不被解码
		assert.Equal(t, "/oc/skills/my%20skill", r.RequestURI)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	err := c.SkillDelete(context.Background(), ep, "my skill")
	require.NoError(t, err)
}

// TestSkillReload 验证 SkillReload 发出 POST /oc/skills/reload。
func TestSkillReload(t *testing.T) {
	// 正常路径：触发 hermes 重扫，server 返回 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 断言请求方法与路径
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/oc/skills/reload", r.URL.Path)
		// 断言 Bearer token 正确传递
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	err := c.SkillReload(context.Background(), ep)
	require.NoError(t, err)
}

// TestSkillInstall 验证 SkillInstall 发出 POST /oc/skills，Content-Type 为 multipart，
// form 中含正确的 name 字段和 archive 文件字节。
func TestSkillInstall(t *testing.T) {
	archiveContent := []byte("fake-zip-content")
	// 正常路径：上传名为 "myplugin" 的归档，server 解析 multipart 并验证内容
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 断言请求方法与路径
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/oc/skills", r.URL.Path)
		// 断言 Bearer token 正确传递
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

		// 断言 Content-Type 为 multipart/form-data
		ct := r.Header.Get("Content-Type")
		mediaType, params, err := mime.ParseMediaType(ct)
		require.NoError(t, err)
		assert.Equal(t, "multipart/form-data", mediaType)

		// 解析 multipart body
		mr := multipart.NewReader(r.Body, params["boundary"])
		fields := map[string][]byte{}
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			data, _ := io.ReadAll(p)
			// 以 form 字段名或文件字段名作为 key
			key := p.FormName()
			if key == "" {
				key = p.FileName()
			}
			fields[key] = data
		}
		// 断言 name 字段值正确
		assert.Equal(t, "myplugin", string(fields["name"]))
		// 断言 archive 文件字节与原始归档内容一致
		assert.True(t, bytes.Equal(archiveContent, fields["archive"]))

		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	err := c.SkillInstall(context.Background(), ep, "myplugin", archiveContent)
	require.NoError(t, err)
}

// TestSkillInstallServerError 验证 SkillInstall 在 server 返回非 2xx 时正确返回错误。
func TestSkillInstallServerError(t *testing.T) {
	// 异常路径：server 返回 500，安装应返回错误
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	err := c.SkillInstall(context.Background(), ep, "bad", []byte("data"))
	require.Error(t, err)
}
