// Package service —— hermes_cron.go 实现 Hermes Cron 管理能力。
// manager 不持久化 Cron 任务，所有读写都通过在 Hermes 容器内执行 `oc-cron`
// 并解析统一 JSON 信封获得。
package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/runtime"
	"oc-manager/internal/store/sqlc"
)

// cronExecer 抽象容器内执行 JSON 命令的能力，runtime.Adapter 可直接满足该接口。
type cronExecer interface {
	ContainerExecJSON(ctx context.Context, nodeID, containerID string, cmd []string) (runtime.ExecJSONResult, error)
}

// cronAppLocator 把 appID 解析为执行 oc-cron 所需的运行时坐标。
type cronAppLocator interface {
	// LocateApp 返回 app 的归属信息与运行时坐标。
	// Stub 表示该 app 运行 dev stub 镜像；ContainerID 为空表示容器未运行。
	LocateApp(ctx context.Context, appID string) (CronAppLocation, error)
}

// CronAppLocation 是执行 oc-cron 所需的全部 app 运行时信息。
type CronAppLocation struct {
	OrgID       string // app 归属组织，用于权限判断
	OwnerUserID string // app 拥有者，用于 org_member 权限判断
	NodeID      string // app 所在 runtime node
	ContainerID string // Hermes 容器 ID，空表示未运行
	Stub        bool   // 是否 dev stub 镜像
}

var (
	// cronJobIDRe 是 manager 允许透传给 oc-cron 的任务 ID 白名单，避免路径/argv 注入。
	cronJobIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	// cronScriptRe 限制 script 为单个相对文件名；不允许绝对路径、目录层级或反斜杠。
	cronScriptRe = regexp.MustCompile(`^[^/\\][^/\\]*$`)
	// cronSkillRe 限制高级 skills 字段为稳定文件名风格标识，避免把任意文本透传给 CLI。
	cronSkillRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

const (
	// 自由文本限制与 oc-cron 适配层保持同量级，避免 manager 向容器传入过大的 argv。
	cronNameMaxRunes      = 200
	cronScheduleMaxRunes  = 200
	cronPromptMaxRunes    = 5000
	cronSmallTextMaxRunes = 512
	cronMaxSkills         = 32
	cronSyntheticFile     = "__scheduler_metadata__.md"
)

// HermesCronService 暴露 Hermes Cron 的读写能力。
type HermesCronService struct {
	execer  cronExecer
	locator cronAppLocator
}

// NewHermesCronService 构造 service。
func NewHermesCronService(execer cronExecer, locator cronAppLocator) *HermesCronService {
	return &HermesCronService{execer: execer, locator: locator}
}

// resolve 解析 appID、校验读权限，并确保实例可执行 oc-cron。
func (s *HermesCronService) resolve(ctx context.Context, principal auth.Principal, appID string) (CronAppLocation, error) {
	loc, err := s.locator.LocateApp(ctx, appID)
	if err != nil {
		return CronAppLocation{}, err
	}
	if !auth.CanViewAppCron(principal, loc.OrgID, loc.OwnerUserID) {
		return CronAppLocation{}, ErrCronForbidden
	}
	if loc.Stub {
		return CronAppLocation{}, ErrCronNotSupported
	}
	if strings.TrimSpace(loc.ContainerID) == "" {
		return CronAppLocation{}, ErrCronRuntimeUnavailable
	}
	return loc, nil
}

// resolveManage 解析 appID 并校验 Cron 写权限。
// 当前 CanManageAppCron 与 CanViewAppCron 等价，仍单独调用以便未来权限收紧。
func (s *HermesCronService) resolveManage(ctx context.Context, principal auth.Principal, appID string) (CronAppLocation, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return CronAppLocation{}, err
	}
	if !auth.CanManageAppCron(principal, loc.OrgID, loc.OwnerUserID) {
		return CronAppLocation{}, ErrCronForbidden
	}
	return loc, nil
}

