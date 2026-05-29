// client_cron_test.go — 11 个 cron 客户端方法的 httptest 单元测试。
//
// 每个测试用 httptest.Server 断言方法发出的 HTTP method / path / query / body
// 与契约表一致，并验证响应正确解码或错误正确映射。
package ocops_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/integrations/ocops"
)

// newTestClient 创建面向指定 httptest.Server 的测试用 Client 和 Endpoint。
func newTestClient(srv *httptest.Server) (*ocops.Client, ocops.Endpoint) {
	return ocops.NewClient(http.DefaultClient), ocops.Endpoint{BaseURL: srv.URL, Token: "test-token"}
}

// TestCronCapabilities 验证 CronCapabilities 发出 GET /oc/cron/capabilities
// 并把响应正确解码为 CronCapabilities 结构体。
func TestCronCapabilities(t *testing.T) {
	// 正常路径：server 返回能力信息，断言字段解码正确
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 断言请求 method 与 path
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/oc/cron/capabilities", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"contract_version": "1.0",
			"oc_cron_version": "2026.5",
			"variant": "hermes-v2026.5.16",
			"verbs": ["list","show","create"],
			"features": {"status":true,"history":true,"output":true,"write":true,"script":false,"advanced_fields":false}
		}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	caps, err := c.CronCapabilities(context.Background(), ep)
	require.NoError(t, err)
	// 断言关键字段解码正确
	assert.Equal(t, "1.0", caps.ContractVersion)
	assert.Equal(t, "hermes-v2026.5.16", caps.Variant)
	assert.Equal(t, []string{"list", "show", "create"}, caps.Verbs)
	assert.True(t, caps.Features.Status)
}

// TestCronStatus 验证 CronStatus 发出 GET /oc/cron/status 并解码响应。
func TestCronStatus(t *testing.T) {
	// 正常路径：server 返回调度器摘要，断言活跃任务数
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/oc/cron/status", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"available":true,"gateway_running":true,"active_jobs":3}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	status, err := c.CronStatus(context.Background(), ep)
	require.NoError(t, err)
	assert.True(t, status.Available)
	assert.Equal(t, 3, status.ActiveJobs)
}

// TestCronListAllTrue 验证 CronList(all=true) 发出 GET /oc/cron/jobs?all=true
// 并正确解码任务列表。
func TestCronListAllTrue(t *testing.T) {
	// all=true：应在 query string 携带 all=true，返回包含禁用任务的列表
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/oc/cron/jobs", r.URL.Path)
		// 断言 query 参数 all=true 正确发送
		assert.Equal(t, "true", r.URL.Query().Get("all"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id":"j1","name":"任务1"},{"id":"j2","name":"任务2"}]`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	jobs, err := c.CronList(context.Background(), ep, true)
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	assert.Equal(t, "j1", jobs[0].ID)
	assert.Equal(t, "j2", jobs[1].ID)
}

// TestCronListAllFalse 验证 CronList(all=false) 发出 all=false query。
func TestCronListAllFalse(t *testing.T) {
	// all=false：只返回活跃任务，query string 应为 all=false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "false", r.URL.Query().Get("all"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	jobs, err := c.CronList(context.Background(), ep, false)
	require.NoError(t, err)
	assert.Empty(t, jobs)
}

// TestCronShow 验证 CronShow 发出 GET /oc/cron/jobs/{id} 并解码 CronJob。
func TestCronShow(t *testing.T) {
	// 正常路径：server 返回单个任务对象，断言 ID 与名称解码正确
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		// 断言 path 包含 url.PathEscape 后的 id
		assert.Equal(t, "/oc/cron/jobs/my-job-id", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"my-job-id","name":"测试任务","enabled":true}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	job, err := c.CronShow(context.Background(), ep, "my-job-id")
	require.NoError(t, err)
	assert.Equal(t, "my-job-id", job.ID)
	assert.Equal(t, "测试任务", job.Name)
	assert.True(t, job.Enabled)
}

// TestCronShowPathEscape 验证含特殊字符的 job id 经 url.PathEscape 转义后正确发送。
func TestCronShowPathEscape(t *testing.T) {
	// 边界：id 含斜杠等特殊字符，必须被 PathEscape 转义防止路径越界
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// url.PathEscape("job/with/slashes") = "job%2Fwith%2Fslashes"
		assert.Equal(t, "/oc/cron/jobs/job%2Fwith%2Fslashes", r.URL.RawPath)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"job/with/slashes"}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	job, err := c.CronShow(context.Background(), ep, "job/with/slashes")
	require.NoError(t, err)
	assert.Equal(t, "job/with/slashes", job.ID)
}

// TestCronShowNotFound 验证任务不存在时 CronShow 返回 ErrNotFound。
func TestCronShowNotFound(t *testing.T) {
	// 异常路径：server 返回 404，客户端应映射为 ocops.ErrNotFound
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"任务不存在"}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	_, err := c.CronShow(context.Background(), ep, "nonexistent")
	// 必须能通过 errors.Is 识别为 ErrNotFound
	require.ErrorIs(t, err, ocops.ErrNotFound)
}

