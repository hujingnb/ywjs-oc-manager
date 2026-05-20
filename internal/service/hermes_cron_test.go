package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/runtime"
)

// cronOKEnvelope 把 data JSON 包成 oc-cron 成功信封，模拟 runtime 容器 stdout。
func cronOKEnvelope(dataJSON string) string {
	return `{"ok":true,"data":` + dataJSON + `}`
}

// cronErrEnvelope 把错误码包成 oc-cron 失败信封，模拟适配层契约错误。
func cronErrEnvelope(code, message string) string {
	return `{"ok":false,"error":{"code":"` + code + `","message":"` + message + `"}}`
}

// fakeCronExecer 记录最后一次容器命令，并返回预设 JSON 执行结果。
type fakeCronExecer struct {
	lastCmd []string
	result  runtime.ExecJSONResult
	err     error
}

func (f *fakeCronExecer) ContainerExecJSON(_ context.Context, _, _ string, cmd []string) (runtime.ExecJSONResult, error) {
	f.lastCmd = cmd
	return f.result, f.err
}

// fakeCronLocator 返回预设 app 坐标，覆盖权限、stub 和容器状态分支。
type fakeCronLocator struct {
	loc CronAppLocation
	err error
}

func (f *fakeCronLocator) LocateApp(_ context.Context, _ string) (CronAppLocation, error) {
	return f.loc, f.err
}

// healthyCronLoc 返回一个可执行 oc-cron 的正常 app 坐标。
func healthyCronLoc() CronAppLocation {
	return CronAppLocation{OrgID: "org-1", OwnerUserID: "u-1", NodeID: "n-1", ContainerID: "c-1"}
}

// cronOrgAdmin 返回 org-1 的组织管理员 principal。
func cronOrgAdmin() auth.Principal {
	return auth.Principal{UserID: "admin-1", OrgID: "org-1", Role: domain.UserRoleOrgAdmin}
}

// TestCronListJobsHappy 验证：ListJobs 解析 oc-cron list 成功信封并保留 schedule/repeat 字段。
func TestCronListJobsHappy(t *testing.T) {
	execer := &fakeCronExecer{result: runtime.ExecJSONResult{
		ExitCode: 0,
		Stdout: cronOKEnvelope(`[{"id":"job_1","name":"日报","prompt":"生成日报",` +
			`"schedule":{"kind":"cron","expr":"0 9 * * *","display":"每天 09:00"},` +
			`"repeat":{"times":3,"completed":1},"enabled":true,"state":"scheduled","skills":["daily"]}]`),
	}}
	svc := NewHermesCronService(execer, &fakeCronLocator{loc: healthyCronLoc()})

	jobs, err := svc.ListJobs(context.Background(), cronOrgAdmin(), "app-1", CronJobFilter{})
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, "job_1", jobs[0].ID)
	assert.Equal(t, "每天 09:00", jobs[0].Schedule.Display)
	require.NotNil(t, jobs[0].Repeat.Times)
	assert.Equal(t, 3, *jobs[0].Repeat.Times)
	assert.Equal(t, []string{"oc-cron", "list"}, execer.lastCmd)
}

// TestCronResolveForbidden 验证：非本组织管理员不能访问应用 Cron。
func TestCronResolveForbidden(t *testing.T) {
	svc := NewHermesCronService(&fakeCronExecer{}, &fakeCronLocator{loc: healthyCronLoc()})
	outsider := auth.Principal{UserID: "x", OrgID: "org-2", Role: domain.UserRoleOrgAdmin}

	_, err := svc.ListJobs(context.Background(), outsider, "app-1", CronJobFilter{})
	require.ErrorIs(t, err, ErrCronForbidden)
}

// TestCronResolveStubUnsupported 验证：dev stub 镜像实例返回 Cron 不支持错误。
func TestCronResolveStubUnsupported(t *testing.T) {
	loc := healthyCronLoc()
	loc.Stub = true
	svc := NewHermesCronService(&fakeCronExecer{}, &fakeCronLocator{loc: loc})

	_, err := svc.ListJobs(context.Background(), cronOrgAdmin(), "app-1", CronJobFilter{})
	require.ErrorIs(t, err, ErrCronNotSupported)
}

