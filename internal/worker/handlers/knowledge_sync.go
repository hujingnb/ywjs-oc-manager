package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// KnowledgeFileSource 抽象 manager 主副本文件读取能力。
// 同步任务不直接依赖 files.KnowledgeMaster，便于在测试中注入内存 reader。
type KnowledgeFileSource interface {
	Open(relativePath string) (io.ReadCloser, int64, error)
}

// KnowledgeFileSink 抽象 agent 文件 API 上传 / 删除能力。
type KnowledgeFileSink interface {
	UploadFile(ctx context.Context, nodeID, remotePath string, content io.Reader) error
	DeletePath(ctx context.Context, nodeID, remotePath string) error
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
	source KnowledgeFileSource
	sink   KnowledgeFileSink
}

// NewKnowledgeSyncHandler 创建 handler。
func NewKnowledgeSyncHandler(source KnowledgeFileSource, sink KnowledgeFileSink) *KnowledgeSyncHandler {
	return &KnowledgeSyncHandler{source: source, sink: sink}
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
	remotePath, err := payload.targetRemotePath()
	if err != nil {
		return err
	}
	switch payload.ChangeType {
	case "upload_file":
		if h.source == nil {
			return fmt.Errorf("knowledge sync handler 未配置主副本源")
		}
		reader, _, err := h.source.Open(payload.MasterPath)
		if err != nil {
			return fmt.Errorf("读取主副本失败: %w", err)
		}
		defer reader.Close()
		if err := h.sink.UploadFile(ctx, payload.NodeID, remotePath, reader); err != nil {
			return fmt.Errorf("上传到节点失败: %w", err)
		}
	case "delete_file":
		if err := h.sink.DeletePath(ctx, payload.NodeID, remotePath); err != nil {
			return fmt.Errorf("删除节点文件失败: %w", err)
		}
	default:
		return fmt.Errorf("未知 change_type: %s", payload.ChangeType)
	}
	return nil
}

// targetRemotePath 根据 scope 拼出 agent 端目标路径。
func (p knowledgeSyncPayload) targetRemotePath() (string, error) {
	switch p.Scope {
	case "org":
		if p.OrgID == "" {
			return "", fmt.Errorf("org scope 缺少 org_id")
		}
		return path.Join("orgs", p.OrgID, "knowledge", p.RelPath), nil
	case "app":
		if p.AppID == "" {
			return "", fmt.Errorf("app scope 缺少 app_id")
		}
		return path.Join("apps", p.AppID, "knowledge", p.RelPath), nil
	default:
		return "", fmt.Errorf("未知 scope: %s", p.Scope)
	}
}

// 占位 import 避免 bytes 包未使用提示（用于将来 tar 全量同步）。
var _ = bytes.MinRead
