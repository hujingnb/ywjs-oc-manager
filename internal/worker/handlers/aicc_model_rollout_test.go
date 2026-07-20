package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/k8sorch"
	"oc-manager/internal/store/sqlc"
)

// TestAICCModelRolloutRestartsOneAgentAfterAnother 验证企业模型切换严格逐台执行：
// 前一台 Deployment 完整就绪并写入 revision 后，才允许触发下一台重启。
func TestAICCModelRolloutRestartsOneAgentAfterAnother(t *testing.T) {
	events := []string{}
	store := &aiccRolloutStore{
		configs: []sqlc.OrganizationAiccConfig{{OrgID: "org-1", Enabled: true, Revision: 8}},
		agents: []sqlc.AiccAgent{
			{ID: "agent-1", OrgID: "org-1", AppID: "app-1", Status: domain.AICCAgentStatusActive},
			{ID: "agent-2", OrgID: "org-1", AppID: "app-2", Status: domain.AICCAgentStatusActive},
		},
		events: &events,
	}
	orch := &aiccRolloutOrchestrator{events: &events}
	handler := NewAICCModelRolloutHandler(store, orch, time.Second)

	require.NoError(t, handler.Handle(context.Background(), rolloutJob(t, 8)))
	assert.Equal(t, []string{"restart:app-1", "wait:app-1", "stamp:agent-1:8", "restart:app-2", "wait:app-2", "stamp:agent-2:8"}, events)
}

// TestAICCModelRolloutStopsWhenCurrentAgentFails 验证当前智能体就绪失败立即返回，
// 不写成功 revision，也绝不启动下一台，交由持久 job 重试当前进度。
func TestAICCModelRolloutStopsWhenCurrentAgentFails(t *testing.T) {
	events := []string{}
	store := &aiccRolloutStore{
		configs: []sqlc.OrganizationAiccConfig{{OrgID: "org-1", Enabled: true, Revision: 8}},
		agents: []sqlc.AiccAgent{
			{ID: "agent-1", OrgID: "org-1", AppID: "app-1", Status: domain.AICCAgentStatusActive},
			{ID: "agent-2", OrgID: "org-1", AppID: "app-2", Status: domain.AICCAgentStatusActive},
		},
		events: &events,
	}
	orch := &aiccRolloutOrchestrator{events: &events, waitErrFor: "app-1"}

	err := NewAICCModelRolloutHandler(store, orch, time.Second).Handle(context.Background(), rolloutJob(t, 8))

	require.Error(t, err)
	assert.ErrorContains(t, err, "org=org-1 agent=agent-1 stage=wait_rollout_ready")
	assert.Equal(t, []string{"restart:app-1", "wait:app-1"}, events)
}

// TestAICCModelRolloutFollowerDefersThenTakesOverLatestRevision 验证新任务不会占住 worker 或提前成功：
// 旧任务仍是 pending/running leader 时返回 defer；旧任务 succeeded 后下一次执行接管 revision 9。
func TestAICCModelRolloutFollowerDefersThenTakesOverLatestRevision(t *testing.T) {
	events := []string{}
	store := &aiccRolloutStore{
		leaderID: "job-leader",
		configs:  []sqlc.OrganizationAiccConfig{{OrgID: "org-1", Enabled: true, Revision: 8}},
		agents: []sqlc.AiccAgent{
			{ID: "agent-1", OrgID: "org-1", AppID: "app-1", Status: domain.AICCAgentStatusActive},
			{ID: "agent-2", OrgID: "org-1", AppID: "app-2", Status: domain.AICCAgentStatusActive},
		},
		events: &events,
	}
	handler := NewAICCModelRolloutHandler(store, &aiccRolloutOrchestrator{events: &events}, time.Second)
	followerJob := rolloutJobWithID(t, "job-follower", 9)

	require.NoError(t, handler.Handle(context.Background(), rolloutJobWithID(t, "job-leader", 8)))
	deferErr := handler.Handle(context.Background(), followerJob)
	var deferred *DeferredJobError
	require.ErrorAs(t, deferErr, &deferred)
	assert.Empty(t, events[6:], "follower defer 不得产生新副作用")

	// 模拟旧 leader 业务失败后由 worker 恢复为 pending：它仍按 created_at/id 排序占据 leader，
	// follower 再次执行也必须 defer，不能因旧任务不再 running 而抢占。
	deferErr = handler.Handle(context.Background(), followerJob)
	require.ErrorAs(t, deferErr, &deferred)
	store.configs = []sqlc.OrganizationAiccConfig{{OrgID: "org-1", Enabled: true, Revision: 9}}
	// 模拟旧 leader 最终 succeeded，leader 查询才切换到 follower。
	store.setLeader("job-follower")
	require.NoError(t, handler.Handle(context.Background(), followerJob))

	assert.Equal(t, []string{
		"restart:app-1", "wait:app-1", "stamp:agent-1:8", "restart:app-2", "wait:app-2", "stamp:agent-2:8",
		"restart:app-1", "wait:app-1", "stamp:agent-1:9", "restart:app-2", "wait:app-2", "stamp:agent-2:9",
	}, events)
}

