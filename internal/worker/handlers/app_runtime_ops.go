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
	"oc-manager/internal/integrations/k8sorch"
	"oc-manager/internal/integrations/storage"
	mlog "oc-manager/internal/log"
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
	// CreateJob 在重启检测到镜像变更后入队 app_initialize job，
	// 复用初始化阶段重建 k8s 资源并拉取新镜像。
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) error
	// SetAppRuntimePhase 写运行时就绪维度;镜像不变重启 Scale(0) 前置 restarting,
	// 标记 pod 重建窗口、关闭渠道发起闸门;reconciler 在 pod 重新 Ready 后写回 ready。
	SetAppRuntimePhase(ctx context.Context, arg sqlc.SetAppRuntimePhaseParams) error
}

// appOrchestrator 是 k8s 生命周期 handler 消费的窄接口，仅包含运行时操作所需方法。
// 便于单测注入 fake，不引入整个 k8sorch.Orchestrator 实现。
type appOrchestrator interface {
	// Scale 伸缩 pod replicas（0=停止，1=启动）。
	Scale(ctx context.Context, appID string, replicas int32) error
	// UpdateImage patch Deployment 主容器镜像，触发 Recreate 重启（镜像变更时使用）。
	UpdateImage(ctx context.Context, appID, hermesImage string) error
	// Delete 删除 Deployment + Service + Secret（幂等，NotFound 视为成功）。
	Delete(ctx context.Context, appID string) error
	// Status 读取 app 的 pod 状态归一视图，用于判定 Deployment 是否已建立
	// （Phase=="NotFound" 严格表示 Deployment 不存在；replicas=0 的已停止态返回 "Pending"）。
	Status(ctx context.Context, appID string) (k8sorch.AppStatus, error)
	// RolloutRestart 触发 Deployment 滚动重启（渠道绑定后重载 hermes platform）。
	RolloutRestart(ctx context.Context, appID string) error
}

// AppDelete 用 NewAPIClientFactory 拿 user-scoped client 调 SetAPIKeyStatus 禁用 token，
// status=2 表示禁用。原来的 APIKeyDisabler 单方法接口已下线（admin token 拿不到别 user 的
// token 完整 key，token 操作必须以业务 user 身份调）。

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

// AppStartContainerHandler 通过 k8s Scale(1) 拉起 pod 并把状态推到 running。
//
// k8s 语义：Scale(1) 是幂等操作，已经 Running 的 Deployment 设 replicas=1 不会重建 pod。
type AppStartContainerHandler struct {
	store AppRuntimeStore
	orch  appOrchestrator
}

// NewAppStartContainerHandler 构造 handler，注入 k8s 编排器。
func NewAppStartContainerHandler(store AppRuntimeStore, orch appOrchestrator) *AppStartContainerHandler {
	return &AppStartContainerHandler{store: store, orch: orch}
}