// TestCronResolveRuntimeUnavailable 验证：容器未运行时拒绝执行 oc-cron。
func TestCronResolveRuntimeUnavailable(t *testing.T) {
	loc := healthyCronLoc()
	loc.ContainerID = " "
	svc := NewHermesCronService(&fakeCronExecer{}, &fakeCronLocator{loc: loc})

	_, err := svc.ListJobs(context.Background(), cronOrgAdmin(), "app-1", CronJobFilter{})
	require.ErrorIs(t, err, ErrCronRuntimeUnavailable)
}

// TestCronErrorCodeMapping 验证：oc-cron 失败信封错误码映射到 service 哨兵错误。
func TestCronErrorCodeMapping(t *testing.T) {
	cases := []struct {
		name    string
		code    string
		wantErr error
	}{
		{"参数非法映射为 BadRequest", "BAD_REQUEST", ErrCronBadRequest},      // 场景：适配层参数校验失败
		{"资源不存在映射为 NotFound", "NOT_FOUND", ErrNotFound},               // 场景：任务或输出文件不存在
		{"能力不支持映射为 NotSupported", "UNSUPPORTED", ErrCronNotSupported}, // 场景：镜像内缺失 hermes cron 能力
		{"hermes 执行失败映射为 CLI 错误", "HERMES_CLI_FAILED", ErrCronCLI},    // 场景：底层 hermes cron 非零退出
		{"内部错误映射为输出非法", "INTERNAL", ErrCronOutputInvalid},             // 场景：适配层内部数据结构异常
		{"未知错误码兜底为 CLI 错误", "UNKNOWN_CODE", ErrCronCLI},               // 场景：未来错误码未被当前 manager 识别
	}
	for _, c := range cases {
		// 当前子测试覆盖单个 oc-cron 错误码的映射路径。
		t.Run(c.name, func(t *testing.T) {
			execer := &fakeCronExecer{result: runtime.ExecJSONResult{
				ExitCode: 1,
				Stdout:   cronErrEnvelope(c.code, "失败详情"),
			}}
			svc := NewHermesCronService(execer, &fakeCronLocator{loc: healthyCronLoc()})

			_, err := svc.ListJobs(context.Background(), cronOrgAdmin(), "app-1", CronJobFilter{})
			require.ErrorIs(t, err, c.wantErr)
		})
	}
}

// TestCronRejectsBadJobID 验证：非法 job id 在 service 层被拒绝，不触达容器 exec。
func TestCronRejectsBadJobID(t *testing.T) {
	execer := &fakeCronExecer{}
	svc := NewHermesCronService(execer, &fakeCronLocator{loc: healthyCronLoc()})

	_, err := svc.ShowJob(context.Background(), cronOrgAdmin(), "app-1", "job; rm -rf /")
	require.ErrorIs(t, err, ErrCronBadRequest)
	assert.Nil(t, execer.lastCmd)
}

// TestCronCreateBuildsArgv 验证：CreateJob 将基础字段和高级字段转换为稳定 oc-cron argv。
func TestCronCreateBuildsArgv(t *testing.T) {
	repeat := 3
	execer := &fakeCronExecer{result: runtime.ExecJSONResult{
		ExitCode: 0,
		Stdout:   cronOKEnvelope(`{"id":"job_1","name":"日报","schedule":{"display":"每天 09:00"},"repeat":{"times":3,"completed":0},"enabled":true,"state":"scheduled"}`),
	}}
	svc := NewHermesCronService(execer, &fakeCronLocator{loc: healthyCronLoc()})

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
	assert.Equal(t, []string{
		"oc-cron", "create",
		"--name", "日报",
		"--schedule", "0 9 * * *",
		"--prompt", "生成日报",
		"--deliver", "wechat",
		"--repeat", "3",
		"--script", "daily.py",
		"--no-agent",
		"--workdir", "/opt/data",
		"--skill", "daily",
		"--skill", "summary",
		"--model", "gpt-5",
		"--provider", "openai",
		"--base-url", "https://api.example.test",
	}, execer.lastCmd)
}