// TestAICCModelRolloutFollowerDefersBehindRecoveredPendingLeader 验证旧任务失败恢复为 pending 后
// 仍参与 leader 排序；较新的 running follower 必须立即 defer，不能抢占旧任务的副作用周期。
func TestAICCModelRolloutFollowerDefersBehindRecoveredPendingLeader(t *testing.T) {
	events := []string{}
	store := &aiccRolloutStore{
		leaderID: "job-old-pending",
		configs:  []sqlc.OrganizationAiccConfig{{OrgID: "org-1", Enabled: true, Revision: 9}},
		events:   &events,
	}
	handler := NewAICCModelRolloutHandler(store, &aiccRolloutOrchestrator{events: &events}, time.Second)

	err := handler.Handle(context.Background(), rolloutJobWithID(t, "job-new-running", 9))

	var deferred *DeferredJobError
	require.ErrorAs(t, err, &deferred)
	assert.Equal(t, aiccModelRolloutDeferDelay, deferred.Delay)
	assert.Empty(t, events)
}

// TestAICCModelRolloutSkipsAppliedAgents 验证已达到目标 revision 的智能体不会被重启；
// store 查询模拟 SQL 的 applied_config_revision < target 条件。
func TestAICCModelRolloutSkipsAppliedAgents(t *testing.T) {
	events := []string{}
	store := &aiccRolloutStore{
		configs: []sqlc.OrganizationAiccConfig{{OrgID: "org-1", Enabled: true, Revision: 8}},
		agents:  []sqlc.AiccAgent{{ID: "agent-1", OrgID: "org-1", AppID: "app-1", Status: domain.AICCAgentStatusActive, AppliedConfigRevision: 8}},
		events:  &events,
	}

	require.NoError(t, NewAICCModelRolloutHandler(store, &aiccRolloutOrchestrator{events: &events}, time.Second).Handle(context.Background(), rolloutJob(t, 8)))
	assert.Empty(t, events)
}

// TestAICCModelRolloutSkipsPausedAgents 验证暂停智能体不进入 pending 集合，模型切换不得唤醒其应用。
func TestAICCModelRolloutSkipsPausedAgents(t *testing.T) {
	events := []string{}
	store := &aiccRolloutStore{
		configs: []sqlc.OrganizationAiccConfig{{OrgID: "org-1", Enabled: true, Revision: 8}},
		agents:  []sqlc.AiccAgent{{ID: "agent-1", OrgID: "org-1", AppID: "app-1", Status: domain.AICCAgentStatusPaused}},
		events:  &events,
	}

	require.NoError(t, NewAICCModelRolloutHandler(store, &aiccRolloutOrchestrator{events: &events}, time.Second).Handle(context.Background(), rolloutJob(t, 8)))
	assert.Empty(t, events)
}

// TestAICCModelRolloutCoalescesToLatestRevision 验证旧任务每轮读取企业最新配置，
// 将 payload revision 合并到当前 revision，避免先完整下发旧配置再重复下发新配置。
func TestAICCModelRolloutCoalescesToLatestRevision(t *testing.T) {
	events := []string{}
	store := &aiccRolloutStore{
		configs: []sqlc.OrganizationAiccConfig{{OrgID: "org-1", Enabled: true, Revision: 9}},
		agents:  []sqlc.AiccAgent{{ID: "agent-1", OrgID: "org-1", AppID: "app-1", Status: domain.AICCAgentStatusActive}},
		events:  &events,
	}

	require.NoError(t, NewAICCModelRolloutHandler(store, &aiccRolloutOrchestrator{events: &events}, time.Second).Handle(context.Background(), rolloutJob(t, 8)))
	assert.Equal(t, []string{"restart:app-1", "wait:app-1", "stamp:agent-1:9"}, events)
}

