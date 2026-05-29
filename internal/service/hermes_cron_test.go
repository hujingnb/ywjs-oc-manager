package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/ocops"
)

// fakeCronOps 是 cronOps 的假实现：记录最后一次调用的入参，返回预设桩值/错误。
// 每个方法只关心被测路径需要的字段，未用到的留零值。
type fakeCronOps struct {
	// lastEndpoint 记录最近一次被调用方法收到的 endpoint，便于断言坐标透传。
	lastEndpoint ocops.Endpoint
	// lastJobID 记录按任务 ID 定位的方法收到的 id。
	lastJobID string
	// lastAll 记录 CronList 收到的 all 参数。
	lastAll bool
	// lastEnabled 记录 CronToggle 收到的 enabled 参数。
	lastEnabled bool
	// lastFile 记录 CronOutput 收到的文件名。
	lastFile string
	// lastCreateReq / lastUpdateReq 记录写方法构造出的类型化请求体。
	lastCreateReq ocops.CronCreateReq
	lastUpdateReq ocops.CronUpdateReq
	// called 记录最近一次被调用的方法名，断言「未触达上游」时用。
	called string

	// 各方法的桩返回值。
	caps    ocops.CronCapabilities
	status  ocops.CronStatus
	jobs    []ocops.CronJob
	job     ocops.CronJob
	history []ocops.CronRunEntry
	output  ocops.CronRunOutput
	// err 为非 nil 时所有方法直接返回它，用于覆盖 mapOcOpsCronErr 路径。
	err error
}

func (f *fakeCronOps) CronCapabilities(_ context.Context, ep ocops.Endpoint) (ocops.CronCapabilities, error) {
	f.called, f.lastEndpoint = "capabilities", ep
	return f.caps, f.err
}

func (f *fakeCronOps) CronStatus(_ context.Context, ep ocops.Endpoint) (ocops.CronStatus, error) {
	f.called, f.lastEndpoint = "status", ep
	return f.status, f.err
}

func (f *fakeCronOps) CronList(_ context.Context, ep ocops.Endpoint, all bool) ([]ocops.CronJob, error) {
	f.called, f.lastEndpoint, f.lastAll = "list", ep, all
	return f.jobs, f.err
}

func (f *fakeCronOps) CronShow(_ context.Context, ep ocops.Endpoint, id string) (ocops.CronJob, error) {
	f.called, f.lastEndpoint, f.lastJobID = "show", ep, id
	return f.job, f.err
}

func (f *fakeCronOps) CronCreate(_ context.Context, ep ocops.Endpoint, req ocops.CronCreateReq) (ocops.CronJob, error) {
	f.called, f.lastEndpoint, f.lastCreateReq = "create", ep, req
	return f.job, f.err
}

func (f *fakeCronOps) CronUpdate(_ context.Context, ep ocops.Endpoint, id string, req ocops.CronUpdateReq) (ocops.CronJob, error) {
	f.called, f.lastEndpoint, f.lastJobID, f.lastUpdateReq = "update", ep, id, req
	return f.job, f.err
}

func (f *fakeCronOps) CronToggle(_ context.Context, ep ocops.Endpoint, id string, enabled bool) (ocops.CronJob, error) {
	f.called, f.lastEndpoint, f.lastJobID, f.lastEnabled = "toggle", ep, id, enabled
	return f.job, f.err
}

func (f *fakeCronOps) CronRun(_ context.Context, ep ocops.Endpoint, id string) (ocops.CronJob, error) {
	f.called, f.lastEndpoint, f.lastJobID = "run", ep, id
	return f.job, f.err
}

func (f *fakeCronOps) CronDelete(_ context.Context, ep ocops.Endpoint, id string) error {
	f.called, f.lastEndpoint, f.lastJobID = "delete", ep, id
	return f.err
}

func (f *fakeCronOps) CronHistory(_ context.Context, ep ocops.Endpoint, id string) ([]ocops.CronRunEntry, error) {
	f.called, f.lastEndpoint, f.lastJobID = "history", ep, id
	return f.history, f.err
}

func (f *fakeCronOps) CronOutput(_ context.Context, ep ocops.Endpoint, id, file string) (ocops.CronRunOutput, error) {
	f.called, f.lastEndpoint, f.lastJobID, f.lastFile = "output", ep, id, file
	return f.output, f.err
}