// Handle 执行 app_start_container job：Scale(1) 后推 running 状态。
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
	// 编排器未配置时（k8s.enabled 未启用或 misconfiguration），无法执行核心操作，
	// 立即返回可诊断错误，让 job 失败重试并暴露配置问题，避免 nil-panic 崩 worker。
	if h.orch == nil {
		return fmt.Errorf("编排器未配置（k8s.enabled 未启用？），无法启动应用 %s", payload.AppID)
	}
	// k8s 创建流程（app_initialize）不写 apps.container_id，ContainerID 字段恒为空/null，
	// 不能用于判定 Deployment 是否已建立。改为经 orch.Status 的 Phase 精确判定：
	// Phase=="NotFound" 严格表示 Deployment 尚不存在（真未初始化或已删除）；
	// Deployment 存在但 replicas=0（已停止态）返回 Phase=="Pending"，允许继续 Scale(1)。
	st, serr := h.orch.Status(ctx, payload.AppID)
	if serr != nil {
		return fmt.Errorf("查询应用状态失败: %w", serr)
	}
	if st.Phase == "NotFound" {
		return fmt.Errorf("应用 %s 尚未完成初始化，无法启动（k8s Deployment 尚未建立）", payload.AppID)
	}
	// 启动前置 runtime_phase=restarting：Scale(1) 后 pod 需要时间 Ready，期间 oc-ops 不可用。
	// 若此前 app 已停止过、runtime_phase 残留 ready（reconciler 不跟踪 stopped 态），则
	// SetAppStatus(running) 后渠道发起闸门误开（running+ready）。先置 restarting 关闭闸门，
	// reconciler 在 pod 真正 Ready 后写回 ready（双轴模型，业务态 status 不在此更改）。
	// 置位失败只记日志、不阻断启动主流程。
	if err := h.store.SetAppRuntimePhase(ctx, sqlc.SetAppRuntimePhaseParams{ID: app.ID, RuntimePhase: domain.RuntimePhaseRestarting}); err != nil {
		slog.ErrorContext(ctx, "启动置 runtime_phase=restarting 失败", "app_id", app.ID, mlog.Err(err))
	}

	// Scale(1) 等价于"起 pod"，幂等：Deployment 已有 replicas=1 时 k8s 不重建 pod。
	if err := h.orch.Scale(ctx, payload.AppID, 1); err != nil {
		return fmt.Errorf("启动应用失败（Scale replicas=1）: %w", err)
	}
	if err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusRunning}); err != nil {
		return fmt.Errorf("更新应用状态失败: %w", err)
	}
	return nil
}

// AppStopContainerHandler 通过 k8s Scale(0) 停止 pod 并把状态推到 stopped。
//
// k8s 语义：Scale(0) 删除所有 pod 但保留 Deployment，preStop hook 触发 sidecar oc-presync
// 同步数据到 S3，无需 manager 额外操作。
type AppStopContainerHandler struct {
	store AppRuntimeStore
	orch  appOrchestrator
}

// NewAppStopContainerHandler 创建停止 handler，依赖由 worker 装配层注入。
func NewAppStopContainerHandler(store AppRuntimeStore, orch appOrchestrator) *AppStopContainerHandler {
	return &AppStopContainerHandler{store: store, orch: orch}
}

// Handle 执行 app_stop_container job，并把无 ContainerID 场景收敛为 stopped。
// preStop hook 由 k8s 在 pod 终止前触发，sidecar oc-presync 负责同步数据，manager 不介入。
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
	// 编排器未配置时（k8s.enabled 未启用或 misconfiguration），无法执行核心操作，
	// 立即返回可诊断错误，让 job 失败重试并暴露配置问题，避免 nil-panic 崩 worker。
	if h.orch == nil {
		return fmt.Errorf("编排器未配置（k8s.enabled 未启用？），无法停止应用 %s", payload.AppID)
	}
	// k8s 创建流程（app_initialize）不写 apps.container_id，ContainerID 字段恒为空/null，
	// 旧以 ContainerID 为空判定「Deployment 未建立」会导致：ContainerID 恒空 → 永远跳过
	// Scale(0) → pod 仍在跑但 DB 谎报 stopped（状态机与实态脱钩）。
	// 改为经 orch.Status 的 Phase 精确判定：Phase=="NotFound" 才表示 Deployment 真不存在，
	// 此时等价于已停止，直接收敛状态机；其余 Phase（含 replicas=0 的 Pending）仍调 Scale(0)。
	st, serr := h.orch.Status(ctx, payload.AppID)
	if serr != nil {
		return fmt.Errorf("查询应用状态失败: %w", serr)
	}
	if st.Phase == "NotFound" {
		// Deployment 尚未建立 / 已删除，等价于已停止，直接收敛状态机（无 Deployment 可 Scale）。
		if err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusStopped}); err != nil {
			return fmt.Errorf("更新应用状态失败: %w", err)
		}
		return nil
	}
	// Scale(0) = 停止所有 pod；Deployment 保留，下次 Scale(1) 可以恢复。
	if err := h.orch.Scale(ctx, payload.AppID, 0); err != nil {
		return fmt.Errorf("停止应用失败（Scale replicas=0）: %w", err)
	}
	if err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusStopped}); err != nil {
		return fmt.Errorf("更新应用状态失败: %w", err)
	}
	return nil
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

