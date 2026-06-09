package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/store/sqlc"
)

// 本测试文件用纯内存假实现替换 store / RAGFlow / Redis 重试簿记三层依赖，
// 不连真实 DB / HTTP / Redis，专注验证自愈编排（Tick）的策略：失败重解析、卡死先 stop 再 reparse、
// 跳过放弃与冷却、达到上限标记放弃且卡死场景额外打 error 日志、硬错误不入队但仍计数、busy 容忍入队。

// fakeHealStore 是 healStore 的内存假实现：预置 failed/stuck 两类候选行，记录被调用的入队 id。
type fakeHealStore struct {
	failed []sqlc.ListRAGFlowFailedDocumentsForHealRow       // Part A 候选
	stuck  []sqlc.ListRAGFlowStuckRunningDocumentsForHealRow // Part B 候选

	queued    []string                                           // 记录 MarkRAGFlowDocumentManualReparseQueued 的入参 id（断言入队行为与顺序）
	stuckArg  sqlc.ListRAGFlowStuckRunningDocumentsForHealParams // 记录卡死查询参数（断言 StuckBefore/Limit）
	queueErr  error                                              // 若设置，MarkRAGFlowDocumentManualReparseQueued 返回此错误（模拟写库失败）
	failedErr error                                              // 若设置，ListRAGFlowFailedDocumentsForHeal 返回此错误
	stuckErr  error                                              // 若设置，ListRAGFlowStuckRunningDocumentsForHeal 返回此错误
}

func (s *fakeHealStore) ListRAGFlowFailedDocumentsForHeal(_ context.Context, _ int32) ([]sqlc.ListRAGFlowFailedDocumentsForHealRow, error) {
	if s.failedErr != nil {
		return nil, s.failedErr
	}
	return s.failed, nil
}

func (s *fakeHealStore) ListRAGFlowStuckRunningDocumentsForHeal(_ context.Context, arg sqlc.ListRAGFlowStuckRunningDocumentsForHealParams) ([]sqlc.ListRAGFlowStuckRunningDocumentsForHealRow, error) {
	s.stuckArg = arg
	if s.stuckErr != nil {
		return nil, s.stuckErr
	}
	return s.stuck, nil
}

func (s *fakeHealStore) MarkRAGFlowDocumentManualReparseQueued(_ context.Context, id string) error {
	if s.queueErr != nil {
		return s.queueErr
	}
	s.queued = append(s.queued, id)
	return nil
}

// rfCall 记录一次 RAGFlow 调用（op = parse/stop），用于断言调用顺序（卡死必须先 stop 再 parse）。
type rfCall struct {
	op      string // "parse" 或 "stop"
	dataset string // 远端 dataset id
	docIDs  []string
}

// fakeHealRAGFlow 是 healRAGFlow 的内存假实现：按 remoteDoc 决定 ParseDocuments 返回的错误。
type fakeHealRAGFlow struct {
	calls     []rfCall         // 全部调用流水（含 parse/stop），用于顺序断言
	parseErrs map[string]error // remoteDocID -> ParseDocuments 返回错误（nil 表示成功）
	stopErr   error            // StopParsing 统一返回的错误（best-effort，应被忽略）
}

func (r *fakeHealRAGFlow) ParseDocuments(_ context.Context, datasetID string, documentIDs []string) error {
	r.calls = append(r.calls, rfCall{op: "parse", dataset: datasetID, docIDs: documentIDs})
	if r.parseErrs != nil && len(documentIDs) > 0 {
		return r.parseErrs[documentIDs[0]]
	}
	return nil
}

func (r *fakeHealRAGFlow) StopParsing(_ context.Context, datasetID string, documentIDs []string) error {
	r.calls = append(r.calls, rfCall{op: "stop", dataset: datasetID, docIDs: documentIDs})
	return r.stopErr
}

