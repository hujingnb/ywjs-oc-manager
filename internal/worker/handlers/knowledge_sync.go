package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/domain"
	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// KnowledgeAuditRecorder 抽象写 audit_logs 能力,与 service 包同构。
// 由 cmd/server 装配时注入 *service.AuditService;handler 测试可用内存 fake。
//
// 用途:app scope 没有 knowledge_sync_status 行可写,worker 完成 / 失败时把事件
// 落 audit_logs,前端可在审计页面查 target_type=app_knowledge_sync 看每次同步结果。
// 完整事件链:service.dispatch_app_* (入队) → worker.app_knowledge_sync (执行)。
type KnowledgeAuditRecorder interface {
	Record(ctx context.Context, event service.AuditEvent) (service.AuditResult, error)
}

// KnowledgeFileSource 抽象 manager 主副本文件读取能力。
// 同步任务不直接依赖 files.KnowledgeMaster，便于在测试中注入内存 reader。
type KnowledgeFileSource interface {
	Open(relativePath string) (io.ReadCloser, int64, error)
}

// KnowledgeFileSink 抽象 agent 文件 API 上传 / 删除能力。
//
// hermes-agent-pull 切换后,manager 写入路径统一指向 apps/<appID>/input/ 沙箱,
// org-level 与 app-level 知识库都最终落到「每个应用自己的 input 目录」下的
// resources/knowledge/{org,app}/。因此 sink 不再区分 org/app scope 路由,只暴露
// 单 app 维度的上传 / 删除原语:handler 内部决定 (scope, scopeID) → (一批 appID)
// 的扇出关系,sink 不感知业务策略。
//
// relPath 由调用方按业务前缀拼好(例如 "resources/knowledge/org/<rel>"),
// agent 端只负责沙箱合法性 + 写盘,不修改路径。
type KnowledgeFileSink interface {
	// UploadAppInputFile 把单文件写到指定节点的 apps/<appID>/input/<relPath>。
	UploadAppInputFile(ctx context.Context, nodeID, appID, relPath string, content io.Reader) error
	// DeleteAppInputFile 删除指定节点的 apps/<appID>/input/<relPath>(幂等)。
	DeleteAppInputFile(ctx context.Context, nodeID, appID, relPath string) error
}

// KnowledgeAppLister 抽象「列出某组织下属于某节点的所有 app」能力,
// 供 org scope 同步事件按节点扇出到该节点上的每个应用 input 目录。
//
// 由 *sqlc.Queries 实现 (ListAppsByOrg): worker 在 handler 内部按
// runtime_node_id == payload.NodeID 过滤; 当组织下有多个 app 落到同一节点时,
// 一份 org 知识库变更会向该节点的每个 app 各发一次 input/file 写请求。
//
// nil 装配时 org scope 同步退化为只翻 sync_status, 不写任何文件; 仅在测试桩
// 或暂未注入新依赖的旧装配下出现, 生产装配必须注入。
type KnowledgeAppLister interface {
	// ListAppsByOrg 透传到 sqlc:返回组织下所有未删除应用,handler 在内存里
	// 按 RuntimeNodeID 过滤。limit 由调用方控制,这里只声明形态。
	ListAppsByOrg(ctx context.Context, arg sqlc.ListAppsByOrgParams) ([]sqlc.App, error)
}

// KnowledgeSyncStatusWriter 抽象 (org, node) 同步状态写入能力。
//
// 用于把组织级 knowledge_sync_node job 的成功 / 失败结果记录到 knowledge_sync_status
// 表，让前端 OrgKnowledgePage 能展示每节点最近态 + 错误原因 + 触发"重试同步"。
//
// 应用级同步是同步推送（不走 job），不写本表；本接口只对 org scope 触发。
// nil 实现表示装配不支持状态记录（旧测试 / 测试桩），handler 跳过写入。
type KnowledgeSyncStatusWriter interface {
	MarkOrgNodeSynced(ctx context.Context, orgID, nodeID string) error
	MarkOrgNodeFailed(ctx context.Context, orgID, nodeID, errMsg string) error
}

