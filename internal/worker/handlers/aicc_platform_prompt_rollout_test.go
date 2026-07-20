// Package handlers 的本文件覆盖平台提示词 hash 静默下发任务的逐台重启与故障恢复行为。
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/k8sorch"
	"oc-manager/internal/store/sqlc"
)

// TestAICCPlatformPromptRolloutRestartsOneStaleAgent 验证每轮只领取一个 hash 落后的活跃客服，
// 等待本次 restart 返回的 generation 就绪，并只在 bootstrap 实际写入目标 hash 后收口 marker。
func TestAICCPlatformPromptRolloutRestartsOneStaleAgent(t *testing.T) {
	events := []string{}
	store := newPromptRolloutStore("old-hash", "current-hash")
	store.events = &events
	orch := &aiccPromptRolloutOrchestrator{events: &events, generation: 7}
	handler := NewAICCPlatformPromptRolloutHandler(store, orch, time.Second)

	require.NoError(t, handler.Handle(context.Background(), promptRolloutJob(t, "current-hash")))
	assert.Equal(t, []string{"restart:app-1", "wait:app-1:7"}, events)
	assert.Equal(t, "current-hash", store.appliedPromptHash["app-1"])
	assert.Equal(t, domain.RuntimePhaseReady, store.appPhases["app-1"])
	assert.False(t, promptPayloadHasRepairMarker(t, store.persistedPayload))
	assert.Equal(t, int32(9), store.agents[0].AppliedConfigRevision, "提示词 rollout 不得改写企业模型应用 revision")
}

// TestAICCPlatformPromptRolloutSkipsPausedAgents 验证暂停客服不会被 pending 查询选中，
// 避免平台提示词更新意外唤醒暂停中的接待服务。
func TestAICCPlatformPromptRolloutSkipsPausedAgents(t *testing.T) {
	events := []string{}
	store := newPromptRolloutStore("old-hash", "current-hash")
	store.agents[0].Status = domain.AICCAgentStatusPaused
	store.events = &events

	require.NoError(t, NewAICCPlatformPromptRolloutHandler(store, &aiccPromptRolloutOrchestrator{events: &events, generation: 7}, time.Second).Handle(context.Background(), promptRolloutJob(t, "current-hash")))
	assert.Empty(t, events)
	assert.Empty(t, store.persistedPayload)
}

// TestAICCPlatformPromptRolloutFollowerDefers 验证较新的同类任务不会与数据库 leader 并行重启，
// 而是无损延迟回队列，保持全局平台提示词逐台下发。
func TestAICCPlatformPromptRolloutFollowerDefers(t *testing.T) {
	events := []string{}
	store := newPromptRolloutStore("old-hash", "current-hash")
	store.events = &events
	store.leaderID = "older-prompt-job"
	job := promptRolloutJob(t, "current-hash")

	err := NewAICCPlatformPromptRolloutHandler(store, &aiccPromptRolloutOrchestrator{events: &events, generation: 7}, time.Second).Handle(context.Background(), job)
	var deferred *DeferredJobError
	require.ErrorAs(t, err, &deferred)
	assert.Equal(t, aiccPlatformPromptRolloutDeferDelay, deferred.Delay)
	assert.Empty(t, events)
}

// TestAICCPlatformPromptRolloutDefersWhenModelRolloutOwnsSameApp 验证同一 app 被活跃模型任务持有时，
// 平台提示词任务不触发第二次 restart，也不能提前把 runtime phase 写回 ready。
func TestAICCPlatformPromptRolloutDefersWhenModelRolloutOwnsSameApp(t *testing.T) {
	events := []string{}
	store := newPromptRolloutStore("old-hash", "current-hash")
	store.events = &events
	store.owner = aiccRolloutOwner{jobID: "model-job", jobType: domain.JobTypeAICCModelRollout, active: true}

	err := NewAICCPlatformPromptRolloutHandler(store, &aiccPromptRolloutOrchestrator{events: &events, generation: 7}, time.Second).Handle(context.Background(), promptRolloutJob(t, "current-hash"))
	var deferred *DeferredJobError
	require.ErrorAs(t, err, &deferred)
	assert.Empty(t, events)
	assert.Empty(t, store.appPhases)
}

// TestAICCPlatformPromptRolloutEnqueuesSuccessorForCurrentHash 验证旧目标 hash 已完成时，
// handler 通过 singleton 协调器检查当前 hash 落后客服并只创建一个后继任务。
func TestAICCPlatformPromptRolloutEnqueuesSuccessorForCurrentHash(t *testing.T) {
	events := []string{}
	store := newPromptRolloutStore("old-hash", "old-target")
	store.events = &events
	successor := &fakePromptRolloutSuccessor{}
	handler := NewAICCPlatformPromptRolloutHandler(store, &aiccPromptRolloutOrchestrator{events: &events, generation: 7}, time.Second)
	handler.SetSuccessorEnqueuer(successor)

	require.NoError(t, handler.Handle(context.Background(), promptRolloutJob(t, "old-target")))
	assert.Equal(t, 1, successor.calls)
}

