package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	null "github.com/guregu/null/v5"
	"github.com/google/uuid"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// AppRuntimeStore 是 start/stop/restart/delete handler 共用的最小数据访问能力。
type AppRuntimeStore interface {
	GetApp(ctx context.Context, id string) (sqlc.App, error)
	SetAppStatus(ctx context.Context, arg sqlc.SetAppStatusParams) error
	SoftDeleteApp(ctx context.Context, id string) error
	// SetAppAppliedVersion 在重启成功后记录已应用的版本修订与镜像 ref，
	// 供前端 version_synced 检测使用。
	SetAppAppliedVersion(ctx context.Context, arg sqlc.SetAppAppliedVersionParams) error
	// SetAppContainer 在重启检测到镜像变更后清空 apps.container_id / container_name，
	// 让后续 app_initialize job 重新创建容器（空 ContainerID/ContainerName 即清空）。
	SetAppContainer(ctx context.Context, arg sqlc.SetAppContainerParams) error
	// CreateJob 在重启检测到镜像变更后入队 app_initialize job，
	// 复用初始化 4 阶段重拉新镜像并重建容器。
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) error
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

// AppKnowledgeCleaner 抽象 app_delete 对实例私有知识库的清理能力。
type AppKnowledgeCleaner interface {
	DeleteAppDataset(ctx context.Context, appID string) error
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
// 返回 app 与 appID 字符串（与旧签名兼容，第二返回值现在直接是 string）。
func loadApp(ctx context.Context, store AppRuntimeStore, payload appOpPayload) (sqlc.App, string, error) {
	app, err := store.GetApp(ctx, payload.AppID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sqlc.App{}, payload.AppID, fmt.Errorf("应用 %s 不存在", payload.AppID)
		}
		return sqlc.App{}, payload.AppID, fmt.Errorf("查询应用失败: %w", err)
	}
	if app.DeletedAt.Valid {
		return sqlc.App{}, payload.AppID, fmt.Errorf("应用 %s 已删除", payload.AppID)
	}
	return app, payload.AppID, nil
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
	// RuntimeNodeID nullable（spec-A2a）：.String 取 Go string 值。
	nodeID := app.RuntimeNodeID.String
	if err := h.containers.StartContainer(ctx, nodeID, app.ContainerID.String); err != nil {
		return fmt.Errorf("启动容器失败: %w", err)
	}
	if err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusRunning}); err != nil {
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
		if err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusStopped}); err != nil {
			return fmt.Errorf("更新应用状态失败: %w", err)
		}
		return nil
	}
	// RuntimeNodeID nullable（spec-A2a）：.String 取 Go string 值。
	nodeID := app.RuntimeNodeID.String
	if err := h.containers.StopContainer(ctx, nodeID, app.ContainerID.String); err != nil {
		return fmt.Errorf("停止容器失败: %w", err)
	}
	if err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusStopped}); err != nil {
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

// AppInputRefreshResult 是 RefreshAppInput 成功后返回的「已应用版本」信息，
// 供 restart handler 写 apps.applied_version_revision / applied_image_ref，
// 标记实例运行时已与当前绑定版本对齐。
type AppInputRefreshResult struct {
	// VersionRevision 是刷新时实例绑定版本的 revision。
	VersionRevision int32
	// ImageRef 是该版本 image_id 解析出的镜像 ref。
	ImageRef string
}

// AppInputRefresher 抽象「restart 前重写 input/manifest.yaml + resources/*.md」的能力。
// 成功后返回 AppInputRefreshResult，包含版本修订与镜像 ref，供 restart handler
// 写入 apps.applied_version_revision / applied_image_ref。
//
// 改三层 prompt / persona 都会落到 apps 表及相关联表, 之后通过
// app_restart_container job 触发本 handler。镜像 oc-entrypoint
// 每次容器启动会幂等地把 input/ 翻译成 hermes 自有 schema(config.yaml / SOUL.md /
// skills 等), 因此只要 restart 前把节点 apps/<id>/input/ 重写成最新数据,
// 下次 start 后容器内 hermes 自然加载到改后的版本配置与 prompt。
//
// 实现方负责: 取 DB 当前 app / org / owner 上下文 + 解密 api key, 装配
// hermes.AppInputData 并通过 hermes.WriteAppInput 写到目标节点的 input/ 目录。
// 实现见 cmd/server 装配层 appInputRefresher。
type AppInputRefresher interface {
	RefreshAppInput(ctx context.Context, nodeID string, app sqlc.App) (AppInputRefreshResult, error)
}

// RestartJobNotifier 抽象向 Redis 队列即时推送 jobID 的能力。
// 与 service.JobNotifier 同形态；nil 时由 scheduler 兜底入队。
type RestartJobNotifier interface {
	Enqueue(ctx context.Context, jobID string) error
}

// AppRestartContainerHandler 触发应用容器重启,由可选的 SessionCleaner 决定走
// stop → clear sessions → start 三步还是退回到原子 docker restart。
//
// 容器内部 hermes schema(config.yaml / SOUL.md / skills)由镜像 oc-entrypoint
// 在每次启动时根据 apps/<id>/input/ 自动重渲染, manager 端只负责:
//  1. 在 stop 前通过 inputRefresher 把节点上 input/manifest.yaml + resources/*.md
//     重写成最新 DB 快照(改 model / 三层 prompt / persona 都靠这一步生效);
//  2. 容器 stop → clear sessions → start, 让 hermes 启动新 session 时 snapshot
//     最新 SOUL.md(覆盖 system_prompt 冻结在 SQLite 的语义)。
//
// 单独把 input 重写抽成接口而不是写死, 是因为生产装配需要持有 DB / cipher /
// nodeID-aware uploader, 这些依赖只存在于 wiring 层; 测试装配可以传 nil 让 restart
// 仍能跑(适合不关心 input 刷新语义的容器生命周期测试)。
type AppRestartContainerHandler struct {
	store          AppRuntimeStore
	containers     ContainerLifecycle
	sessionCleaner SessionCleaner
	inputRefresher AppInputRefresher
	// notifier 在重启检测到镜像变更、入队 app_initialize job 后即时推送 jobID，
	// 让 worker 不必等 scheduler 轮询即可拾取；nil 时由 scheduler 兜底入队。
	notifier RestartJobNotifier
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

// SetInputRefresher 注入「restart 前重写 input/ 目录」的能力。
//
// 生产环境必须注入: 没有这一步, 改 model / 三层 prompt / persona 后 restart,
// oc-entrypoint 仍会读到旧 manifest.yaml, 容器内 hermes 模型 / SOUL.md
// 永远停留在初始化时的值。
// nil 时跳过 input 重写(保持原 restart 行为, 仅适合不关心 input 刷新的测试装配)。
func (h *AppRestartContainerHandler) SetInputRefresher(r AppInputRefresher) {
	h.inputRefresher = r
}

// SetJobNotifier 注入「向 Redis 队列即时推送 jobID」的能力。
//
// 仅在重启检测到镜像变更、入队 app_initialize job 后用于即时唤醒 worker；
// nil 时入队的 job 由 scheduler 周期轮询兜底拾取（与现有 SetSessionCleaner /
// SetInputRefresher 一致，为可选的 nil 安全注入）。
func (h *AppRestartContainerHandler) SetJobNotifier(n RestartJobNotifier) {
	h.notifier = n
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
	// 注意：「缺 container_id」的拒绝校验下移到原 stop/clear/start 路径之前。
	// 镜像变更重建分支被 worker 重试时 container_id 可能已被上一次尝试清空，
	// 此时仍须放行进入重建分支重新入队 app_initialize，不能在此提前报错。
	// RuntimeNodeID nullable（spec-A2a）：.String 取 Go string 值。
	nodeID := app.RuntimeNodeID.String
	// 在 stop 之前先把节点上的 apps/<id>/input/ 重写成 DB 当前快照:
	// oc-entrypoint 在下次容器启动时读取该目录,渲染出新的 config.yaml(含 model)
	// 与 SOUL.md(三层 prompt + persona)。改 model / 改 prompt / 改 persona
	// 之所以需要 restart 生效,就是因为镜像内部只在启动期渲染一次,所以这里
	// 必须先把输入数据更新到节点,再进入容器停 → 启循环。
	//
	// 失败时直接冒泡让 worker 重试: 没刷新就 restart 等于"重启后还是老配置",
	// 比让容器先 stop 再失败更糟(用户感知不到, version_synced 还会被错误置位)。
	var refreshResult AppInputRefreshResult
	if h.inputRefresher != nil {
		refreshResult, err = h.inputRefresher.RefreshAppInput(ctx, nodeID, app)
		if err != nil {
			return fmt.Errorf("刷新应用 input 失败: %w", err)
		}
	}
	// 镜像变更重建分支：容器镜像在创建时即固定，restart 只是 stop → start 同一个
	// 容器，绑定版本换了运行时镜像后 restart 永远拉不到新镜像。检测到 refresher
	// 解析出的镜像 ref 与 apps.runtime_image_ref(容器当前镜像)不一致时，必须重建
	// 容器：stop + remove 旧容器 → 清空 container_id → 置 status=pulling_runtime_image
	// → 入队 app_initialize job，复用已测的初始化 4 阶段(pull → prepare → create →
	// start → binding_waiting)重拉新镜像并重建容器，避免 restart 对镜像维度谎报
	// version_synced。
	//
	// 委托给 re-initialize 而非在此重新装配 ContainerSpec / 拉镜像逻辑，是因为这些
	// 逻辑已由 AppInitializeHandler 完整实现并测试覆盖，复制一份只会引入重复依赖。
	//
	// 幂等：本分支若被 worker 重试(job MaxAttempts=3)，重入时 container_id 可能已被
	// 上一次尝试清空——此时跳过 stop/remove，仅重新建 job 并入队即可。
	if h.inputRefresher != nil && refreshResult.ImageRef != "" && refreshResult.ImageRef != app.RuntimeImageRef {
		if app.ContainerID.String != "" {
			if err := h.containers.StopContainer(ctx, nodeID, app.ContainerID.String); err != nil {
				return fmt.Errorf("镜像变更重建前停止旧容器失败: %w", err)
			}
			if err := h.containers.RemoveContainer(ctx, nodeID, app.ContainerID.String); err != nil {
				return fmt.Errorf("镜像变更重建前删除旧容器失败: %w", err)
			}
		}
		// 清空 container_id / container_name，让 app_initialize 重新创建容器。
		if err := h.store.SetAppContainer(ctx, sqlc.SetAppContainerParams{ID: app.ID}); err != nil {
			return fmt.Errorf("清空容器引用失败: %w", err)
		}
		// raw SetAppStatus：restart 一贯不走 EnsureAppTransition，与原逻辑一致。
		if err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusPullingRuntimeImage}); err != nil {
			return fmt.Errorf("更新应用状态失败: %w", err)
		}
		// payload 与 RuntimeOperationService.RequestInitialize 入队的 app_initialize
		// job 同形态；init handler 的 decodePayload 只读 app_id + runtime_node。
		initPayload, err := json.Marshal(map[string]any{
			"app_id":       app.ID,
			"runtime_node": nodeID,
		})
		if err != nil {
			return fmt.Errorf("构造 app_initialize payload 失败: %w", err)
		}
		newJobID := uuid.NewString()
		if err := h.store.CreateJob(ctx, sqlc.CreateJobParams{
			ID:          newJobID,
			Type:        domain.JobTypeAppInitialize,
			Priority:    100,
			RunAfter:    time.Now(),
			MaxAttempts: 3,
			PayloadJson: initPayload,
		}); err != nil {
			return fmt.Errorf("入队 app_initialize job 失败: %w", err)
		}
		// 入队失败不阻塞：scheduler 周期轮询会兜底拾取该 job。
		if h.notifier != nil {
			_ = h.notifier.Enqueue(ctx, newJobID)
		}
		// 直接返回：不再走后续 stop/clear/start，也不调 SetAppStatus(running) /
		// SetAppAppliedVersion——这些由 app_initialize handler
		// 在到达 binding_waiting 时负责，避免对镜像维度谎报 synced。
		return nil
	}
	// 走原 stop/clear/start 重启路径前必须有容器：镜像未变时 restart 操作的就是
	// 当前容器，缺 container_id 说明实例尚未初始化，无法重启。
	// （镜像变更重建分支已在上方处理并 return，不受此校验影响。）
	if app.ContainerID.String == "" {
		return fmt.Errorf("应用 %s 尚未创建容器，无法重启", payload.AppID)
	}
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
	if err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusRunning}); err != nil {
		return fmt.Errorf("更新应用状态失败: %w", err)
	}
	// 重启刷新已把节点 input 重写成当前版本快照，记录已应用版本修订与镜像 ref，
	// 供前端 version_synced 检测；inputRefresher 为 nil（测试装配）时跳过。
	if h.inputRefresher != nil {
		if err := h.store.SetAppAppliedVersion(ctx, sqlc.SetAppAppliedVersionParams{
			ID:                     app.ID,
			AppliedVersionRevision: refreshResult.VersionRevision,
			AppliedImageRef:        refreshResult.ImageRef,
		}); err != nil {
			return fmt.Errorf("记录已应用版本信息失败: %w", err)
		}
	}
	return nil
}

