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
	"strings"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/k8sorch"
	"oc-manager/internal/store/sqlc"
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
}

// AppStatusReconciler 周期读「期望运行」app 的 pod 状态同步 DB。
//
// 设计约束：reconciler 是单向的——只把【运行中(running)】且 pod 已崩溃/消失的 app
// 推到 error；不做任何 promotion（binding_waiting→running 由渠道绑定流程负责，
// error→running 由 re-init 负责）。
// 所有写入都落在 app 状态机允许的 running→error 这一条迁移上，不越权改状态。
type AppStatusReconciler struct {
	store appStatusStore
	orch  k8sorch.Orchestrator
}

// NewAppStatusReconciler 创建 app 状态 poll reconciler。
func NewAppStatusReconciler(store appStatusStore, orch k8sorch.Orchestrator) *AppStatusReconciler {
	return &AppStatusReconciler{store: store, orch: orch}
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
	return nil
}

// podIsBad 判断 pod 是否处于确定性坏态（应推 error），而非瞬态（让 Deployment 自管）。
//
// 只认以下三种确定性坏态，原因：
//   - Phase=="NotFound"：Deployment/pod 被带外删除，manager 已感知不到该 pod，
//     继续保持 running 状态会让用户误认为服务正常，必须推 error。
//   - Phase=="Failed"：pod 已进入 Failed 相位，k8s 不会自动重启（OOMKilled restartPolicy=Never 等），
//     Deployment 会创建新 pod，但当前 pod 已确认失败，需告知业务层。
//   - Message 含 "CrashLoopBackOff"：容器反复崩溃，k8s 正在退避重启；
//     虽然 Deployment 会继续尝试，但这已是业务可见的持续异常态，需推 error。
//
// 以下情况不推 error（瞬态，Deployment 控制器自管）：
//   - Phase=="Pending"：pod 正在调度/拉镜像，属于正常启动流程中的瞬态。
//   - Phase=="Running" 但 Ready==false：容器已起但健康检查未通过，可能是刚重启的短暂抖动。
//   - Phase=="Unknown"：网络分区导致无法获取状态，保守不推 error。
//   - Ready==true：显然正常，无需任何操作。
//
// 已知取舍：ImagePullBackOff / ErrImagePull 等 Waiting reason 属于镜像拉取瞬态，
// 当前归为「Pending 类瞬态」由 Deployment 自管，本 reconciler 不推 error。
// 长期镜像拉取失败（如镜像不存在）理论上应推 error，但 spec 约定短期抖动与长期
// 失败难以在 reconciler 层区分，故暂不处理，留待后续迭代补充超时判定逻辑。
func podIsBad(st k8sorch.AppStatus) bool {
	if st.Phase == "NotFound" {
		return true
	}
	if st.Phase == "Failed" {
		return true
	}
	if strings.Contains(st.Message, "CrashLoopBackOff") {
		return true
	}
	return false
}
