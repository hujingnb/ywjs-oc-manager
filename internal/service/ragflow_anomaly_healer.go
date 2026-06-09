// Package service 的 RAGFlow 解析异常自愈任务。
//
// RagflowAnomalyHealer 是该特性的核心：无需任何人工 UI 介入，周期性地修复 RAGFlow 解析异常。
// 每轮 Tick 处理两类异常：
//   - Part A 失败文档：manager 侧记录为 failed / stopped 的文档，直接重新解析。
//   - Part B 卡死文档：long-running（updated_at 长时间不动，疑似 RAGFlow 任务孤儿 / 挂起）的文档，
//     先 StopParsing 重置（取消远端任务、清残留 chunk），再重新解析。
//
// 自愈策略要点：
//   - 单文档自愈次数有上限（MaxAttempts），每次失败后按 Backoffs 退避，避免对始终修不好的文档无限重试空耗。
//   - 达到上限即「放弃」（MarkGivenUp，写 Redis 黑名单一段时间），不再自动重试。
//   - 卡死文档若耗尽上限仍未恢复，额外打 error 日志——这通常意味着 RAGFlow 自身需要重启，靠重解析救不回来。
//   - 重试簿记（计数 / 冷却 / 放弃）全部放 Redis（HealState），瞬时、自动过期、不落库。
//   - busy 容忍：RAGFlow 若以「文档正在解析中」拒绝，说明它其实已在解析流程里，应视为「已在重解析」而入队，
//     而非失败重试，否则会每轮被拒、永远循环（线上死循环根因）。
package service

import (
	"context"
	"log/slog"
	"time"

	"oc-manager/internal/integrations/ragflow"
	"oc-manager/internal/store/sqlc"
)

// healStore 是自愈任务所需的数据访问子集（store 层）。
// 单独定义接口而非复用大 store，是为了测试可注入内存替身、依赖面最小。
type healStore interface {
	// ListRAGFlowFailedDocumentsForHeal 全库列出 failed/stopped 且远端 dataset 仍在的文档（Part A 候选）。
	ListRAGFlowFailedDocumentsForHeal(ctx context.Context, limit int32) ([]sqlc.ListRAGFlowFailedDocumentsForHealRow, error)
	// ListRAGFlowStuckRunningDocumentsForHeal 全库列出 running 超过 StuckBefore 仍未推进的文档（Part B 候选）。
	ListRAGFlowStuckRunningDocumentsForHeal(ctx context.Context, arg sqlc.ListRAGFlowStuckRunningDocumentsForHealParams) ([]sqlc.ListRAGFlowStuckRunningDocumentsForHealRow, error)
	// MarkRAGFlowDocumentManualReparseQueued 把文档重置为 queued，交刷新任务继续推进状态。
	MarkRAGFlowDocumentManualReparseQueued(ctx context.Context, id string) error
}

// healRAGFlow 是自愈任务所需的 RAGFlow 操作子集。
type healRAGFlow interface {
	// ParseDocuments 触发指定文档解析。
	ParseDocuments(ctx context.Context, datasetID string, documentIDs []string) error
	// StopParsing 取消 running 文档的解析并清残留（仅 Part B 卡死重置时调用）。
	StopParsing(ctx context.Context, datasetID string, documentIDs []string) error
}

// healRetryState 是自愈重试簿记子集（HealState 在 Redis 上的实现）。
type healRetryState interface {
	// GivenUp 文档是否已被标记放弃（放弃期内跳过自愈）。
	GivenUp(ctx context.Context, doc string) (bool, error)
	// InCooldown 文档是否处于退避冷却期（冷却期内跳过本轮）。
	InCooldown(ctx context.Context, doc string) (bool, error)
	// RecordAttempt 递增并返回本文档的自愈尝试计数（每轮每文档仅调一次）。
	RecordAttempt(ctx context.Context, doc string) (int, error)
	// SetCooldown 为文档设置退避冷却（TTL=d）。
	SetCooldown(ctx context.Context, doc string, d time.Duration) error
	// MarkGivenUp 把文档标记为放弃（到达上限时）。
	MarkGivenUp(ctx context.Context, doc string) error
}

// 编译期断言：真实实现满足三个依赖接口（签名一旦漂移，构建即失败而非运行期 panic）。
var (
	_ healStore      = (*sqlc.Queries)(nil)
	_ healRAGFlow    = (*ragflow.Client)(nil)
	_ healRetryState = (*HealState)(nil)
)