// AppDeleteHandler 串起删除流程：
//  1. 停止并删除容器（缺 container_id 时跳过）；
//  2. 禁用 new-api token（已有 newapi_key_id 时执行）；
//  3. agent 上把应用工作目录清理（fileOps != nil 时执行；后续 task 升级为归档）；
//  4. 清理实例私有 RAGFlow dataset（knowledge != nil 时执行）；
//  5. 软删 apps 行。
//
// 任意一步失败立即冒泡，由 worker 重试；重试时各步骤需自行幂等。
type AppDeleteHandler struct {
	store      AppRuntimeStore
	containers ContainerLifecycle
	factory    NewAPIClientFactory
	fileOps    AppDeleteFileOps
	knowledge  AppKnowledgeCleaner
}

// NewAppDeleteHandler 创建删除应用 handler，允许 new-api 与文件操作依赖按环境为空。
func NewAppDeleteHandler(store AppRuntimeStore, containers ContainerLifecycle, factory NewAPIClientFactory, fileOps AppDeleteFileOps, cleaners ...AppKnowledgeCleaner) *AppDeleteHandler {
	var knowledge AppKnowledgeCleaner
	if len(cleaners) > 0 {
		knowledge = cleaners[0]
	}
	return &AppDeleteHandler{store: store, containers: containers, factory: factory, fileOps: fileOps, knowledge: knowledge}
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
	app, err := h.store.GetApp(ctx, payload.AppID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil // 已经删除则直接成功
		}
		return fmt.Errorf("查询应用失败: %w", err)
	}
	alreadyDeleted := app.DeletedAt.Valid

	// RuntimeNodeID nullable（spec-A2a）：.String 取 Go string 值。
	nodeID := app.RuntimeNodeID.String
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

	if h.knowledge != nil {
		if err := h.knowledge.DeleteAppDataset(ctx, app.ID); err != nil {
			// RAGFlow dataset 是外部派生资源，删除失败不能阻断本地应用下线；
			// 后续可通过 ragflow_datasets 状态和运维脚本补偿清理。
			slog.WarnContext(ctx, "清理应用 RAGFlow dataset 失败", "app_id", payload.AppID, "error", err)
		}
	}

	if alreadyDeleted {
		// 删除成员会先软删应用再入队 app_delete；此处仍要清理容器、key、目录和 RAGFlow dataset，
		// 但不再重复执行 SoftDeleteApp，避免把已删除行当作错误。
		return nil
	}
	if err := h.store.SoftDeleteApp(ctx, app.ID); err != nil {
		return fmt.Errorf("软删应用失败: %w", err)
	}
	return nil
}

// nullStringFrom 把字符串转换为 null.String，空串时 Valid=false。
// 供 channel_login、worker 等包内小工具使用。
func nullStringFrom(s string) null.String {
	return null.NewString(s, s != "")
}
