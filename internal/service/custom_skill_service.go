// Package service 的 custom_skill_service.go 实现定制技能的交付与取装。
// 交付:平台管理员把前端打包好的扁平 skill tar 交付到某工单——解析归档 name、强制技能名
// 一致性(同一工单迭代必须沿用同一 name)、按上传时间自动生成版本号、写归档到 skill 库与
// custom_skills 表、首次交付写目标可见范围、并把工单置为 delivered。
// 取装:安装时按 name(+version) 取回归档原始字节与 SHA256,供 AppSkillService 复用平台库同构契约。
package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/hermes"
	"oc-manager/internal/store/sqlc"
)

// CustomSkillStore 是 CustomSkillService 所需的数据能力(custom_skills/targets + 工单读取与置交付)。
type CustomSkillStore interface {
	CreateCustomSkill(ctx context.Context, arg sqlc.CreateCustomSkillParams) error
	GetCustomSkillByNameVersion(ctx context.Context, arg sqlc.GetCustomSkillByNameVersionParams) (sqlc.CustomSkill, error)
	GetSkillTicket(ctx context.Context, id string) (sqlc.SkillTicket, error)
	CreateCustomSkillTarget(ctx context.Context, arg sqlc.CreateCustomSkillTargetParams) error
	DeleteCustomSkillTargetsByName(ctx context.Context, name string) error
	MarkSkillTicketDelivered(ctx context.Context, arg sqlc.MarkSkillTicketDeliveredParams) error
}

// CustomSkillService 负责定制技能交付与取装。
type CustomSkillService struct {
	store CustomSkillStore
	blobs LibraryBlobStore
	now   func() time.Time // 可注入时钟,生成版本号与测试用
}

// NewCustomSkillService 构造交付 service。
func NewCustomSkillService(store CustomSkillStore, blobs LibraryBlobStore) *CustomSkillService {
	return &CustomSkillService{store: store, blobs: blobs, now: time.Now}
}

// CustomSkillTargetInput 是交付时的单条目标范围。
type CustomSkillTargetInput struct {
	OrgID    string
	Audience string // all_org | org_admins | requester_only
}

// DeliverCustomSkillInput 是交付入参(Data 为前端打包好的扁平 tar 字节)。
type DeliverCustomSkillInput struct {
	TicketID    string
	Description string
	Data        []byte
	Targets     []CustomSkillTargetInput
}

// CustomSkillResult 是交付结果视图。
type CustomSkillResult struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	TicketID   string `json:"ticket_id"`
	FileSize   int64  `json:"file_size"`
	FileSha256 string `json:"file_sha256"`
}

// Deliver 交付一个定制技能版本:校验权限/归档/技能名一致性 → 自动生成版本 → 写归档与库 → 写/沿用目标范围 → 置工单 delivered。
func (s *CustomSkillService) Deliver(ctx context.Context, p auth.Principal, in DeliverCustomSkillInput) (CustomSkillResult, error) {
	if !auth.CanManageSkillTicket(p) {
		return CustomSkillResult{}, ErrCustomSkillDenied
	}
	if len(in.Data) == 0 {
		return CustomSkillResult{}, fmt.Errorf("%w: 技能包为空", ErrCustomSkillInvalid)
	}
	if len(in.Targets) == 0 {
		return CustomSkillResult{}, fmt.Errorf("%w: 至少一个目标范围", ErrCustomSkillInvalid)
	}
	if err := validateCustomSkillTargets(in.Targets); err != nil {
		return CustomSkillResult{}, err
	}
	// 解析扁平 tar 取 name(复用平台库扁平契约校验)。
	info, err := hermes.InspectFlatSkillArchive(bytes.NewReader(in.Data))
	if err != nil {
		return CustomSkillResult{}, fmt.Errorf("%w: %v", ErrCustomSkillInvalid, err)
	}
	name := info.Name

	ticket, err := s.store.GetSkillTicket(ctx, in.TicketID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CustomSkillResult{}, ErrSkillTicketNotFound
		}
		return CustomSkillResult{}, fmt.Errorf("查询工单失败: %w", err)
	}
	if ticket.Status == SkillTicketStatusRejected {
		return CustomSkillResult{}, fmt.Errorf("%w: 已拒绝工单需先重新受理才能交付", ErrCustomSkillInvalid)
	}
	// 技能名一致性(后端强制):再次交付须沿用工单已锁定的 name。
	if ticket.CustomSkillName.Valid && ticket.CustomSkillName.String != name {
		return CustomSkillResult{}, fmt.Errorf("%w: 应为 %q", ErrCustomSkillNameMismatch, ticket.CustomSkillName.String)
	}

	version, err := s.uniqueVersion(ctx, name)
	if err != nil {
		return CustomSkillResult{}, err
	}
	sum := sha256.Sum256(in.Data)
	sha := hex.EncodeToString(sum[:])
	relPath, err := s.blobs.PutLibrarySkill("custom", name, version, "tar", in.Data)
	if err != nil {
		return CustomSkillResult{}, fmt.Errorf("写入定制技能归档失败: %w", err)
	}
	id := newUUID()
	if err := s.store.CreateCustomSkill(ctx, sqlc.CreateCustomSkillParams{
		ID: id, Name: name, Description: strings.TrimSpace(in.Description), Version: version,
		TarPath: relPath, FileSize: int64(len(in.Data)), FileSha256: sha,
		TicketID: in.TicketID, CreatedBy: null.StringFrom(p.UserID),
	}); err != nil {
		// 落库失败回滚已写入的归档,避免库内残留无主对象。
		_ = s.blobs.DeleteLibrarySkill(relPath)
		return CustomSkillResult{}, fmt.Errorf("写入定制技能失败: %w", err)
	}
	// 首次交付才写目标范围(再次交付沿用既有 targets,不重复写)。
	if !ticket.CustomSkillName.Valid {
		for _, tg := range in.Targets {
			if err := s.store.CreateCustomSkillTarget(ctx, sqlc.CreateCustomSkillTargetParams{
				ID: newUUID(), CustomSkillName: name, OrgID: tg.OrgID, Audience: tg.Audience,
			}); err != nil {
				return CustomSkillResult{}, fmt.Errorf("写入目标范围失败: %w", err)
			}
		}
	}
	if err := s.store.MarkSkillTicketDelivered(ctx, sqlc.MarkSkillTicketDeliveredParams{
		CustomSkillName: null.StringFrom(name), ID: in.TicketID,
	}); err != nil {
		return CustomSkillResult{}, fmt.Errorf("置工单交付失败: %w", err)
	}
	return CustomSkillResult{ID: id, Name: name, Version: version, TicketID: in.TicketID, FileSize: int64(len(in.Data)), FileSha256: sha}, nil
}

