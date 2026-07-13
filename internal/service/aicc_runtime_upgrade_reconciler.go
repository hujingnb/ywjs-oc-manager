package service

import (
	"context"
	"fmt"
	"strings"

	"oc-manager/internal/store/sqlc"
	"oc-manager/internal/worker/jobutil"
)

// aiccRuntimeUpgradeStore 是客服运行时升级协调器所需的最小存储边界。
// 初始化任务的读写接口与 jobutil.EnsureInitJob 对齐，避免重复实现任务重入规则。
type aiccRuntimeUpgradeStore interface {
	ListStaleAICCRuntimeApps(ctx context.Context, arg sqlc.ListStaleAICCRuntimeAppsParams) ([]string, error)
	jobutil.InitJobStore
}

// aiccRuntimeUpgradeNotifier 将已确认可执行的初始化任务即时交给 worker。
// 通知失败会返回错误，由下一轮周期任务重试；任务记录仍留在数据库供 scheduler 扫描兜底。
type aiccRuntimeUpgradeNotifier interface {
	Enqueue(ctx context.Context, jobID string) error
}

// AICCRuntimeUpgradeReconciler 逐个将 AICC 隐藏应用收敛到当前客服专用镜像。
// 每轮最多入队一个应用，防止更新镜像时所有接待运行时同时被重建。
type AICCRuntimeUpgradeReconciler struct {
	store        aiccRuntimeUpgradeStore
	notifier     aiccRuntimeUpgradeNotifier
	runtimeImage string
}

// NewAICCRuntimeUpgradeReconciler 创建客服运行时升级协调器。
func NewAICCRuntimeUpgradeReconciler(store aiccRuntimeUpgradeStore, notifier aiccRuntimeUpgradeNotifier, runtimeImage string) *AICCRuntimeUpgradeReconciler {
	return &AICCRuntimeUpgradeReconciler{store: store, notifier: notifier, runtimeImage: runtimeImage}
}

// Tick 找到一个镜像漂移的客服隐藏应用，并复用 app_initialize 完成重建与就绪等待。
func (r *AICCRuntimeUpgradeReconciler) Tick(ctx context.Context) error {
	if strings.TrimSpace(r.runtimeImage) == "" {
		return fmt.Errorf("aicc.runtime_image 不能为空")
	}
	if r.store == nil {
		return fmt.Errorf("AICC 运行时升级存储未配置")
	}
	if r.notifier == nil {
		return fmt.Errorf("AICC 运行时升级任务通知器未配置")
	}
	appIDs, err := r.store.ListStaleAICCRuntimeApps(ctx, sqlc.ListStaleAICCRuntimeAppsParams{
		TargetImageRef: r.runtimeImage,
		Limit:          1,
	})
	if err != nil {
		return fmt.Errorf("查询待升级 AICC 运行时失败: %w", err)
	}
	if len(appIDs) == 0 {
		return nil
	}
	jobID, err := jobutil.EnsureInitJob(ctx, r.store, appIDs[0])
	if err != nil {
		return fmt.Errorf("入队 AICC 运行时升级 app %s 失败: %w", appIDs[0], err)
	}
	if err := r.notifier.Enqueue(ctx, jobID); err != nil {
		return fmt.Errorf("通知 AICC 运行时升级 app %s 失败: %w", appIDs[0], err)
	}
	return nil
}
