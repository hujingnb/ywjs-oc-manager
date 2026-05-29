// client_cron.go — ocops 包 cron 的 11 个类型化客户端方法。
//
// 每个方法对应 oc-ops server 的一个 cron 端点：内部统一调 c.DoJSON，
// 调用方无需关心 HTTP method / path / query string 拼装细节。
// path 参数（job id）用 url.PathEscape 转义，防止含特殊字符时路径越界。
package ocops

import (
	"context"
	"net/http"
	"net/url"
)

// CronCreateReq 是 POST /oc/cron/jobs 的请求体。
// JSON 字段名与 server 端 _CRON_CREATE_KEYS 完全对齐：
//
//	name/schedule/prompt/deliver/repeat/script/no_agent/workdir/skills/model/provider/base_url
//
// 未填写的可选字段以零值序列化时会被 omitempty 忽略，server 侧 _pick 也
// 只透传已出现的键，两端行为一致。
type CronCreateReq struct {
	// Name 是任务名称（必填）。
	Name string `json:"name"`
	// Schedule 是调度表达式（必填），如 "every 10m" / "cron 0 9 * * 1-5"。
	Schedule string `json:"schedule"`
	// Prompt 是运行时交给 hermes 的提示词，可留空。
	Prompt string `json:"prompt,omitempty"`
	// Deliver 是任务输出投递目标（如 "wechat"），可留空。
	Deliver string `json:"deliver,omitempty"`
	// Repeat 是重复次数描述，nil 表示不限；服务端类型为 dict，此处用 any 保持透传。
	Repeat any `json:"repeat,omitempty"`
	// Script 是仓库内脚本文件名，可留空。
	Script string `json:"script,omitempty"`
	// NoAgent 为 true 时跳过 agent 执行路径。
	NoAgent bool `json:"no_agent,omitempty"`
	// Workdir 是任务运行目录；平台管理员高级字段。
	Workdir string `json:"workdir,omitempty"`
	// Skills 是任务所需技能列表；平台管理员高级字段。
	Skills []string `json:"skills,omitempty"`
	// Model 是任务指定模型；平台管理员高级字段。
	Model string `json:"model,omitempty"`
	// Provider 是任务指定模型提供方；平台管理员高级字段。
	Provider string `json:"provider,omitempty"`
	// BaseURL 是任务指定 provider base URL；平台管理员高级字段。
	BaseURL string `json:"base_url,omitempty"`
}

// CronUpdateReq 是 PATCH /oc/cron/jobs/{id} 的请求体。
// JSON 字段名与 server 端 _CRON_UPDATE_KEYS 完全对齐：
//
//	name/schedule/prompt/deliver/repeat/script/no_agent/agent/workdir/skills/clear_skills/model/provider/base_url
//
// 全部字段用指针 + omitempty 表达「未提交」语义：nil 不出现在请求体，
// server 侧 _pick 仅透传已出现的键，从而实现真正的 partial update。
type CronUpdateReq struct {
	// Name 如非 nil 则更新任务名称。
	Name *string `json:"name,omitempty"`
	// Schedule 如非 nil 则更新调度表达式。
	Schedule *string `json:"schedule,omitempty"`
	// Prompt 如非 nil 则更新提示词。
	Prompt *string `json:"prompt,omitempty"`
	// Deliver 如非 nil 则更新投递目标。
	Deliver *string `json:"deliver,omitempty"`
	// Repeat 如非 nil 则更新重复次数；any 类型保持透传（server 端为 dict）。
	Repeat any `json:"repeat,omitempty"`
	// Script 如非 nil 则更新脚本文件名。
	Script *string `json:"script,omitempty"`
	// NoAgent 如非 nil 则更新是否跳过 agent 路径。
	NoAgent *bool `json:"no_agent,omitempty"`
	// Agent 如非 nil 则更新 agent 字段（update 特有，create 无此字段）。
	Agent *bool `json:"agent,omitempty"`
	// Workdir 如非 nil 则更新任务运行目录。
	Workdir *string `json:"workdir,omitempty"`
	// Skills 如非 nil 则替换技能列表（ClearSkills 优先级更高时忽略此字段）。
	Skills []string `json:"skills,omitempty"`
	// ClearSkills 为 true 时清空技能列表；server 端是 bool，omitempty 仅在 false 时省略。
	// 注意：Go 的 bool omitempty 在 false 时省略，符合「未提交」语义。
	ClearSkills *bool `json:"clear_skills,omitempty"`
	// Model 如非 nil 则更新指定模型。
	Model *string `json:"model,omitempty"`
	// Provider 如非 nil 则更新模型提供方。
	Provider *string `json:"provider,omitempty"`
	// BaseURL 如非 nil 则更新 provider base URL。
	BaseURL *string `json:"base_url,omitempty"`
}