// knowledgeSyncPayload 是 knowledge_sync_node job 的 payload schema。
//
// Scope 取值 'org' | 'app',决定 handler 在节点 apps/<id>/input/ 沙箱下的
// 子目录前缀:
//   - org:写到 apps/<每个该 org 在本节点的 appID>/input/resources/knowledge/org/<rel>
//   - app:写到 apps/<app_id>/input/resources/knowledge/app/<rel>
//
// 历史 legacy 路径 apps/<id>/knowledge/ 与 orgs/<id>/knowledge/ 已在 agent T13 下线。
//
// ChangeType 取值 'upload_file' | 'delete_file' | 'noop'。
type knowledgeSyncPayload struct {
	Scope      string `json:"scope"`
	OrgID      string `json:"org_id"`
	AppID      string `json:"app_id"`
	NodeID     string `json:"node_id"`
	ChangeType string `json:"change_type"`
	RelPath    string `json:"rel_path"`
	// MasterPath 是 manager 主副本上的相对路径，由 service 在入队时计算好放进 payload，
	// worker 直接据此 Open 读文件，避免 worker 二次推断目录结构。
	MasterPath string `json:"master_path"`
}

// KnowledgeSyncHandler 把 manager 主副本变更同步到目标 agent 节点。
type KnowledgeSyncHandler struct {
	source       KnowledgeFileSource
	sink         KnowledgeFileSink
	statusWriter KnowledgeSyncStatusWriter
	// apps 用于 org scope 同步:把单条 (org, node) job 扇出到该节点上属于该 org 的
	// 每个 app 的 input/ 目录。nil 时 org scope 同步退化为不写文件 (仅供测试装配)。
	apps KnowledgeAppLister
	// auditor 用于 app scope 同步完成 / 失败时落 audit_logs。
	// org scope 已经有 knowledge_sync_status 表展示;auditor 仅在 app scope 启用,
	// 避免双写(org 已有结构化存储,再写 audit 只会让事件流冗余)。
	auditor KnowledgeAuditRecorder
}

// NewKnowledgeSyncHandler 创建 handler。
func NewKnowledgeSyncHandler(source KnowledgeFileSource, sink KnowledgeFileSink) *KnowledgeSyncHandler {
	return &KnowledgeSyncHandler{source: source, sink: sink}
}

// SetStatusWriter 注入 (org, node) 同步状态写入器；不调时 handler 不写状态表，
// 与旧装配兼容。生产装配应从 cmd/server 注入 db-backed 实现。
func (h *KnowledgeSyncHandler) SetStatusWriter(w KnowledgeSyncStatusWriter) {
	h.statusWriter = w
}

// SetAppLister 注入「按组织列出 app」能力。
// 生产装配必须注入: org scope 同步靠它扇出到节点上的每个 app input 目录。
// 不注入时 org scope 同步仅翻状态、不写文件——只允许测试装配使用。
func (h *KnowledgeSyncHandler) SetAppLister(l KnowledgeAppLister) {
	h.apps = l
}

// SetAuditor 注入 app scope 的审计写入器。
// 不注入时 app scope 完成 / 失败完全不可观测(前端无法定位是哪个 app 同步出错);
// 生产应注入 *service.AuditService 让 audit_logs 留痕。
func (h *KnowledgeSyncHandler) SetAuditor(a KnowledgeAuditRecorder) {
	h.auditor = a
}

// Handle 处理一次同步事件。
// upload_file 路径走 master 读 + agent upload；delete_file 直接调 agent delete。
func (h *KnowledgeSyncHandler) Handle(ctx context.Context, job sqlc.Job) error {
	if job.Type != domain.JobTypeKnowledgeSyncNode {
		return fmt.Errorf("非 knowledge_sync_node 任务: %s", job.Type)
	}
	var payload knowledgeSyncPayload
	if err := json.Unmarshal(job.PayloadJson, &payload); err != nil {
		return fmt.Errorf("解析 payload 失败: %w", err)
	}
	if payload.NodeID == "" {
		return fmt.Errorf("缺少 node_id")
	}
	if err := payload.validate(); err != nil {
		return err
	}
	if err := h.execute(ctx, payload); err != nil {
		// 仅 org scope 写状态：app scope 是同步路径不入此 handler 的状态表。
		if payload.Scope == "org" && h.statusWriter != nil {
			_ = h.statusWriter.MarkOrgNodeFailed(ctx, payload.OrgID, payload.NodeID, err.Error())
		}
		// app scope 走 audit_logs:无结构化状态表,把失败事件作为审计记录,
		// 前端审计页面可按 target_type=app_knowledge_sync 检索失败原因。
		if payload.Scope == "app" {
			h.recordAppSync(ctx, payload, "failed", err.Error())
		}
		return err
	}
	if payload.Scope == "org" && h.statusWriter != nil {
		_ = h.statusWriter.MarkOrgNodeSynced(ctx, payload.OrgID, payload.NodeID)
	}
	if payload.Scope == "app" {
		h.recordAppSync(ctx, payload, "succeeded", "")
	}
	return nil
}

