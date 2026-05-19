package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// AppRuntimeStore 是 start/stop/restart/delete handler 共用的最小数据访问能力。
type AppRuntimeStore interface {
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	SetAppStatus(ctx context.Context, arg sqlc.SetAppStatusParams) (sqlc.App, error)
	// SetAppModelSynced 在实例重启完成后标记模型已同步（model_synced=true）。
	SetAppModelSynced(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	SoftDeleteApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
}

// ContainerLifecycle 抽象 worker 需要的容器生命周期能力。
// 与 runtime.AgentBackedAdapter 兼容；测试中可替换为内存桩。
type ContainerLifecycle interface {
	StartContainer(ctx context.Context, nodeID, containerID string) error
	StopContainer(ctx context.Context, nodeID, containerID string) error
	RestartContainer(ctx context.Context, nodeID, containerID string) error
	RemoveContainer(ctx context.Context, nodeID, containerID string) error
}

// AppDelete 用 NewAPIClientFactory 拿 user-scoped client 调 SetAPIKeyStatus 禁用 token，
// status=2 表示禁用。原来的 APIKeyDisabler 单方法接口已下线（admin token 拿不到别 user 的
// token 完整 key，token 操作必须以业务 user 身份调）。

// AppDeleteFileOps 抽象 app_delete 需要的 agent 文件 API 子集。
//
// Sprint 2 起优先调 ArchiveApp 把节点上 apps/<id>/ 整目录 mv 到 archived/，
// 不再粗暴删除。manager 端调用方通过类型断言探测 ArchiveApp 是否实现，
// 未实现的旧实现仍走 DeleteAppPath 兼容路径。
type AppDeleteFileOps interface {
	DeleteAppPath(ctx context.Context, nodeID, appID string) error
}

// AppArchiver 是 AppDeleteFileOps 的扩展：实现该接口的 fileOps 会优先走 ArchiveApp。
// AgentBackedAdapter 已实现此接口（Sprint 1 加的 ArchiveApp 方法）。
type AppArchiver interface {
	ArchiveApp(ctx context.Context, nodeID, appID string) error
}

// payload 描述四个 handler 共享的输入。
type appOpPayload struct {
	AppID string `json:"app_id"`
}

func decodeAppOpPayload(raw []byte) (appOpPayload, error) {
	var p appOpPayload
	if len(raw) == 0 {
		return p, fmt.Errorf("payload 为空")
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("解析 payload 失败: %w", err)
	}
	if p.AppID == "" {
		return p, fmt.Errorf("payload 缺少 app_id")
	}
	return p, nil
}

// loadApp 是四个 handler 共用的"取 app + 校验存在"流程。
// soft-deleted 的 app 视为不存在，避免重复处理已经删除的应用。
func loadApp(ctx context.Context, store AppRuntimeStore, payload appOpPayload) (sqlc.App, pgtype.UUID, error) {
	id, err := parseUUID(payload.AppID)
	if err != nil {
		return sqlc.App{}, pgtype.UUID{}, fmt.Errorf("非法 app_id: %w", err)
	}
	app, err := store.GetApp(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.App{}, id, fmt.Errorf("应用 %s 不存在", payload.AppID)
		}
		return sqlc.App{}, id, fmt.Errorf("查询应用失败: %w", err)
	}
	if app.DeletedAt.Valid {
		return sqlc.App{}, id, fmt.Errorf("应用 %s 已删除", payload.AppID)
	}
	return app, id, nil
}

// AppStartContainerHandler 拉起容器并把状态推到 running。
//
// 幂等说明：worker 不在 handler 内 inspect 容器状态——docker 端 ContainerStart
// 对 already running 会返 304，由调用方 SetAppStatus 兜底语义。
type AppStartContainerHandler struct {
	store      AppRuntimeStore
	containers ContainerLifecycle
}

// NewAppStartContainerHandler 构造 handler。
func NewAppStartContainerHandler(store AppRuntimeStore, containers ContainerLifecycle) *AppStartContainerHandler {
	return &AppStartContainerHandler{store: store, containers: containers}
}

// Handle 执行 app_start_container job。
func (h *AppStartContainerHandler) Handle(ctx context.Context, job sqlc.Job) error {
	if job.Type != domain.JobTypeAppStartContainer {
		return fmt.Errorf("非 app_start_container 任务: %s", job.Type)
	}
	payload, err := decodeAppOpPayload(job.PayloadJson)
	if err != nil {
		return err
	}
	app, _, err := loadApp(ctx, h.store, payload)
	if err != nil {
		return err
	}
	if app.ContainerID.String == "" {
		return fmt.Errorf("应用 %s 尚未创建容器，无法启动", payload.AppID)
	}
	nodeID := uuidToString(app.RuntimeNodeID)
	if err := h.containers.StartContainer(ctx, nodeID, app.ContainerID.String); err != nil {
		return fmt.Errorf("启动容器失败: %w", err)
	}
	if _, err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusRunning}); err != nil {
		return fmt.Errorf("更新应用状态失败: %w", err)
	}
	return nil
}

