// app_skill_adapters.go — AppSkillService 依赖接口的生产实现适配器。
//
// 本文件包含一个轻量适配器：
//   - AssistantVersionSkillLoader：适配 AssistantVersionStore → AssistantVersionLoader
//     通过 GetAssistantVersion + decodeSkills 取 skills_json 内所有 skill name。
//
// AppLocator 由 OcOpsResolverFromStore.LocateApp 直接实现（见 ocops.go），
// 无需单独适配器（LocateApp 复用 Resolve 同一 GetApp 调用，额外取 VersionID 字段）。
package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"oc-manager/internal/store/sqlc"
)

// assistantVersionGetStore 是 AssistantVersionSkillLoader 所需的最小存储接口，
// 只声明 GetAssistantVersion，避免依赖完整的 AssistantVersionStore 接口。
// 由 store.AssistantVersionStore（或 *sqlc.Queries）满足；单测可注入 fake。
type assistantVersionGetStore interface {
	// GetAssistantVersion 按 ID 查询助手版本；不存在时返回 sql.ErrNoRows。
	GetAssistantVersion(ctx context.Context, id string) (sqlc.AssistantVersion, error)
}

// AssistantVersionSkillLoader 把 assistantVersionGetStore 适配为 AssistantVersionLoader。
// SkillNames 调用 GetAssistantVersion → decodeSkills，取所有 skill 的 name。
type AssistantVersionSkillLoader struct {
	// store 提供版本行的最小查询能力（GetAssistantVersion）
	store assistantVersionGetStore
}

// NewAssistantVersionSkillLoader 构造 AssistantVersionSkillLoader，注入 store。
func NewAssistantVersionSkillLoader(store assistantVersionGetStore) *AssistantVersionSkillLoader {
	return &AssistantVersionSkillLoader{store: store}
}

// SkillNames 按 versionID 取该版本 skills_json 中所有 skill 的 name 列表。
//
// 边界条件：
//   - versionID 为空：app 未绑定任何版本，无删除保护，返回 nil（空切片），不报错。
//   - 版本不存在（sql.ErrNoRows）：版本已删除或 ID 无效，降级为空切片，不阻塞卸载。
//   - skills_json 为空或未配置 skill：返回 nil，无保护。
func (l *AssistantVersionSkillLoader) SkillNames(ctx context.Context, versionID string) ([]string, error) {
	if versionID == "" {
		// app 未绑定任何版本，无需删除保护
		return nil, nil
	}
	row, err := l.store.GetAssistantVersion(ctx, versionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// 版本已被软删除或 ID 无效：降级为无保护（不阻塞卸载操作）
			return nil, nil
		}
		return nil, fmt.Errorf("查询助手版本失败: %w", err)
	}
	// 复用 assistant_version_service.go 中的 decodeSkills 解析 skills_json
	skills, err := decodeSkills(row.SkillsJson)
	if err != nil {
		return nil, fmt.Errorf("解析助手版本 skills_json 失败: %w", err)
	}
	names := make([]string, 0, len(skills))
	for _, s := range skills {
		names = append(names, s.Name)
	}
	return names, nil
}