// cronEnvelope 是 oc-cron 输出的统一信封。
type cronEnvelope struct {
	OK    bool               `json:"ok"`
	Data  json.RawMessage    `json:"data"`
	Error *cronEnvelopeError `json:"error"`
}

// cronEnvelopeError 是失败信封里的错误对象。
type cronEnvelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// mapCronErrorCode 把 oc-cron 契约错误码映射成 service 哨兵错误。
func mapCronErrorCode(e *cronEnvelopeError) error {
	if e == nil {
		return ErrCronCLI
	}
	switch e.Code {
	case "BAD_REQUEST":
		return fmt.Errorf("%w: %s", ErrCronBadRequest, e.Message)
	case "NOT_FOUND":
		return ErrNotFound
	case "UNSUPPORTED":
		return ErrCronNotSupported
	case "INTERNAL":
		return fmt.Errorf("%w: %s", ErrCronOutputInvalid, e.Message)
	default:
		return fmt.Errorf("%w: %s", ErrCronCLI, e.Message)
	}
}

// runOCCron 在 Hermes 容器内执行一条 oc-cron 命令并解析统一信封。
// args 是 oc-cron 的 verb 与 flag，不含 "oc-cron" 前缀。
func (s *HermesCronService) runOCCron(ctx context.Context, loc CronAppLocation, args []string) (json.RawMessage, error) {
	cmd := append([]string{"oc-cron"}, args...)
	res, err := s.execer.ContainerExecJSON(ctx, loc.NodeID, loc.ContainerID, cmd)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCronCLI, err)
	}
	var env cronEnvelope
	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil {
		if res.ExitCode != 0 {
			return nil, cronExitError(res, "stdout 不是合法信封 JSON")
		}
		return nil, fmt.Errorf("%w: 信封解析失败: %v (stderr: %s)", ErrCronOutputInvalid, err, truncateCronDiagnostic(res.Stderr))
	}
	if !env.OK {
		return nil, mapCronErrorCode(env.Error)
	}
	if res.ExitCode != 0 {
		return nil, cronExitError(res, "ok:true 但进程非零退出")
	}
	return env.Data, nil
}

// cronExitError 统一包装容器内命令非零退出，保留 exit code 与 stderr/stdout 摘要。
func cronExitError(res runtime.ExecJSONResult, reason string) error {
	stderr := truncateCronDiagnostic(res.Stderr)
	stdout := truncateCronDiagnostic(res.Stdout)
	if stderr == "" && stdout == "" {
		return fmt.Errorf("%w: %s: exit %d", ErrCronCLI, reason, res.ExitCode)
	}
	if stderr == "" {
		return fmt.Errorf("%w: %s: exit %d (stdout: %s)", ErrCronCLI, reason, res.ExitCode, stdout)
	}
	if stdout == "" {
		return fmt.Errorf("%w: %s: exit %d (stderr: %s)", ErrCronCLI, reason, res.ExitCode, stderr)
	}
	return fmt.Errorf("%w: %s: exit %d (stderr: %s; stdout: %s)", ErrCronCLI, reason, res.ExitCode, stderr, stdout)
}

// truncateCronDiagnostic 按 rune 截断诊断文本，避免错误消息携带过大 stdout/stderr。
func truncateCronDiagnostic(value string) string {
	value = strings.TrimSpace(value)
	if runes := []rune(value); len(runes) > 1024 {
		return string(runes[:1024])
	}
	return value
}

// decodeCronData 把成功信封 data 解析到目标 DTO，统一包裹输出格式错误。
func decodeCronData(data json.RawMessage, out any) error {
	if len(data) == 0 || string(bytes.TrimSpace(data)) == "null" {
		return fmt.Errorf("%w: data 为空", ErrCronOutputInvalid)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%w: %v", ErrCronOutputInvalid, err)
	}
	return nil
}

// validateCronJobID 校验任务 ID，所有按任务 ID 定位的 verb 共用。
func validateCronJobID(jobID string) error {
	if !cronJobIDRe.MatchString(jobID) {
		return fmt.Errorf("%w: 非法 job id", ErrCronBadRequest)
	}
	return nil
}

