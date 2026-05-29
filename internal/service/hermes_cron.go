// Package service —— hermes_cron.go 实现 Hermes Cron 管理能力。
// manager 不持久化 Cron 任务，所有读写都通过 oc-ops HTTP 客户端调用 app 实例内
// 的 cron 端点（类型化请求/响应），manager 仅做权限判断与输入/输出校验。
package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/ocops"
)

var (
	// cronJobIDRe 是 manager 允许透传给 oc-ops 的任务 ID 白名单，避免路径/请求注入。
	cronJobIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	// cronScriptRe 限制 script 为单个相对文件名；不允许绝对路径、目录层级或反斜杠。
	cronScriptRe = regexp.MustCompile(`^[^/\\][^/\\]*$`)
	// cronSkillRe 限制高级 skills 字段为稳定文件名风格标识，避免把任意文本透传给上游。
	cronSkillRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

const (
	// 自由文本限制与 oc-cron 适配层保持同量级，避免 manager 向实例传入过大的请求体。
	cronNameMaxRunes      = 200
	cronScheduleMaxRunes  = 200
	cronPromptMaxRunes    = 5000
	cronSmallTextMaxRunes = 512
	cronMaxSkills         = 32
	cronSyntheticFile     = "__scheduler_metadata__.md"
)

// HermesCronService 暴露 Hermes Cron 的读写能力。
type HermesCronService struct {
	ops      cronOps       // oc-ops 的类型化 cron 客户端窄接口
	resolver OcOpsResolver // 把 appID 解析为 oc-ops 调用坐标
}

// NewHermesCronService 构造 service。
func NewHermesCronService(ops cronOps, resolver OcOpsResolver) *HermesCronService {
	return &HermesCronService{ops: ops, resolver: resolver}
}

// resolve 解析 appID、校验读权限，并确保实例可调用 oc-ops。
func (s *HermesCronService) resolve(ctx context.Context, principal auth.Principal, appID string) (OcOpsAppLocation, error) {
	loc, err := s.resolver.Resolve(ctx, appID)
	if err != nil {
		return OcOpsAppLocation{}, err
	}
	if !auth.CanViewAppCron(principal, loc.OrgID, loc.OwnerUserID) {
		return OcOpsAppLocation{}, ErrCronForbidden
	}
	// dev stub 实例不含真实 hermes cron 能力，按不支持处理。
	if !loc.Supported {
		return OcOpsAppLocation{}, ErrCronNotSupported
	}
	// 没有可用的 oc-ops 基址说明实例运行时尚未就绪。
	if strings.TrimSpace(loc.Endpoint.BaseURL) == "" {
		return OcOpsAppLocation{}, ErrCronRuntimeUnavailable
	}
	return loc, nil
}

// resolveManage 解析 appID 并校验 Cron 写权限。
// 当前 CanManageAppCron 与 CanViewAppCron 等价，仍单独调用以便未来权限收紧。
func (s *HermesCronService) resolveManage(ctx context.Context, principal auth.Principal, appID string) (OcOpsAppLocation, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return OcOpsAppLocation{}, err
	}
	if !auth.CanManageAppCron(principal, loc.OrgID, loc.OwnerUserID) {
		return OcOpsAppLocation{}, ErrCronForbidden
	}
	return loc, nil
}

// validateCronJobID 校验任务 ID，所有按任务 ID 定位的方法共用。
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

// validateCronJobData 校验 oc-ops 返回的任务对象是否包含稳定标识。
func validateCronJobData(job ocops.CronJob) error {
	if !cronJobIDRe.MatchString(job.ID) {
		return fmt.Errorf("%w: 任务缺少合法 id", ErrCronOutputInvalid)
	}
	return nil
}

// validateCronJobsData 校验任务数组里的每个任务对象；空数组合法。
func validateCronJobsData(jobs []ocops.CronJob) error {
	for _, job := range jobs {
		if err := validateCronJobData(job); err != nil {
			return err
		}
	}
	return nil
}

