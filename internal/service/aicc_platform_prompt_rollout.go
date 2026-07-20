package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"oc-manager/internal/config"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// AICCPlatformPromptRolloutStore 是启动时检查并创建全局平台提示词下发任务的最小数据接口。
type AICCPlatformPromptRolloutStore interface {
	// HasActiveAICCPlatformPromptRolloutJob 判断是否已有同类 pending/running job，防止启动副本重复创建。
	HasActiveAICCPlatformPromptRolloutJob(ctx context.Context) (bool, error)
	// HasStaleAICCPlatformPromptAgents 判断是否仍有活跃客服尚未 bootstrap 当前提示词 hash。
	HasStaleAICCPlatformPromptAgents(ctx context.Context, promptHash string) (bool, error)
	// CreateJob 持久化任务，成功后才允许通知队列。
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) error
}

// AICCPlatformPromptRolloutTxRunner 提供单个事务边界；实现必须先锁定 singleton guard 行再运行回调。
type AICCPlatformPromptRolloutTxRunner interface {
	// WithAICCPlatformPromptRolloutTx 将活跃任务检查、落后客服检查和创建任务串行化。
	WithAICCPlatformPromptRolloutTx(ctx context.Context, fn func(AICCPlatformPromptRolloutStore) error) error
}

// AICCPlatformPromptRolloutCoordinator 在服务启动时按需创建独立的平台提示词 rollout。
type AICCPlatformPromptRolloutCoordinator struct {
	tx       AICCPlatformPromptRolloutTxRunner
	notifier JobNotifier
}

// NewAICCPlatformPromptRolloutCoordinator 构造启动协调器；notifier 缺失会在真正需要下发时显式报错。
func NewAICCPlatformPromptRolloutCoordinator(tx AICCPlatformPromptRolloutTxRunner, notifier JobNotifier) *AICCPlatformPromptRolloutCoordinator {
	return &AICCPlatformPromptRolloutCoordinator{tx: tx, notifier: notifier}
}

// EnqueueIfNeeded 仅在没有同类活跃任务且存在提示词落后客服时创建任务，并在写入成功后通知 worker。
func (c *AICCPlatformPromptRolloutCoordinator) EnqueueIfNeeded(ctx context.Context) error {
	if c.tx == nil {
		return fmt.Errorf("AICC 平台提示词发布任务事务 runner 未配置")
	}
	var jobID string
	err := c.tx.WithAICCPlatformPromptRolloutTx(ctx, func(store AICCPlatformPromptRolloutStore) error {
		active, err := store.HasActiveAICCPlatformPromptRolloutJob(ctx)
		if err != nil {
			return fmt.Errorf("检查活跃 AICC 平台提示词发布任务失败: %w", err)
		}
		if active {
			return nil
		}
		promptHash := config.PlatformPromptHash(domain.AppTypeAICC)
		stale, err := store.HasStaleAICCPlatformPromptAgents(ctx, promptHash)
		if err != nil {
			return fmt.Errorf("检查提示词落后 AICC 客服失败: %w", err)
		}
		if !stale {
			return nil
		}
		payload, err := json.Marshal(struct {
			TargetPromptHash string `json:"target_prompt_hash"`
		}{TargetPromptHash: promptHash})
		if err != nil {
			return fmt.Errorf("编码 AICC 平台提示词发布任务失败: %w", err)
		}
		jobID = newUUID()
		if err := store.CreateJob(ctx, sqlc.CreateJobParams{
			ID: jobID, Type: domain.JobTypeAICCPlatformPromptRollout, Priority: 100,
			RunAfter: time.Now().UTC(), MaxAttempts: 20, PayloadJson: payload,
		}); err != nil {
			return fmt.Errorf("创建 AICC 平台提示词发布任务失败: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if jobID == "" {
		return nil
	}
	if c.notifier == nil {
		return fmt.Errorf("AICC 平台提示词发布任务通知器未配置")
	}
	if err := c.notifier.Enqueue(ctx, jobID); err != nil {
		return fmt.Errorf("通知 AICC 平台提示词发布任务失败: %w", err)
	}
	return nil
}