// AppStopContainerHandler 停止容器并把状态推到 stopped。
type AppStopContainerHandler struct {
	store      AppRuntimeStore
	containers ContainerLifecycle
}

// NewAppStopContainerHandler 创建停止容器 handler，依赖由 worker 装配层注入。
func NewAppStopContainerHandler(store AppRuntimeStore, containers ContainerLifecycle) *AppStopContainerHandler {
	return &AppStopContainerHandler{store: store, containers: containers}
}

// Handle 执行 app_stop_container job，并把无容器场景收敛为 stopped。
func (h *AppStopContainerHandler) Handle(ctx context.Context, job sqlc.Job) error {
	if job.Type != domain.JobTypeAppStopContainer {
		return fmt.Errorf("非 app_stop_container 任务: %s", job.Type)
	}
	payload, err := decodeAppOpPayload(job.PayloadJson)
	if err != nil {
		return err
	}
	app, _, err := loadApp(ctx, h.store, payload)
	if err != nil {
		return err
	}
	if app.ContainerID.String == "" {
		// 没容器 ID 等价于已经停止：直接推状态便于状态机收敛。
		if _, err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusStopped}); err != nil {
			return fmt.Errorf("更新应用状态失败: %w", err)
		}
		return nil
	}
	nodeID := uuidToString(app.RuntimeNodeID)
	if err := h.containers.StopContainer(ctx, nodeID, app.ContainerID.String); err != nil {
		return fmt.Errorf("停止容器失败: %w", err)
	}
	if _, err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusStopped}); err != nil {
		return fmt.Errorf("更新应用状态失败: %w", err)
	}
	return nil
}

// SessionCleaner 抽象"清空 app 会话"能力,使 restart 后开新 session 时
// 重新 snapshot SOUL.md。Hermes 在 session 启动时把 system_prompt 冻结
// 存进 SQLite,后续 SOUL.md 改动对老 session 不生效——所以配置变更类
// 操作(改 model / persona / 知识库 / 重启)必须配合清 session 才能让
// 最新配置进入对话。
//
// runtime.AgentBackedAdapter.ClearAppSessions 实现此接口。
type SessionCleaner interface {
	ClearAppSessions(ctx context.Context, nodeID, appID string) error
}

// AppRestartContainerHandler 触发应用容器重启,由可选的 SessionCleaner 决定走
// stop → clear sessions → start 三步还是退回到原子 docker restart。
//
// restart 链路不再重渲染 Hermes 的 config.yaml / SOUL.md / skills:
// 镜像 oc-entrypoint 每次容器启动幂等重渲染这些配置文件 + 知识库 skills,
// manager 端只负责"停 → 清 session → 起",保证启动后 Hermes 加载到的是
// 最新 DB / 主副本快照,且新 session 重新 snapshot 最新 SOUL.md。
type AppRestartContainerHandler struct {
	store          AppRuntimeStore
	containers     ContainerLifecycle
	sessionCleaner SessionCleaner
}

// NewAppRestartContainerHandler 创建重启容器 handler，复用容器生命周期接口。
func NewAppRestartContainerHandler(store AppRuntimeStore, containers ContainerLifecycle) *AppRestartContainerHandler {
	return &AppRestartContainerHandler{store: store, containers: containers}
}

// SetSessionCleaner 注入"清空 app 会话"能力。
// 注入后 restart 会在容器实际 restart 前清空 .hermes/sessions/,
// 让新 session snapshot 最新 SOUL.md(含最新模型 / persona / 知识库)。
// nil 时 restart 不清 session,等价于旧行为。
func (h *AppRestartContainerHandler) SetSessionCleaner(cleaner SessionCleaner) {
	h.sessionCleaner = cleaner
}

// Handle 执行 app_restart_container job，并在成功后把应用状态推回 running。
func (h *AppRestartContainerHandler) Handle(ctx context.Context, job sqlc.Job) error {
	if job.Type != domain.JobTypeAppRestartContainer {
		return fmt.Errorf("非 app_restart_container 任务: %s", job.Type)
	}
	payload, err := decodeAppOpPayload(job.PayloadJson)
	if err != nil {
		return err
	}
	app, _, err := loadApp(ctx, h.store, payload)
	if err != nil {
		return err
	}
	if app.ContainerID.String == "" {
		return fmt.Errorf("应用 %s 尚未创建容器，无法重启", payload.AppID)
	}
	// restart 链路不再 refresh hermes 文件:镜像 oc-entrypoint 每次启动会幂等
	// 重渲染 config.yaml / SOUL.md / skills,manager 端只需要确保容器实际
	// 经历一次"停 → 起",新启动周期里 entrypoint 会读 DB / 主副本最新快照。
	nodeID := uuidToString(app.RuntimeNodeID)
	// session 真正存储是 .hermes/state.db (SQLite),需要在容器 stop 后才能删
	// (运行中 SQLite 持有文件锁)。所以这里把 docker restart 拆成
	// stop → clear sessions → start 三步,而不是用原子 RestartContainer。
	// 失败时立即冒泡让 worker 重试,避免半重启状态(容器跑着但 state.db 被清的不一致)。
	if h.sessionCleaner != nil {
		if err := h.containers.StopContainer(ctx, nodeID, app.ContainerID.String); err != nil {
			return fmt.Errorf("停止容器失败: %w", err)
		}
		if err := h.sessionCleaner.ClearAppSessions(ctx, nodeID, payload.AppID); err != nil {
			return fmt.Errorf("清空 sessions 失败: %w", err)
		}
		if err := h.containers.StartContainer(ctx, nodeID, app.ContainerID.String); err != nil {
			return fmt.Errorf("启动容器失败: %w", err)
		}
	} else {
		// 没注入 sessionCleaner 时退回到原 docker restart 行为(向后兼容)。
		if err := h.containers.RestartContainer(ctx, nodeID, app.ContainerID.String); err != nil {
			return fmt.Errorf("重启容器失败: %w", err)
		}
	}
	if _, err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusRunning}); err != nil {
		return fmt.Errorf("更新应用状态失败: %w", err)
	}
	// 重启完成后标记模型已同步：容器已加载最新 config.yaml 中的 model_id，DB 与运行时一致。
	if _, err := h.store.SetAppModelSynced(ctx, app.ID); err != nil {
		return fmt.Errorf("标记模型同步状态失败: %w", err)
	}
	return nil
}

