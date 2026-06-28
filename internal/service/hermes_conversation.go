// Package service —— hermes_conversation.go 实现实例会话能力。
// manager 不持有会话数据，所有读写经 oc-ops HTTP 转发到 app 实例内 hermes
// api_server，manager 仅做权限判断与最小输入校验。
package service

import (
	"context"
	"fmt"
	"strings"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/ocops"
)

// validateSessionID 校验会话 id：非空、长度受限、不含路径分隔/空白（防越界与注入）。
// hermes session id 形如 weixin:xxx / api_xxx，放宽字符但禁斜杠与空白。
func validateSessionID(sid string) error {
	sid = strings.TrimSpace(sid)
	if sid == "" || len(sid) > 256 || strings.ContainsAny(sid, " \t\r\n/") {
		return fmt.Errorf("%w: 非法 session id", ErrConversationBadRequest)
	}
	return nil
}

// ConversationFileResolver 把消息里的 file_id 解析为预签名 URL 与元数据。
// 由对话文件 service 实现（*ConversationFileService.ResolveFileURL），
// 在续聊富化阶段把文件 part 补全为可被下游 hermes 直接下载的引用。
type ConversationFileResolver interface {
	ResolveFileURL(ctx context.Context, appID, sid, fileID string) (url, filename, mime string, err error)
}

// HermesConversationService 暴露实例会话的读写能力。
type HermesConversationService struct {
	ops          conversationOps          // oc-ops 的类型化会话客户端窄接口
	resolver     OcOpsResolver            // 把 appID 解析为 oc-ops 调用坐标
	fileResolver ConversationFileResolver // 文件 part 解析器；未注入（S3 未启用）时不支持文件消息
}

// NewHermesConversationService 构造 service。
func NewHermesConversationService(ops conversationOps, resolver OcOpsResolver) *HermesConversationService {
	return &HermesConversationService{ops: ops, resolver: resolver}
}

// SetFileResolver 注入文件 part 解析器（对话文件 service 实现）。
// 仅在 S3 启用、对话文件能力可用时装配；未装配时含文件 part 的消息会被拒绝。
func (s *HermesConversationService) SetFileResolver(r ConversationFileResolver) { s.fileResolver = r }

// resolve 解析 appID、校验读权限、确保实例可调用 oc-ops。
func (s *HermesConversationService) resolve(ctx context.Context, p auth.Principal, appID string) (OcOpsAppLocation, error) {
	loc, err := s.resolver.Resolve(ctx, appID)
	if err != nil {
		return OcOpsAppLocation{}, err
	}
	if !auth.CanViewAppConversations(p, loc.OrgID, loc.OwnerUserID) {
		return OcOpsAppLocation{}, ErrConversationForbidden
	}
	// dev stub 实例不含真实 hermes 会话能力，按不支持处理。
	if !loc.Supported {
		return OcOpsAppLocation{}, ErrConversationNotSupported
	}
	// 没有可用的 oc-ops 基址说明实例运行时尚未就绪。
	if strings.TrimSpace(loc.Endpoint.BaseURL) == "" {
		return OcOpsAppLocation{}, ErrConversationRuntimeUnavailable
	}
	return loc, nil
}

// resolveManage 在 resolve 基础上加写权限校验（比 resolve 多一层 CanManageAppConversations）。
// 注：resolve 内部已含 CanViewAppConversations 读权限检查；此处 CanManageAppConversations 当前
// 与 CanViewAppConversations 等价，存在冗余，但有意保留以便将来读写权限分离演化时此处可独立收紧
// 写权限，无需改动调用方。
func (s *HermesConversationService) resolveManage(ctx context.Context, p auth.Principal, appID string) (OcOpsAppLocation, error) {
	loc, err := s.resolve(ctx, p, appID)
	if err != nil {
		return OcOpsAppLocation{}, err
	}
	if !auth.CanManageAppConversations(p, loc.OrgID, loc.OwnerUserID) {
		return OcOpsAppLocation{}, ErrConversationForbidden
	}
	return loc, nil
}