// fakeHealRetryState 是 healRetryState 的内存假实现：
// given/cool 预置放弃/冷却命中；attempts 跟踪每文档计数，使 RecordAttempt 返回递增值；
// startAttempts 让测试预置「已尝试 N-1 次」，下一次 RecordAttempt 即到达上限。
type fakeHealRetryState struct {
	given         map[string]bool // 预置 GivenUp 命中
	cool          map[string]bool // 预置 InCooldown 命中
	attempts      map[string]int  // 当前各文档累计尝试数（RecordAttempt 自增并返回）
	startAttempts map[string]int  // 预置初始计数（用于触发达到上限场景）

	gaveUp      []string                 // 记录被 MarkGivenUp 的文档 id
	cooldownSet map[string]time.Duration // 记录被 SetCooldown 的文档及时长
}

func newFakeRetryState() *fakeHealRetryState {
	return &fakeHealRetryState{
		given:         map[string]bool{},
		cool:          map[string]bool{},
		attempts:      map[string]int{},
		startAttempts: map[string]int{},
		cooldownSet:   map[string]time.Duration{},
	}
}

func (s *fakeHealRetryState) GivenUp(_ context.Context, doc string) (bool, error) {
	return s.given[doc], nil
}

func (s *fakeHealRetryState) InCooldown(_ context.Context, doc string) (bool, error) {
	return s.cool[doc], nil
}

func (s *fakeHealRetryState) RecordAttempt(_ context.Context, doc string) (int, error) {
	// 首次访问时以预置初始值起步，模拟此前已尝试若干次
	if _, seen := s.attempts[doc]; !seen {
		s.attempts[doc] = s.startAttempts[doc]
	}
	s.attempts[doc]++
	return s.attempts[doc], nil
}

func (s *fakeHealRetryState) SetCooldown(_ context.Context, doc string, d time.Duration) error {
	s.cooldownSet[doc] = d
	return nil
}

func (s *fakeHealRetryState) MarkGivenUp(_ context.Context, doc string) error {
	s.gaveUp = append(s.gaveUp, doc)
	return nil
}

// failedRow 构造一条 Part A（failed）候选行，仅填自愈关心的三个字段。
func failedRow(id, remoteDoc, remoteDS string) sqlc.ListRAGFlowFailedDocumentsForHealRow {
	return sqlc.ListRAGFlowFailedDocumentsForHealRow{
		ID:                id,
		RagflowDocumentID: remoteDoc,
		RemoteDatasetID:   null.StringFrom(remoteDS),
	}
}

// stuckRow 构造一条 Part B（stuck running）候选行，仅填自愈关心的三个字段。
func stuckRow(id, remoteDoc, remoteDS string) sqlc.ListRAGFlowStuckRunningDocumentsForHealRow {
	return sqlc.ListRAGFlowStuckRunningDocumentsForHealRow{
		ID:                id,
		RagflowDocumentID: remoteDoc,
		RemoteDatasetID:   null.StringFrom(remoteDS),
	}
}

// newHealerForTest 用统一默认配置构造 healer，便于各用例复用。
func newHealerForTest(store healStore, rf healRAGFlow, state healRetryState) *RagflowAnomalyHealer {
	return NewRagflowAnomalyHealer(store, rf, state, HealerConfig{
		MaxAttempts:    3,
		Backoffs:       []time.Duration{10 * time.Minute, 30 * time.Minute},
		StuckThreshold: 30 * time.Minute,
		BatchLimit:     100,
	})
}