const (
	// defaultHealMaxAttempts 是单文档自愈次数上限的默认值。
	// 选 3：失败文档通常要么一次重解析即恢复（瞬时过载），要么是数据本身问题，重试 3 次后再试也无益。
	defaultHealMaxAttempts = 3
	// defaultHealStuckThreshold 是判定 running 卡死的默认时长。
	// running 超过 30 分钟仍无状态推进，几乎可断定是孤儿任务 / 远端挂起，需主动 stop+reparse 救回。
	defaultHealStuckThreshold = 30 * time.Minute
	// defaultHealBatchLimit 是每轮每类（failed / stuck）处理上限的默认值，控制单轮 Tick 总开销。
	defaultHealBatchLimit int32 = 100
)

// HealerConfig 是自愈任务的可调策略参数。
type HealerConfig struct {
	// MaxAttempts 单文档自愈次数上限（<=0 取默认 3）。达到即放弃。
	MaxAttempts int
	// Backoffs 第 n 次尝试后的冷却时长（0-based 下标）；超出范围用最后一个；为空则不设冷却（0）。
	Backoffs []time.Duration
	// StuckThreshold running 超过此时长判定卡死（<=0 取默认 30m）。
	StuckThreshold time.Duration
	// BatchLimit 每轮每类处理上限（<=0 取默认 100）。
	BatchLimit int32
	// Now 取当前时刻（用于计算卡死截止点），便于测试注入固定时钟；nil 取 time.Now。
	Now func() time.Time
}

// RagflowAnomalyHealer 是 RAGFlow 解析异常自愈任务。
type RagflowAnomalyHealer struct {
	store healStore      // 候选查询与入队
	rf    healRAGFlow    // 调 RAGFlow 解析 / 停止
	state healRetryState // Redis 上的重试簿记
	cfg   HealerConfig   // 策略参数（已注入默认值）
	log   *slog.Logger   // 日志（卡死耗尽上限时打 error）
}

// NewRagflowAnomalyHealer 构造自愈任务，并对未配置项注入默认值。
func NewRagflowAnomalyHealer(store healStore, rf healRAGFlow, state healRetryState, cfg HealerConfig) *RagflowAnomalyHealer {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaultHealMaxAttempts
	}
	if cfg.StuckThreshold <= 0 {
		cfg.StuckThreshold = defaultHealStuckThreshold
	}
	if cfg.BatchLimit <= 0 {
		cfg.BatchLimit = defaultHealBatchLimit
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &RagflowAnomalyHealer{
		store: store,
		rf:    rf,
		state: state,
		cfg:   cfg,
		log:   slog.Default(),
	}
}

// SetLogger 替换日志器（默认 slog.Default）。
func (h *RagflowAnomalyHealer) SetLogger(l *slog.Logger) {
	if l != nil {
		h.log = l
	}
}

// Tick 执行单轮自愈：先 Part B（卡死）后 Part A（失败）。
// 处理顺序取舍：卡死文档占着远端任务槽位且更可能拖垮 RAGFlow，优先抢救；失败文档相对独立，随后处理。
// 错误语义：返回首个遇到的错误，但绝不提前中断——每个符合条件的文档都要处理完，避免单点错误饿死其它文档。
func (h *RagflowAnomalyHealer) Tick(ctx context.Context) error {
	var firstErr error
	// 收敛错误：仅记录第一个，后续错误吞掉（但处理继续）。
	record := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// Part B：卡死 running 文档，先 stop 重置再 reparse。
	stuckBefore := h.cfg.Now().UTC().Add(-h.cfg.StuckThreshold)
	stuckRows, err := h.store.ListRAGFlowStuckRunningDocumentsForHeal(ctx, sqlc.ListRAGFlowStuckRunningDocumentsForHealParams{
		StuckBefore: stuckBefore,
		Limit:       h.cfg.BatchLimit,
	})
	record(err)
	for _, row := range stuckRows {
		// isStuck=true：本类需先 StopParsing 重置，且耗尽上限时额外打 error 日志。
		record(h.healOne(ctx, row.ID, row.RemoteDatasetID.String, row.RagflowDocumentID, true))
	}

	// Part A：失败 / 停止文档，直接 reparse。
	failedRows, err := h.store.ListRAGFlowFailedDocumentsForHeal(ctx, h.cfg.BatchLimit)
	record(err)
	for _, row := range failedRows {
		record(h.healOne(ctx, row.ID, row.RemoteDatasetID.String, row.RagflowDocumentID, false))
	}

	return firstErr
}