// TestAICCModelRolloutMarkerRecoversStampFailure 验证 marker 已持久化后 stamp 失败，
// 重试从 marker 指定 generation 再核验并完成 stamp/ready/clear，不触发第二次 restart。
func TestAICCModelRolloutMarkerRecoversStampFailure(t *testing.T) {
	testAICCModelRolloutMarkerRecovery(t, "stamp")
}

// TestAICCModelRolloutMarkerRecoversReadyFailure 验证 stamp 成功但 ready 失败时，
// 重试沿任务专属 marker 安全重放，不能借用通用 runtime_phase 推断。
func TestAICCModelRolloutMarkerRecoversReadyFailure(t *testing.T) {
	testAICCModelRolloutMarkerRecovery(t, "ready")
}

// TestAICCModelRolloutMarkerRecoversClearFailure 验证业务步骤全成功但 marker 清理失败时，
// 重试仍按原 generation 幂等核验并最终清除 marker。
func TestAICCModelRolloutMarkerRecoversClearFailure(t *testing.T) {
	testAICCModelRolloutMarkerRecovery(t, "clear")
}

// testAICCModelRolloutMarkerRecovery 统一构造三种持久恢复故障，并验证不发生额外 restart。
func testAICCModelRolloutMarkerRecovery(t *testing.T, failStage string) {
	t.Helper()
	events, trace := []string{}, []string{}
	store := &aiccRolloutStore{
		configs:   []sqlc.OrganizationAiccConfig{{OrgID: "org-1", Enabled: true, Revision: 8}},
		agents:    []sqlc.AiccAgent{{ID: "agent-1", OrgID: "org-1", AppID: "app-1", Status: domain.AICCAgentStatusActive}},
		events:    &events,
		trace:     &trace,
		appPhases: map[string]string{"app-1": domain.RuntimePhaseReady},
	}
	switch failStage {
	case "stamp": // 场景：generation 已确认且 marker 已写，revision stamp 首次失败。
		store.failStampOnce = true
	case "ready": // 场景：stamp 成功后 runtime phase ready 首次失败。
		store.failReadyOnce = true
	case "clear": // 场景：stamp/ready 成功后 marker 清理首次失败。
		store.failClearOnce = true
	}
	orch := &aiccRolloutOrchestrator{events: &events, trace: &trace}
	handler := NewAICCModelRolloutHandler(store, orch, time.Second)
	job := rolloutJob(t, 8)

	require.Error(t, handler.Handle(context.Background(), job))
	job.PayloadJson = append([]byte(nil), store.persistedPayload...)
	require.NoError(t, handler.Handle(context.Background(), job))

	assert.Equal(t, 1, orch.restartCalls)
	assert.Equal(t, domain.RuntimePhaseReady, store.appPhases["app-1"])
	assert.False(t, payloadHasRepairMarker(t, store.persistedPayload))
	assertOrder(t, trace, "marker:set:0", "restarting", "restart", "marker:set:2", "wait", "stamp", "ready", "marker:clear")
}

// TestAICCModelRolloutPersistsOwnershipBeforeRestartAndRecoversGenerationZero 验证任务在任何
// restarting/restart 副作用前持久化 app ownership；restart 首次失败后，generation=0 marker
// 可在 job 重试时重新执行 restart、写回 generation，再完成 wait/stamp/ready/clear。
func TestAICCModelRolloutPersistsOwnershipBeforeRestartAndRecoversGenerationZero(t *testing.T) {
	events, trace := []string{}, []string{}
	store := &aiccRolloutStore{
		configs: []sqlc.OrganizationAiccConfig{{OrgID: "org-1", Enabled: true, Revision: 8}},
		agents:  []sqlc.AiccAgent{{ID: "agent-1", OrgID: "org-1", AppID: "app-1", Status: domain.AICCAgentStatusActive}},
		events:  &events,
		trace:   &trace,
	}
	orch := &aiccRolloutOrchestrator{events: &events, trace: &trace, failRestartOnce: true}
	handler := NewAICCModelRolloutHandler(store, orch, time.Second)
	job := rolloutJob(t, 8)

	err := handler.Handle(context.Background(), job)
	require.Error(t, err)
	assert.ErrorContains(t, err, "stage=restart")
	var persisted AICCModelRolloutPayload
	require.NoError(t, json.Unmarshal(store.persistedPayload, &persisted))
	assert.Equal(t, "agent-1", persisted.RepairAgentID)
	assert.Equal(t, "app-1", persisted.RepairAppID)
	assert.Equal(t, int64(0), persisted.RepairTargetGeneration)
	assert.Equal(t, int32(8), persisted.RepairTargetRevision)
	assertOrder(t, trace, "marker:set:0", "restarting", "restart")

	job.PayloadJson = append([]byte(nil), store.persistedPayload...)
	require.NoError(t, handler.Handle(context.Background(), job))

	assert.Equal(t, 2, orch.restartCalls)
	assert.False(t, payloadHasRepairMarker(t, store.persistedPayload))
	assertOrder(t, trace, "marker:set:0", "restarting", "restart", "restarting", "restart", "marker:set:2", "wait", "stamp", "ready", "marker:clear")
}