// TestAICCPlatformPromptRolloutPersistsMarkerBeforeRestartFailure 验证 restart 失败时 ownership marker
// 已落库；后续 job 重试从 generation=0 marker 恢复，而不会丢失本次待重启客服。
func TestAICCPlatformPromptRolloutPersistsMarkerBeforeRestartFailure(t *testing.T) {
	events, trace := []string{}, []string{}
	store := newPromptRolloutStore("old-hash", "current-hash")
	store.events, store.trace = &events, &trace
	orch := &aiccPromptRolloutOrchestrator{events: &events, trace: &trace, generation: 7, failRestartOnce: true}
	handler := NewAICCPlatformPromptRolloutHandler(store, orch, time.Second)
	job := promptRolloutJob(t, "current-hash")

	err := handler.Handle(context.Background(), job)
	require.Error(t, err)
	assert.ErrorContains(t, err, "stage=restart")
	var payload AICCPlatformPromptRolloutPayload
	require.NoError(t, json.Unmarshal(store.persistedPayload, &payload))
	assert.Equal(t, "agent-1", payload.RepairAgentID)
	assert.Equal(t, "app-1", payload.RepairAppID)
	assert.Zero(t, payload.RepairTargetGeneration)
	assertOrder(t, trace, "marker:set:0", "restarting", "restart")

	job.PayloadJson = append([]byte(nil), store.persistedPayload...)
	require.NoError(t, handler.Handle(context.Background(), job))
	assert.Equal(t, 2, orch.restartCalls)
	assert.False(t, promptPayloadHasRepairMarker(t, store.persistedPayload))
}

// TestAICCPlatformPromptRolloutRetriesWhenBootstrapHashMismatches 验证 Deployment 就绪不等于配置生效；
// bootstrap 未写入目标 hash 时保留 marker 并返回错误，让持久 job 重试核验而非错误标记 ready。
func TestAICCPlatformPromptRolloutRetriesWhenBootstrapHashMismatches(t *testing.T) {
	events := []string{}
	store := newPromptRolloutStore("old-hash", "current-hash")
	store.events = &events
	store.keepOldHashAfterWait = true
	orch := &aiccPromptRolloutOrchestrator{events: &events, generation: 7}
	handler := NewAICCPlatformPromptRolloutHandler(store, orch, time.Second)

	err := handler.Handle(context.Background(), promptRolloutJob(t, "current-hash"))
	require.Error(t, err)
	assert.ErrorContains(t, err, "stage=verify_prompt_hash")
	assert.Equal(t, domain.RuntimePhaseRestarting, store.appPhases["app-1"])
	assert.True(t, promptPayloadHasRepairMarker(t, store.persistedPayload))
	assert.Equal(t, []string{"restart:app-1", "wait:app-1:7"}, events)
}

// promptRolloutJob 构造全局平台提示词下发任务，目标 hash 由协调器在服务启动时写入。
func promptRolloutJob(t *testing.T, hash string) sqlc.Job {
	t.Helper()
	return sqlc.Job{ID: "prompt-job", Type: domain.JobTypeAICCPlatformPromptRollout, PayloadJson: []byte(fmt.Sprintf(`{"target_prompt_hash":%q}`, hash))}
}

// promptRolloutStore 用内存状态模拟 active/hash 落后筛选、marker 持久化和 bootstrap 回写。
type promptRolloutStore struct {
	leaderMu             sync.Mutex
	leaderID             string
	agents               []sqlc.AiccAgent
	appliedPromptHash    map[string]string
	bootstrapPromptHash  string
	appPhases            map[string]string
	persistedPayload     []byte
	events               *[]string
	trace                *[]string
	keepOldHashAfterWait bool
	owner                aiccRolloutOwner
}

// aiccRolloutOwner 记录测试中数据库 ownership guard 的当前持有任务。
type aiccRolloutOwner struct {
	jobID   string
	jobType string
	active  bool
}

// fakePromptRolloutSuccessor 记录 handler 完成旧 hash 后是否请求协调器检查后继任务。
type fakePromptRolloutSuccessor struct{ calls int }

// EnqueueIfNeeded 满足后继协调器接口；真实实现会在 singleton guard 事务中去重创建任务。
func (f *fakePromptRolloutSuccessor) EnqueueIfNeeded(context.Context) error { f.calls++; return nil }