// validateCronText 校验自由文本长度；required 为 true 时拒绝空白字符串。
func validateCronText(label, value string, maxRunes int, required bool) error {
	if required && strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s 不能为空", ErrCronBadRequest, label)
	}
	if len([]rune(value)) > maxRunes {
		return fmt.Errorf("%w: %s 超过 %d 字符", ErrCronBadRequest, label, maxRunes)
	}
	return nil
}

// validateCronScript 校验 script 字段。空字符串表示不设置脚本。
func validateCronScript(script string) error {
	if script == "" {
		return nil
	}
	if !cronScriptRe.MatchString(script) || script == "." || script == ".." {
		return fmt.Errorf("%w: 非法 script", ErrCronBadRequest)
	}
	return nil
}

// validateCronSkills 校验高级 skills 字段数量和名称白名单。
func validateCronSkills(skills []string) error {
	if len(skills) > cronMaxSkills {
		return fmt.Errorf("%w: skills 数量超过 %d", ErrCronBadRequest, cronMaxSkills)
	}
	for _, skill := range skills {
		if !cronSkillRe.MatchString(skill) {
			return fmt.Errorf("%w: 非法 skill 名称: %s", ErrCronBadRequest, skill)
		}
	}
	return nil
}

// validateCronRepeat 校验 repeat 为正整数；nil 表示不设置重复次数。
func validateCronRepeat(repeat *int) error {
	if repeat != nil && *repeat <= 0 {
		return fmt.Errorf("%w: repeat 必须是正整数", ErrCronBadRequest)
	}
	return nil
}

// validateCronOutputFile 校验 output 文件名仅为单个 markdown 文件或合成元数据文件。
func validateCronOutputFile(fileName string) error {
	if !isCronOutputFileName(fileName) {
		return fmt.Errorf("%w: 非法输出文件名", ErrCronBadRequest)
	}
	return nil
}