// UpdateTargets 覆盖写已交付定制技能的可见范围,用于交付后调整企业/角色可见性。
func (s *CustomSkillService) UpdateTargets(ctx context.Context, p auth.Principal, ticketID string, targets []CustomSkillTargetInput) error {
	if !auth.CanManageSkillTicket(p) {
		return ErrCustomSkillDenied
	}
	if len(targets) == 0 {
		return fmt.Errorf("%w: 至少一个目标范围", ErrCustomSkillInvalid)
	}
	if err := validateCustomSkillTargets(targets); err != nil {
		return err
	}
	ticket, err := s.store.GetSkillTicket(ctx, ticketID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSkillTicketNotFound
		}
		return fmt.Errorf("查询工单失败: %w", err)
	}
	if !ticket.CustomSkillName.Valid {
		return fmt.Errorf("%w: 工单尚未交付,无可见范围可编辑", ErrCustomSkillInvalid)
	}
	name := ticket.CustomSkillName.String
	// 覆盖写:当前 store 接口沿用既有无事务写法;任一写入失败会返回错误供上层提示重试。
	if err := s.store.DeleteCustomSkillTargetsByName(ctx, name); err != nil {
		return fmt.Errorf("清空旧目标范围失败: %w", err)
	}
	for _, target := range targets {
		if err := s.store.CreateCustomSkillTarget(ctx, sqlc.CreateCustomSkillTargetParams{
			ID: newUUID(), CustomSkillName: name, OrgID: target.OrgID, Audience: target.Audience,
		}); err != nil {
			return fmt.Errorf("写入目标范围失败: %w", err)
		}
	}
	return nil
}

// validateCustomSkillTargets 校验目标范围的 audience 枚举,避免前端传入不可解释的可见策略。
func validateCustomSkillTargets(targets []CustomSkillTargetInput) error {
	for _, target := range targets {
		switch target.Audience {
		case "all_org", "org_admins", "requester_only":
		default:
			return fmt.Errorf("%w: 非法受众 %q", ErrCustomSkillInvalid, target.Audience)
		}
	}
	return nil
}

// uniqueVersion 按上传时间生成 YYYYMMDD-HHmmss(UTC)版本;若同名同版本已存在(同秒多次交付)追加 -N。
func (s *CustomSkillService) uniqueVersion(ctx context.Context, name string) (string, error) {
	base := s.now().UTC().Format("20060102-150405")
	candidate := base
	for i := 2; i < 100; i++ {
		_, err := s.store.GetCustomSkillByNameVersion(ctx, sqlc.GetCustomSkillByNameVersionParams{Name: name, Version: candidate})
		if errors.Is(err, sql.ErrNoRows) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("校验版本唯一性失败: %w", err)
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
	return "", fmt.Errorf("%w: 版本号碰撞过多", ErrCustomSkillInvalid)
}

// GetForInstall 供 AppSkillService 安装时取归档原始字节(实现 PlatformInstaller 同构契约)。
func (s *CustomSkillService) GetForInstall(ctx context.Context, name, version string) ([]byte, string, error) {
	row, err := s.store.GetCustomSkillByNameVersion(ctx, sqlc.GetCustomSkillByNameVersionParams{Name: name, Version: version})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", ErrCustomSkillNotFound
		}
		return nil, "", fmt.Errorf("查询定制技能失败: %w", err)
	}
	rc, err := s.blobs.OpenLibrarySkill(row.TarPath)
	if err != nil {
		return nil, "", fmt.Errorf("打开定制技能归档失败: %w", err)
	}
	defer func() { _ = rc.Close() }()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(rc); err != nil {
		return nil, "", fmt.Errorf("读取定制技能归档失败: %w", err)
	}
	return buf.Bytes(), row.FileSha256, nil
}
