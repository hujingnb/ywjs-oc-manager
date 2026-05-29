// client_test.go
package ocops_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/integrations/ocops"
)

// TestClientDoJSONMapsStatusToError 验证 HTTP 状态码→哨兵错误映射：
// 400→ErrBadRequest、404→ErrNotFound、409→ErrUnsupported、500→ErrOutputInvalid、
// 502→ErrCLI、401→ErrUnauthorized；2xx 正常解码 body。
func TestClientDoJSONMapsStatusToError(t *testing.T) {
	// table-driven：每行一个 (HTTP 状态, body) → 期望哨兵错误 / 解码结果
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			// 2xx 路径：正常返回 CronJob 对象供解码断言
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"id":"j1"}`))
		case "/nf":
			// 404 路径：返回契约错误体
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"任务不存在"}`))
		case "/br":
			// 400 路径：请求参数非法
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"code":"BAD_REQUEST","message":"参数错误"}`))
		case "/unsupported":
			// 409 路径：操作不支持
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"code":"UNSUPPORTED","message":"不支持"}`))
		case "/internal":
			// 500 路径：服务内部错误
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"code":"INTERNAL","message":"内部错误"}`))
		case "/cli":
			// 502 路径：hermes CLI 失败
			w.WriteHeader(502)
			_, _ = w.Write([]byte(`{"code":"HERMES_CLI_FAILED","message":"cli 错误"}`))
		case "/unauth":
			// 401 路径：鉴权失败
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"code":"UNAUTHORIZED","message":"未授权"}`))
		}
	}))
	defer srv.Close()

	c := ocops.NewClient(http.DefaultClient)
	ep := ocops.Endpoint{BaseURL: srv.URL, Token: "t"}

	// 2xx 正常解码：body 中 id 字段应映射到 CronJob.ID
	var job ocops.CronJob
	require.NoError(t, c.DoJSON(context.Background(), ep, "GET", "/ok", nil, &job))
	assert.Equal(t, "j1", job.ID)

	// 404 → ErrNotFound
	err := c.DoJSON(context.Background(), ep, "GET", "/nf", nil, nil)
	require.ErrorIs(t, err, ocops.ErrNotFound)

	// 400 → ErrBadRequest
	err = c.DoJSON(context.Background(), ep, "GET", "/br", nil, nil)
	require.ErrorIs(t, err, ocops.ErrBadRequest)

	// 409 → ErrUnsupported
	err = c.DoJSON(context.Background(), ep, "GET", "/unsupported", nil, nil)
	require.ErrorIs(t, err, ocops.ErrUnsupported)

	// 500 → ErrOutputInvalid
	err = c.DoJSON(context.Background(), ep, "GET", "/internal", nil, nil)
	require.ErrorIs(t, err, ocops.ErrOutputInvalid)

	// 502 → ErrCLI
	err = c.DoJSON(context.Background(), ep, "GET", "/cli", nil, nil)
	require.ErrorIs(t, err, ocops.ErrCLI)

	// 401 → ErrUnauthorized
	err = c.DoJSON(context.Background(), ep, "GET", "/unauth", nil, nil)
	require.ErrorIs(t, err, ocops.ErrUnauthorized)
}

// TestClientSendsBearer 验证请求带 Authorization: Bearer <token>。
func TestClientSendsBearer(t *testing.T) {
	var gotAuth string
	// httptest server 记录实际收到的 Authorization 头
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := ocops.NewClient(nil) // nil → 使用 http.DefaultClient
	ep := ocops.Endpoint{BaseURL: srv.URL, Token: "my-secret-token"}

	// 断言 server 收到的头必须是 Bearer + token
	err := c.DoJSON(context.Background(), ep, "GET", "/", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "Bearer my-secret-token", gotAuth)
}