// isCronOutputFileName 判断文件名是否符合 oc-cron output 的单文件名契约。
func isCronOutputFileName(fileName string) bool {
	if fileName == "" || strings.ContainsAny(fileName, `/\`) || fileName == "." || fileName == ".." {
		return false
	}
	return fileName == cronSyntheticFile || strings.HasSuffix(fileName, ".md")
}

// validateCronJobData 校验 oc-cron 返回的任务对象是否包含稳定标识。
func validateCronJobData(job CronJob) error {
	if !cronJobIDRe.MatchString(job.ID) {
		return fmt.Errorf("%w: 任务缺少合法 id", ErrCronOutputInvalid)
	}
	return nil
}

// validateCronJobsData 校验任务数组里的每个任务对象；空数组合法。
func validateCronJobsData(jobs []CronJob) error {
	for _, job := range jobs {
		if err := validateCronJobData(job); err != nil {
			return err
		}
	}
	return nil
}

// validateCronStatusData 校验 status 输出里的可选任务标识，避免把坏 ID 透传给上层。
func validateCronStatusData(status CronStatus) error {
	if status.ActiveJobs < 0 {
		return fmt.Errorf("%w: active_jobs 不能为负数", ErrCronOutputInvalid)
	}
	for _, jobID := range []string{status.NextJobID, status.LastErrorJobID} {
		if jobID != "" && !cronJobIDRe.MatchString(jobID) {
			return fmt.Errorf("%w: status 含非法 job id", ErrCronOutputInvalid)
		}
	}
	return nil
}

// validateCronRunEntriesData 校验 history 输出中的任务 ID 与输出文件名。
func validateCronRunEntriesData(entries []CronRunEntry) error {
	for _, entry := range entries {
		if !cronJobIDRe.MatchString(entry.JobID) {
			return fmt.Errorf("%w: history 缺少合法 job id", ErrCronOutputInvalid)
		}
		if !isCronOutputFileName(entry.FileName) {
			return fmt.Errorf("%w: history 含非法输出文件名", ErrCronOutputInvalid)
		}
		if entry.Size < 0 {
			return fmt.Errorf("%w: history 输出大小不能为负数", ErrCronOutputInvalid)
		}
	}
	return nil
}

// validateCronRunOutputData 校验 output 输出中的任务 ID 与文件名。
func validateCronRunOutputData(out CronRunOutput) error {
	if !cronJobIDRe.MatchString(out.JobID) {
		return fmt.Errorf("%w: output 缺少合法 job id", ErrCronOutputInvalid)
	}
	if !isCronOutputFileName(out.FileName) {
		return fmt.Errorf("%w: output 含非法输出文件名", ErrCronOutputInvalid)
	}
	return nil
}

// validateCronCapabilitiesData 校验 capabilities 必须携带契约版本标识。
func validateCronCapabilitiesData(caps CronCapabilities) error {
	if strings.TrimSpace(caps.ContractVersion) == "" || strings.TrimSpace(caps.OCCronVersion) == "" {
		return fmt.Errorf("%w: capabilities 缺少契约版本", ErrCronOutputInvalid)
	}
	return nil
}

// appendCreateArgs 把 CreateCronJobInput 转成 oc-cron create argv。
func appendCreateArgs(args []string, in CreateCronJobInput) ([]string, error) {
	if err := validateCronText("name", in.Name, cronNameMaxRunes, true); err != nil {
		return nil, err
	}
	if err := validateCronText("schedule", in.Schedule, cronScheduleMaxRunes, true); err != nil {
		return nil, err
	}
	args = append(args, "--name", in.Name, "--schedule", in.Schedule)
	if in.Prompt != "" {
		if err := validateCronText("prompt", in.Prompt, cronPromptMaxRunes, false); err != nil {
			return nil, err
		}
		args = append(args, "--prompt", in.Prompt)
	}
	if in.Deliver != "" {
		if err := validateCronText("deliver", in.Deliver, cronSmallTextMaxRunes, false); err != nil {
			return nil, err
		}
		args = append(args, "--deliver", in.Deliver)
	}
	if err := validateCronRepeat(in.Repeat); err != nil {
		return nil, err
	}
	if in.Repeat != nil {
		args = append(args, "--repeat", fmt.Sprintf("%d", *in.Repeat))
	}
	if err := validateCronScript(in.Script); err != nil {
		return nil, err
	}
	if in.Script != "" {
		args = append(args, "--script", in.Script)
	}
	if in.NoAgent {
		args = append(args, "--no-agent")
	}
	return appendCronAdvancedArgs(args, cronAdvancedArgs{
		Workdir:  stringPtrIfNotEmpty(in.Workdir),
		Skills:   in.Skills,
		Model:    stringPtrIfNotEmpty(in.Model),
		Provider: stringPtrIfNotEmpty(in.Provider),
		BaseURL:  stringPtrIfNotEmpty(in.BaseURL),
	})
}

// cronAdvancedArgs 承载 create/update 共用高级字段。
type cronAdvancedArgs struct {
	Workdir     *string
	Skills      []string
	ClearSkills bool
	Model       *string
	Provider    *string
	BaseURL     *string
}

// appendCronAdvancedArgs 追加平台管理员高级字段对应的 argv；service 只做格式校验。
func appendCronAdvancedArgs(args []string, in cronAdvancedArgs) ([]string, error) {
	if in.Workdir != nil {
		if err := validateCronText("workdir", *in.Workdir, cronSmallTextMaxRunes, false); err != nil {
			return nil, err
		}
		args = append(args, "--workdir", *in.Workdir)
	}
	if in.ClearSkills {
		args = append(args, "--clear-skills")
	}
	if err := validateCronSkills(in.Skills); err != nil {
		return nil, err
	}
	for _, skill := range in.Skills {
		args = append(args, "--skill", skill)
	}
	if in.Model != nil {
		if err := validateCronText("model", *in.Model, cronSmallTextMaxRunes, false); err != nil {
			return nil, err
		}
		args = append(args, "--model", *in.Model)
	}
	if in.Provider != nil {
		if err := validateCronText("provider", *in.Provider, cronSmallTextMaxRunes, false); err != nil {
			return nil, err
		}
		args = append(args, "--provider", *in.Provider)
	}
	if in.BaseURL != nil {
		if err := validateCronText("base_url", *in.BaseURL, cronSmallTextMaxRunes, false); err != nil {
			return nil, err
		}
		args = append(args, "--base-url", *in.BaseURL)
	}
	return args, nil
}

// stringPtrIfNotEmpty 用于 create 输入：空字符串表示不传对应 flag。
func stringPtrIfNotEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// appendUpdateArgs 把 UpdateCronJobInput 转成 oc-cron update argv。
func appendUpdateArgs(args []string, in UpdateCronJobInput) ([]string, error) {
	if in.Name != nil {
		if err := validateCronText("name", *in.Name, cronNameMaxRunes, false); err != nil {
			return nil, err
		}
		args = append(args, "--name", *in.Name)
	}
	if in.Schedule != nil {
		if err := validateCronText("schedule", *in.Schedule, cronScheduleMaxRunes, false); err != nil {
			return nil, err
		}
		args = append(args, "--schedule", *in.Schedule)
	}
	if in.Prompt != nil {
		if err := validateCronText("prompt", *in.Prompt, cronPromptMaxRunes, false); err != nil {
			return nil, err
		}
		args = append(args, "--prompt", *in.Prompt)
	}
	if in.Deliver != nil {
		if err := validateCronText("deliver", *in.Deliver, cronSmallTextMaxRunes, false); err != nil {
			return nil, err
		}
		args = append(args, "--deliver", *in.Deliver)
	}
	if err := validateCronRepeat(in.Repeat); err != nil {
		return nil, err
	}
	if in.Repeat != nil {
		args = append(args, "--repeat", fmt.Sprintf("%d", *in.Repeat))
	}
	if in.Script != nil {
		if err := validateCronScript(*in.Script); err != nil {
			return nil, err
		}
		args = append(args, "--script", *in.Script)
	}
	if in.NoAgent {
		args = append(args, "--no-agent")
	}
	if in.Agent {
		args = append(args, "--agent")
	}
	return appendCronAdvancedArgs(args, cronAdvancedArgs{
		Workdir:     in.Workdir,
		Skills:      in.Skills,
		ClearSkills: in.ClearSkills,
		Model:       in.Model,
		Provider:    in.Provider,
		BaseURL:     in.BaseURL,
	})
}

// Capabilities 探测实例 oc-cron 的契约版本与可用能力。
func (s *HermesCronService) Capabilities(ctx context.Context, principal auth.Principal, appID string) (CronCapabilities, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return CronCapabilities{}, err
	}
	data, err := s.runOCCron(ctx, loc, []string{"capabilities"})
	if err != nil {
		return CronCapabilities{}, err
	}
	var caps CronCapabilities
	if err := decodeCronData(data, &caps); err != nil {
		return CronCapabilities{}, err
	}
	if err := validateCronCapabilitiesData(caps); err != nil {
		return CronCapabilities{}, err
	}
	return caps, nil
}

// Status 返回实例内 Hermes Cron 调度器状态摘要。
func (s *HermesCronService) Status(ctx context.Context, principal auth.Principal, appID string) (CronStatus, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return CronStatus{}, err
	}
	data, err := s.runOCCron(ctx, loc, []string{"status"})
	if err != nil {
		return CronStatus{}, err
	}
	var status CronStatus
	if err := decodeCronData(data, &status); err != nil {
		return CronStatus{}, err
	}
	if err := validateCronStatusData(status); err != nil {
		return CronStatus{}, err
	}
	return status, nil
}

// ListJobs 返回实例内 Cron 任务列表。
func (s *HermesCronService) ListJobs(ctx context.Context, principal auth.Principal, appID string, f CronJobFilter) ([]CronJob, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return nil, err
	}
	args := []string{"list"}
	if f.All {
		args = append(args, "--all")
	}
	data, err := s.runOCCron(ctx, loc, args)
	if err != nil {
		return nil, err
	}
	var jobs []CronJob
	if err := decodeCronData(data, &jobs); err != nil {
		return nil, err
	}
	if err := validateCronJobsData(jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

// ShowJob 返回单个 Cron 任务详情。
func (s *HermesCronService) ShowJob(ctx context.Context, principal auth.Principal, appID, jobID string) (CronJob, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return CronJob{}, err
	}
	if err := validateCronJobID(jobID); err != nil {
		return CronJob{}, err
	}
	data, err := s.runOCCron(ctx, loc, []string{"show", "--id", jobID})
	if err != nil {
		return CronJob{}, err
	}
	var job CronJob
	if err := decodeCronData(data, &job); err != nil {
		return CronJob{}, err
	}
	if err := validateCronJobData(job); err != nil {
		return CronJob{}, err
	}
	return job, nil
}

// CreateJob 创建 Cron 任务并返回 oc-cron 重读后的稳定任务对象。
func (s *HermesCronService) CreateJob(ctx context.Context, principal auth.Principal, appID string, in CreateCronJobInput) (CronJob, error) {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return CronJob{}, err
	}
	args, err := appendCreateArgs([]string{"create"}, in)
	if err != nil {
		return CronJob{}, err
	}
	data, err := s.runOCCron(ctx, loc, args)
	if err != nil {
		return CronJob{}, err
	}
	var job CronJob
	if err := decodeCronData(data, &job); err != nil {
		return CronJob{}, err
	}
	if err := validateCronJobData(job); err != nil {
		return CronJob{}, err
	}
	return job, nil
}

// UpdateJob 更新 Cron 任务并返回更新后的稳定任务对象。
func (s *HermesCronService) UpdateJob(ctx context.Context, principal auth.Principal, appID, jobID string, in UpdateCronJobInput) (CronJob, error) {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return CronJob{}, err
	}
	if err := validateCronJobID(jobID); err != nil {
		return CronJob{}, err
	}
	args, err := appendUpdateArgs([]string{"update", "--id", jobID}, in)
	if err != nil {
		return CronJob{}, err
	}
	data, err := s.runOCCron(ctx, loc, args)
	if err != nil {
		return CronJob{}, err
	}
	var job CronJob
	if err := decodeCronData(data, &job); err != nil {
		return CronJob{}, err
	}
	if err := validateCronJobData(job); err != nil {
		return CronJob{}, err
	}
	return job, nil
}

// PauseJob 暂停 Cron 任务，对应 oc-cron toggle --enabled false。
func (s *HermesCronService) PauseJob(ctx context.Context, principal auth.Principal, appID, jobID string) (CronJob, error) {
	return s.toggleJob(ctx, principal, appID, jobID, false)
}

// ResumeJob 恢复 Cron 任务，对应 oc-cron toggle --enabled true。
func (s *HermesCronService) ResumeJob(ctx context.Context, principal auth.Principal, appID, jobID string) (CronJob, error) {
	return s.toggleJob(ctx, principal, appID, jobID, true)
}

// toggleJob 执行启停切换，并返回切换后的任务对象。
func (s *HermesCronService) toggleJob(ctx context.Context, principal auth.Principal, appID, jobID string, enabled bool) (CronJob, error) {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return CronJob{}, err
	}
	if err := validateCronJobID(jobID); err != nil {
		return CronJob{}, err
	}
	data, err := s.runOCCron(ctx, loc, []string{"toggle", "--id", jobID, "--enabled", fmt.Sprintf("%t", enabled)})
	if err != nil {
		return CronJob{}, err
	}
	var job CronJob
	if err := decodeCronData(data, &job); err != nil {
		return CronJob{}, err
	}
	if err := validateCronJobData(job); err != nil {
		return CronJob{}, err
	}
	return job, nil
}

// RunJob 立即触发 Cron 任务并返回触发后的任务对象。
func (s *HermesCronService) RunJob(ctx context.Context, principal auth.Principal, appID, jobID string) (CronJob, error) {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return CronJob{}, err
	}
	if err := validateCronJobID(jobID); err != nil {
		return CronJob{}, err
	}
	data, err := s.runOCCron(ctx, loc, []string{"run", "--id", jobID})
	if err != nil {
		return CronJob{}, err
	}
	var job CronJob
	if err := decodeCronData(data, &job); err != nil {
		return CronJob{}, err
	}
	if err := validateCronJobData(job); err != nil {
		return CronJob{}, err
	}
	return job, nil
}

// DeleteJob 删除 Cron 任务。oc-cron 成功时 data 仅表示执行成功，service 层不暴露额外 DTO。
func (s *HermesCronService) DeleteJob(ctx context.Context, principal auth.Principal, appID, jobID string) error {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return err
	}
	if err := validateCronJobID(jobID); err != nil {
		return err
	}
	_, err = s.runOCCron(ctx, loc, []string{"delete", "--id", jobID})
	return err
}

// History 返回某个 Cron 任务的运行输出历史。
func (s *HermesCronService) History(ctx context.Context, principal auth.Principal, appID, jobID string) ([]CronRunEntry, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return nil, err
	}
	if err := validateCronJobID(jobID); err != nil {
		return nil, err
	}
	data, err := s.runOCCron(ctx, loc, []string{"history", "--id", jobID})
	if err != nil {
		return nil, err
	}
	var entries []CronRunEntry
	if err := decodeCronData(data, &entries); err != nil {
		return nil, err
	}
	if err := validateCronRunEntriesData(entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// Output 读取某次 Cron 运行的 markdown 输出。
func (s *HermesCronService) Output(ctx context.Context, principal auth.Principal, appID, jobID, fileName string) (CronRunOutput, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return CronRunOutput{}, err
	}
	if err := validateCronJobID(jobID); err != nil {
		return CronRunOutput{}, err
	}
	if err := validateCronOutputFile(fileName); err != nil {
		return CronRunOutput{}, err
	}
	data, err := s.runOCCron(ctx, loc, []string{"output", "--id", jobID, "--file", fileName})
	if err != nil {
		return CronRunOutput{}, err
	}
	var out CronRunOutput
	if err := decodeCronData(data, &out); err != nil {
		return CronRunOutput{}, err
	}
	if err := validateCronRunOutputData(out); err != nil {
		return CronRunOutput{}, err
	}
	return out, nil
}

// cronAppStore 是 CronAppLocatorFromStore 依赖的最小 app 查询能力。
// 只声明 GetApp，避免依赖整个 Querier 接口，便于单测注入假实现。
type cronAppStore interface {
	GetApp(ctx context.Context, id string) (sqlc.App, error)
}

// CronAppLocatorFromStore 基于 app store 把 appID 解析为 Cron 执行坐标。
type CronAppLocatorFromStore struct {
	store cronAppStore
}

// NewCronAppLocatorFromStore 构造 locator。
func NewCronAppLocatorFromStore(store cronAppStore) *CronAppLocatorFromStore {
	return &CronAppLocatorFromStore{store: store}
}

// LocateApp 查询 app 行并组装 CronAppLocation。
// appID 直接作为字符串传入；app 不存在时 store 返回 sql.ErrNoRows。
func (l *CronAppLocatorFromStore) LocateApp(ctx context.Context, appID string) (CronAppLocation, error) {
	app, err := l.store.GetApp(ctx, appID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CronAppLocation{}, ErrNotFound
		}
		return CronAppLocation{}, fmt.Errorf("查询 app 失败: %w", err)
	}
	loc := CronAppLocation{
		OrgID:       app.OrgID,
		OwnerUserID: app.OwnerUserID,
		NodeID:      app.RuntimeNodeID,
	}
	if app.ContainerID.Valid {
		loc.ContainerID = app.ContainerID.String
	}
	// dev stub 镜像约定以 -dev 结尾；真实能力探测仍通过 capabilities 完成。
	loc.Stub = strings.HasSuffix(app.RuntimeImageRef, "-dev")
	return loc, nil
}
