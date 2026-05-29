// Package main 是 Playwright e2e 用的固定 fixture 种子命令。
//
// 与 cmd/seed-admin 区别：
//   - seed-admin 创建一个 platform_admin 行，幂等且只追加；
//   - seed-e2e 会 TRUNCATE 大量业务表，然后从裸 SQL 重建组织 / 节点 / 成员 / 应用 fixture。
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
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"

	"oc-manager/internal/auth"
	"oc-manager/internal/config"
)

// fixture 是 stdout 末行写出的 JSON 结构；字段名与 web/tests/e2e/fixtures.ts 一致。
//
// 注意：
//   - OrgID / NodeID / AppID 在数据库 schema 中是 CHAR(36) UUID（应用层生成），故用 string。
type fixture struct {
	// PlatformAdminLogin/Password 是 Playwright 全局登录用的固定平台管理员账密。
	PlatformAdminLogin    string `json:"platform_admin_login"`
	PlatformAdminPassword string `json:"platform_admin_password"`
	// OrgID/OrgName/OrgCode 标识本次 e2e 组织边界，成员、应用和知识库用例都依赖它。
	OrgID   string `json:"org_id"`
	OrgName string `json:"org_name"`
	OrgCode string `json:"org_code"`
	// NodeID/NodeName 标识占位 runtime node，e2e 不真实连接 agent。
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
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
	if os.Getenv("OCM_E2E") != "1" {
		log.Fatalf("seed-e2e 需要 OCM_E2E=1 环境变量；本命令会 TRUNCATE 业务表，禁止误在生产执行")
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

	fx, err := buildFixture(ctx, db)
	if err != nil {
		log.Fatalf("构造 fixture 失败: %v", err)
	}

	// 标准输出最后一行写 fixture JSON；前面的 log 走 stderr。
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(fx); err != nil {
		log.Fatalf("打印 fixture 失败: %v", err)
	}
}

// truncate 清掉 e2e 相关表；users 表只保留 platform_admin 行（保留 cmd/seed-admin
// 已建好的 admin 账号，e2e 直接复用）。
//
// MySQL 适配：
//   - 无 PG 的 TRUNCATE … RESTART IDENTITY CASCADE；用 SET FOREIGN_KEY_CHECKS=0 包裹整批，
//     绕过外键约束后逐表 TRUNCATE/DELETE，结束再恢复 FK 检查。
//   - users 需保留 platform_admin，故用 DELETE WHERE role<>'platform_admin' 而非整表 TRUNCATE。
//   - 同时清掉 ragflow_* 与 *_resource_samples 等下游表，replicate 原 PG CASCADE 的清空范围，避免遗留孤儿。
func truncate(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`SET FOREIGN_KEY_CHECKS = 0`,
		`TRUNCATE TABLE channel_bindings`,
		`TRUNCATE TABLE ragflow_documents`,
		`TRUNCATE TABLE ragflow_datasets`,
		`TRUNCATE TABLE instance_resource_samples`,
		`TRUNCATE TABLE node_resource_samples`,
		`TRUNCATE TABLE apps`,
		`TRUNCATE TABLE recharge_records`,
		`TRUNCATE TABLE jobs`,
		`TRUNCATE TABLE audit_logs`,
		`TRUNCATE TABLE refresh_tokens`,
		`TRUNCATE TABLE assistant_versions`,
		`TRUNCATE TABLE runtime_nodes`,
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

// buildFixture 以裸 SQL 写入组织 / 节点 / 两个普通账号 / 一个应用 / 一个未绑定渠道。
// 所有主键由应用层 uuid.NewString() 生成（CHAR(36)）；MySQL :exec 无 RETURNING，直接复用生成的 id。
func buildFixture(ctx context.Context, db *sql.DB) (fixture, error) {
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

	// 2) 创建 runtime_node：dummy endpoint，e2e 不真连 agent；status 直接置 active 跳过注册流程。
	fx.NodeName = "e2e-node"
	fx.NodeID = uuid.NewString()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO runtime_nodes
			(id, name, status, agent_docker_endpoint, agent_file_endpoint,
			 agent_token_hash, node_data_root, heartbeat_interval_seconds)
		VALUES (?, ?, 'active', 'http://127.0.0.1:9999', 'http://127.0.0.1:9999',
			 'placeholder', '/tmp/e2e-node', 60)`,
		fx.NodeID, fx.NodeName,
	); err != nil {
		return fx, fmt.Errorf("create node: %w", err)
	}

	// 3) 创建 org_admin / org_member 两个账号；后续 app.owner_user_id 用 admin id。
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

	// 4) 创建 fixture app（status=running，跳过实际容器路径）。owner_user_id 用 org_admin。
	fx.AppName = "e2e-app"
	fx.AppID = uuid.NewString()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO apps
			(id, org_id, owner_user_id, runtime_node_id, name, status, api_key_status)
		VALUES (?, ?, ?, ?, ?, 'running', 'active')`,
		fx.AppID, fx.OrgID, orgAdminID, fx.NodeID, fx.AppName,
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