// newPromptRolloutStore 创建一台默认 active 客服；oldHash 表示 bootstrap 更新前的 app 状态。
func newPromptRolloutStore(oldHash, bootstrapHash string) *promptRolloutStore {
	return &promptRolloutStore{
		agents:              []sqlc.AiccAgent{{ID: "agent-1", AppID: "app-1", Status: domain.AICCAgentStatusActive, AppliedConfigRevision: 9}},
		appliedPromptHash:   map[string]string{"app-1": oldHash},
		bootstrapPromptHash: bootstrapHash,
		appPhases:           map[string]string{},
	}
}

// GetAICCPlatformPromptRolloutLeaderJob 返回全局 pending/running 稳定 leader，避免多个 worker 并行重启客服。
func (s *promptRolloutStore) GetAICCPlatformPromptRolloutLeaderJob(context.Context) (sqlc.Job, error) {
	s.leaderMu.Lock()
	defer s.leaderMu.Unlock()
	id := s.leaderID
	if id == "" {
		id = "prompt-job"
	}
	return sqlc.Job{ID: id}, nil
}

// ListPendingAICCPlatformPromptRolloutAgents 模拟 SQL 仅返回 active 且 hash 与任务目标不同的一台客服。
func (s *promptRolloutStore) ListPendingAICCPlatformPromptRolloutAgents(_ context.Context, arg sqlc.ListPendingAICCPlatformPromptRolloutAgentsParams) ([]sqlc.AiccAgent, error) {
	for _, agent := range s.agents {
		if agent.Status == domain.AICCAgentStatusActive && s.appliedPromptHash[agent.AppID] != arg.AppliedPlatformPromptHash {
			return []sqlc.AiccAgent{agent}, nil
		}
	}
	return nil, nil
}

// GetAppAppliedPlatformPromptHash 返回 bootstrap 实际写入 app 的 hash，禁止从 agent revision 推导。
func (s *promptRolloutStore) GetAppAppliedPlatformPromptHash(_ context.Context, appID string) (string, error) {
	hash, ok := s.appliedPromptHash[appID]
	if !ok {
		return "", errors.New("app 不存在")
	}
	if !s.keepOldHashAfterWait {
		hash = s.bootstrapPromptHash
		s.appliedPromptHash[appID] = hash
	}
	return hash, nil
}

// SetAppRuntimePhase 模拟重启窗口和就绪状态对外可见。
func (s *promptRolloutStore) SetAppRuntimePhase(_ context.Context, arg sqlc.SetAppRuntimePhaseParams) error {
	s.appPhases[arg.ID] = arg.RuntimePhase
	if s.trace != nil {
		*s.trace = append(*s.trace, arg.RuntimePhase)
	}
	return nil
}

// UpdateJobPayload 模拟 running job 的条件 payload 更新，记录 marker 写入先后关系。
func (s *promptRolloutStore) UpdateJobPayload(_ context.Context, arg sqlc.UpdateJobPayloadParams) (int64, error) {
	s.persistedPayload = append([]byte(nil), arg.PayloadJson...)
	if s.trace != nil {
		var payload AICCPlatformPromptRolloutPayload
		if err := json.Unmarshal(arg.PayloadJson, &payload); err == nil && payload.RepairAgentID != "" {
			*s.trace = append(*s.trace, fmt.Sprintf("marker:set:%d", payload.RepairTargetGeneration))
		} else {
			*s.trace = append(*s.trace, "marker:clear")
		}
	}
	return 1, nil
}

// ClaimAICCRolloutAppOwnership 模拟数据库仅在无活跃异类 owner 时把 ownership 交给当前任务。
func (s *promptRolloutStore) ClaimAICCRolloutAppOwnership(_ context.Context, arg sqlc.ClaimAICCRolloutAppOwnershipParams) error {
	if !s.owner.active || (s.owner.jobID == arg.OwnerJobID && s.owner.jobType == arg.OwnerJobType) {
		s.owner = aiccRolloutOwner{jobID: arg.OwnerJobID, jobType: arg.OwnerJobType, active: true}
	}
	return nil
}

// GetAICCRolloutAppOwnership 返回 claim 后唯一 owner，供 handler 判断应继续还是 defer。
func (s *promptRolloutStore) GetAICCRolloutAppOwnership(context.Context, string) (sqlc.AiccRolloutAppOwner, error) {
	return sqlc.AiccRolloutAppOwner{AppID: "app-1", OwnerJobID: s.owner.jobID, OwnerJobType: s.owner.jobType}, nil
}

