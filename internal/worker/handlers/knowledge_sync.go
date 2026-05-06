package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// KnowledgeFileSource 抽象 manager 主副本文件读取能力。
// 同步任务不直接依赖 files.KnowledgeMaster，便于在测试中注入内存 reader。
type KnowledgeFileSource interface {
	Open(relativePath string) (io.ReadCloser, int64, error)
}

// KnowledgeFileSink 抽象 agent 文件 API 上传 / 删除能力。
//
// Sprint 1 改用 scope-aware 接口：handler 不再拼 remotePath（避免 sink 实现去猜
// "apps/<id>/knowledge/<rel>" 这种业务级路径），由 sink 内部按 (scope, scopeID, relPath)
// 直接调 agent /v1/scopes/* 端点。
type KnowledgeFileSink interface {
	UploadOrgFile(ctx context.Context, nodeID, orgID, relPath string, content io.Reader) error
	UploadAppFile(ctx context.Context, nodeID, appID, relPath string, content io.Reader) error
	DeleteOrgFile(ctx context.Context, nodeID, orgID, relPath string) error
	DeleteAppFile(ctx context.Context, nodeID, appID, relPath string) error
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
// Scope 取值 'org' | 'app'：
//   - org：rel_path 是相对组织知识库根的路径，目标节点路径为 orgs/{org_id}/knowledge/<rel>
//   - app：rel_path 是相对应用知识库根的路径，目标节点路径为 apps/{app_id}/knowledge/<rel>
//
// ChangeType 取值 'upload_file' | 'delete_file'。
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
		return err
	}
	if payload.Scope == "org" && h.statusWriter != nil {
		_ = h.statusWriter.MarkOrgNodeSynced(ctx, payload.OrgID, payload.NodeID)
	}
	return nil
}

// execute 拆出原核心同步逻辑，让 Handle 集中做 status 旁路。
func (h *KnowledgeSyncHandler) execute(ctx context.Context, payload knowledgeSyncPayload) error {
	switch payload.ChangeType {
	case "noop":
		// 「重试同步」入口：仅触发状态翻转，不读文件不调 agent。
		// 真正的全量重新同步在 spec §16.11 的"管理员触发全量重新同步"功能里实现。
		return nil
	case "upload_file":
		if h.source == nil {
			return fmt.Errorf("knowledge sync handler 未配置主副本源")
		}
		reader, _, err := h.source.Open(payload.MasterPath)
		if err != nil {
			return fmt.Errorf("读取主副本失败: %w", err)
		}
		defer reader.Close()
		switch payload.Scope {
		case "org":
			if err := h.sink.UploadOrgFile(ctx, payload.NodeID, payload.OrgID, payload.RelPath, reader); err != nil {
				return fmt.Errorf("上传到节点失败: %w", err)
			}
		case "app":
			if err := h.sink.UploadAppFile(ctx, payload.NodeID, payload.AppID, payload.RelPath, reader); err != nil {
				return fmt.Errorf("上传到节点失败: %w", err)
			}
		}
	case "delete_file":
		switch payload.Scope {
		case "org":
			if err := h.sink.DeleteOrgFile(ctx, payload.NodeID, payload.OrgID, payload.RelPath); err != nil {
				return fmt.Errorf("删除节点文件失败: %w", err)
			}
		case "app":
			if err := h.sink.DeleteAppFile(ctx, payload.NodeID, payload.AppID, payload.RelPath); err != nil {
				return fmt.Errorf("删除节点文件失败: %w", err)
			}
		}
	default:
		return fmt.Errorf("未知 change_type: %s", payload.ChangeType)
	}
	return nil
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

// 占位 import 避免 bytes 包未使用提示（用于将来 tar 全量同步）。
var _ = bytes.MinRead