// recordAppSync 把 app scope 同步事件落到 audit_logs。
// 失败时 errorMessage 非空,成功时 ""。任何审计写入失败仅打日志,不抛回 worker。
// 备注:noop change_type 也会留痕,便于排查"重试按钮按了但啥都没做"的情况(虽然
// 当前 dispatcher 没有为 app scope 入过 noop,留 hook 给未来扩展)。
func (h *KnowledgeSyncHandler) recordAppSync(ctx context.Context, payload knowledgeSyncPayload, result, errMsg string) {
	if h.auditor == nil {
		return
	}
	// 详情字段填写「文件 <relPath>」便于审计页面识别同步对象；
	// noop 或缺路径时留空，前端展示「—」。
	detail := ""
	if payload.ChangeType != "noop" && payload.RelPath != "" {
		detail = fmt.Sprintf("文件 %s", payload.RelPath)
	}
	event := service.AuditEvent{
		ActorRole:    "system",
		OrgID:        payload.OrgID,
		TargetType:   "app_knowledge_sync",
		TargetID:     payload.AppID,
		Action:       payload.ChangeType, // upload_file / delete_file / noop
		Result:       result,
		ErrorMessage: errMsg,
		Metadata: map[string]any{
			"node_id":  payload.NodeID,
			"rel_path": payload.RelPath,
		},
		DetailMessage: detail,
	}
	if _, err := h.auditor.Record(ctx, event); err != nil {
		slog.ErrorContext(ctx, "写 app_knowledge_sync 审计失败", "error", err)
	}
}

// execute 拆出原核心同步逻辑，让 Handle 集中做 status 旁路。
//
// 路径策略(hermes-agent-pull 切换后):
//   - scope=app:目标 = apps/<app_id>/input/resources/knowledge/app/<rel>
//   - scope=org:遍历该 org 在 node 上的每个 app,对每个 app 写
//     apps/<each_app>/input/resources/knowledge/org/<rel>
//
// 旧 apps/<id>/knowledge/ 与 orgs/<id>/knowledge/ 沙箱已在 agent T13 下线;
// 任何残留对老路径的写入都会得到 404, 与本切换不兼容。
func (h *KnowledgeSyncHandler) execute(ctx context.Context, payload knowledgeSyncPayload) error {
	switch payload.ChangeType {
	case "noop":
		// 「重试同步」入口：仅触发状态翻转，不读文件不调 agent。
		// 真正的全量重新同步在 spec §16.11 的"管理员触发全量重新同步"功能里实现。
		return nil
	case "upload_file":
		return h.executeUpload(ctx, payload)
	case "delete_file":
		return h.executeDelete(ctx, payload)
	default:
		return fmt.Errorf("未知 change_type: %s", payload.ChangeType)
	}
}

// executeUpload 处理 upload_file 事件。
// 主副本只读一次(单文件可能扇出到多个 app),读入内存后用 bytes.NewReader 多次重放。
func (h *KnowledgeSyncHandler) executeUpload(ctx context.Context, payload knowledgeSyncPayload) error {
	if h.source == nil {
		return fmt.Errorf("knowledge sync handler 未配置主副本源")
	}
	reader, _, err := h.source.Open(payload.MasterPath)
	if err != nil {
		return fmt.Errorf("读取主副本失败: %w", err)
	}
	body, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr != nil {
		return fmt.Errorf("读取主副本内容失败: %w", readErr)
	}
	targets, err := h.resolveTargetApps(ctx, payload)
	if err != nil {
		return err
	}
	scopeDir := scopeSubdir(payload.Scope)
	for _, appID := range targets {
		// org scope 一份变更扇出到多个 app:每次都新建 bytes.Reader,避免 reader 被一次性读完。
		target := scopeDir + "/" + payload.RelPath
		if err := h.sink.UploadAppInputFile(ctx, payload.NodeID, appID, target, bytes.NewReader(body)); err != nil {
			return fmt.Errorf("上传到节点失败: %w", err)
		}
	}
	return nil
}

// executeDelete 处理 delete_file 事件,对每个目标 app 各发一次 input/file DELETE。
// 中间任一 app 失败立即返回,worker 会按 max_attempts 重试整条 job。
func (h *KnowledgeSyncHandler) executeDelete(ctx context.Context, payload knowledgeSyncPayload) error {
	targets, err := h.resolveTargetApps(ctx, payload)
	if err != nil {
		return err
	}
	scopeDir := scopeSubdir(payload.Scope)
	for _, appID := range targets {
		target := scopeDir + "/" + payload.RelPath
		if err := h.sink.DeleteAppInputFile(ctx, payload.NodeID, appID, target); err != nil {
			return fmt.Errorf("删除节点文件失败: %w", err)
		}
	}
	return nil
}