// validateCronStatusData 校验 status 输出里的可选任务标识，避免把坏 ID 透传给上层。
func validateCronStatusData(status ocops.CronStatus) error {
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
func validateCronRunEntriesData(entries []ocops.CronRunEntry) error {
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
func validateCronRunOutputData(out ocops.CronRunOutput) error {
	if !cronJobIDRe.MatchString(out.JobID) {
		return fmt.Errorf("%w: output 缺少合法 job id", ErrCronOutputInvalid)
	}
	if !isCronOutputFileName(out.FileName) {
		return fmt.Errorf("%w: output 含非法输出文件名", ErrCronOutputInvalid)
	}
	return nil
}

// validateCronCapabilitiesData 校验 capabilities 必须携带契约版本标识。
func validateCronCapabilitiesData(caps ocops.CronCapabilities) error {
	if strings.TrimSpace(caps.ContractVersion) == "" || strings.TrimSpace(caps.OCCronVersion) == "" {
		return fmt.Errorf("%w: capabilities 缺少契约版本", ErrCronOutputInvalid)
	}
	return nil
}

// buildCronCreateReq 把 CreateCronJobInput 校验并转成类型化的 ocops.CronCreateReq。
// 复用原 appendCreateArgs 的全部校验逻辑（长度/正则/repeat>0/script 等），
// 只是把「拼 argv」改为「填请求体」。
func buildCronCreateReq(in CreateCronJobInput) (ocops.CronCreateReq, error) {
	if err := validateCronText("name", in.Name, cronNameMaxRunes, true); err != nil {
		return ocops.CronCreateReq{}, err
	}
	if err := validateCronText("schedule", in.Schedule, cronScheduleMaxRunes, true); err != nil {
		return ocops.CronCreateReq{}, err
	}
	req := ocops.CronCreateReq{Name: in.Name, Schedule: in.Schedule}
	if in.Prompt != "" {
		if err := validateCronText("prompt", in.Prompt, cronPromptMaxRunes, false); err != nil {
			return ocops.CronCreateReq{}, err
		}
		req.Prompt = in.Prompt
	}
	if in.Deliver != "" {
		if err := validateCronText("deliver", in.Deliver, cronSmallTextMaxRunes, false); err != nil {
			return ocops.CronCreateReq{}, err
		}
		req.Deliver = in.Deliver
	}
	if err := validateCronRepeat(in.Repeat); err != nil {
		return ocops.CronCreateReq{}, err
	}
	if in.Repeat != nil {
		// server 端 _normalize_repeat 接受裸整数（times=raw），与旧 --repeat N 语义等价。
		req.Repeat = *in.Repeat
	}
	if err := validateCronScript(in.Script); err != nil {
		return ocops.CronCreateReq{}, err
	}
	if in.Script != "" {
		req.Script = in.Script
	}
	if in.NoAgent {
		req.NoAgent = true
	}
	// 高级字段（平台管理员）：workdir/skills/model/provider/base_url。
	if in.Workdir != "" {
		if err := validateCronText("workdir", in.Workdir, cronSmallTextMaxRunes, false); err != nil {
			return ocops.CronCreateReq{}, err
		}
		req.Workdir = in.Workdir
	}
	if err := validateCronSkills(in.Skills); err != nil {
		return ocops.CronCreateReq{}, err
	}
	req.Skills = in.Skills
	if in.Model != "" {
		if err := validateCronText("model", in.Model, cronSmallTextMaxRunes, false); err != nil {
			return ocops.CronCreateReq{}, err
		}
		req.Model = in.Model
	}
	if in.Provider != "" {
		if err := validateCronText("provider", in.Provider, cronSmallTextMaxRunes, false); err != nil {
			return ocops.CronCreateReq{}, err
		}
		req.Provider = in.Provider
	}
	if in.BaseURL != "" {
		if err := validateCronText("base_url", in.BaseURL, cronSmallTextMaxRunes, false); err != nil {
			return ocops.CronCreateReq{}, err
		}
		req.BaseURL = in.BaseURL
	}
	return req, nil
}

// buildCronUpdateReq 把 UpdateCronJobInput 校验并转成类型化的 ocops.CronUpdateReq。
// 复用原 appendUpdateArgs 的校验逻辑；指针字段保留「未提交 = nil」的 partial update 语义。
func buildCronUpdateReq(in UpdateCronJobInput) (ocops.CronUpdateReq, error) {
	var req ocops.CronUpdateReq
	if in.Name != nil {
		if err := validateCronText("name", *in.Name, cronNameMaxRunes, false); err != nil {
			return ocops.CronUpdateReq{}, err
		}
		req.Name = in.Name
	}
	if in.Schedule != nil {
		if err := validateCronText("schedule", *in.Schedule, cronScheduleMaxRunes, false); err != nil {
			return ocops.CronUpdateReq{}, err
		}
		req.Schedule = in.Schedule
	}
	if in.Prompt != nil {
		if err := validateCronText("prompt", *in.Prompt, cronPromptMaxRunes, false); err != nil {
			return ocops.CronUpdateReq{}, err
		}
		req.Prompt = in.Prompt
	}
	if in.Deliver != nil {
		if err := validateCronText("deliver", *in.Deliver, cronSmallTextMaxRunes, false); err != nil {
			return ocops.CronUpdateReq{}, err
		}
		req.Deliver = in.Deliver
	}
	if err := validateCronRepeat(in.Repeat); err != nil {
		return ocops.CronUpdateReq{}, err
	}
	if in.Repeat != nil {
		// 同 create：server 接受裸整数 repeat，等价旧 --repeat N。
		req.Repeat = *in.Repeat
	}
	if in.Script != nil {
		if err := validateCronScript(*in.Script); err != nil {
			return ocops.CronUpdateReq{}, err
		}
		req.Script = in.Script
	}
	if in.NoAgent {
		v := true
		req.NoAgent = &v
	}
	if in.Agent {
		v := true
		req.Agent = &v
	}
	// 高级字段：workdir/clear_skills/skills/model/provider/base_url。
	if in.Workdir != nil {
		if err := validateCronText("workdir", *in.Workdir, cronSmallTextMaxRunes, false); err != nil {
			return ocops.CronUpdateReq{}, err
		}
		req.Workdir = in.Workdir
	}
	if in.ClearSkills {
		v := true
		req.ClearSkills = &v
	}
	if err := validateCronSkills(in.Skills); err != nil {
		return ocops.CronUpdateReq{}, err
	}
	req.Skills = in.Skills
	if in.Model != nil {
		if err := validateCronText("model", *in.Model, cronSmallTextMaxRunes, false); err != nil {
			return ocops.CronUpdateReq{}, err
		}
		req.Model = in.Model
	}
	if in.Provider != nil {
		if err := validateCronText("provider", *in.Provider, cronSmallTextMaxRunes, false); err != nil {
			return ocops.CronUpdateReq{}, err
		}
		req.Provider = in.Provider
	}
	if in.BaseURL != nil {
		if err := validateCronText("base_url", *in.BaseURL, cronSmallTextMaxRunes, false); err != nil {
			return ocops.CronUpdateReq{}, err
		}
		req.BaseURL = in.BaseURL
	}
	return req, nil
}

// Capabilities 探测实例 oc-cron 的契约版本与可用能力。
func (s *HermesCronService) Capabilities(ctx context.Context, principal auth.Principal, appID string) (ocops.CronCapabilities, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return ocops.CronCapabilities{}, err
	}
	caps, err := s.ops.CronCapabilities(ctx, loc.Endpoint)
	if err != nil {
		return ocops.CronCapabilities{}, mapOcOpsCronErr(err)
	}
	if err := validateCronCapabilitiesData(caps); err != nil {
		return ocops.CronCapabilities{}, err
	}
	return caps, nil
}

// Status 返回实例内 Hermes Cron 调度器状态摘要。
func (s *HermesCronService) Status(ctx context.Context, principal auth.Principal, appID string) (ocops.CronStatus, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return ocops.CronStatus{}, err
	}
	status, err := s.ops.CronStatus(ctx, loc.Endpoint)
	if err != nil {
		return ocops.CronStatus{}, mapOcOpsCronErr(err)
	}
	if err := validateCronStatusData(status); err != nil {
		return ocops.CronStatus{}, err
	}
	return status, nil
}

