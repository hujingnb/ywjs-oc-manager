// Package main 是 Playwright e2e 用的固定 fixture 种子命令。
//
// 与 cmd/seed-admin 区别：
//   - seed-admin 创建一个 platform_admin 行，幂等且只追加；
//   - seed-e2e 会 TRUNCATE 大量业务表，然后从裸 SQL 重建组织 / 成员 / 应用 fixture。
//
// 安全守门：
//   - 必须设置 OCM_E2E=1 才会执行 truncate；否则直接退出非零，避免误在生产环境跑。
//   - 命令仅打算通过 docker compose run 在容器内对开发库执行（make seed-e2e）。
//
// 输出：
//   - stdout 最后一行打印 fixture JSON；Playwright globalSetup 解析这一行。
package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"

	"oc-manager/internal/auth"
	"oc-manager/internal/config"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/service"
)

// fixture 是 stdout 末行写出的 JSON 结构；字段名与 web/tests/e2e/fixtures.ts 一致。
//
// 注意：
//   - OrgID / AppID 在数据库 schema 中是 CHAR(36) UUID（应用层生成），故用 string。
//   - migration 000003 已删除 runtime_nodes 表及 apps.runtime_node_id 列，
//     fixture 不再携带 NodeID / NodeName，k8s 调度器负责 pod 落点，应用层无节点绑定。
type fixture struct {
	// PlatformAdminLogin/Password 是 Playwright 全局登录用的固定平台管理员账密。
	PlatformAdminLogin    string `json:"platform_admin_login"`
	PlatformAdminPassword string `json:"platform_admin_password"`
	// OrgID/OrgName/OrgCode 标识本次 e2e 组织边界，成员、应用和知识库用例都依赖它。
	OrgID   string `json:"org_id"`
	OrgName string `json:"org_name"`
	OrgCode string `json:"org_code"`
	// OrgAdminLogin/Password 是组织管理员固定账密，用于覆盖组织级管理能力。
	OrgAdminLogin    string `json:"org_admin_login"`
	OrgAdminPassword string `json:"org_admin_password"`
	// OrgMemberLogin/Password 是普通成员固定账密，用于覆盖成员权限边界。
	OrgMemberLogin    string `json:"org_member_login"`
	OrgMemberPassword string `json:"org_member_password"`
	// AppID/AppName 标识预置 running 应用，用于渠道、运行态和权限用例。
	AppID   string `json:"app_id"`
	AppName string `json:"app_name"`
}