// TestHealer_PartA_ReparsesFailed 覆盖 Part A 正常路径：
// 单个 failed 文档 → 触发 ParseDocuments（远端 doc id）、入队（本地 doc id）、计数 1、设置首档退避冷却。
func TestHealer_PartA_ReparsesFailed(t *testing.T) {
	store := &fakeHealStore{
		failed: []sqlc.ListRAGFlowFailedDocumentsForHealRow{failedRow("doc-1", "rf-doc-1", "ds-remote")},
	}
	rf := &fakeHealRAGFlow{}
	state := newFakeRetryState()
	h := newHealerForTest(store, rf, state)

	require.NoError(t, h.Tick(context.Background()))

	// 应仅有一次 parse 调用，且用远端 dataset / 远端 doc id
	require.Len(t, rf.calls, 1)
	assert.Equal(t, "parse", rf.calls[0].op)
	assert.Equal(t, "ds-remote", rf.calls[0].dataset)
	assert.Equal(t, []string{"rf-doc-1"}, rf.calls[0].docIDs)
	// 入队用本地 doc id
	assert.Equal(t, []string{"doc-1"}, store.queued)
	// 本次尝试计数为 1
	assert.Equal(t, 1, state.attempts["doc-1"])
	// 未到上限 → 设置首档退避（Backoffs[0]=10m），不放弃
	assert.Equal(t, 10*time.Minute, state.cooldownSet["doc-1"])
	assert.Empty(t, state.gaveUp)
}

// TestHealer_SkipsGivenUpAndCooldown 覆盖跳过逻辑：
// 已放弃或处于冷却的文档不应触发任何 parse / 入队 / 计数。
func TestHealer_SkipsGivenUpAndCooldown(t *testing.T) {
	store := &fakeHealStore{
		failed: []sqlc.ListRAGFlowFailedDocumentsForHealRow{
			failedRow("doc-given", "rf-1", "ds-remote"), // 预置已放弃
			failedRow("doc-cool", "rf-2", "ds-remote"),  // 预置冷却中
		},
	}
	rf := &fakeHealRAGFlow{}
	state := newFakeRetryState()
	state.given["doc-given"] = true
	state.cool["doc-cool"] = true
	h := newHealerForTest(store, rf, state)

	require.NoError(t, h.Tick(context.Background()))

	// 两个文档都被跳过：无 RAGFlow 调用、无入队、无计数
	assert.Empty(t, rf.calls)
	assert.Empty(t, store.queued)
	assert.Empty(t, state.attempts)
}

// TestHealer_CapReachedSetsGiveup 覆盖达到上限：
// RecordAttempt 返回值 == MaxAttempts 时应 MarkGivenUp 且不再设置冷却（Part A，不打 error 日志）。
func TestHealer_CapReachedSetsGiveup(t *testing.T) {
	store := &fakeHealStore{
		failed: []sqlc.ListRAGFlowFailedDocumentsForHealRow{failedRow("doc-cap", "rf-cap", "ds-remote")},
	}
	rf := &fakeHealRAGFlow{}
	state := newFakeRetryState()
	state.startAttempts["doc-cap"] = 2 // 已尝试 2 次，本轮 RecordAttempt 返回 3 == MaxAttempts
	h := newHealerForTest(store, rf, state)

	require.NoError(t, h.Tick(context.Background()))

	// 本轮仍执行了一次 parse + 入队（成功路径），但计数到达上限
	assert.Equal(t, 3, state.attempts["doc-cap"])
	assert.Equal(t, []string{"doc-cap"}, store.queued)
	// 达到上限 → 放弃，且不设冷却
	assert.Equal(t, []string{"doc-cap"}, state.gaveUp)
	assert.NotContains(t, state.cooldownSet, "doc-cap")
}