// AppDeleteHandler 串起删除流程：
//  1. 停止并删除容器（缺 container_id 时跳过）；
//  2. 禁用 new-api token（已有 newapi_key_id 时执行）；
//  3. agent 上把应用工作目录清理（fileOps != nil 时执行；后续 task 升级为归档）；
//  4. 软删 apps 行。
//
// 任意一步失败立即冒泡，由 worker 重试；重试时各步骤需自行幂等。
type AppDeleteHandler struct {
	store      AppRuntimeStore
	containers ContainerLifecycle
	factory    NewAPIClientFactory
	fileOps    AppDeleteFileOps
}

// NewAppDeleteHandler 创建删除应用 handler，允许 new-api 与文件操作依赖按环境为空。
func NewAppDeleteHandler(store AppRuntimeStore, containers ContainerLifecycle, factory NewAPIClientFactory, fileOps AppDeleteFileOps) *AppDeleteHandler {
	return &AppDeleteHandler{store: store, containers: containers, factory: factory, fileOps: fileOps}
}

// Handle 执行 app_delete job；任一步失败都返回错误交给 worker 重试。
func (h *AppDeleteHandler) Handle(ctx context.Context, job sqlc.Job) error {
	if job.Type != domain.JobTypeAppDelete {
		return fmt.Errorf("非 app_delete 任务: %s", job.Type)
	}
	payload, err := decodeAppOpPayload(job.PayloadJson)
	if err != nil {
		return err
	}
	id, err := parseUUID(payload.AppID)
	if err != nil {
		return fmt.Errorf("非法 app_id: %w", err)
	}
	app, err := h.store.GetApp(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // 已经删除则直接成功
		}
		return fmt.Errorf("查询应用失败: %w", err)
	}
	if app.DeletedAt.Valid {
		return nil
	}

	nodeID := uuidToString(app.RuntimeNodeID)
	if app.ContainerID.String != "" {
		if err := h.containers.StopContainer(ctx, nodeID, app.ContainerID.String); err != nil {
			// stop 失败不阻塞 remove：force remove 可以兜底，但 stop 错误必须冒泡用于审计排障。
			return fmt.Errorf("停止容器失败: %w", err)
		}
		if err := h.containers.RemoveContainer(ctx, nodeID, app.ContainerID.String); err != nil {
			return fmt.Errorf("删除容器失败: %w", err)
		}
	}

	if h.factory != nil && app.NewapiKeyID.Valid && app.NewapiKeyID.String != "" {
		keyID, parseErr := strconv.ParseInt(app.NewapiKeyID.String, 10, 64)
		if parseErr == nil {
			client, err := h.factory.UserScopedFor(ctx, app)
			if err != nil {
				return fmt.Errorf("构造 user-scoped client 失败: %w", err)
			}
			// status=2 表示禁用
			if err := client.SetAPIKeyStatus(ctx, keyID, 2); err != nil {
				return fmt.Errorf("禁用 new-api token 失败: %w", err)
			}
		}
	}

	if h.fileOps != nil && nodeID != "" {
		// Sprint 2：fileOps 实现了 AppArchiver 时优先归档（保留节点目录用于审计 / 误删恢复），
		// 否则回退到原 DeleteAppPath 直接删除。归档目录由 agent 周期性 cleanup-archives 清理。
		if archiver, ok := h.fileOps.(AppArchiver); ok {
			if err := archiver.ArchiveApp(ctx, nodeID, payload.AppID); err != nil {
				return fmt.Errorf("归档应用工作目录失败: %w", err)
			}
		} else if err := h.fileOps.DeleteAppPath(ctx, nodeID, payload.AppID); err != nil {
			return fmt.Errorf("清理应用工作目录失败: %w", err)
		}
	}

	if _, err := h.store.SoftDeleteApp(ctx, id); err != nil {
		return fmt.Errorf("软删应用失败: %w", err)
	}
	return nil
}