// rolloutJob 构造企业模型 rollout 的持久任务载荷，序列化失败直接终止测试。
func rolloutJob(t *testing.T, revision int32) sqlc.Job {
	t.Helper()
	return rolloutJobWithID(t, "job-leader", revision)
}

// rolloutJobWithID 构造指定 ID 的任务，用于验证数据库 leader 选举结果。
func rolloutJobWithID(t *testing.T, id string, revision int32) sqlc.Job {
	t.Helper()
	return sqlc.Job{ID: id, Type: domain.JobTypeAICCModelRollout, PayloadJson: []byte(fmt.Sprintf(`{"org_id":"org-1","target_revision":%d}`, revision))}
}

// aiccRolloutStore 用内存状态模拟 pending SQL 和条件 revision 更新。
type aiccRolloutStore struct {
	leaderMu         sync.Mutex
	leaderID         string
	configs          []sqlc.OrganizationAiccConfig
	agents           []sqlc.AiccAgent
	events           *[]string
	trace            *[]string
	appPhases        map[string]string
	persistedPayload []byte
	failStampOnce    bool
	failReadyOnce    bool
	failClearOnce    bool
	owner            aiccRolloutOwner
}

// GetAICCModelRolloutLeaderJob 返回数据库确定性排序得到的同企业 leader。
func (s *aiccRolloutStore) GetAICCModelRolloutLeaderJob(_ context.Context, _ json.RawMessage) (sqlc.Job, error) {
	s.leaderMu.Lock()
	defer s.leaderMu.Unlock()
	id := s.leaderID
	if id == "" {
		id = "job-leader"
	}
	return sqlc.Job{ID: id}, nil
}

// setLeader 模拟旧 running job 状态切走后，数据库 leader 切换到等待中的新任务。
func (s *aiccRolloutStore) setLeader(id string) {
	s.leaderMu.Lock()
	defer s.leaderMu.Unlock()
	s.leaderID = id
}

// GetOrganizationAICCConfig 每次返回最新快照；多快照时逐次推进，模拟任务执行期间配置继续更新。
func (s *aiccRolloutStore) GetOrganizationAICCConfig(_ context.Context, _ string) (sqlc.OrganizationAiccConfig, error) {
	if len(s.configs) == 0 {
		return sqlc.OrganizationAiccConfig{}, errors.New("缺少配置")
	}
	config := s.configs[0]
	if len(s.configs) > 1 {
		s.configs = s.configs[1:]
	}
	return config, nil
}

// ListPendingAICCModelRolloutAgents 仅返回一台 active 且 revision 落后的智能体。
func (s *aiccRolloutStore) ListPendingAICCModelRolloutAgents(_ context.Context, arg sqlc.ListPendingAICCModelRolloutAgentsParams) ([]sqlc.AiccAgent, error) {
	for _, agent := range s.agents {
		if agent.OrgID == arg.OrgID && agent.Status == domain.AICCAgentStatusActive && agent.AppliedConfigRevision < arg.AppliedConfigRevision {
			return []sqlc.AiccAgent{agent}, nil
		}
	}
	return nil, nil
}

// SetAICCAgentAppliedConfigRevision 模拟条件前进写入，并记录严格时序断言事件。
func (s *aiccRolloutStore) SetAICCAgentAppliedConfigRevision(_ context.Context, arg sqlc.SetAICCAgentAppliedConfigRevisionParams) error {
	if s.failStampOnce {
		s.failStampOnce = false
		return errors.New("stamp 写入失败")
	}
	*s.events = append(*s.events, "stamp:"+arg.ID+":"+string(rune('0'+arg.AppliedConfigRevision)))
	if s.trace != nil {
		*s.trace = append(*s.trace, "stamp")
	}
	for index := range s.agents {
		if s.agents[index].ID == arg.ID && s.agents[index].AppliedConfigRevision < arg.AppliedConfigRevision {
			s.agents[index].AppliedConfigRevision = arg.AppliedConfigRevision
		}
	}
	return nil
}