// TestHealer_PartB_StopThenReparse 覆盖 Part B 正常路径：
// 卡死文档必须「先 StopParsing 重置、再 ParseDocuments」并入队，且断言调用顺序与卡死查询参数。
func TestHealer_PartB_StopThenReparse(t *testing.T) {
	store := &fakeHealStore{
		stuck: []sqlc.ListRAGFlowStuckRunningDocumentsForHealRow{stuckRow("doc-stuck", "rf-stuck", "ds-remote")},
	}
	rf := &fakeHealRAGFlow{}
	state := newFakeRetryState()
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	h := NewRagflowAnomalyHealer(store, rf, state, HealerConfig{
		MaxAttempts:    3,
		Backoffs:       []time.Duration{10 * time.Minute},
		StuckThreshold: 30 * time.Minute,
		BatchLimit:     50,
		Now:            func() time.Time { return now },
	})

	require.NoError(t, h.Tick(context.Background()))

	// 调用顺序：stop 在前、parse 在后
	require.Len(t, rf.calls, 2)
	assert.Equal(t, "stop", rf.calls[0].op)
	assert.Equal(t, []string{"rf-stuck"}, rf.calls[0].docIDs)
	assert.Equal(t, "parse", rf.calls[1].op)
	assert.Equal(t, []string{"rf-stuck"}, rf.calls[1].docIDs)
	// 入队成功
	assert.Equal(t, []string{"doc-stuck"}, store.queued)
	// 卡死查询参数：StuckBefore = now - 阈值，Limit = BatchLimit
	assert.Equal(t, now.UTC().Add(-30*time.Minute), store.stuckArg.StuckBefore)
	assert.Equal(t, int32(50), store.stuckArg.Limit)
}

// TestHealer_PartB_HardErrorSkipsQueue 覆盖硬错误与 busy 容忍的对比：
// 非 busy 错误 → 不入队但仍计数；busy 错误 → 视为已在解析，照常入队。
func TestHealer_PartB_HardErrorSkipsQueue(t *testing.T) {
	store := &fakeHealStore{
		stuck: []sqlc.ListRAGFlowStuckRunningDocumentsForHealRow{
			stuckRow("doc-hard", "rf-hard", "ds-remote"), // ParseDocuments 返回普通错误
			stuckRow("doc-busy", "rf-busy", "ds-remote"), // ParseDocuments 返回 busy 错误
		},
	}
	rf := &fakeHealRAGFlow{
		parseErrs: map[string]error{
			"rf-hard": errors.New("ragflow 500 internal error"),
			"rf-busy": errors.New("Can't parse document that is currently being processed"),
		},
	}
	state := newFakeRetryState()
	h := newHealerForTest(store, rf, state)

	err := h.Tick(context.Background())
	// 硬错误应作为 firstErr 从 Tick 返回
	require.Error(t, err)

	// 硬错误文档：未入队，但本次尝试仍计数
	assert.NotContains(t, store.queued, "doc-hard")
	assert.Equal(t, 1, state.attempts["doc-hard"])
	// busy 文档：视为已在解析，照常入队，也计数
	assert.Contains(t, store.queued, "doc-busy")
	assert.Equal(t, 1, state.attempts["doc-busy"])
}

// TestHealer_PartB_ExhaustedLogsError 覆盖卡死文档耗尽上限：
// 应 MarkGivenUp 并额外打 error 日志（提示 RAGFlow 可能需重启）。
func TestHealer_PartB_ExhaustedLogsError(t *testing.T) {
	store := &fakeHealStore{
		stuck: []sqlc.ListRAGFlowStuckRunningDocumentsForHealRow{stuckRow("doc-stuck-cap", "rf-sc", "ds-remote")},
	}
	rf := &fakeHealRAGFlow{}
	state := newFakeRetryState()
	state.startAttempts["doc-stuck-cap"] = 2 // 本轮 RecordAttempt 返回 3 == MaxAttempts
	h := newHealerForTest(store, rf, state)

	// 注入写到 buffer 的 logger，断言 error 级日志被打出
	var buf bytes.Buffer
	h.SetLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError})))

	require.NoError(t, h.Tick(context.Background()))

	// 放弃该文档，且日志含「卡死」与上限提示
	assert.Equal(t, []string{"doc-stuck-cap"}, state.gaveUp)
	logged := buf.String()
	assert.Contains(t, logged, "level=ERROR")
	assert.Contains(t, logged, "doc-stuck-cap")
	assert.Contains(t, logged, "RAGFlow")
}

