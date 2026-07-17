package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// e2eSuite 表示 e2e 的三级执行范围，seed 输出会原样携带该值供 Playwright 使用。
type e2eSuite string

const (
	// suiteQuick 只准备核心冒烟场景使用的 fixture。
	suiteQuick e2eSuite = "quick"
	// suiteRegression 准备全部确定性回归场景使用的 fixture。
	suiteRegression e2eSuite = "regression"
	// suiteSlow 准备真实外部依赖专项场景使用的 fixture，并强制单 worker。
	suiteSlow e2eSuite = "slow"
)

// e2eAction 表示 seed 命令的运行意图；清理枚举先作为 Task 4 的输入契约保留。
type e2eAction string

const (
	// actionSeed 创建当前 run 的 fixture 池。
	actionSeed e2eAction = "seed"
	// actionCleanup 清理指定 run 的 fixture，执行逻辑由 Task 4 接入。
	actionCleanup e2eAction = "cleanup"
	// actionCleanupExpired 清理过期 fixture，执行逻辑由 Task 4 接入。
	actionCleanupExpired e2eAction = "cleanup-expired"
)

// runOptions 是环境变量解析后的运行参数，供 seed 主流程和后续 cleanup 共用。
type runOptions struct {
	// RunID 是限定在 16 字符内的安全运行标识，用于隔离不同测试批次。
	RunID string
	// Suite 决定本轮 e2e 的测试范围。
	Suite e2eSuite
	// Workers 是本轮需要生成的隔离 fixture 数量。
	Workers int
	// Action 决定创建或清理 fixture；当前主流程只允许 seed。
	Action e2eAction
}

// fixturePool 是 stdout 输出的单一 JSON 对象，包含当前 run 的全部 worker fixture。
type fixturePool struct {
	// RunID 让 Playwright 和后续清理流程精确关联本轮资源。
	RunID string `json:"run_id"`
	// Suite 让消费端保留本轮三级测试范围。
	Suite e2eSuite `json:"suite"`
	// Fixtures 按 worker 索引顺序保存隔离 fixture。
	Fixtures []fixture `json:"fixtures"`
}

// fixtureIdentity 汇总单个 worker 在数据库中的稳定命名边界。
type fixtureIdentity struct {
	// RunID 标识本轮测试批次。
	RunID string
	// WorkerIndex 是从零开始的 Playwright worker 索引。
	WorkerIndex int
	// OrgName 是 worker 独占的组织显示名。
	OrgName string
	// OrgCode 是 worker 独占的组织代码，也是 scoped cleanup 的匹配边界。
	OrgCode string
	// PlatformAdminLogin 是 worker 独占的平台管理员账号，避免 locale 等状态互相污染。
	PlatformAdminLogin string
	// OrgAdminLogin 是 worker 独占的组织管理员账号。
	OrgAdminLogin string
	// OrgMemberLogin 是 worker 独占的普通成员账号。
	OrgMemberLogin string
	// AppName 是 worker 独占的预置应用名。
	AppName string
}

// unsafeRunID 匹配 run ID 中不能安全用于 fixture 名称和 SQL LIKE 前缀的字符。
var unsafeRunID = regexp.MustCompile(`[^a-z0-9-]+`)

// loadRunOptions 从环境变量加载并校验运行参数，保证数据库写入前参数已收敛。
func loadRunOptions() (runOptions, error) {
	runID := strings.ToLower(strings.TrimSpace(os.Getenv("OCM_E2E_RUN_ID")))
	if runID == "" {
		runID = "manual"
	}
	// 连续不安全字符统一折叠为连字符，并移除两端连字符，保持名称适合前缀匹配。
	runID = strings.Trim(unsafeRunID.ReplaceAllString(runID, "-"), "-")
	if runID == "" || len(runID) > 16 {
		return runOptions{}, fmt.Errorf("OCM_E2E_RUN_ID 必须为 1 到 16 个安全字符")
	}

	suite := e2eSuite(strings.TrimSpace(os.Getenv("OCM_E2E_SUITE")))
	if suite == "" {
		suite = suiteRegression
	}
	if suite != suiteQuick && suite != suiteRegression && suite != suiteSlow {
		return runOptions{}, fmt.Errorf("未知 OCM_E2E_SUITE: %s", suite)
	}

	action := e2eAction(strings.TrimSpace(os.Getenv("OCM_E2E_ACTION")))
	if action == "" {
		action = actionSeed
	}
	if action != actionSeed && action != actionCleanup && action != actionCleanupExpired {
		return runOptions{}, fmt.Errorf("未知 OCM_E2E_ACTION: %s", action)
	}

	workers := 1
	if raw := strings.TrimSpace(os.Getenv("OCM_E2E_WORKERS")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 4 {
			return runOptions{}, fmt.Errorf("OCM_E2E_WORKERS 必须是 1 到 4 的整数，实际为 %s", raw)
		}
		workers = parsed
	}
	// slow 必须在显式 worker 参数通过通用边界校验后再降为 1，禁止非法值绕过校验。
	if suite == suiteSlow {
		workers = 1
	}

	return runOptions{RunID: runID, Suite: suite, Workers: workers, Action: action}, nil
}

// requireSeedAction 在 Task 4 接入 cleanup 前阻止清理请求误入 truncate 后重新 seed 的流程。
func requireSeedAction(opts runOptions) error {
	if opts.Action != actionSeed {
		return fmt.Errorf("OCM_E2E_ACTION=%s 尚未实现", opts.Action)
	}
	return nil
}

// fixtureIdentities 为每个 worker 生成互不重叠的组织、账号和应用命名空间。
func fixtureIdentities(opts runOptions) ([]fixtureIdentity, error) {
	if opts.Workers < 1 || opts.Workers > 4 {
		return nil, fmt.Errorf("fixture worker 数必须为 1 到 4，实际为 %d", opts.Workers)
	}

	items := make([]fixtureIdentity, 0, opts.Workers)
	for index := 0; index < opts.Workers; index++ {
		suffix := fmt.Sprintf("%s-w%d", opts.RunID, index)
		items = append(items, fixtureIdentity{
			RunID:              opts.RunID,
			WorkerIndex:        index,
			OrgName:            "e2e-" + suffix,
			OrgCode:            "e2e-" + suffix,
			PlatformAdminLogin: "e2e-" + suffix + "-platform",
			OrgAdminLogin:      "e2e-" + suffix + "-admin",
			OrgMemberLogin:     "e2e-" + suffix + "-member",
			AppName:            "e2e-" + suffix + "-app",
		})
	}
	return items, nil
}