// TestCronCreate 验证 CronCreate 发出 POST /oc/cron/jobs，body 序列化字段与
// _CRON_CREATE_KEYS 对齐，响应正确解码为 CronJob。
func TestCronCreate(t *testing.T) {
	// 正常路径：断言 POST body 包含 name/schedule，可选字段 prompt 也正确序列化
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/oc/cron/jobs", r.URL.Path)
		// Content-Type 必须是 application/json
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		// 解析请求体
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"new-job","name":"早报","enabled":true}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	req := ocops.CronCreateReq{
		Name:     "早报",
		Schedule: "cron 0 9 * * 1-5",
		Prompt:   "生成今日早报",
	}
	job, err := c.CronCreate(context.Background(), ep, req)
	require.NoError(t, err)
	assert.Equal(t, "new-job", job.ID)
	// 断言 body 字段名与 _CRON_CREATE_KEYS 一致
	assert.Equal(t, "早报", gotBody["name"])
	assert.Equal(t, "cron 0 9 * * 1-5", gotBody["schedule"])
	assert.Equal(t, "生成今日早报", gotBody["prompt"])
}

// TestCronCreateNoAgentField 验证 no_agent=true 时 body 中出现 no_agent 字段。
func TestCronCreateNoAgentField(t *testing.T) {
	// 边界：no_agent 为 bool omitempty，true 时必须出现在 body 中
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"j3"}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	req := ocops.CronCreateReq{Name: "脚本任务", Schedule: "every 1h", NoAgent: true}
	_, err := c.CronCreate(context.Background(), ep, req)
	require.NoError(t, err)
	// no_agent=true 应出现在 body
	assert.Equal(t, true, gotBody["no_agent"])
}

// TestCronUpdate 验证 CronUpdate 发出 PATCH /oc/cron/jobs/{id}，
// 只有非 nil 指针字段出现在 body（partial update 语义）。
func TestCronUpdate(t *testing.T) {
	// 正常路径：只更新 name，schedule/prompt 等应不出现在 body
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPatch, r.Method)
		assert.Equal(t, "/oc/cron/jobs/job-abc", r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"job-abc","name":"新名称"}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	newName := "新名称"
	req := ocops.CronUpdateReq{Name: &newName}
	job, err := c.CronUpdate(context.Background(), ep, "job-abc", req)
	require.NoError(t, err)
	assert.Equal(t, "job-abc", job.ID)
	// 只有 name 出现在 body，schedule/prompt 等不应序列化
	assert.Equal(t, "新名称", gotBody["name"])
	_, hasSchedule := gotBody["schedule"]
	assert.False(t, hasSchedule, "schedule 为 nil 时不应出现在请求体")
}