// TestHealer_StoreListErrorIsReturnedButOtherPartStillRuns 覆盖"累积错误但不提前中断"语义：
// Part B（卡死列表）查询报错时，Tick 应把该错误作为 firstErr 返回，同时继续处理 Part A（失败）文档。
// 验证 Tick 绝不因一个查询错误而饿死其它候选文档。
func TestHealer_StoreListErrorIsReturnedButOtherPartStillRuns(t *testing.T) {
	// Part A 有一个正常 failed 文档；Part B 查询直接报错（模拟 DB/网络异常）
	store := &fakeHealStore{
		failed:   []sqlc.ListRAGFlowFailedDocumentsForHealRow{failedRow("doc-a1", "rf-a1", "ds-remote")},
		stuckErr: errors.New("db timeout: stuck query failed"),
	}
	rf := &fakeHealRAGFlow{}
	state := newFakeRetryState()
	h := newHealerForTest(store, rf, state)

	err := h.Tick(context.Background())

	// Tick 应返回 Part B 查询的错误（Part B 先执行，是 firstErr 来源）
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stuck query failed")

	// Part A 的 failed 文档应仍被正常处理：ParseDocuments 被调用且文档入队
	require.Len(t, rf.calls, 1)
	assert.Equal(t, "parse", rf.calls[0].op)
	assert.Equal(t, []string{"rf-a1"}, rf.calls[0].docIDs)
	assert.Equal(t, []string{"doc-a1"}, store.queued)
}

// TestHealer_PartBBeforePartA 覆盖处理次序：Part B（卡死）先于 Part A（失败）。
// 通过同一 RAGFlow 调用流水验证 stop（仅 Part B 触发）出现在 Part A 的 parse 之前。
func TestHealer_PartBBeforePartA(t *testing.T) {
	store := &fakeHealStore{
		failed: []sqlc.ListRAGFlowFailedDocumentsForHealRow{failedRow("doc-a", "rf-a", "ds-remote")},
		stuck:  []sqlc.ListRAGFlowStuckRunningDocumentsForHealRow{stuckRow("doc-b", "rf-b", "ds-remote")},
	}
	rf := &fakeHealRAGFlow{}
	state := newFakeRetryState()
	h := newHealerForTest(store, rf, state)

	require.NoError(t, h.Tick(context.Background()))

	// 流水应为：stop(rf-b) → parse(rf-b) → parse(rf-a)
	require.Len(t, rf.calls, 3)
	assert.Equal(t, "stop", rf.calls[0].op)
	assert.Equal(t, []string{"rf-b"}, rf.calls[0].docIDs)
	assert.Equal(t, "parse", rf.calls[1].op)
	assert.Equal(t, []string{"rf-b"}, rf.calls[1].docIDs)
	assert.Equal(t, "parse", rf.calls[2].op)
	assert.Equal(t, []string{"rf-a"}, rf.calls[2].docIDs)
}

// TestHealer_BackoffFor 直接覆盖 backoffFor 的边界条件：
// Backoffs[1]（n=2）取第二档；n 超出切片长度时钳到最后一个。
func TestHealer_BackoffFor(t *testing.T) {
	h := NewRagflowAnomalyHealer(
		&fakeHealStore{}, &fakeHealRAGFlow{}, newFakeRetryState(),
		HealerConfig{
			MaxAttempts: 5,
			Backoffs:    []time.Duration{10 * time.Minute, 30 * time.Minute},
		},
	)

	// n=2 → 取 Backoffs[1]=30m
	assert.Equal(t, 30*time.Minute, h.backoffFor(2))
	// n=99 → 超出切片，钳到 Backoffs[1]=30m（最后一个）
	assert.Equal(t, 30*time.Minute, h.backoffFor(99))
	// n=1 → 取 Backoffs[0]=10m
	assert.Equal(t, 10*time.Minute, h.backoffFor(1))
}