// ListJobs 返回实例内 Cron 任务列表。
func (s *HermesCronService) ListJobs(ctx context.Context, principal auth.Principal, appID string, f CronJobFilter) ([]ocops.CronJob, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return nil, err
	}
	jobs, err := s.ops.CronList(ctx, loc.Endpoint, f.All)
	if err != nil {
		return nil, mapOcOpsCronErr(err)
	}
	if err := validateCronJobsData(jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

// ShowJob 返回单个 Cron 任务详情。
func (s *HermesCronService) ShowJob(ctx context.Context, principal auth.Principal, appID, jobID string) (ocops.CronJob, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return ocops.CronJob{}, err
	}
	if err := validateCronJobID(jobID); err != nil {
		return ocops.CronJob{}, err
	}
	job, err := s.ops.CronShow(ctx, loc.Endpoint, jobID)
	if err != nil {
		return ocops.CronJob{}, mapOcOpsCronErr(err)
	}
	if err := validateCronJobData(job); err != nil {
		return ocops.CronJob{}, err
	}
	return job, nil
}

// CreateJob 创建 Cron 任务并返回 oc-ops 重读后的稳定任务对象。
func (s *HermesCronService) CreateJob(ctx context.Context, principal auth.Principal, appID string, in CreateCronJobInput) (ocops.CronJob, error) {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return ocops.CronJob{}, err
	}
	req, err := buildCronCreateReq(in)
	if err != nil {
		return ocops.CronJob{}, err
	}
	job, err := s.ops.CronCreate(ctx, loc.Endpoint, req)
	if err != nil {
		return ocops.CronJob{}, mapOcOpsCronErr(err)
	}
	if err := validateCronJobData(job); err != nil {
		return ocops.CronJob{}, err
	}
	return job, nil
}

