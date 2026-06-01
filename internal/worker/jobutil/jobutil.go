// Package jobutil 提供 worker 任务相关的共享辅助，供 reaper / reconciler 等复用，
// 避免「重新入队 app_initialize job」这类逻辑在多处重复实现、各自漂移。
package jobutil

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// InitJobStore 是 EnsureInitJob 需要的最小数据访问能力；sqlc 生成的 *sqlc.Queries 直接满足。
type InitJobStore interface {
	// GetLatestAppInitJob 取最近一份 app_initialize job；不存在返回 sql.ErrNoRows。
	GetLatestAppInitJob(ctx context.Context, appID json.RawMessage) (sqlc.Job, error)
	// RequeueJob 把 running / succeeded 的 job 重置回 pending。
	RequeueJob(ctx context.Context, id string) error
	// CreateJob 没有历史 job 时新建一份。
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) error
}

// EnsureInitJob 确保指定 app 有一份可执行的 app_initialize job，并返回其 ID 供调用方 Enqueue：
//   - 历史无 job：新建一份（pending）。
//   - 已 running / succeeded：重置回 pending（带外接管，让 worker 重新跑初始化）。
//   - 已 pending：直接复用（仍返回 ID 让上层 Enqueue 一次，防 scheduler 漏触发）。
//
// reaper（回收 init 子状态孤儿）与 reconciler（兜底 error 但 pod 已 Ready）共用此逻辑，
// 使「重新入队初始化」的行为单点维护、不漂移。
func EnsureInitJob(ctx context.Context, store InitJobStore, appID string) (string, error) {
	// GetLatestAppInitJob 参数是 app ID 的 JSON 字符串字面量（带双引号），MySQL 端做 JSON 比较。
	appIDJSON, _ := json.Marshal(appID)
	job, err := store.GetLatestAppInitJob(ctx, json.RawMessage(appIDJSON))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("查 job: %w", err)
	}
	if errors.Is(err, sql.ErrNoRows) {
		// 历史从未建过 app_initialize job，新建一份。payload 只含 app_id（k8s 路径按 appID 寻址）。
		payload, perr := json.Marshal(map[string]any{"app_id": appID})
		if perr != nil {
			return "", fmt.Errorf("序列化 payload: %w", perr)
		}
		newID := uuid.NewString()
		if cerr := store.CreateJob(ctx, sqlc.CreateJobParams{
			ID:          newID,
			Type:        domain.JobTypeAppInitialize,
			Priority:    100,
			RunAfter:    time.Now(),
			MaxAttempts: 3,
			PayloadJson: payload,
		}); cerr != nil {
			return "", fmt.Errorf("CreateJob: %w", cerr)
		}
		return newID, nil
	}
	if job.Status == domain.JobStatusPending {
		// 已 pending 不动 status，但仍返回 ID 让上层 Enqueue 一次。
		return job.ID, nil
	}
	if err := store.RequeueJob(ctx, job.ID); err != nil {
		return "", fmt.Errorf("RequeueJob: %w", err)
	}
	return job.ID, nil
}