// TestCronCapabilitiesParsesEnvelope 验证：Capabilities 解析契约版本、verb 清单与 feature 开关。
func TestCronCapabilitiesParsesEnvelope(t *testing.T) {
	capsJSON := `{"contract_version":"1.0","oc_cron_version":"1","hermes_version":"v0.14.0",` +
		`"variant":"hermes-v2026.5.16","verbs":["status","list","create"],` +
		`"features":{"status":true,"history":true,"output":true,"write":true,"script":true,"advanced_fields":true}}`
	execer := &fakeCronExecer{result: runtime.ExecJSONResult{ExitCode: 0, Stdout: cronOKEnvelope(capsJSON)}}
	svc := NewHermesCronService(execer, &fakeCronLocator{loc: healthyCronLoc()})

	caps, err := svc.Capabilities(context.Background(), cronOrgAdmin(), "app-1")
	require.NoError(t, err)
	assert.Equal(t, "1.0", caps.ContractVersion)
	assert.Equal(t, "1", caps.OCCronVersion)
	assert.True(t, caps.Features.AdvancedFields)
	assert.Contains(t, caps.Verbs, "create")
	assert.Equal(t, []string{"oc-cron", "capabilities"}, execer.lastCmd)
}

// TestCronRejectsNonzeroExitWithOKEnvelope 验证：非零退出码即使返回 ok:true 也按 CLI 失败处理。
func TestCronRejectsNonzeroExitWithOKEnvelope(t *testing.T) {
	execer := &fakeCronExecer{result: runtime.ExecJSONResult{
		ExitCode: 2,
		Stdout:   cronOKEnvelope(`{"available":true,"gateway_running":true,"active_jobs":1}`),
		Stderr:   "permission denied",
	}}
	svc := NewHermesCronService(execer, &fakeCronLocator{loc: healthyCronLoc()})

	_, err := svc.Status(context.Background(), cronOrgAdmin(), "app-1")
	require.ErrorIs(t, err, ErrCronCLI)
	assert.ErrorContains(t, err, "exit 2")
	assert.ErrorContains(t, err, "permission denied")
}

// TestCronNonzeroExitMalformedStdoutIsCLI 验证：非零退出码且 stdout 非信封时稳定归类为 CLI 失败。
func TestCronNonzeroExitMalformedStdoutIsCLI(t *testing.T) {
	execer := &fakeCronExecer{result: runtime.ExecJSONResult{
		ExitCode: 127,
		Stdout:   "oc-cron: not found",
		Stderr:   "executable file not found",
	}}
	svc := NewHermesCronService(execer, &fakeCronLocator{loc: healthyCronLoc()})

	_, err := svc.Status(context.Background(), cronOrgAdmin(), "app-1")
	require.ErrorIs(t, err, ErrCronCLI)
	require.NotErrorIs(t, err, ErrCronOutputInvalid)
	assert.ErrorContains(t, err, "exit 127")
}

// TestCronRejectsNullData 验证：成功信封的 data:null 在结构和切片路径都按输出非法处理。
func TestCronRejectsNullData(t *testing.T) {
	cases := []struct {
		name string
		call func(*HermesCronService) error
	}{
		{
			name: "struct 路径拒绝 null data", // 场景：Status 需要结构化对象，null 不能被当成零值状态
			call: func(svc *HermesCronService) error {
				_, err := svc.Status(context.Background(), cronOrgAdmin(), "app-1")
				return err
			},
		},
		{
			name: "slice 路径拒绝 null data", // 场景：ListJobs 需要数组，null 不能被当成空列表
			call: func(svc *HermesCronService) error {
				_, err := svc.ListJobs(context.Background(), cronOrgAdmin(), "app-1", CronJobFilter{})
				return err
			},
		},
	}
	for _, c := range cases {
		// 当前子测试覆盖一种成功信封 data:null 被拒绝的解析路径。
		t.Run(c.name, func(t *testing.T) {
			execer := &fakeCronExecer{result: runtime.ExecJSONResult{
				ExitCode: 0,
				Stdout:   cronOKEnvelope(`null`),
			}}
			svc := NewHermesCronService(execer, &fakeCronLocator{loc: healthyCronLoc()})

			err := c.call(svc)
			require.ErrorIs(t, err, ErrCronOutputInvalid)
		})
	}
}