// UpdateJob 更新 Cron 任务并返回更新后的稳定任务对象。
func (s *HermesCronService) UpdateJob(ctx context.Context, principal auth.Principal, appID, jobID string, in UpdateCronJobInput) (ocops.CronJob, error) {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return ocops.CronJob{}, err
	}
	if err := validateCronJobID(jobID); err != nil {
		return ocops.CronJob{}, err
	}
	req, err := buildCronUpdateReq(in)
	if err != nil {
		return ocops.CronJob{}, err
	}
	job, err := s.ops.CronUpdate(ctx, loc.Endpoint, jobID, req)
	if err != nil {
		return ocops.CronJob{}, mapOcOpsCronErr(err)
	}
	if err := validateCronJobData(job); err != nil {
		return ocops.CronJob{}, err
	}
	return job, nil
}

// PauseJob 暂停 Cron 任务，对应 oc-ops toggle enabled=false。
func (s *HermesCronService) PauseJob(ctx context.Context, principal auth.Principal, appID, jobID string) (ocops.CronJob, error) {
	return s.toggleJob(ctx, principal, appID, jobID, false)
}

// ResumeJob 恢复 Cron 任务，对应 oc-ops toggle enabled=true。
func (s *HermesCronService) ResumeJob(ctx context.Context, principal auth.Principal, appID, jobID string) (ocops.CronJob, error) {
	return s.toggleJob(ctx, principal, appID, jobID, true)
}

// toggleJob 执行启停切换，并返回切换后的任务对象。
func (s *HermesCronService) toggleJob(ctx context.Context, principal auth.Principal, appID, jobID string, enabled bool) (ocops.CronJob, error) {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return ocops.CronJob{}, err
	}
	if err := validateCronJobID(jobID); err != nil {
		return ocops.CronJob{}, err
	}
	job, err := s.ops.CronToggle(ctx, loc.Endpoint, jobID, enabled)
	if err != nil {
		return ocops.CronJob{}, mapOcOpsCronErr(err)
	}
	if err := validateCronJobData(job); err != nil {
		return ocops.CronJob{}, err
	}
	return job, nil
}

// RunJob 立即触发 Cron 任务并返回触发后的任务对象。
func (s *HermesCronService) RunJob(ctx context.Context, principal auth.Principal, appID, jobID string) (ocops.CronJob, error) {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return ocops.CronJob{}, err
	}
	if err := validateCronJobID(jobID); err != nil {
		return ocops.CronJob{}, err
	}
	job, err := s.ops.CronRun(ctx, loc.Endpoint, jobID)
	if err != nil {
		return ocops.CronJob{}, mapOcOpsCronErr(err)
	}
	if err := validateCronJobData(job); err != nil {
		return ocops.CronJob{}, err
	}
	return job, nil
}

// DeleteJob 删除 Cron 任务。oc-ops 成功时返回 204，service 层不暴露额外 DTO。
func (s *HermesCronService) DeleteJob(ctx context.Context, principal auth.Principal, appID, jobID string) error {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return err
	}
	if err := validateCronJobID(jobID); err != nil {
		return err
	}
	if err := s.ops.CronDelete(ctx, loc.Endpoint, jobID); err != nil {
		return mapOcOpsCronErr(err)
	}
	return nil
}

// History 返回某个 Cron 任务的运行输出历史。
func (s *HermesCronService) History(ctx context.Context, principal auth.Principal, appID, jobID string) ([]ocops.CronRunEntry, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return nil, err
	}
	if err := validateCronJobID(jobID); err != nil {
		return nil, err
	}
	entries, err := s.ops.CronHistory(ctx, loc.Endpoint, jobID)
	if err != nil {
		return nil, mapOcOpsCronErr(err)
	}
	if err := validateCronRunEntriesData(entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// Output 读取某次 Cron 运行的 markdown 输出。
func (s *HermesCronService) Output(ctx context.Context, principal auth.Principal, appID, jobID, fileName string) (ocops.CronRunOutput, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return ocops.CronRunOutput{}, err
	}
	if err := validateCronJobID(jobID); err != nil {
		return ocops.CronRunOutput{}, err
	}
	if err := validateCronOutputFile(fileName); err != nil {
		return ocops.CronRunOutput{}, err
	}
	out, err := s.ops.CronOutput(ctx, loc.Endpoint, jobID, fileName)
	if err != nil {
		return ocops.CronRunOutput{}, mapOcOpsCronErr(err)
	}
	if err := validateCronRunOutputData(out); err != nil {
		return ocops.CronRunOutput{}, err
	}
	return out, nil
}