// scopeSubdir 把 payload.Scope 翻译成 apps/<id>/input/ 下的子目录前缀。
// 与镜像 oc-entrypoint 约定的 resources/knowledge/{org,app}/ 命名严格一致;
// 任何不在 {org, app} 内的值都不会走到这里 (validate 已过滤)。
func scopeSubdir(scope string) string {
	if scope == "org" {
		return "resources/knowledge/org"
	}
	return "resources/knowledge/app"
}

// resolveTargetApps 返回本次同步需要写入的目标 app 列表(每个 app 都对应节点
// apps/<id>/input/ 一份独立沙箱)。
//
//   - scope=app:目标就是 payload.AppID,单元素列表;
//   - scope=org:列该 org 下所有 app,过滤出 runtime_node_id == payload.NodeID
//     的那部分(同节点上属于该 org 的所有 app 都要收到这份 org 知识库)。
//     listing 失败直接冒泡;返回空列表视为「该节点上没有该 org 的应用」,
//     handler 退化为 no-op 让 status 仍翻 synced (主副本变更与该节点无关)。
//
// apps 未注入时 (测试桩) org scope 直接返回 nil,handler 不写文件;调用方应在
// 装配里注入 sqlc.Queries 以保证生产链路完备。
func (h *KnowledgeSyncHandler) resolveTargetApps(ctx context.Context, payload knowledgeSyncPayload) ([]string, error) {
	if payload.Scope == "app" {
		return []string{payload.AppID}, nil
	}
	if h.apps == nil {
		// 测试装配兜底:无 lister 时 org scope 不写文件,等同于「主副本变更没有需要落地的节点」。
		return nil, nil
	}
	orgUUID, err := parseUUIDForKnowledgeSync(payload.OrgID)
	if err != nil {
		return nil, fmt.Errorf("非法 org_id: %w", err)
	}
	// limit=500:单组织 app 数量极少超过此规模;若超过则后续 page 不写本次同步,
	// 与 wiring 内 EnqueueOrgReload 的 limit 取齐(同一假设,容量来日再扩)。
	apps, err := h.apps.ListAppsByOrg(ctx, sqlc.ListAppsByOrgParams{OrgID: orgUUID, Limit: 500, Offset: 0})
	if err != nil {
		return nil, fmt.Errorf("列出组织应用失败: %w", err)
	}
	out := make([]string, 0, len(apps))
	for _, a := range apps {
		// 仅同步给落在本 job 目标节点上的 app:跨节点 app 由 dispatcher 在 ListRuntimeNodes
		// 阶段拆成独立 (org, node) job 各自处理。runtime_node_id 为 NULL 表示该 app 未绑定
		// 节点 (初始化中或已下线),跳过避免无效写入。
		if !a.RuntimeNodeID.Valid {
			continue
		}
		if uuidStringForKnowledgeSync(a.RuntimeNodeID) != payload.NodeID {
			continue
		}
		out = append(out, uuidStringForKnowledgeSync(a.ID))
	}
	return out, nil
}

// parseUUIDForKnowledgeSync 把 service 层传来的 16 位字符串 org_id 转成 pgtype.UUID,
// 与 wiring 包 parseUUIDForWiring 等价;放在本包内是为了避免 worker → cmd/server 反向引用。
func parseUUIDForKnowledgeSync(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, err
	}
	return u, nil
}

// uuidStringForKnowledgeSync 把 sqlc 取出的 pgtype.UUID 转回 16 位标准串,
// 与 wiring 内的 uuidToStringWiring 实现一致;不复用是因为反向依赖会引入 cycle。
func uuidStringForKnowledgeSync(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, b := range id.Bytes {
		out = append(out, digits[b>>4], digits[b&0x0f])
		if i == 3 || i == 5 || i == 7 || i == 9 {
			out = append(out, '-')
		}
	}
	return string(out)
}

// validate 校验 payload 的 scope 与对应 ID 字段。
func (p knowledgeSyncPayload) validate() error {
	switch p.Scope {
	case "org":
		if p.OrgID == "" {
			return fmt.Errorf("org scope 缺少 org_id")
		}
	case "app":
		if p.AppID == "" {
			return fmt.Errorf("app scope 缺少 app_id")
		}
	default:
		return fmt.Errorf("未知 scope: %s", p.Scope)
	}
	// noop 是「重试同步」专用 change_type，不需要 rel_path。
	if p.ChangeType != "noop" && p.RelPath == "" {
		return fmt.Errorf("缺少 rel_path")
	}
	return nil
}