// AppInputRefresher 抽象「restart 前刷新版本配置」的能力（bootstrap 接管 pod 启动配置后
// 实现层逻辑简化，但接口保留供旧 wiring 兼容）。
// 成功后返回 AppInputRefreshResult，包含版本修订与镜像 ref，供 restart handler
// 写入 apps.applied_version_revision / applied_image_ref。
type AppInputRefresher interface {
	RefreshAppInput(ctx context.Context, nodeID string, app sqlc.App) (AppInputRefreshResult, error)
}

// RestartJobNotifier 抽象向 Redis 队列即时推送 jobID 的能力。
// 与 service.JobNotifier 同形态；nil 时由 scheduler 兜底入队。
type RestartJobNotifier interface {
	Enqueue(ctx context.Context, jobID string) error
}

// RestartSeedStore 是重启 handler 在镜像不变分支执行版本 skill 种子注入所需的最小接口。
// 在 AppSkillSeedStore 基础上加入 GetAssistantVersion，以便从 app.VersionID 加载版本快照。
// 由 dbStore.Queries 实现；nil 时跳过种子注入（兼容测试装配与无 skill 库部署）。
type RestartSeedStore interface {
	AppSkillSeedStore
	// GetAssistantVersion 按 ID 加载助手版本，用于获取 skills_json 快照。
	GetAssistantVersion(ctx context.Context, id string) (sqlc.AssistantVersion, error)
}

// AppRestartContainerHandler 触发应用重启，根据是否有镜像变更走不同分支：
//
//   - 镜像变更：UpdateImage 触发 Recreate（k8s 自动停旧 pod 起新 pod），
//     由 app_initialize 路径写回 applied 版本。
//   - 镜像不变：删除 S3 sessions 与 state.db（清会话数据让 hermes 启动新 session），
//     然后 Scale(0) → Scale(1) 重建 pod，最后调 seedVersionSkills 补齐新增 skill，
//     最后记录 applied 版本。
//
// k8s 语义说明：
//   - preStop hook 由 k8s 控制，oc-presync 在 pod 终止前同步数据到 S3，无需 manager 介入。
//   - sessions 清除操作在 Scale(0) 之前完成（S3 侧操作，与 pod 状态无关，不存在文件锁问题）。
type AppRestartContainerHandler struct {
	store          AppRuntimeStore
	orch           appOrchestrator
	objects        storage.ObjectStore
	inputRefresher AppInputRefresher
	// notifier 在重启检测到镜像变更、入队 app_initialize job 后即时推送 jobID，
	// 让 worker 不必等 scheduler 轮询即可拾取；nil 时由 scheduler 兜底入队。
	notifier RestartJobNotifier
	// seedStore 用于镜像不变重启分支的版本 skill 种子注入；nil 时跳过（兼容测试装配）。
	seedStore RestartSeedStore
}

// NewAppRestartContainerHandler 创建重启 handler，注入 k8s 编排器与对象存储。
// objects 用于镜像不变时清理 S3 sessions + state.db；nil 时跳过（仅用于不关心会话清除的测试）。
func NewAppRestartContainerHandler(store AppRuntimeStore, orch appOrchestrator, objects storage.ObjectStore) *AppRestartContainerHandler {
	return &AppRestartContainerHandler{store: store, orch: orch, objects: objects}
}

// SetInputRefresher 注入「restart 前刷新版本配置」能力。
// nil 时跳过刷新（测试装配 / 旧 wiring 兼容）。
func (h *AppRestartContainerHandler) SetInputRefresher(r AppInputRefresher) {
	h.inputRefresher = r
}