// CronCapabilities 查询 cron 能力信息：返回契约版本、支持的 verb 与 feature 开关。
// GET /oc/cron/capabilities
func (c *Client) CronCapabilities(ctx context.Context, ep Endpoint) (CronCapabilities, error) {
	var out CronCapabilities
	err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/cron/capabilities", nil, &out)
	return out, err
}

// CronStatus 查询调度器运行摘要：活跃任务数、下次运行时间等。
// GET /oc/cron/status
func (c *Client) CronStatus(ctx context.Context, ep Endpoint) (CronStatus, error) {
	var out CronStatus
	err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/cron/status", nil, &out)
	return out, err
}

// CronList 列出 cron 任务列表。all=true 时包含已禁用的任务；false 只返回活跃任务。
// GET /oc/cron/jobs?all=true|false
func (c *Client) CronList(ctx context.Context, ep Endpoint, all bool) ([]CronJob, error) {
	// query string：?all=true 或 ?all=false
	q := url.Values{}
	if all {
		q.Set("all", "true")
	} else {
		q.Set("all", "false")
	}
	var out []CronJob
	err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/cron/jobs?"+q.Encode(), nil, &out)
	return out, err
}

// CronShow 查询单个 cron 任务详情；任务不存在时返回 ErrNotFound。
// GET /oc/cron/jobs/{id}
func (c *Client) CronShow(ctx context.Context, ep Endpoint, id string) (CronJob, error) {
	var out CronJob
	err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/cron/jobs/"+url.PathEscape(id), nil, &out)
	return out, err
}

// CronCreate 创建新 cron 任务，返回完整任务对象（server 侧写后重读 jobs.json）。
// POST /oc/cron/jobs
func (c *Client) CronCreate(ctx context.Context, ep Endpoint, req CronCreateReq) (CronJob, error) {
	var out CronJob
	err := c.DoJSON(ctx, ep, http.MethodPost, "/oc/cron/jobs", req, &out)
	return out, err
}

// CronUpdate 部分更新 cron 任务字段（PATCH 语义：只更新 req 中非零值的字段）。
// PATCH /oc/cron/jobs/{id}
func (c *Client) CronUpdate(ctx context.Context, ep Endpoint, id string, req CronUpdateReq) (CronJob, error) {
	var out CronJob
	err := c.DoJSON(ctx, ep, http.MethodPatch, "/oc/cron/jobs/"+url.PathEscape(id), req, &out)
	return out, err
}

// CronToggle 启用（enabled=true）或禁用（enabled=false）指定 cron 任务。
// POST /oc/cron/jobs/{id}/toggle，body {"enabled":true/false}
func (c *Client) CronToggle(ctx context.Context, ep Endpoint, id string, enabled bool) (CronJob, error) {
	// toggle body：{"enabled": true/false}
	body := struct {
		Enabled bool `json:"enabled"`
	}{Enabled: enabled}
	var out CronJob
	err := c.DoJSON(ctx, ep, http.MethodPost, "/oc/cron/jobs/"+url.PathEscape(id)+"/toggle", body, &out)
	return out, err
}

// CronRun 立即触发一次 cron 任务（不等待执行完成，返回任务当前快照）。
// POST /oc/cron/jobs/{id}/run
func (c *Client) CronRun(ctx context.Context, ep Endpoint, id string) (CronJob, error) {
	var out CronJob
	err := c.DoJSON(ctx, ep, http.MethodPost, "/oc/cron/jobs/"+url.PathEscape(id)+"/run", nil, &out)
	return out, err
}

// CronDelete 删除指定 cron 任务；成功时 server 返回 204 No Content。
// DELETE /oc/cron/jobs/{id}
func (c *Client) CronDelete(ctx context.Context, ep Endpoint, id string) error {
	return c.DoJSON(ctx, ep, http.MethodDelete, "/oc/cron/jobs/"+url.PathEscape(id), nil, nil)
}

// CronHistory 查询任务历史运行记录列表；任务不存在时返回 ErrNotFound。
// GET /oc/cron/jobs/{id}/history
func (c *Client) CronHistory(ctx context.Context, ep Endpoint, id string) ([]CronRunEntry, error) {
	var out []CronRunEntry
	err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/cron/jobs/"+url.PathEscape(id)+"/history", nil, &out)
	return out, err
}

// CronOutput 查询任务某次运行的 markdown 输出内容；file 是 history 返回的文件名。
// GET /oc/cron/jobs/{id}/output?file={file}
func (c *Client) CronOutput(ctx context.Context, ep Endpoint, id, file string) (CronRunOutput, error) {
	// query string：?file=<filename>，file 本身不含路径分隔，按规范用 QueryEscape
	q := url.Values{}
	q.Set("file", file)
	var out CronRunOutput
	err := c.DoJSON(ctx, ep, http.MethodGet,
		"/oc/cron/jobs/"+url.PathEscape(id)+"/output?"+q.Encode(),
		nil, &out)
	return out, err
}