// SetAppRuntimePhase 模拟运行时状态写入，并可制造一次 ready 持久化失败。
func (s *aiccRolloutStore) SetAppRuntimePhase(_ context.Context, arg sqlc.SetAppRuntimePhaseParams) error {
	if s.appPhases == nil {
		s.appPhases = map[string]string{}
	}
	if arg.RuntimePhase == domain.RuntimePhaseReady && s.failReadyOnce {
		s.failReadyOnce = false
		return errors.New("ready 写入失败")
	}
	s.appPhases[arg.ID] = arg.RuntimePhase
	if s.trace != nil {
		*s.trace = append(*s.trace, arg.RuntimePhase)
	}
	return nil
}

// UpdateJobPayload 模拟持久化或清除任务专属 repair marker，并可制造一次 clear 失败。
func (s *aiccRolloutStore) UpdateJobPayload(_ context.Context, arg sqlc.UpdateJobPayloadParams) (int64, error) {
	var payload AICCModelRolloutPayload
	err := json.Unmarshal(arg.PayloadJson, &payload)
	hasMarker := err == nil && payload.RepairAgentID != ""
	if !hasMarker && s.failClearOnce {
		s.failClearOnce = false
		return 0, errors.New("clear marker 失败")
	}
	s.persistedPayload = append([]byte(nil), arg.PayloadJson...)
	if s.trace != nil {
		if hasMarker {
			*s.trace = append(*s.trace, fmt.Sprintf("marker:set:%d", payload.RepairTargetGeneration))
		} else {
			*s.trace = append(*s.trace, "marker:clear")
		}
	}
	return 1, nil
}

// ClaimAICCRolloutAppOwnership 模拟跨类型 guard：只有无 owner、失效 owner 或同一任务可重入领取。
func (s *aiccRolloutStore) ClaimAICCRolloutAppOwnership(_ context.Context, arg sqlc.ClaimAICCRolloutAppOwnershipParams) error {
	if !s.owner.active || (s.owner.jobID == arg.OwnerJobID && s.owner.jobType == arg.OwnerJobType) {
		s.owner = aiccRolloutOwner{jobID: arg.OwnerJobID, jobType: arg.OwnerJobType, active: true}
	}
	return nil
}

// GetAICCRolloutAppOwnership 返回当前 owner，用于 handler 对异类 rollout defer。
func (s *aiccRolloutStore) GetAICCRolloutAppOwnership(context.Context, string) (sqlc.AiccRolloutAppOwner, error) {
	return sqlc.AiccRolloutAppOwner{AppID: "app-1", OwnerJobID: s.owner.jobID, OwnerJobType: s.owner.jobType}, nil
}

// SetAppRuntimePhaseReadyForAICCRolloutOwner 仅在 guard 尚属当前任务时推进 ready。
func (s *aiccRolloutStore) SetAppRuntimePhaseReadyForAICCRolloutOwner(_ context.Context, arg sqlc.SetAppRuntimePhaseReadyForAICCRolloutOwnerParams) (int64, error) {
	if s.owner.jobID != arg.OwnerJobID || s.owner.jobType != arg.OwnerJobType {
		return 0, nil
	}
	if s.failReadyOnce {
		s.failReadyOnce = false
		return 0, errors.New("ready 写入失败")
	}
	if s.appPhases == nil {
		s.appPhases = map[string]string{}
	}
	s.appPhases[arg.ID] = domain.RuntimePhaseReady
	if s.trace != nil {
		*s.trace = append(*s.trace, "ready")
	}
	return 1, nil
}

// ReleaseAICCRolloutAppOwnership 只释放当前任务自己的 app guard。
func (s *aiccRolloutStore) ReleaseAICCRolloutAppOwnership(_ context.Context, arg sqlc.ReleaseAICCRolloutAppOwnershipParams) (int64, error) {
	if s.owner.jobID != arg.OwnerJobID || s.owner.jobType != arg.OwnerJobType {
		return 0, nil
	}
	s.owner = aiccRolloutOwner{}
	return 1, nil
}

// ReleaseAICCRolloutAppOwnershipByOwner 模拟 clear 后重试清理自己的遗留 guard。
func (s *aiccRolloutStore) ReleaseAICCRolloutAppOwnershipByOwner(_ context.Context, arg sqlc.ReleaseAICCRolloutAppOwnershipByOwnerParams) (int64, error) {
	if s.owner.jobID != arg.OwnerJobID || s.owner.jobType != arg.OwnerJobType {
		return 0, nil
	}
	s.owner = aiccRolloutOwner{}
	return 1, nil
}