// SetAppRuntimePhaseReadyForAICCRolloutOwner 仅匹配 owner 时模拟 ready 收口。
func (s *promptRolloutStore) SetAppRuntimePhaseReadyForAICCRolloutOwner(_ context.Context, arg sqlc.SetAppRuntimePhaseReadyForAICCRolloutOwnerParams) (int64, error) {
	if s.owner.jobID != arg.OwnerJobID || s.owner.jobType != arg.OwnerJobType {
		return 0, nil
	}
	s.appPhases[arg.ID] = domain.RuntimePhaseReady
	return 1, nil
}

// ReleaseAICCRolloutAppOwnership 仅释放匹配当前任务的 app guard。
func (s *promptRolloutStore) ReleaseAICCRolloutAppOwnership(_ context.Context, arg sqlc.ReleaseAICCRolloutAppOwnershipParams) (int64, error) {
	if s.owner.jobID != arg.OwnerJobID || s.owner.jobType != arg.OwnerJobType {
		return 0, nil
	}
	s.owner = aiccRolloutOwner{}
	return 1, nil
}

// ReleaseAICCRolloutAppOwnershipByOwner 模拟无 marker 重试时只清理当前任务残留 guard。
func (s *promptRolloutStore) ReleaseAICCRolloutAppOwnershipByOwner(_ context.Context, arg sqlc.ReleaseAICCRolloutAppOwnershipByOwnerParams) (int64, error) {
	if s.owner.jobID != arg.OwnerJobID || s.owner.jobType != arg.OwnerJobType {
		return 0, nil
	}
	s.owner = aiccRolloutOwner{}
	return 1, nil
}

// aiccPromptRolloutOrchestrator 记录独立提示词 rollout 使用的 restart/wait，绝不调用通用 restart handler。
type aiccPromptRolloutOrchestrator struct {
	events          *[]string
	trace           *[]string
	generation      int64
	restartCalls    int
	failRestartOnce bool
}

// EnsureApp 满足完整编排接口；平台提示词下发只重启已存在 Deployment。
func (*aiccPromptRolloutOrchestrator) EnsureApp(context.Context, k8sorch.AppSpec) error { return nil }
func (*aiccPromptRolloutOrchestrator) DeploymentGeneration(context.Context, string) (int64, error) {
	return 1, nil
}
func (*aiccPromptRolloutOrchestrator) WaitReady(context.Context, string, time.Duration, func(k8sorch.AppStatus)) error {
	return nil
}

// WaitRolloutReady 记录对 restart 产生 generation 的精确等待。
func (o *aiccPromptRolloutOrchestrator) WaitRolloutReady(_ context.Context, appID string, generation int64, _ time.Duration, _ func(k8sorch.AppStatus)) error {
	*o.events = append(*o.events, fmt.Sprintf("wait:%s:%d", appID, generation))
	if o.trace != nil {
		*o.trace = append(*o.trace, "wait")
	}
	return nil
}

// RolloutRestartAndGetGeneration 返回指定 generation，模拟 Deployment annotation 触发的静默滚动升级。
func (o *aiccPromptRolloutOrchestrator) RolloutRestartAndGetGeneration(_ context.Context, appID string) (int64, error) {
	*o.events = append(*o.events, "restart:"+appID)
	o.restartCalls++
	if o.trace != nil {
		*o.trace = append(*o.trace, "restart")
	}
	if o.failRestartOnce {
		o.failRestartOnce = false
		return 0, errors.New("restart 失败")
	}
	return o.generation, nil
}

func (*aiccPromptRolloutOrchestrator) Scale(context.Context, string, int32) error        { return nil }
func (*aiccPromptRolloutOrchestrator) Start(context.Context, string) error               { return nil }
func (*aiccPromptRolloutOrchestrator) Stop(context.Context, string) error                { return nil }
func (*aiccPromptRolloutOrchestrator) UpdateImage(context.Context, string, string) error { return nil }
func (*aiccPromptRolloutOrchestrator) Delete(context.Context, string) error              { return nil }
func (o *aiccPromptRolloutOrchestrator) Status(context.Context, string) (k8sorch.AppStatus, error) {
	return k8sorch.AppStatus{}, nil
}
func (o *aiccPromptRolloutOrchestrator) RolloutRestart(ctx context.Context, appID string) error {
	_, err := o.RolloutRestartAndGetGeneration(ctx, appID)
	return err
}

// promptPayloadHasRepairMarker 判断 payload 是否仍持有未完成客服，供失败恢复断言使用。
func promptPayloadHasRepairMarker(t *testing.T, raw []byte) bool {
	t.Helper()
	var payload AICCPlatformPromptRolloutPayload
	require.NoError(t, json.Unmarshal(raw, &payload))
	return payload.RepairAgentID != ""
}