// fakeOcOpsResolver 返回预设的 app 坐标，覆盖权限、Supported 和 BaseURL 分支。
type fakeOcOpsResolver struct {
	loc OcOpsAppLocation
	err error
}

func (f *fakeOcOpsResolver) Resolve(_ context.Context, _ string) (OcOpsAppLocation, error) {
	return f.loc, f.err
}

// healthyCronLoc 返回一个可调用 oc-ops 的正常 app 坐标（Supported 且 BaseURL 非空）。
func healthyCronLoc() OcOpsAppLocation {
	return OcOpsAppLocation{
		OrgID:       "org-1",
		OwnerUserID: "u-1",
		Endpoint:    ocops.Endpoint{BaseURL: "http://app-1-ocops:8080"},
		Supported:   true,
	}
}

// cronOrgAdmin 返回 org-1 的组织管理员 principal。
func cronOrgAdmin() auth.Principal {
	return auth.Principal{UserID: "admin-1", OrgID: "org-1", Role: domain.UserRoleOrgAdmin}
}

// newCronSvc 构造注入假 ops / resolver 的 service。
func newCronSvc(ops cronOps, loc OcOpsAppLocation) *HermesCronService {
	return NewHermesCronService(ops, &fakeOcOpsResolver{loc: loc})
}

// TestCronListJobsHappy 验证：ListJobs 透传 all 标志、回传类型化任务并保留 schedule/repeat 字段。
func TestCronListJobsHappy(t *testing.T) {
	times := 3
	ops := &fakeCronOps{jobs: []ocops.CronJob{{
		ID:       "job_1",
		Name:     "日报",
		Prompt:   "生成日报",
		Schedule: ocops.CronSchedule{Kind: "cron", Expr: "0 9 * * *", Display: "每天 09:00"},
		Repeat:   ocops.CronRepeat{Times: &times, Completed: 1},
		Enabled:  true,
		State:    "scheduled",
		Skills:   []string{"daily"},
	}}}
	svc := newCronSvc(ops, healthyCronLoc())

	jobs, err := svc.ListJobs(context.Background(), cronOrgAdmin(), "app-1", CronJobFilter{All: true})
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, "job_1", jobs[0].ID)
	assert.Equal(t, "每天 09:00", jobs[0].Schedule.Display)
	require.NotNil(t, jobs[0].Repeat.Times)
	assert.Equal(t, 3, *jobs[0].Repeat.Times)
	// all=true 必须透传给 ops，endpoint 必须来自 resolver 坐标。
	assert.True(t, ops.lastAll)
	assert.Equal(t, "http://app-1-ocops:8080", ops.lastEndpoint.BaseURL)
}

// TestCronResolveForbidden 验证：非本组织管理员不能访问应用 Cron，且不触达上游。
func TestCronResolveForbidden(t *testing.T) {
	ops := &fakeCronOps{}
	svc := newCronSvc(ops, healthyCronLoc())
	outsider := auth.Principal{UserID: "x", OrgID: "org-2", Role: domain.UserRoleOrgAdmin}

	_, err := svc.ListJobs(context.Background(), outsider, "app-1", CronJobFilter{})
	require.ErrorIs(t, err, ErrCronForbidden)
	assert.Empty(t, ops.called)
}

// TestCronResolveUnsupported 验证：Supported=false（dev stub）实例返回 Cron 不支持错误。
func TestCronResolveUnsupported(t *testing.T) {
	loc := healthyCronLoc()
	loc.Supported = false
	ops := &fakeCronOps{}
	svc := newCronSvc(ops, loc)

	_, err := svc.ListJobs(context.Background(), cronOrgAdmin(), "app-1", CronJobFilter{})
	require.ErrorIs(t, err, ErrCronNotSupported)
	assert.Empty(t, ops.called)
}

// TestCronResolveRuntimeUnavailable 验证：Endpoint.BaseURL 为空白时拒绝调用 oc-ops。
func TestCronResolveRuntimeUnavailable(t *testing.T) {
	loc := healthyCronLoc()
	loc.Endpoint.BaseURL = "  "
	ops := &fakeCronOps{}
	svc := newCronSvc(ops, loc)

	_, err := svc.ListJobs(context.Background(), cronOrgAdmin(), "app-1", CronJobFilter{})
	require.ErrorIs(t, err, ErrCronRuntimeUnavailable)
	assert.Empty(t, ops.called)
}