// ListSessions 列出实例下会话；source 非空时按渠道过滤。
// limit 超出合法范围（1-200）时兜底为 50，避免越界透传上游。
func (s *HermesConversationService) ListSessions(ctx context.Context, p auth.Principal, appID, source string, limit, offset int) ([]ocops.ConversationSession, error) {
	loc, err := s.resolve(ctx, p, appID)
	if err != nil {
		return nil, err
	}
	// 兜底默认，避免越界透传
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	out, err := s.ops.ListSessions(ctx, loc.Endpoint, source, limit, offset)
	if err != nil {
		return nil, mapOcOpsConversationErr(err)
	}
	return out, nil
}

// Messages 读会话历史消息。sid 必须通过 validateSessionID 校验。
func (s *HermesConversationService) Messages(ctx context.Context, p auth.Principal, appID, sid string) ([]ocops.ConversationMessage, error) {
	loc, err := s.resolve(ctx, p, appID)
	if err != nil {
		return nil, err
	}
	if err := validateSessionID(sid); err != nil {
		return nil, err
	}
	out, err := s.ops.SessionMessages(ctx, loc.Endpoint, sid)
	if err != nil {
		return nil, mapOcOpsConversationErr(err)
	}
	return out, nil
}

// CreateSession 新建一条 web 会话，title 可选。
func (s *HermesConversationService) CreateSession(ctx context.Context, p auth.Principal, appID, title string) (ocops.ConversationSession, error) {
	loc, err := s.resolveManage(ctx, p, appID)
	if err != nil {
		return ocops.ConversationSession{}, err
	}
	out, err := s.ops.CreateSession(ctx, loc.Endpoint, ocops.ConversationCreateReq{
		Source: "web",
		Title:  strings.TrimSpace(title),
	})
	if err != nil {
		return ocops.ConversationSession{}, mapOcOpsConversationErr(err)
	}
	return out, nil
}

// DeleteSession 删除会话。sid 必须通过 validateSessionID 校验。
func (s *HermesConversationService) DeleteSession(ctx context.Context, p auth.Principal, appID, sid string) error {
	loc, err := s.resolveManage(ctx, p, appID)
	if err != nil {
		return err
	}
	if err := validateSessionID(sid); err != nil {
		return err
	}
	if err := s.ops.DeleteSession(ctx, loc.Endpoint, sid); err != nil {
		return mapOcOpsConversationErr(err)
	}
	return nil
}

// Rename 重命名会话；title 不能为空白。
func (s *HermesConversationService) Rename(ctx context.Context, p auth.Principal, appID, sid, title string) (ocops.ConversationSession, error) {
	loc, err := s.resolveManage(ctx, p, appID)
	if err != nil {
		return ocops.ConversationSession{}, err
	}
	if err := validateSessionID(sid); err != nil {
		return ocops.ConversationSession{}, err
	}
	if strings.TrimSpace(title) == "" {
		return ocops.ConversationSession{}, fmt.Errorf("%w: 标题不能为空", ErrConversationBadRequest)
	}
	out, err := s.ops.UpdateSessionTitle(ctx, loc.Endpoint, sid, strings.TrimSpace(title))
	if err != nil {
		return ocops.ConversationSession{}, mapOcOpsConversationErr(err)
	}
	return out, nil
}