// TestCronToggleRunAndDeleteBuildArgv 验证：Pause/Resume/Run/Delete 使用稳定 oc-cron verbs。
func TestCronToggleRunAndDeleteBuildArgv(t *testing.T) {
	cases := []struct {
		name    string
		call    func(*HermesCronService) error
		wantCmd []string
	}{
		{
			name: "暂停任务映射为 toggle false", // 场景：PauseJob 必须调用稳定 toggle verb 并显式关闭
			call: func(svc *HermesCronService) error {
				_, err := svc.PauseJob(context.Background(), cronOrgAdmin(), "app-1", "job_1")
				return err
			},
			wantCmd: []string{"oc-cron", "toggle", "--id", "job_1", "--enabled", "false"},
		},
		{
			name: "恢复任务映射为 toggle true", // 场景：ResumeJob 必须调用稳定 toggle verb 并显式开启
			call: func(svc *HermesCronService) error {
				_, err := svc.ResumeJob(context.Background(), cronOrgAdmin(), "app-1", "job_1")
				return err
			},
			wantCmd: []string{"oc-cron", "toggle", "--id", "job_1", "--enabled", "true"},
		},
		{
			name: "立即运行任务调用 run", // 场景：RunJob 透传稳定 run verb
			call: func(svc *HermesCronService) error {
				_, err := svc.RunJob(context.Background(), cronOrgAdmin(), "app-1", "job_1")
				return err
			},
			wantCmd: []string{"oc-cron", "run", "--id", "job_1"},
		},
		{
			name: "删除任务调用 delete", // 场景：DeleteJob 透传稳定 delete verb 并忽略成功 data
			call: func(svc *HermesCronService) error {
				return svc.DeleteJob(context.Background(), cronOrgAdmin(), "app-1", "job_1")
			},
			wantCmd: []string{"oc-cron", "delete", "--id", "job_1"},
		},
	}
	for _, c := range cases {
		// 当前子测试覆盖一个写 verb 到 oc-cron argv 的稳定映射。
		t.Run(c.name, func(t *testing.T) {
			execer := &fakeCronExecer{result: runtime.ExecJSONResult{
				ExitCode: 0,
				Stdout:   cronOKEnvelope(`{"id":"job_1","name":"日报","schedule":{"display":"每天"},"repeat":{"completed":0},"enabled":true,"state":"scheduled"}`),
			}}
			svc := NewHermesCronService(execer, &fakeCronLocator{loc: healthyCronLoc()})

			err := c.call(svc)
			require.NoError(t, err)
			assert.Equal(t, c.wantCmd, execer.lastCmd)
		})
	}
}

// TestCronHistoryAndOutputParse 验证：History/Output 解析输出历史列表与 markdown 内容。
func TestCronHistoryAndOutputParse(t *testing.T) {
	t.Run("history 解析输出列表", func(t *testing.T) {
		// 场景：history 返回一次真实 markdown 输出记录。
		execer := &fakeCronExecer{result: runtime.ExecJSONResult{
			ExitCode: 0,
			Stdout:   cronOKEnvelope(`[{"job_id":"job_1","file_name":"2026-05-20.md","run_time":"2026-05-20T09:00:00Z","size":12,"has_output":true,"synthetic":false}]`),
		}}
		svc := NewHermesCronService(execer, &fakeCronLocator{loc: healthyCronLoc()})

		entries, err := svc.History(context.Background(), cronOrgAdmin(), "app-1", "job_1")
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "2026-05-20.md", entries[0].FileName)
		assert.Equal(t, []string{"oc-cron", "history", "--id", "job_1"}, execer.lastCmd)
	})

	t.Run("output 解析 markdown 内容", func(t *testing.T) {
		// 场景：output 返回指定 markdown 文件内容。
		execer := &fakeCronExecer{result: runtime.ExecJSONResult{
			ExitCode: 0,
			Stdout:   cronOKEnvelope(`{"job_id":"job_1","file_name":"2026-05-20.md","run_time":"2026-05-20T09:00:00Z","content":"# 日报\n"}`),
		}}
		svc := NewHermesCronService(execer, &fakeCronLocator{loc: healthyCronLoc()})

		out, err := svc.Output(context.Background(), cronOrgAdmin(), "app-1", "job_1", "2026-05-20.md")
		require.NoError(t, err)
		assert.Equal(t, "# 日报\n", out.Content)
		assert.Equal(t, []string{"oc-cron", "output", "--id", "job_1", "--file", "2026-05-20.md"}, execer.lastCmd)
	})
}

// TestCronExecErrorWrapped 验证：runtime exec 失败时统一包成 ErrCronCLI。
func TestCronExecErrorWrapped(t *testing.T) {
	execer := &fakeCronExecer{err: errors.New("agent offline")}
	svc := NewHermesCronService(execer, &fakeCronLocator{loc: healthyCronLoc()})

	_, err := svc.Status(context.Background(), cronOrgAdmin(), "app-1")
	require.ErrorIs(t, err, ErrCronCLI)
}
