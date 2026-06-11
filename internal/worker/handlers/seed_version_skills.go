package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/google/uuid"

	mlog "oc-manager/internal/log"
	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// AppSkillSeedStore 是种子注入所需的最小存取接口（无权限，系统内部用）。
// 由 dbStore.Queries 实现：GetAppSkillByAppAndName + CreateAppSkill 均已由 Plan 3 的
// sqlc 生成。
type AppSkillSeedStore interface {
	// GetAppSkillByAppAndName 按 app_id + name 查 app_skills；不存在时返回 sql.ErrNoRows。
	GetAppSkillByAppAndName(ctx context.Context, arg sqlc.GetAppSkillByAppAndNameParams) (sqlc.AppSkill, error)
	// CreateAppSkill 写入一条 app_skill 行（:exec，无返回行）。
	CreateAppSkill(ctx context.Context, arg sqlc.CreateAppSkillParams) error
}

// seedVersionSkills 把 version.SkillsJson 里实例尚无的 skill 并集注入 app_skills。
//
// 设计原则：
//   - 已有的 skill（按 name 精确匹配）不覆盖，保留用户可能的自定义改动；
//   - 残留 skill（实例有但版本没有）不删除，避免误清用户在 per-app 单独安装的 skill；
//   - 最大努力：单条失败只 slog.Warn，不中断主流程（初始化/重启 job 不因此 markFailed）；
//   - 无 principal，不调 oc-ops，只写 DB，适合在 worker 系统内部调用。
//
// version.ID 写入 source_metadata["seeded_from_version"]，供排查追溯。
func seedVersionSkills(ctx context.Context, store AppSkillSeedStore, appID string, version sqlc.AssistantVersion) error {
	// 解析版本快照里的 skill 列表；空版本或解析失败直接返回（非致命）。
	skills, err := service.DecodeVersionSkills(version.SkillsJson)
	if err != nil {
		// skills_json 格式异常属于数据问题，记录 warn 后直接返回；
		// 不阻断 app_initialize 主流程，后续运维修正 skills_json 后重启可补齐。
		slog.WarnContext(ctx, "种子注入：解析 skills_json 失败", "app", appID, "version", version.ID, mlog.Err(err))
		return nil
	}

	for _, k := range skills {
		// 检查实例是否已有该 skill（按 name 精确匹配）。
		_, err := store.GetAppSkillByAppAndName(ctx, sqlc.GetAppSkillByAppAndNameParams{
			AppID: appID,
			Name:  k.Name,
		})
		if err == nil {
			// 已有，不覆盖；跳过本条。
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			// 查询失败（非「不存在」），记录 warn 后跳过本条，不阻断其他 skill 的注入。
			slog.WarnContext(ctx, "种子注入：查询 app_skill 失败", "app", appID, "skill", k.Name, mlog.Err(err))
			continue
		}

		// 构造 source_metadata：记录注入来源版本 ID，便于事后追溯。
		meta, _ := json.Marshal(map[string]any{
			"seeded_from_version": version.ID,
		})

		// 写入新 app_skill 行；CachedTarPath 直接复用 platform_skills 的归档路径，不二次拷贝。
		if err := store.CreateAppSkill(ctx, sqlc.CreateAppSkillParams{
			ID:             uuid.NewString(),
			AppID:          appID,
			Name:           k.Name,
			Source:         k.Source,
			SourceRef:      k.SourceRef,
			Version:        k.Version,
			CachedTarPath:  k.CachedPath,
			SourceMetadata: meta,
			FileSize:       k.FileSize,
			FileSha256:     k.FileSha256,
			// InstalledBy 留空：种子注入为系统行为，无操作用户。
		}); err != nil {
			// 写入失败只记录 warn，不中断其他 skill 的注入。
			slog.WarnContext(ctx, "种子注入：写入 app_skill 失败", "app", appID, "skill", k.Name, mlog.Err(err))
		}
	}

	return nil
}