// TestCronResolverError 验证：resolver 返回 ErrNotFound（app 不存在）原样透出。
func TestCronResolverError(t *testing.T) {
	svc := NewHermesCronService(&fakeCronOps{}, &fakeOcOpsResolver{err: ErrNotFound})

	_, err := svc.ListJobs(context.Background(), cronOrgAdmin(), "app-1", CronJobFilter{})
	require.ErrorIs(t, err, ErrNotFound)
}

// TestCronErrorMapping 验证：ops 返回的 ocops 哨兵错误经 mapOcOpsCronErr 翻译为 service 哨兵错误。
func TestCronErrorMapping(t *testing.T) {
	cases := []struct {
		name    string
		opsErr  error
		wantErr error
	}{
		{"400 映射为 BadRequest", ocops.ErrBadRequest, ErrCronBadRequest},     // 场景：上游参数校验失败
		{"404 映射为 NotFound", ocops.ErrNotFound, ErrNotFound},               // 场景：任务不存在
		{"409 映射为 NotSupported", ocops.ErrUnsupported, ErrCronNotSupported}, // 场景：实例缺失 cron 能力
		{"500 映射为输出非法", ocops.ErrOutputInvalid, ErrCronOutputInvalid},     // 场景：上游内部错误
		{"未知错误兜底为 CLI 错误", errors.New("boom"), ErrCronCLI},               // 场景：传输/未知上游错误
	}
	for _, c := range cases {
		// 当前子测试覆盖单个 ocops 错误到 service 哨兵错误的翻译路径。
		t.Run(c.name, func(t *testing.T) {
			ops := &fakeCronOps{err: c.opsErr}
			svc := newCronSvc(ops, healthyCronLoc())

			_, err := svc.ListJobs(context.Background(), cronOrgAdmin(), "app-1", CronJobFilter{})
			require.ErrorIs(t, err, c.wantErr)
		})
	}
}

// TestCronRejectsBadJobID 验证：非法 job id 在 service 层被拒绝，不触达上游。
func TestCronRejectsBadJobID(t *testing.T) {
	ops := &fakeCronOps{}
	svc := newCronSvc(ops, healthyCronLoc())

	_, err := svc.ShowJob(context.Background(), cronOrgAdmin(), "app-1", "job; rm -rf /")
	require.ErrorIs(t, err, ErrCronBadRequest)
	assert.Empty(t, ops.called)
}

// TestCronCreateBuildsReq 验证：CreateJob 把基础字段和高级字段转换为类型化 CronCreateReq。
func TestCronCreateBuildsReq(t *testing.T) {
	repeat := 3
	ops := &fakeCronOps{job: ocops.CronJob{ID: "job_1", Name: "日报", Enabled: true, State: "scheduled"}}
	svc := newCronSvc(ops, healthyCronLoc())

	job, err := svc.CreateJob(context.Background(), cronOrgAdmin(), "app-1", CreateCronJobInput{
		Name:     "日报",
		Schedule: "0 9 * * *",
		Prompt:   "生成日报",
		Deliver:  "wechat",
		Repeat:   &repeat,
		Script:   "daily.py",
		NoAgent:  true,
		Workdir:  "/opt/data",
		Skills:   []string{"daily", "summary"},
		Model:    "gpt-5",
		Provider: "openai",
		BaseURL:  "https://api.example.test",
	})
	require.NoError(t, err)
	assert.Equal(t, "job_1", job.ID)
	// 断言构造出的类型化请求体逐字段正确。
	req := ops.lastCreateReq
	assert.Equal(t, "日报", req.Name)
	assert.Equal(t, "0 9 * * *", req.Schedule)
	assert.Equal(t, "生成日报", req.Prompt)
	assert.Equal(t, "wechat", req.Deliver)
	assert.Equal(t, 3, req.Repeat) // repeat 以裸整数透传，等价旧 --repeat 3
	assert.Equal(t, "daily.py", req.Script)
	assert.True(t, req.NoAgent)
	assert.Equal(t, "/opt/data", req.Workdir)
	assert.Equal(t, []string{"daily", "summary"}, req.Skills)
	assert.Equal(t, "gpt-5", req.Model)
	assert.Equal(t, "openai", req.Provider)
	assert.Equal(t, "https://api.example.test", req.BaseURL)
}

