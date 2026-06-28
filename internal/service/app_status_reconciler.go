// Package service 的 app_status_reconciler 实现 pod 状态周期同步逻辑。
// 职责：对期望运行（running / binding_waiting）的 app 调 Orchestrator.Status 读 pod 状态，
// 写入 runtime_snapshot_json（观测用），并在【运行中的 app 其 pod 已崩溃/消失】时把
// status 推到 error。
//
// 设计约束（状态机单向守卫）：
//   - reconciler 只做 running → error 这一条迁移，严格读取当前 status 后再决定是否写入。
//   - binding_waiting → running 由渠道绑定流程负责，不在此处 promote。
//   - error → running 由用户/re-init 流程负责，不在此处恢复。
//   - pod 崩溃重启由 Deployment 控制器自管，manager 不主动重建/拉起（不自愈）。
package service

import (
	"context"
	"encoding/json"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/k8sorch"
	"oc-manager/internal/store/sqlc"
	"oc-manager/internal/worker/jobutil"
)

// appStatusStore 是 AppStatusReconciler 所需最小 DB 能力。
// 接口最小化原则：只声明本 reconciler 实际调用的方法，避免与其他 store 接口耦合。
type appStatusStore interface {
	// ListRunningApps 返回 status IN ('running','binding_waiting') 的 app id 列表。
	// spec-A2b：不再含 runtime_node_id / container_id，消费方仅用 id。
	// 注意：返回列表不含 status 字段，需通过 GetApp 读取当前状态做守卫。
	ListRunningApps(ctx context.Context) ([]string, error)
	// GetApp 按 ID 读取 app 完整记录，用于在写状态前确认当前 status。
	GetApp(ctx context.Context, id string) (sqlc.App, error)
	// SetAppStatus 裸 UPDATE status 字段，无状态机守卫；守卫由调用方在 Go 层负责。
	SetAppStatus(ctx context.Context, arg sqlc.SetAppStatusParams) error
	// SetAppRuntimeSnapshot 更新 runtime_snapshot_json 与 runtime_snapshot_at。
	SetAppRuntimeSnapshot(ctx context.Context, arg sqlc.SetAppRuntimeSnapshotParams) error
	// SetAppRuntimePhase 裸 UPDATE runtime_phase(运行时就绪维度,与 status 正交)。
	SetAppRuntimePhase(ctx context.Context, arg sqlc.SetAppRuntimePhaseParams) error
	// ListErrorApps 返回 status=error 的 app id，供兜底恢复「pod 已 Ready 但卡 error」。
	ListErrorApps(ctx context.Context) ([]string, error)
	// ListRestartingApps 返回 status=restarting 的 app id，供收敛「解绑触发重启」过渡态。
	ListRestartingApps(ctx context.Context) ([]string, error)
	// 以下三个方法供 jobutil.EnsureInitJob 重新入队 app_initialize job（兜底恢复用）。
	GetLatestAppInitJob(ctx context.Context, appID json.RawMessage) (sqlc.Job, error)
	RequeueJob(ctx context.Context, id string) error
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) error
}

// jobEnqueuer 通知 scheduler 立即拾取被重新入队的 job（与 internal/redis ZSET queue 一致）；
// 通知失败仅记日志，scheduler 兜底扫表会拾起。
type jobEnqueuer interface {
	Enqueue(ctx context.Context, jobID string) error
}

// AppStatusReconciler 周期读 app 的 pod 状态并双向收敛 DB：
//   - 运行中(running) 且 pod 已崩溃/消失（IsTerminalBad）→ 推 error。
//   - 卡在 error 但 pod 实际已 Ready（如 WaitReady 曾误超时但 pod 后来起来了，或 manager
//     重启等导致状态没收敛）→ 重新入队 init job 让 worker 推进回 running。这是兜底，补上
//     「init 失败成 error 后无法自愈」的洞；守卫为仅当 pod 真 Ready 才恢复，pod 真坏保持 error。
//
// binding_waiting→running 仍由渠道绑定流程负责，不在此 promote。
type AppStatusReconciler struct {
	store    appStatusStore
	orch     k8sorch.Orchestrator
	notifier jobEnqueuer
}

// NewAppStatusReconciler 创建 app 状态 poll reconciler。
func NewAppStatusReconciler(store appStatusStore, orch k8sorch.Orchestrator, notifier jobEnqueuer) *AppStatusReconciler {
	return &AppStatusReconciler{store: store, orch: orch, notifier: notifier}
}