// SetJobNotifier 注入「向 Redis 队列即时推送 jobID」的能力。
// 仅在重启检测到镜像变更、入队 app_initialize job 后用于即时唤醒 worker；
// nil 时由 scheduler 周期轮询兜底拾取。
func (h *AppRestartContainerHandler) SetJobNotifier(n RestartJobNotifier) {
	h.notifier = n
}

// SetRestartSeedStore 注入镜像不变重启分支的版本 skill 种子注入 store。
// 生产环境由 cmd/server 装配时注入 dbStore.Queries（满足 RestartSeedStore 接口）；
// nil 时跳过种子注入（兼容测试装配及无 skill 库的早期部署）。
func (h *AppRestartContainerHandler) SetRestartSeedStore(s RestartSeedStore) {
	h.seedStore = s
}

// Handle 执行 app_restart_container job。
// 镜像变更时走 UpdateImage 重建路径；镜像不变时清 S3 会话后 Scale(0)→Scale(1) 重启。
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

	// 可选：调 refresher 刷新版本配置并获取当前版本镜像 ref。
	// 失败时立即冒泡让 worker 重试：没刷新就 restart 等于"重启后还是老配置"，
	// 比让 pod 先停再失败更糟（用户感知不到，version_synced 还会被错误置位）。
	// k8s 路径无节点概念，nodeID 传空串（RefreshAppInput 接口签名兼容旧 docker 链路）。
	var refreshResult AppInputRefreshResult
	if h.inputRefresher != nil {
		refreshResult, err = h.inputRefresher.RefreshAppInput(ctx, "", app)
		if err != nil {
			return fmt.Errorf("刷新应用版本配置失败: %w", err)
		}
	}

	// 编排器未配置时（k8s.enabled 未启用或 misconfiguration），无法执行核心操作，
	// 立即返回可诊断错误，让 job 失败重试并暴露配置问题，避免 nil-panic 崩 worker。
	if h.orch == nil {
		return fmt.Errorf("编排器未配置（k8s.enabled 未启用？），无法重启应用 %s", payload.AppID)
	}

	// 镜像变更重建分支：refresher 解析出的镜像 ref 与当前 apps.runtime_image_ref 不一致时，
	// 调 UpdateImage 触发 Deployment Recreate——k8s 自动停旧 pod 起新 pod，拉取新镜像。
	// 委托给 app_initialize 路径写回 applied 版本（避免复制初始化逻辑）。
	if h.inputRefresher != nil && refreshResult.ImageRef != "" && refreshResult.ImageRef != app.RuntimeImageRef {
		// UpdateImage patch Deployment 镜像，触发 Recreate 策略：k8s 自动停旧 pod 起新 pod。
		// 幂等：若上一次 UpdateImage 成功但 handler 在 SetAppStatus 前崩溃，重入时再次
		// UpdateImage 是幂等的（已是新镜像，Deployment 不会重建）。
		if err := h.orch.UpdateImage(ctx, payload.AppID, refreshResult.ImageRef); err != nil {
			return fmt.Errorf("更新应用镜像失败（UpdateImage）: %w", err)
		}
		// 置 status=pulling_runtime_image，交由 app_initialize / status reconciler 跟踪。
		if err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusPullingRuntimeImage}); err != nil {
			return fmt.Errorf("更新应用状态失败: %w", err)
		}
		// 入队 app_initialize job，复用已测的初始化阶段（WaitReady → binding_waiting）
		// 让 init handler 在 pod Ready 后写回 applied 版本，避免镜像维度谎报 version_synced。
		// k8s 路径无节点概念，payload 只含 app_id。
		initPayload, err := json.Marshal(map[string]any{
			"app_id": app.ID,
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
		// 即时唤醒 worker 拾取 job；nil notifier 时由 scheduler 轮询兜底。
		if h.notifier != nil {
			_ = h.notifier.Enqueue(ctx, newJobID)
		}
		// 直接返回：不走后续 Scale(0→1)，也不调 SetAppAppliedVersion。
		return nil
	}

	// 镜像不变路径：清 S3 会话数据后 Scale(0) → Scale(1) 重建 pod。
	// S3 侧操作在 Scale(0) 之前完成，无文件锁问题（与 docker 路径需等容器 stop 不同）。
	// sessions 目录：apps/<appID>/sessions/（hermes 会话归档）
	// state.db：apps/<appID>/state.db（hermes SQLite 快照）
	// 清除后新 pod 启动时 hermes 会重新初始化 session，snapshot 最新 SOUL.md（含
	// 改后的 model / persona / 知识库），确保配置变更进入对话。
	if h.objects != nil {
		sessionsPrefix := storage.AppPrefix(payload.AppID) + "sessions/"
		if err := h.objects.DeletePrefix(ctx, sessionsPrefix); err != nil {
			return fmt.Errorf("清除 S3 sessions 失败: %w", err)
		}
		// 用 DeletePrefix 清 state.db 前缀，连带清理可能残留的 -wal/-shm 衍生对象，
		// 保证下次干净重开（hermes 启动时会重建 state.db，不会读到旧快照）。
		stateDBKey := storage.StateDBKey(payload.AppID)
		if err := h.objects.DeletePrefix(ctx, stateDBKey); err != nil {
			return fmt.Errorf("清除 S3 state.db 失败: %w", err)
		}
	}

	// 镜像不变重启即将 Scale(0)→Scale(1) 重建 pod，期间 oc-ops 不可用。先置 runtime_phase=restarting
	// 关闭渠道发起闸门(业务态 status 保持不动,双轴模型),避免重启窗口内发起打到 down 的 pod 拿 502;
	// reconciler 在新 pod Ready 后写回 ready。置位失败只记日志、不阻断重启主流程。
	if err := h.store.SetAppRuntimePhase(ctx, sqlc.SetAppRuntimePhaseParams{ID: app.ID, RuntimePhase: domain.RuntimePhaseRestarting}); err != nil {
		slog.ErrorContext(ctx, "重启置 runtime_phase=restarting 失败", "app_id", app.ID, mlog.Err(err))
	}

	// Scale(0) 停止 pod（k8s preStop 触发 oc-presync 同步 workspace）。
	if err := h.orch.Scale(ctx, payload.AppID, 0); err != nil {
		return fmt.Errorf("重启前停止应用失败（Scale replicas=0）: %w", err)
	}
	// Scale(1) 重新起 pod，hermes 启动时从 bootstrap 获取配置（含最新版本数据）。
	if err := h.orch.Scale(ctx, payload.AppID, 1); err != nil {
		return fmt.Errorf("重启应用失败（Scale replicas=1）: %w", err)
	}
	if err := h.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{ID: app.ID, Status: domain.AppStatusRunning}); err != nil {
		return fmt.Errorf("更新应用状态失败: %w", err)
	}

	// 镜像不变重启：Scale(1) 成功后补齐版本新增 skill。
	// 版本可能在上次启动后新增了 skill，重启时种子注入确保 app_skills 与版本保持同步（并集）。
	// 最大努力：加载版本或注入失败只 warn，不阻断重启主流程。
	if h.seedStore != nil && app.VersionID.Valid {
		if ver, err := h.seedStore.GetAssistantVersion(ctx, app.VersionID.String); err != nil {
			slog.WarnContext(ctx, "镜像不变重启：加载助手版本失败，跳过 skill 种子注入", "app", app.ID, mlog.Err(err))
		} else if err := seedVersionSkills(ctx, h.seedStore, app.ID, ver); err != nil {
			slog.WarnContext(ctx, "镜像不变重启：版本 skill 种子注入失败", "app", app.ID, "version", ver.ID, mlog.Err(err))
		}
	}

	// 记录已应用版本修订与镜像 ref，供前端 version_synced 检测；
	// inputRefresher 为 nil（测试装配）时跳过，避免写入零值误置位。
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
//  1. 删除 k8s 资源（Deployment + Service + Secret），幂等；
//  2. 禁用 new-api token（已有 newapi_key_id 时执行）；
//  3. 归档 S3 应用目录（MovePrefix apps/<id>/ → apps/<id>/archive/）；
//  4. 清理实例私有 RAGFlow dataset（knowledge != nil 时执行，失败不阻断）；
//  5. 软删 apps 行。
//
// 任意步骤失败立即冒泡，由 worker 重试；重试时各步骤需自行幂等。
type AppDeleteHandler struct {
	store     AppRuntimeStore
	orch      appOrchestrator
	factory   NewAPIClientFactory
	objects   storage.ObjectStore
	knowledge AppKnowledgeCleaner
}

