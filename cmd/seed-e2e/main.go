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
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/config"
)

// fixture 是 stdout 末行写出的 JSON 结构；字段名与 web/tests/e2e/fixtures.ts 一致。
//
// 注意：
//   - OrgID / NodeID 在数据库 schema 中是 uuid（见 internal/migrations/000002_core_schema.up.sql），
//     因此这里使用 string 而不是 plan 草稿里的 int64。
//   - AppID 同样是 uuid。
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

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, cfg.Database.URL)
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	defer conn.Close(ctx)

	if err := truncate(ctx, conn); err != nil {
		log.Fatalf("truncate 失败: %v", err)
	}

	fx, err := buildFixture(ctx, conn)
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
// 顺序按外键依赖从下游到上游：先清掉指向 users / organizations / runtime_nodes 的子表，
// 再 DELETE users 中的非 platform_admin 行，最后清空 organizations / runtime_nodes。
//
// 与 plan 草稿的差异：
//   - 增加 refresh_tokens：refresh_tokens.user_id 引用 users(id)，否则 DELETE users 会被外键阻挡。
//   - knowledge_sync_status 引用 organizations / runtime_nodes，必须先于这两张表清。
//   - organization_personas.created_by 引用 users(id)，必须先于 DELETE users 清。
//   - organizations 不能用 TRUNCATE … CASCADE：users.org_id 引用 organizations，CASCADE
//     会顺带把 platform_admin 也清掉；改用 DELETE，让外键保护已经从 users 解除关联的 admin 行。
func truncate(ctx context.Context, conn *pgx.Conn) error {
	stmts := []string{
		`TRUNCATE TABLE channel_bindings RESTART IDENTITY CASCADE`,
		`TRUNCATE TABLE apps RESTART IDENTITY CASCADE`,
		`TRUNCATE TABLE knowledge_sync_status RESTART IDENTITY CASCADE`,
		`TRUNCATE TABLE recharge_records RESTART IDENTITY CASCADE`,
		`TRUNCATE TABLE jobs RESTART IDENTITY CASCADE`,
		`TRUNCATE TABLE audit_logs RESTART IDENTITY CASCADE`,
		`TRUNCATE TABLE organization_personas RESTART IDENTITY CASCADE`,
		`TRUNCATE TABLE refresh_tokens RESTART IDENTITY CASCADE`,
		`DELETE FROM users WHERE role <> 'platform_admin'`,
		`TRUNCATE TABLE runtime_nodes RESTART IDENTITY CASCADE`,
		`DELETE FROM organizations`,
	}
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return nil
}

// ensurePlatformAdmin 保证 fixture 声明的 platform_admin 行存在；如果 cmd/seed-admin 没跑过，
// 或 truncate 把它带走了，就用 fixture 里的账密重新创建一份。
//
// ON CONFLICT 用 UPSERT 而非 DO NOTHING：环境里如果已有同名 admin 但密码不同，
// e2e 必须能用 fixture 里写死的密码登录，因此每次都把 hash 与 status 重置回 fixture 状态。
func ensurePlatformAdmin(ctx context.Context, conn *pgx.Conn, username, password string) error {
	hash, err := auth.HashPassword(password, auth.DefaultPasswordParams)
	if err != nil {
		return fmt.Errorf("hash platform_admin password: %w", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO users (username, password_hash, display_name, role, status)
		VALUES ($1, $2, $1, 'platform_admin', 'active')
		ON CONFLICT (username) WHERE org_id IS NULL DO UPDATE
			SET password_hash = EXCLUDED.password_hash,
			    role = 'platform_admin',
			    status = 'active',
			    updated_at = now()
	`, username, hash); err != nil {
		return fmt.Errorf("upsert platform_admin: %w", err)
	}
	return nil
}

// buildFixture 以裸 SQL 写入组织 / 节点 / 两个普通账号 / 一个应用 / 一个未绑定渠道。
//
// 与 plan 草稿的字段差异（以 internal/migrations 实际 schema 为准）：
//   - users / apps 的外键列叫 org_id（plan 写的是 organization_id）。
//   - runtime_nodes 文件 endpoint 列名是 agent_file_endpoint（单数），数据根目录列名是 node_data_root。
//   - apps.persona_mode 合法枚举为 'org_inherited'（plan 写的 'org_inherit' 会被 CHECK 拒绝）。
//   - apps.owner_user_id NOT NULL，必须挂在某个真实用户上，这里用 org_admin。
func buildFixture(ctx context.Context, conn *pgx.Conn) (fixture, error) {
	var fx fixture
	fx.PlatformAdminLogin = "admin"
	fx.PlatformAdminPassword = "admin123"

	// 0) 保证 platform_admin 行存在（truncate 之后行可能空）。
	if err := ensurePlatformAdmin(ctx, conn, fx.PlatformAdminLogin, fx.PlatformAdminPassword); err != nil {
		return fx, err
	}

	// 1) 创建组织。
	fx.OrgName = "e2e-org"
	fx.OrgCode = "test-org"
	if err := conn.QueryRow(ctx,
		`INSERT INTO organizations (name, code, status) VALUES ($1, $2, 'active') RETURNING id`,
		fx.OrgName,
		fx.OrgCode,
	).Scan(&fx.OrgID); err != nil {
		return fx, fmt.Errorf("create org: %w", err)
	}

	// 2) 创建 runtime_node：dummy endpoint，e2e 不真连 agent。
	//    bootstrap_token_hash / agent_token_hash 用占位符；status 直接置 active 跳过注册流程。
	fx.NodeName = "e2e-node"
	if err := conn.QueryRow(ctx, `
		INSERT INTO runtime_nodes
			(name, status, agent_docker_endpoint, agent_file_endpoint,
			 agent_token_hash, node_data_root, heartbeat_interval_seconds)
		VALUES ($1, 'active', 'http://127.0.0.1:9999', 'http://127.0.0.1:9999',
			 'placeholder', '/tmp/e2e-node', 60)
		RETURNING id`, fx.NodeName,
	).Scan(&fx.NodeID); err != nil {
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
		var id string
		if err := conn.QueryRow(ctx, `
			INSERT INTO users (username, password_hash, display_name, role, status, org_id)
			VALUES ($1, $2, $1, $3, 'active', $4)
			RETURNING id
		`, u.name, hash, u.role, fx.OrgID).Scan(&id); err != nil {
			return fx, fmt.Errorf("create user %s: %w", u.name, err)
		}
		if u.role == "org_admin" {
			orgAdminID = id
		}
	}

	// 4) 创建 fixture app（status=running，跳过实际容器路径）。owner_user_id 用 org_admin。
	fx.AppName = "e2e-app"
	if err := conn.QueryRow(ctx, `
		INSERT INTO apps
			(org_id, owner_user_id, runtime_node_id, name, status, persona_mode, api_key_status)
		VALUES ($1, $2, $3, $4, 'running', 'org_inherited', 'active')
		RETURNING id`, fx.OrgID, orgAdminID, fx.NodeID, fx.AppName,
	).Scan(&fx.AppID); err != nil {
		return fx, fmt.Errorf("create app: %w", err)
	}

	// 5) 创建对应 channel_binding（unbound 占位，留给 e2e 走绑定流程）。
	if _, err := conn.Exec(ctx, `
		INSERT INTO channel_bindings (app_id, channel_type, status)
		VALUES ($1, 'wechat', 'unbound')
	`, fx.AppID); err != nil {
		return fx, fmt.Errorf("create channel_binding: %w", err)
	}

	return fx, nil
}