// TestCronCreateRejectsEmptyName 验证：缺少必填 name 时在 service 层校验失败，不触达上游。
func TestCronCreateRejectsEmptyName(t *testing.T) {
	ops := &fakeCronOps{}
	svc := newCronSvc(ops, healthyCronLoc())

	_, err := svc.CreateJob(context.Background(), cronOrgAdmin(), "app-1", CreateCronJobInput{
		Name:     "  ",
		Schedule: "0 9 * * *",
	})
	require.ErrorIs(t, err, ErrCronBadRequest)
	assert.Empty(t, ops.called)
}

// TestCronCreateRejectsBadScript 验证：含路径分隔的 script 被拒绝（防目录逃逸），不触达上游。
func TestCronCreateRejectsBadScript(t *testing.T) {
	ops := &fakeCronOps{}
	svc := newCronSvc(ops, healthyCronLoc())

	_, err := svc.CreateJob(context.Background(), cronOrgAdmin(), "app-1", CreateCronJobInput{
		Name:     "日报",
		Schedule: "0 9 * * *",
		Script:   "../etc/passwd",
	})
	require.ErrorIs(t, err, ErrCronBadRequest)
	assert.Empty(t, ops.called)
}

// TestCronUpdateBuildsReq 验证：UpdateJob 把指针字段转换为 partial CronUpdateReq，未提交字段保持 nil。
func TestCronUpdateBuildsReq(t *testing.T) {
	name := "新名"
	ops := &fakeCronOps{job: ocops.CronJob{ID: "job_1", Enabled: true, State: "scheduled"}}
	svc := newCronSvc(ops, healthyCronLoc())

	_, err := svc.UpdateJob(context.Background(), cronOrgAdmin(), "app-1", "job_1", UpdateCronJobInput{
		Name:        &name,
		ClearSkills: true,
		Agent:       true,
	})
	require.NoError(t, err)
	assert.Equal(t, "job_1", ops.lastJobID)
	req := ops.lastUpdateReq
	// 提交了的字段非 nil。
	require.NotNil(t, req.Name)
	assert.Equal(t, "新名", *req.Name)
	require.NotNil(t, req.ClearSkills)
	assert.True(t, *req.ClearSkills)
	require.NotNil(t, req.Agent)
	assert.True(t, *req.Agent)
	// 未提交字段保持 nil，体现 partial update 语义。
	assert.Nil(t, req.Schedule)
	assert.Nil(t, req.Prompt)
}

// TestCronCapabilitiesValidates 验证：Capabilities 回传类型化能力并校验契约版本存在。
func TestCronCapabilitiesParses(t *testing.T) {
	ops := &fakeCronOps{caps: ocops.CronCapabilities{
		ContractVersion: "1.0",
		OCCronVersion:   "1",
		Verbs:           []string{"status", "list", "create"},
		Features:        ocops.CronFeatures{AdvancedFields: true},
	}}
	svc := newCronSvc(ops, healthyCronLoc())

	caps, err := svc.Capabilities(context.Background(), cronOrgAdmin(), "app-1")
	require.NoError(t, err)
	assert.Equal(t, "1.0", caps.ContractVersion)
	assert.Equal(t, "1", caps.OCCronVersion)
	assert.True(t, caps.Features.AdvancedFields)
	assert.Contains(t, caps.Verbs, "create")
}

// TestCronCapabilitiesRejectsMissingVersion 验证：缺少契约版本的 capabilities 被判定为输出非法。
func TestCronCapabilitiesRejectsMissingVersion(t *testing.T) {
	ops := &fakeCronOps{caps: ocops.CronCapabilities{OCCronVersion: "1"}} // 缺 contract_version
	svc := newCronSvc(ops, healthyCronLoc())

	_, err := svc.Capabilities(context.Background(), cronOrgAdmin(), "app-1")
	require.ErrorIs(t, err, ErrCronOutputInvalid)
}