func main() {
	if err := requireE2EGuard(); err != nil {
		log.Fatal(err)
	}

	configPath := os.Getenv("OCM_CONFIG")
	if configPath == "" {
		configPath = "config/manager.yaml"
	}
	cfg, err := config.LoadFile(configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	if cfg.Database.URL == "" {
		log.Fatalf("配置 %s 缺 database.url", configPath)
	}

	// go-sql-driver 的 DSN 不接受 mysql:// scheme 前缀，剥离后再交给 sql.Open。
	dsn := strings.TrimPrefix(cfg.Database.URL, "mysql://")
	ctx := context.Background()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	defer db.Close()
	// 单连接执行：truncate 期间的 SET FOREIGN_KEY_CHECKS 是会话级变量，限制连接池为 1
	// 可保证所有语句落在同一连接上，避免连接池切换导致 FK 检查状态丢失。
	db.SetMaxOpenConns(1)

	if err := truncate(ctx, db); err != nil {
		log.Fatalf("truncate 失败: %v", err)
	}

	runtimeImageID, err := e2eRuntimeImageID(cfg)
	if err != nil {
		log.Fatalf("选择 E2E runtime image 失败: %v", err)
	}
	fx, err := buildFixture(ctx, db, runtimeImageID)
	if err != nil {
		log.Fatalf("构造 fixture 失败: %v", err)
	}
	if err := provisionE2ENewAPIUser(ctx, db, cfg, fx); err != nil {
		log.Fatalf("构造 fixture new-api 凭据失败: %v", err)
	}

	// 标准输出最后一行写 fixture JSON；前面的 log 走 stderr。
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(fx); err != nil {
		log.Fatalf("打印 fixture 失败: %v", err)
	}
}

// requireE2EGuard 强制要求调用方显式声明 e2e 场景，防止误执行清库型 fixture 初始化。
func requireE2EGuard() error {
	if os.Getenv("OCM_E2E") != "1" {
		return errors.New("seed-e2e 需要 OCM_E2E=1 环境变量；本命令会 TRUNCATE 业务表，禁止误在生产执行")
	}
	return nil
}

// e2eRuntimeImageID 选择 manager 当前配置中的首个 Hermes runtime image ID。
// E2E 隐藏 app 必须复用真实 allowlist，不能写入仅存在于测试代码中的占位镜像。
func e2eRuntimeImageID(cfg config.Config) (string, error) {
	if len(cfg.Hermes.RuntimeImages) == 0 || strings.TrimSpace(cfg.Hermes.RuntimeImages[0].ID) == "" {
		return "", errors.New("Hermes runtime image 未配置")
	}
	return strings.TrimSpace(cfg.Hermes.RuntimeImages[0].ID), nil
}

// provisionE2ENewAPIUser 为 raw SQL 创建的 fixture 企业补齐正式运行链路需要的
// new-api 用户三件套。密文格式与 OrganizationService 完全一致，明文不写日志或 stdout。
func provisionE2ENewAPIUser(ctx context.Context, db *sql.DB, cfg config.Config, fx fixture) error {
	if strings.TrimSpace(cfg.NewAPI.BaseURL) == "" || strings.TrimSpace(cfg.NewAPI.AdminToken) == "" || cfg.NewAPI.AdminUserID == 0 {
		return errors.New("new-api 管理配置不完整")
	}
	masterKey, err := base64.StdEncoding.DecodeString(cfg.Security.MasterKey)
	if err != nil {
		return fmt.Errorf("master_key base64 解码失败: %w", err)
	}
	cipher, err := auth.NewCipher(masterKey)
	if err != nil {
		return fmt.Errorf("初始化 cipher 失败: %w", err)
	}

	// 固定测试用户名并先删除旧账号，使破坏性 E2E seed 可重复执行且不会在 new-api 累积孤儿用户。
	// 该名称保持在 new-api 的 12 字符上限内，并与任何正式组织命名空间区分。
	username := e2eNewAPIUsername()
	password := "E2e-" + strings.ReplaceAll(uuid.NewString()[:16], "-", "")
	client := newapi.NewClient(cfg.NewAPI.BaseURL, cfg.NewAPI.AdminToken, cfg.NewAPI.AdminUserID)
	oldUser, err := client.FindUserByUsername(ctx, username)
	if err == nil {
		if err := client.DeleteUser(ctx, oldUser.ID); err != nil && !errors.Is(err, newapi.ErrNotFound) {
			return fmt.Errorf("删除旧 new-api E2E 用户失败: %w", err)
		}
	} else if !errors.Is(err, newapi.ErrNotFound) {
		return fmt.Errorf("查询旧 new-api E2E 用户失败: %w", err)
	}
	user, err := client.CreateUser(ctx, newapi.CreateUserInput{
		Username:    username,
		Password:    password,
		DisplayName: fx.OrgName,
	})
	if err != nil {
		return fmt.Errorf("创建 new-api E2E 用户失败: %w", err)
	}
	// new-api 新建用户余额默认为 0；真实公开问答会在上游余额校验阶段被拒绝。
	// 使用正式充值接口给本轮临时用户发放有限测试额度，避免绕过企业余额契约。
	if _, err := client.RechargeUser(ctx, newapi.RechargeInput{
		NewAPIUserID: user.ID,
		CreditAmount: e2eNewAPICreditAmount(),
	}); err != nil {
		_ = client.DeleteUser(ctx, user.ID)
		return fmt.Errorf("充值 new-api E2E 用户失败: %w", err)
	}
	accessToken, err := retryE2EAccessToken(ctx, func() (string, error) {
		return client.BootstrapUserAccessToken(ctx, username, password)
	}, waitE2ERetry)
	if err != nil {
		_ = client.DeleteUser(ctx, user.ID)
		return fmt.Errorf("获取 new-api E2E access token 失败: %w", err)
	}
	payload, err := json.Marshal(service.OrganizationCredentials{
		Username:    username,
		Password:    password,
		AccessToken: accessToken,
	})
	if err != nil {
		_ = client.DeleteUser(ctx, user.ID)
		return fmt.Errorf("序列化 new-api E2E 凭据失败: %w", err)
	}
	ciphertext, err := cipher.Encrypt(payload)
	if err != nil {
		_ = client.DeleteUser(ctx, user.ID)
		return fmt.Errorf("加密 new-api E2E 凭据失败: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE organizations
		SET newapi_user_id = ?, newapi_username = ?, newapi_user_credentials_ciphertext = ?
		WHERE id = ?`, fmt.Sprintf("%d", user.ID), username, ciphertext, fx.OrgID); err != nil {
		_ = client.DeleteUser(ctx, user.ID)
		return fmt.Errorf("写入 new-api E2E 凭据失败: %w", err)
	}
	return nil
}

// retryE2EAccessToken 只处理本地高频回归触发的 new-api 登录 429。
// 其它上游错误立即返回，避免 E2E 初始化把真实配置或鉴权故障伪装成暂时性波动。
func retryE2EAccessToken(ctx context.Context, bootstrap func() (string, error), wait func(context.Context, time.Duration) error) (string, error) {
	delays := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second}
	for attempt := 0; ; attempt++ {
		token, err := bootstrap()
		if err == nil {
			return token, nil
		}
		if !strings.Contains(err.Error(), "status=429") || attempt >= len(delays) {
			return "", err
		}
		if err := wait(ctx, delays[attempt]); err != nil {
			return "", err
		}
	}
}

// waitE2ERetry 等待下一次 E2E 登录尝试；context 取消时立即停止初始化。
func waitE2ERetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// e2eNewAPIUsername 返回 seed-e2e 独占的固定 new-api 用户名。
// 固定值允许初始化前精确删除上一次测试账号，避免随机用户名持续占用本地测试资源。
func e2eNewAPIUsername() string {
	return "e2eaicc"
}

// truncate 清掉 e2e 相关表；users 表只保留 platform_admin 行（保留 cmd/seed-admin
// 已建好的 admin 账号，e2e 直接复用）。
//
// MySQL 适配：
//   - 无 PG 的 TRUNCATE … RESTART IDENTITY CASCADE；用 SET FOREIGN_KEY_CHECKS=0 包裹整批，
//     绕过外键约束后逐表 TRUNCATE/DELETE，结束再恢复 FK 检查。
//   - users 需保留 platform_admin，故用 DELETE WHERE role<>'platform_admin' 而非整表 TRUNCATE。
//   - migration 000003 已删除 runtime_nodes / instance_resource_samples / node_resource_samples 三张表，
//     此处不再 TRUNCATE 它们；apps.runtime_node_id / container_id / container_name 列同样已删。
func truncate(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`SET FOREIGN_KEY_CHECKS = 0`,
		// AICC 由后续 migration 引入，必须在 apps/organizations 前清理；否则历史智能体会引用已清掉的隐藏 app，
		// 造成下一轮工作台默认选中脏数据，破坏角色、知识库和会话回归的可重复性。
		`TRUNCATE TABLE aicc_feedback`,
		`TRUNCATE TABLE aicc_lead_values`,
		`TRUNCATE TABLE aicc_leads`,
		`TRUNCATE TABLE aicc_lead_fields`,
		`TRUNCATE TABLE aicc_images`,
		`TRUNCATE TABLE aicc_messages`,
		`TRUNCATE TABLE aicc_sessions`,
		`TRUNCATE TABLE aicc_blocked_visitors`,
		`TRUNCATE TABLE aicc_agent_settings`,
		`TRUNCATE TABLE aicc_agent_knowledge`,
		`TRUNCATE TABLE aicc_agents`,
		`TRUNCATE TABLE channel_bindings`,
		`TRUNCATE TABLE ragflow_documents`,
		`TRUNCATE TABLE ragflow_datasets`,
		`TRUNCATE TABLE apps`,
		`TRUNCATE TABLE recharge_records`,
		`TRUNCATE TABLE jobs`,
		`TRUNCATE TABLE audit_logs`,
		`TRUNCATE TABLE refresh_tokens`,
		`TRUNCATE TABLE assistant_versions`,
		`DELETE FROM users WHERE role <> 'platform_admin'`,
		`TRUNCATE TABLE organizations`,
		`SET FOREIGN_KEY_CHECKS = 1`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return nil
}

// ensurePlatformAdmin 保证 fixture 声明的 platform_admin 行存在；如果 cmd/seed-admin 没跑过，
// 或 truncate 把它带走了，就用 fixture 里的账密重新创建一份。
//
// 用 ON DUPLICATE KEY UPDATE（UPSERT）而非 INSERT IGNORE：环境里如果已有同名 admin 但密码不同，
// e2e 必须能用 fixture 里写死的密码登录，因此每次都把 hash 与 status 重置回 fixture 状态。
// 命中的唯一键是 uk_users_platform_username（org_id 为 NULL 的平台管理员用户名）。
func ensurePlatformAdmin(ctx context.Context, db *sql.DB, username, password string) error {
	hash, err := auth.HashPassword(password, auth.DefaultPasswordParams)
	if err != nil {
		return fmt.Errorf("hash platform_admin password: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (id, username, password_hash, display_name, role, status)
		VALUES (?, ?, ?, ?, 'platform_admin', 'active')
		ON DUPLICATE KEY UPDATE
			password_hash = VALUES(password_hash),
			role = 'platform_admin',
			status = 'active',
			updated_at = NOW()
	`, uuid.NewString(), username, hash, username); err != nil {
		return fmt.Errorf("upsert platform_admin: %w", err)
	}
	return nil
}

// buildFixture 以裸 SQL 写入组织 / 两个普通账号 / 一个应用 / 一个未绑定渠道。
// 所有主键由应用层 uuid.NewString() 生成（CHAR(36)）；MySQL :exec 无 RETURNING，直接复用生成的 id。
// migration 000003 已删除 runtime_nodes 表及 apps.runtime_node_id / container_id / container_name 列，
// 因此不再插入节点行，apps INSERT 也不含上述列。
func buildFixture(ctx context.Context, db *sql.DB, runtimeImageID string) (fixture, error) {
	var fx fixture
	fx.PlatformAdminLogin = "admin"
	fx.PlatformAdminPassword = "admin123"

	// 0) 保证 platform_admin 行存在（truncate 之后行可能空）。
	if err := ensurePlatformAdmin(ctx, db, fx.PlatformAdminLogin, fx.PlatformAdminPassword); err != nil {
		return fx, err
	}

	// 1) 创建组织。
	fx.OrgName = "e2e-org"
	fx.OrgCode = "test-org"
	fx.OrgID = uuid.NewString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO organizations (id, name, code, status) VALUES (?, ?, ?, 'active')`,
		fx.OrgID, fx.OrgName, fx.OrgCode,
	); err != nil {
		return fx, fmt.Errorf("create org: %w", err)
	}

	// 2) 创建 org_admin / org_member 两个账号；后续 app.owner_user_id 用 admin id。
	// 注：migration 000003 已删除 runtime_nodes 表，不再插入节点行；
	// k8s 场景下 pod 落点由调度器决定，应用层无需节点绑定。
	fx.OrgAdminLogin = "e2e-org-admin"
	fx.OrgAdminPassword = "e2e-pass-123"
	fx.OrgMemberLogin = "e2e-org-member"
	fx.OrgMemberPassword = "e2e-pass-123"

	var orgAdminID string
	for _, u := range []struct {
		name, password, role string
	}{
		{fx.OrgAdminLogin, fx.OrgAdminPassword, "org_admin"},
		{fx.OrgMemberLogin, fx.OrgMemberPassword, "org_member"},
	} {
		hash, err := auth.HashPassword(u.password, auth.DefaultPasswordParams)
		if err != nil {
			return fx, fmt.Errorf("hash %s: %w", u.name, err)
		}
		id := uuid.NewString()
		if _, err := db.ExecContext(ctx, `
			INSERT INTO users (id, username, password_hash, display_name, role, status, org_id)
			VALUES (?, ?, ?, ?, ?, 'active', ?)
		`, id, u.name, hash, u.name, u.role, fx.OrgID); err != nil {
			return fx, fmt.Errorf("create user %s: %w", u.name, err)
		}
		if u.role == "org_admin" {
			orgAdminID = id
		}
	}

	// 3) 创建一个 assistant_version 并绑定到 fixture app。实例相关查询（GetAppWithVersion /
	// ListAppsByOrgWithVersion）经 INNER JOIN assistant_versions，未绑定版本的 app 既不出现在
	// 实例列表，也会让 app locale 等按实例查询的端点查不到（404），故 fixture app 必须绑定版本。
	versionID := uuid.NewString()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO assistant_versions
			(id, name, system_prompt, image_id, main_model)
		VALUES (?, 'e2e-version', 'You are a test assistant.', ?, ?)`,
		versionID, runtimeImageID, e2eMainModel(),
	); err != nil {
		return fx, fmt.Errorf("create assistant_version: %w", err)
	}
	// AICC 隐藏 app 创建会复用企业可用助手版本 allowlist；fixture 组织需要显式允许
	// 刚创建的版本，避免端到端用例在创建 AICC 智能体时因缺少版本配置失败。
	if _, err := db.ExecContext(ctx, `
		UPDATE organizations
		SET assistant_version_ids = JSON_ARRAY(?)
		WHERE id = ?`,
		versionID, fx.OrgID,
	); err != nil {
		return fx, fmt.Errorf("allow assistant_version for org: %w", err)
	}

	// 4) 创建 fixture app（status=running，绑定上面的版本）。owner_user_id 用 org_admin。
	// migration 000003 已删除 apps.runtime_node_id / container_id / container_name 列，
	// INSERT 不含上述列；k8s 下应用无节点绑定，pod 落点由调度器决定。
	fx.AppName = "e2e-app"
	fx.AppID = uuid.NewString()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO apps
			(id, org_id, owner_user_id, name, status, api_key_status, version_id)
		VALUES (?, ?, ?, ?, 'running', 'active', ?)`,
		fx.AppID, fx.OrgID, orgAdminID, fx.AppName, versionID,
	); err != nil {
		return fx, fmt.Errorf("create app: %w", err)
	}

	// 5) 创建对应 channel_binding（unbound 占位，留给 e2e 走绑定流程）。
	if _, err := db.ExecContext(ctx, `
		INSERT INTO channel_bindings (id, app_id, channel_type, status)
		VALUES (?, ?, 'wechat', 'unbound')
	`, uuid.NewString(), fx.AppID); err != nil {
		return fx, fmt.Errorf("create channel_binding: %w", err)
	}

	return fx, nil
}

// e2eMainModel 返回本地 local-init-models 初始化的可用聊天模型。
// 不能使用历史硬编码 gpt-4：本地 new-api 仅配置 DeepSeek 渠道，错误模型会让 Hermes 在真实问答时返回 503。
func e2eMainModel() string {
	return "deepseek-chat"
}

// e2eNewAPICreditAmount 返回临时 E2E 用户的展示额度，覆盖多轮公开问答而不影响正式企业余额。
func e2eNewAPICreditAmount() int64 {
	return 100
}