// Tick 处理一轮 pod 状态同步。
//
// 整轮失败（ListRunningApps 出错）立即返回错误；单个 app 的 orch.Status 或 DB
// 写入失败采用「尽力而为」策略：continue 跳过该 app，不阻塞同轮其他 app 处理，
// 最终返回 nil（等待下一轮重试）。
func (r *AppStatusReconciler) Tick(ctx context.Context) error {
	// 整轮失败时直接返回，让调度器记录错误。
	// spec-A2b：ListRunningApps 返回 []string（app id），不再含节点/容器信息。
	ids, err := r.store.ListRunningApps(ctx)
	if err != nil {
		return err
	}

	for _, appID := range ids {
		// 单个 app 的 orch 调用失败不阻塞整轮，直接跳过；下一轮重试。
		st, serr := r.orch.Status(ctx, appID)
		if serr != nil {
			continue
		}

		// 写快照（观测用）：Raw 有内容才写，避免空快照覆盖有效历史记录。
		// 快照仅用于观测，写失败静默忽略，不阻塞下方 running→error 状态守卫
		// （避免 DB 抖动把已崩溃的 app 延迟一轮才被标 error）。
		if len(st.Raw) > 0 {
			_ = r.store.SetAppRuntimeSnapshot(ctx, sqlc.SetAppRuntimeSnapshotParams{
				RuntimeSnapshotJson: st.Raw,
				ID:                  appID,
			})
		}

		// 刷新运行时就绪维度(与 status 正交):pod 真就绪→ready,Recreate 空窗/未就绪→restarting,
		// 坏死→unknown。写失败静默忽略,下一轮重试(与快照同口径,不阻塞业务态守卫)。
		_ = r.store.SetAppRuntimePhase(ctx, sqlc.SetAppRuntimePhaseParams{
			RuntimePhase: runtimePhaseFor(st),
			ID:           appID,
		})

		// 状态守卫：只有 app 当前确实是 running 状态，且 pod 处于确定性坏态时才推 error。
		// 必须重新 GetApp 读取最新 status，避免依赖 ListRunningApps 返回的陈旧快照。
		if !podIsBad(st) {
			// pod 处于正常或瞬态状态，无需变更 DB status。
			continue
		}
		app, gerr := r.store.GetApp(ctx, appID)
		if gerr != nil {
			// 读取失败跳过，下一轮重试。
			continue
		}
		// 守卫：只允许 running → error 这一条迁移，binding_waiting 等其他状态不推 error。
		if app.Status != domain.AppStatusRunning {
			continue
		}
		// 忽略 SetAppStatus 写失败，下一轮重试。
		_ = r.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{
			Status: domain.AppStatusError,
			ID:     appID,
		})
	}

	// 兜底恢复：error 态但 pod 实际已 Ready 的 app，重新入队 init job 推进回 running。
	r.recoverReadyButError(ctx)
	// 收敛 restarting：解绑触发 RolloutRestart 后的过渡态，pod 重新 Ready → running。
	r.convergeRestarting(ctx)
	return nil
}

// convergeRestarting 收敛 status=restarting 的 app（渠道解绑触发 RolloutRestart 重建 pod）：
//   - pod 重新 Ready → 直接 SetAppStatus(running)。注意：与 error→running 兜底不同，restarting
//     只是重启没重新初始化，无需 re-enqueue init job，直接置 running 即可。
//   - pod 确定性坏死（IsTerminalBad：Failed/CrashLoop/重启≥阈值，或 Deployment 真消失的 NotFound）
//     → SetAppStatus(error)。
//   - 重启空窗（Recreate 过渡期无 pod，Status 返回 Pending；或 pod 起来了但还没 Ready）
//     → 不动，等下个 tick。Recreate 旧 pod 先停再起的空窗不会被误判为坏死：Deployment 仍在时
//     Status 返回 Pending 而非 NotFound（见 k8sorch.Status）。
//
// 整段尽力而为：ListRestartingApps / orch.Status / 写库任一步失败都 continue 或返回，等下一轮重试。
// 关键：先判 Ready 再判坏死——新 pod 启动期可能偶发重启（RestartCount 升高）但最终 Ready，
// 若先判 IsTerminalBad 会把已恢复的 pod 误推 error。
func (r *AppStatusReconciler) convergeRestarting(ctx context.Context) {
	ids, err := r.store.ListRestartingApps(ctx)
	if err != nil {
		return
	}
	for _, appID := range ids {
		st, serr := r.orch.Status(ctx, appID)
		if serr != nil {
			continue
		}
		switch {
		case st.Ready:
			// pod 重新 Ready：收敛回 running。
			r.convergeRestartingApp(ctx, appID, domain.AppStatusRunning)
		case podIsBad(st):
			// pod 确定性坏死：收敛到 error。
			r.convergeRestartingApp(ctx, appID, domain.AppStatusError)
		default:
			// 重启空窗（Pending / 未 Ready）：保持 restarting，等下个 tick。
		}
	}
}