// TestCronToggleRunDelete 验证：Pause/Resume/Run/Delete 调对应 ops 方法并透传 enabled / job id。
func TestCronToggleRunDelete(t *testing.T) {
	cases := []struct {
		name        string
		call        func(*HermesCronService) error
		wantCalled  string
		wantEnabled bool // 仅对 toggle 有意义
		checkToggle bool
	}{
		{
			name: "暂停任务调 toggle false", // 场景：PauseJob 调 CronToggle 并显式关闭
			call: func(svc *HermesCronService) error {
				_, err := svc.PauseJob(context.Background(), cronOrgAdmin(), "app-1", "job_1")
				return err
			},
			wantCalled: "toggle", wantEnabled: false, checkToggle: true,
		},
		{
			name: "恢复任务调 toggle true", // 场景：ResumeJob 调 CronToggle 并显式开启
			call: func(svc *HermesCronService) error {
				_, err := svc.ResumeJob(context.Background(), cronOrgAdmin(), "app-1", "job_1")
				return err
			},
			wantCalled: "toggle", wantEnabled: true, checkToggle: true,
		},
		{
			name: "立即运行任务调 run", // 场景：RunJob 调 CronRun
			call: func(svc *HermesCronService) error {
				_, err := svc.RunJob(context.Background(), cronOrgAdmin(), "app-1", "job_1")
				return err
			},
			wantCalled: "run",
		},
		{
			name: "删除任务调 delete", // 场景：DeleteJob 调 CronDelete
			call: func(svc *HermesCronService) error {
				return svc.DeleteJob(context.Background(), cronOrgAdmin(), "app-1", "job_1")
			},
			wantCalled: "delete",
		},
	}
	for _, c := range cases {
		// 当前子测试覆盖一个写方法到对应 ops 方法的稳定映射。
		t.Run(c.name, func(t *testing.T) {
			ops := &fakeCronOps{job: ocops.CronJob{ID: "job_1", Enabled: true, State: "scheduled"}}
			svc := newCronSvc(ops, healthyCronLoc())

			err := c.call(svc)
			require.NoError(t, err)
			assert.Equal(t, c.wantCalled, ops.called)
			assert.Equal(t, "job_1", ops.lastJobID)
			if c.checkToggle {
				assert.Equal(t, c.wantEnabled, ops.lastEnabled)
			}
		})
	}
}

// TestCronHistoryAndOutput 验证：History/Output 回传类型化结果并透传 job id / 文件名。
func TestCronHistoryAndOutput(t *testing.T) {
	t.Run("history 返回输出列表", func(t *testing.T) {
		// 场景：history 返回一次真实 markdown 输出记录。
		ops := &fakeCronOps{history: []ocops.CronRunEntry{{
			JobID:    "job_1",
			FileName: "2026-05-20.md",
			RunTime:  "2026-05-20T09:00:00Z",
			Size:     12,
		}}}
		svc := newCronSvc(ops, healthyCronLoc())

		entries, err := svc.History(context.Background(), cronOrgAdmin(), "app-1", "job_1")
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "2026-05-20.md", entries[0].FileName)
		assert.Equal(t, "job_1", ops.lastJobID)
	})

	t.Run("output 返回 markdown 内容", func(t *testing.T) {
		// 场景：output 返回指定 markdown 文件内容。
		ops := &fakeCronOps{output: ocops.CronRunOutput{
			JobID:    "job_1",
			FileName: "2026-05-20.md",
			Content:  "# 日报\n",
		}}
		svc := newCronSvc(ops, healthyCronLoc())

		out, err := svc.Output(context.Background(), cronOrgAdmin(), "app-1", "job_1", "2026-05-20.md")
		require.NoError(t, err)
		assert.Equal(t, "# 日报\n", out.Content)
		assert.Equal(t, "2026-05-20.md", ops.lastFile)
	})
}

// TestCronOutputRejectsPathEscape 验证：含路径分隔的 output 文件名被拒绝（防目录逃逸），不触达上游。
func TestCronOutputRejectsPathEscape(t *testing.T) {
	ops := &fakeCronOps{}
	svc := newCronSvc(ops, healthyCronLoc())

	_, err := svc.Output(context.Background(), cronOrgAdmin(), "app-1", "job_1", "../secret.md")
	require.ErrorIs(t, err, ErrCronBadRequest)
	assert.Empty(t, ops.called)
}

// TestCronRejectsInvalidJobIDInResult 验证：上游返回缺少合法 id 的任务对象时判定为输出非法。
func TestCronRejectsInvalidJobIDInResult(t *testing.T) {
	ops := &fakeCronOps{job: ocops.CronJob{ID: ""}} // 上游回传空 id
	svc := newCronSvc(ops, healthyCronLoc())

	_, err := svc.ShowJob(context.Background(), cronOrgAdmin(), "app-1", "job_1")
	require.ErrorIs(t, err, ErrCronOutputInvalid)
}