// Chat 续聊一轮。message 为文字字符串或多模态 parts 数组；
// 文件 part 经富化补全预签名 file_url 后转发上游，富化后无内容时拒绝。
func (s *HermesConversationService) Chat(ctx context.Context, p auth.Principal, appID, sid string, message any) (ocops.ConversationChatResult, error) {
	loc, err := s.resolveManage(ctx, p, appID)
	if err != nil {
		return ocops.ConversationChatResult{}, err
	}
	if err := validateSessionID(sid); err != nil {
		return ocops.ConversationChatResult{}, err
	}
	enriched, err := s.enrichMessage(ctx, appID, sid, message)
	if err != nil {
		return ocops.ConversationChatResult{}, err
	}
	// 富化后仍无可发送内容（空文字且无文件）则提前拒绝，不打到上游 hermes。
	if !messageHasContent(enriched) {
		return ocops.ConversationChatResult{}, fmt.Errorf("%w: 消息内容不能为空", ErrConversationBadRequest)
	}
	out, err := s.ops.SessionChat(ctx, loc.Endpoint, sid, ocops.ConversationChatReq{Message: enriched})
	if err != nil {
		return ocops.ConversationChatResult{}, mapOcOpsConversationErr(err)
	}
	return out, nil
}

// ChatStream 流式续聊，返回事件 channel，由 handler 逐帧转 SSE。
// message 语义与富化逻辑与 Chat 一致，但走流式 oc-ops 接口。
func (s *HermesConversationService) ChatStream(ctx context.Context, p auth.Principal, appID, sid string, message any) (<-chan ocops.ConversationStreamEvent, error) {
	loc, err := s.resolveManage(ctx, p, appID)
	if err != nil {
		return nil, err
	}
	if err := validateSessionID(sid); err != nil {
		return nil, err
	}
	enriched, err := s.enrichMessage(ctx, appID, sid, message)
	if err != nil {
		return nil, err
	}
	if !messageHasContent(enriched) {
		return nil, fmt.Errorf("%w: 消息内容不能为空", ErrConversationBadRequest)
	}
	ch, err := s.ops.SessionChatStream(ctx, loc.Endpoint, sid, ocops.ConversationChatReq{Message: enriched})
	if err != nil {
		return nil, mapOcOpsConversationErr(err)
	}
	return ch, nil
}

// enrichMessage 把多模态消息里的 input_file part 富化为带 file_url 的引用。
// message 为字符串时原样返回；为 parts 数组时逐个处理，input_file part 必须含 file_id，
// 经 fileResolver 解析为预签名 file_url 与 filename/mime；非 map 或非文件 part 原样保留。
func (s *HermesConversationService) enrichMessage(ctx context.Context, appID, sid string, message any) (any, error) {
	parts, ok := message.([]any)
	if !ok {
		return message, nil
	}
	out := make([]any, 0, len(parts))
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok {
			out = append(out, raw)
			continue
		}
		if part["type"] == "input_file" {
			fileID, _ := part["file_id"].(string)
			if fileID == "" {
				return nil, fmt.Errorf("%w: input_file 缺少 file_id", ErrConversationBadRequest)
			}
			// 未注入解析器（S3 未启用）时无法富化文件，按 bad request 拒绝。
			if s.fileResolver == nil {
				return nil, fmt.Errorf("%w: 文件解析器未配置", ErrConversationBadRequest)
			}
			url, filename, mime, err := s.fileResolver.ResolveFileURL(ctx, appID, sid, fileID)
			if err != nil {
				// 归属校验失败/记录不存在等统一按 bad request，不暴露内部错误。
				return nil, ErrConversationBadRequest
			}
			part["file_url"] = url
			part["filename"] = filename
			part["mime"] = mime
		}
		out = append(out, part)
	}
	return out, nil
}

// messageHasContent 判断富化后的消息是否有可发送内容（非空文字 或 至少一个文件 part）。
func messageHasContent(message any) bool {
	switch m := message.(type) {
	case string:
		return strings.TrimSpace(m) != ""
	case []any:
		for _, raw := range m {
			part, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if part["type"] == "input_file" {
				return true
			}
			if part["type"] == "text" {
				if t, _ := part["text"].(string); strings.TrimSpace(t) != "" {
					return true
				}
			}
		}
		return false
	default:
		return false
	}
}