// NewAppDeleteHandler 创建删除应用 handler。
// objects 不为 nil 时在 k8s 资源删除后归档 S3 应用目录；nil 时跳过归档（无 S3 时兼容）。
// cleaners 为可选的 KB 清理器，最多取第一个。
func NewAppDeleteHandler(store AppRuntimeStore, orch appOrchestrator, factory NewAPIClientFactory, objects storage.ObjectStore, cleaners ...AppKnowledgeCleaner) *AppDeleteHandler {
	var knowledge AppKnowledgeCleaner
	if len(cleaners) > 0 {
		knowledge = cleaners[0]
	}
	return &AppDeleteHandler{store: store, orch: orch, factory: factory, objects: objects, knowledge: knowledge}
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

	// Step 1: 删除 k8s Deployment + Service + Secret（幂等，NotFound 视为成功）。
	// 不判断 ContainerID：k8s 路径按 appID 寻址资源，无论是否完成 init 都可安全调用 Delete。
	if h.orch != nil {
		if err := h.orch.Delete(ctx, payload.AppID); err != nil {
			return fmt.Errorf("删除 k8s 资源失败: %w", err)
		}
	}

	// Step 2: 禁用 new-api token（status=2 表示禁用）。
	if h.factory != nil && app.NewapiKeyID.Valid && app.NewapiKeyID.String != "" {
		keyID, parseErr := strconv.ParseInt(app.NewapiKeyID.String, 10, 64)
		if parseErr == nil {
			client, err := h.factory.UserScopedFor(ctx, app)
			if err != nil {
				return fmt.Errorf("构造 user-scoped client 失败: %w", err)
			}
			if err := client.SetAPIKeyStatus(ctx, keyID, 2); err != nil {
				return fmt.Errorf("禁用 new-api token 失败: %w", err)
			}
		}
	}

	// Step 3: 把 S3 应用目录整体归档（apps/<id>/ → apps/<id>/archive/）。
	// MovePrefix 幂等：若归档后 src 已空，再次调用只是空操作。
	if h.objects != nil {
		src := storage.AppPrefix(payload.AppID)
		dst := storage.AppArchivePrefix(payload.AppID)
		if err := h.objects.MovePrefix(ctx, src, dst); err != nil {
			return fmt.Errorf("归档 S3 应用目录失败: %w", err)
		}
	}

	// Step 4: 清理实例私有 RAGFlow dataset（外部派生资源，失败不阻断本地应用下线）。
	if h.knowledge != nil {
		if err := h.knowledge.DeleteAppDataset(ctx, app.ID); err != nil {
			// RAGFlow dataset 是外部派生资源，删除失败不能阻断本地应用下线；
			// 后续可通过 ragflow_datasets 状态和运维脚本补偿清理。
			slog.WarnContext(ctx, "清理应用 RAGFlow dataset 失败", "app_id", payload.AppID, mlog.Err(err))
		}
	}

	if alreadyDeleted {
		// 删除成员会先软删应用再入队 app_delete；此处仍要清理 k8s、key、S3 和 RAGFlow dataset，
		// 但不再重复执行 SoftDeleteApp，避免把已删除行当作错误。
		return nil
	}
	// Step 5: 软删 apps 行。
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