// aiccRolloutOrchestrator 记录 restart/wait 顺序并可注入单台就绪失败。
type aiccRolloutOrchestrator struct {
	events       *[]string
	trace        *[]string
	waitErrFor   string
	restartCalls int
	// failRestartOnce 模拟 marker 已持久后、generation 尚未取得时 restart 首次失败。
	failRestartOnce bool
}

// EnsureApp 满足完整编排接口；rollout handler 不创建 Deployment。
func (o *aiccRolloutOrchestrator) EnsureApp(context.Context, k8sorch.AppSpec) error { return nil }

// DeploymentGeneration 满足编排接口；rollout handler 不读取 EnsureApp generation。
func (o *aiccRolloutOrchestrator) DeploymentGeneration(context.Context, string) (int64, error) {
	return 1, nil
}

// WaitReady 满足完整编排接口；rollout handler 只使用 generation 版本的等待。
func (o *aiccRolloutOrchestrator) WaitReady(context.Context, string, time.Duration, func(k8sorch.AppStatus)) error {
	return nil
}

// WaitRolloutReady 记录对本次 restart 返回 generation 的等待；fake 中 generation 固定有效。
func (o *aiccRolloutOrchestrator) WaitRolloutReady(_ context.Context, appID string, _ int64, _ time.Duration, _ func(k8sorch.AppStatus)) error {
	*o.events = append(*o.events, "wait:"+appID)
	if o.trace != nil {
		*o.trace = append(*o.trace, "wait")
	}
	if appID == o.waitErrFor {
		return errors.New("rollout 未就绪")
	}
	return nil
}

// RolloutRestartAndGetGeneration 记录滚动重启并返回本次更新的确定 generation。
func (o *aiccRolloutOrchestrator) RolloutRestartAndGetGeneration(_ context.Context, appID string) (int64, error) {
	*o.events = append(*o.events, "restart:"+appID)
	o.restartCalls++
	if o.trace != nil {
		*o.trace = append(*o.trace, "restart")
	}
	if o.failRestartOnce {
		o.failRestartOnce = false
		return 0, errors.New("restart 失败")
	}
	return 2, nil
}

// Scale 满足完整编排接口；rollout 测试禁止通过缩放替代滚动重启。
func (o *aiccRolloutOrchestrator) Scale(context.Context, string, int32) error { return nil }

// Start 满足完整编排接口；paused 排除测试可证明不会调用启动。
func (o *aiccRolloutOrchestrator) Start(context.Context, string) error { return nil }

// Stop 满足完整编排接口；rollout handler 不停止应用。
func (o *aiccRolloutOrchestrator) Stop(context.Context, string) error { return nil }

// UpdateImage 满足完整编排接口；模型配置切换不改镜像。
func (o *aiccRolloutOrchestrator) UpdateImage(context.Context, string, string) error { return nil }

// Delete 满足完整编排接口；rollout handler 不删除应用。
func (o *aiccRolloutOrchestrator) Delete(context.Context, string) error { return nil }

// Status 满足完整编排接口；rollout 使用 Deployment 状态而非通用 Pod Ready。
func (o *aiccRolloutOrchestrator) Status(context.Context, string) (k8sorch.AppStatus, error) {
	return k8sorch.AppStatus{}, nil
}

// RolloutRestart 保留兼容接口并委托可返回 generation 的测试实现。
func (o *aiccRolloutOrchestrator) RolloutRestart(_ context.Context, appID string) error {
	_, err := o.RolloutRestartAndGetGeneration(context.Background(), appID)
	return err
}

// payloadHasRepairMarker 判断持久任务 payload 是否携带未完成智能体标记。
func payloadHasRepairMarker(t *testing.T, raw []byte) bool {
	if t != nil {
		t.Helper()
	}
	var payload AICCModelRolloutPayload
	err := json.Unmarshal(raw, &payload)
	if t != nil {
		require.NoError(t, err)
	}
	return err == nil && payload.RepairAgentID != ""
}

// assertOrder 验证关键事件按给定先后出现，允许失败重试产生额外重复事件。
func assertOrder(t *testing.T, events []string, expected ...string) {
	t.Helper()
	position := -1
	for _, event := range expected {
		next := slices.Index(events[position+1:], event)
		require.NotEqualf(t, -1, next, "缺少顺序事件 %s，全部事件=%v", event, events)
		position += next + 1
	}
}