// healOne 处理单个候选文档的自愈，返回本文档处理过程中遇到的首个错误（供 Tick 收敛为 firstErr）。
//
// isStuck 区分两类来源：true 为 Part B 卡死文档（需先 StopParsing 重置、耗尽上限时打 error 日志），
// false 为 Part A 失败文档。
//
// 关键不变量：每文档每轮「恰好」调用一次 RecordAttempt（无论成功 / busy / 硬失败都计这一次），
// 严禁双重递增——否则计数虚高会提前触发放弃。
func (h *RagflowAnomalyHealer) healOne(ctx context.Context, docID, remoteDS, remoteDoc string, isStuck bool) error {
	// 1) 已放弃：放弃期内不再自愈，直接跳过。
	// fail-open：Redis 查询出错时忽略错误，视为「未放弃」继续执行自愈——瞬时 Redis 抖动不应阻止修复。
	if gu, _ := h.state.GivenUp(ctx, docID); gu {
		return nil
	}
	// 2) 冷却中：上一次尝试后的退避窗口未到，跳过本轮。
	// fail-open：Redis 查询出错时忽略错误，视为「未冷却」继续执行自愈——瞬时 Redis 抖动不应阻止修复。
	if cd, _ := h.state.InCooldown(ctx, docID); cd {
		return nil
	}

	// 3) Part B 才需要：best-effort StopParsing 重置（文档可能已非 running，错误忽略）。
	//    必须先于 ParseDocuments，使远端先取消旧任务、清残留 chunk，再重新提交。
	if isStuck {
		_ = h.rf.StopParsing(ctx, remoteDS, []string{remoteDoc})
	}

	// 4) 触发重新解析。
	parseErr := h.rf.ParseDocuments(ctx, remoteDS, []string{remoteDoc})

	// 5) 计本次尝试（成功 / busy / 硬失败都算一次，唯一一次 RecordAttempt）。
	// RecordAttempt 出错时 n=0 无意义：跳过退避/放弃簿记，本轮不设冷却，下一轮自然重试；
	// 不应用 n=0 调用 applyBackoffOrGiveup，否则会虚增冷却、甚至误触发"第 0 次放弃"逻辑。
	n, recordErr := h.state.RecordAttempt(ctx, docID)

	var firstErr error
	// 6) 硬失败（非 busy）：不入队（避免把仍 failed 的文档误置 queued），但已计数；记录错误后走退避 / 放弃。
	if parseErr != nil && !isRAGFlowDocBusyError(parseErr) {
		firstErr = parseErr
	} else {
		// 7) 成功，或 busy（视为已在解析中，照常入队）：重置为 queued 交刷新任务推进状态。
		if err := h.store.MarkRAGFlowDocumentManualReparseQueued(ctx, docID); err != nil {
			firstErr = err
		}
	}

	// 8) 退避 / 放弃：RecordAttempt 出错时跳过簿记（计数未知，不能据此设冷却或放弃），
	//    将错误并入 firstErr，让 Tick 上报给调用方，文档下一轮自然重试。
	if recordErr != nil {
		h.log.Warn("[heal] 记录自愈尝试次数失败,跳过本轮退避/给上", "doc", docID, "error", recordErr)
		if firstErr == nil {
			firstErr = recordErr
		}
		return firstErr
	}
	h.applyBackoffOrGiveup(ctx, docID, n, isStuck)
	return firstErr
}

// applyBackoffOrGiveup 根据本次尝试计数 n 决定后续：达到上限 → MarkGivenUp（不设冷却）；否则设退避冷却。
// 卡死文档（isStuck）耗尽上限时额外打 error 日志：重解析救不回卡死通常意味着 RAGFlow 自身需重启。
func (h *RagflowAnomalyHealer) applyBackoffOrGiveup(ctx context.Context, docID string, n int, isStuck bool) {
	if n >= h.cfg.MaxAttempts {
		_ = h.state.MarkGivenUp(ctx, docID)
		if isStuck {
			h.log.Error("[heal] 文档卡死自愈达上限仍未恢复，RAGFlow 可能需重启", "doc", docID, "attempts", n)
		}
		return
	}
	// 未到上限：按第 n 次尝试对应的退避档设置冷却（0 表示不设冷却）。
	if d := h.backoffFor(n); d > 0 {
		_ = h.state.SetCooldown(ctx, docID, d)
	}
}

// backoffFor 返回第 n 次尝试（n>=1）后的退避时长：
// 取 Backoffs[n-1]，越界则钳到最后一个；Backoffs 为空时返回 0（不设冷却）。
func (h *RagflowAnomalyHealer) backoffFor(n int) time.Duration {
	if len(h.cfg.Backoffs) == 0 {
		return 0
	}
	idx := n - 1
	if idx >= len(h.cfg.Backoffs) {
		idx = len(h.cfg.Backoffs) - 1
	}
	if idx < 0 {
		idx = 0
	}
	return h.cfg.Backoffs[idx]
}