// convergeRestartingApp 守卫式把单个 restarting app 收敛到目标状态 to（running 或 error）。
// 守卫：重新 GetApp 确认当前仍是 restarting（不依赖 ListRestartingApps 的陈旧快照），
// 再用 EnsureAppTransition 校验 restarting→to 合法后才写——与 running→error 守卫同口径。
func (r *AppStatusReconciler) convergeRestartingApp(ctx context.Context, appID, to string) {
	app, gerr := r.store.GetApp(ctx, appID)
	if gerr != nil {
		return
	}
	if app.Status != domain.AppStatusRestarting {
		return
	}
	if err := domain.EnsureAppTransition(app.Status, to); err != nil {
		return
	}
	_ = r.store.SetAppStatus(ctx, sqlc.SetAppStatusParams{Status: to, ID: appID})
}

// recoverReadyButError 扫 status=error 的 app，对其中 pod 实际已 Ready 的重新入队 init job，
// 让 worker 重跑初始化推进到 running。这补上「init 失败成 error 后无法自愈」的洞：
// WaitReady 曾误超时、manager 重启打断 worker 等都会留下 error 行，但 pod 后来其实起来了。
//
// 守卫：仅当 st.Ready==true（hermes 容器真就绪）才恢复——pod 真坏（不 Ready）保持 error 不动，
// 避免对真失败的 app 无限重试。整段尽力而为：任一步失败 continue，等下一轮再试。
func (r *AppStatusReconciler) recoverReadyButError(ctx context.Context) {
	ids, err := r.store.ListErrorApps(ctx)
	if err != nil {
		return
	}
	for _, appID := range ids {
		st, serr := r.orch.Status(ctx, appID)
		if serr != nil || !st.Ready {
			continue
		}
		// 复用与 reaper 同一份「确保 init job 回 pending」逻辑，再通知 scheduler 立即拾取。
		jobID, eerr := jobutil.EnsureInitJob(ctx, r.store, appID)
		if eerr != nil {
			continue
		}
		_ = r.notifier.Enqueue(ctx, jobID)
	}
}

// podIsBad 判断 pod 是否处于确定性坏态（reconciler 据此把 running→error），口径统一委托给
// k8sorch.IsTerminalBad —— 与 WaitReady 启动期「确定坏态快速失败」用同一份判定，避免两处
// 漂移（一处认为坏、另一处认为正常）。具体判定（NotFound/Failed/CrashLoopBackOff/反复重启）
// 与「拉镜像/调度等瞬态不算坏」的取舍见 IsTerminalBad 的注释。
func podIsBad(st k8sorch.AppStatus) bool {
	return k8sorch.IsTerminalBad(st)
}

// runtimePhaseFor 把 pod 观测态映射到 runtime_phase(运行时就绪维度)。仅对已过初始化、期望
// 在服务的 app(running/binding_waiting)调用——这类 app 之前已 Ready,故「未就绪且非坏死」
// 一律视为发生了重启/正在恢复(restarting),给出清晰的全局重启标识。starting 是首次冷启动语义,
// 由 init worker 在 phaseStart 写,reconciler 不产出。
//   - Ready                  → ready(可服务)
//   - 确定性坏死(IsTerminalBad)→ unknown(不可服务;业务态由 Tick 的 running→error 守卫另推 error)
//   - 其余(Pending 重建空窗 / Running 未 Ready) → restarting
func runtimePhaseFor(st k8sorch.AppStatus) string {
	switch {
	case st.Ready:
		return domain.RuntimePhaseReady
	case k8sorch.IsTerminalBad(st):
		return domain.RuntimePhaseUnknown
	default:
		return domain.RuntimePhaseRestarting
	}
}