// TestCronToggle 验证 CronToggle 发出 POST /oc/cron/jobs/{id}/toggle
// 并在 body 中携带 {"enabled":true/false}。
func TestCronToggle(t *testing.T) {
	testCases := []struct {
		name    string
		enabled bool
	}{
		// 启用任务：body 中 enabled 为 true
		{"启用任务", true},
		// 禁用任务：body 中 enabled 为 false
		{"禁用任务", false},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var gotBody map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/oc/cron/jobs/job-toggle/toggle", r.URL.Path)
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &gotBody)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"id":"job-toggle","enabled":true}`))
			}))
			defer srv.Close()

			c, ep := newTestClient(srv)
			_, err := c.CronToggle(context.Background(), ep, "job-toggle", tc.enabled)
			require.NoError(t, err)
			// 断言 enabled 字段值与参数一致
			assert.Equal(t, tc.enabled, gotBody["enabled"])
		})
	}
}

// TestCronRun 验证 CronRun 发出 POST /oc/cron/jobs/{id}/run（无请求体）。
func TestCronRun(t *testing.T) {
	// 正常路径：立即触发任务，body 为空，返回任务快照
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/oc/cron/jobs/job-run/run", r.URL.Path)
		// 无请求体时 Content-Type 不应设置
		assert.Empty(t, r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"job-run","last_status":"running"}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	job, err := c.CronRun(context.Background(), ep, "job-run")
	require.NoError(t, err)
	assert.Equal(t, "job-run", job.ID)
}

// TestCronDelete 验证 CronDelete 发出 DELETE /oc/cron/jobs/{id}；
// server 返回 204 No Content，方法不返回错误。
func TestCronDelete(t *testing.T) {
	// 正常路径：DELETE 204，无 body，方法返回 nil error
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		assert.Equal(t, "/oc/cron/jobs/job-del", r.URL.Path)
		// DELETE 成功返回 204 No Content（无响应体）
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	err := c.CronDelete(context.Background(), ep, "job-del")
	require.NoError(t, err)
}

// TestCronDeleteNotFound 验证任务不存在时 CronDelete 返回 ErrNotFound。
func TestCronDeleteNotFound(t *testing.T) {
	// 异常路径：DELETE 404，应映射为 ErrNotFound
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"任务不存在"}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	err := c.CronDelete(context.Background(), ep, "ghost-job")
	require.ErrorIs(t, err, ocops.ErrNotFound)
}

// TestCronHistory 验证 CronHistory 发出 GET /oc/cron/jobs/{id}/history
// 并将响应解码为 []CronRunEntry。
func TestCronHistory(t *testing.T) {
	// 正常路径：server 返回两条历史记录，断言解码正确
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/oc/cron/jobs/job-hist/history", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"job_id":"job-hist","file_name":"2026-05-29.md","size":1024,"has_output":true,"synthetic":false},
			{"job_id":"job-hist","file_name":"2026-05-28.md","size":0,"has_output":false,"synthetic":true}
		]`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	entries, err := c.CronHistory(context.Background(), ep, "job-hist")
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "2026-05-29.md", entries[0].FileName)
	assert.True(t, entries[0].HasOutput)
	assert.True(t, entries[1].Synthetic)
}

// TestCronOutput 验证 CronOutput 发出 GET /oc/cron/jobs/{id}/output?file={file}，
// query 参数 file 正确序列化，响应解码为 CronRunOutput。
func TestCronOutput(t *testing.T) {
	// 正常路径：断言 query 中 file 参数正确，响应内容解码到 CronRunOutput.Content
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/oc/cron/jobs/job-out/output", r.URL.Path)
		// 断言 file query 参数已正确传递
		assert.Equal(t, "2026-05-29.md", r.URL.Query().Get("file"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"job_id":"job-out","file_name":"2026-05-29.md","content":"# 今日早报\n..."}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	out, err := c.CronOutput(context.Background(), ep, "job-out", "2026-05-29.md")
	require.NoError(t, err)
	assert.Equal(t, "job-out", out.JobID)
	assert.Equal(t, "2026-05-29.md", out.FileName)
	assert.Contains(t, out.Content, "今日早报")
}

// TestCronOutputFileQueryEscape 验证 file 参数含特殊字符时正确 URL 编码。
func TestCronOutputFileQueryEscape(t *testing.T) {
	// 边界：file 名含空格等特殊字符，query string 必须正确编码
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// url.Values.Encode 会对 value 做 query escape
		assert.Equal(t, "2026 05 29.md", r.URL.Query().Get("file"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"job_id":"j","file_name":"2026 05 29.md","content":""}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	_, err := c.CronOutput(context.Background(), ep, "j", "2026 05 29.md")
	require.NoError(t, err)
}

// TestCronCreateSkillsField 验证 skills 列表正确序列化进请求体。
func TestCronCreateSkillsField(t *testing.T) {
	// 正常路径：skills 字段为字符串数组，server 端应收到 JSON 数组
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"j-sk"}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	req := ocops.CronCreateReq{
		Name:     "带技能任务",
		Schedule: "every 1h",
		Skills:   []string{"golang", "python"},
	}
	_, err := c.CronCreate(context.Background(), ep, req)
	require.NoError(t, err)
	// 断言 skills 字段为数组且包含正确元素
	skills, ok := gotBody["skills"].([]any)
	require.True(t, ok, "skills 应为 JSON 数组")
	assert.Equal(t, []any{"golang", "python"}, skills)
}

// TestCronUpdateClearSkills 验证 CronUpdateReq.ClearSkills=true 时出现在请求体中。
func TestCronUpdateClearSkills(t *testing.T) {
	// 边界：clear_skills=true 应出现在 PATCH body，表示清空技能列表
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"j-cs"}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	clearSkills := true
	req := ocops.CronUpdateReq{ClearSkills: &clearSkills}
	_, err := c.CronUpdate(context.Background(), ep, "j-cs", req)
	require.NoError(t, err)
	// clear_skills=true 应出现在 body
	assert.Equal(t, true, gotBody["clear_skills"])
}
