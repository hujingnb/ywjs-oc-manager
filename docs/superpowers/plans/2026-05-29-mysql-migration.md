# spec-C：Postgres → MySQL 8 迁移 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 oc-manager 的数据库从 PostgreSQL(pgx) 1:1 方言移植到自建 MySQL 8(database/sql)，保持现有全部功能与表结构不变。

**Architecture:** 纯方言移植——不删表、不改业务语义，只换 DB 引擎/驱动/类型。sqlc 引擎 `postgresql`→`mysql`，驱动 `pgx/v5`→`go-sql-driver/mysql`，30 个增量 migration 压成单个 MySQL 基线，UUID 由应用层生成存 `CHAR(36)`，可空列用 `guregu/null`。**节点表（runtime_nodes / *_resource_samples）在本 spec 保留**，它们的删除属于 workstream A（去节点概念），不在 spec-C 范围。

**Tech Stack:** Go、sqlc v1.30.0、go-sql-driver/mysql、guregu/null/v5、golang-migrate/v4(mysql driver)、google/uuid、testify、MySQL 8.0.

---

## 关键前提与全局约定

1. **全新库、无 ETL**：manager 与 new-api 都是空库；本计划只建 schema，不迁数据。
2. **类型映射约定**（决定所有 ~960 处改写与 helper）：
   - `uuid`(PG) → `CHAR(36)`(MySQL) → **Go `string`**（应用层 `uuid.NewString()` 生成）。
   - 非空 `text/varchar` → Go `string`；非空 `timestamptz`→`DATETIME(6)`→Go `time.Time`；非空 `bigint/int`→Go `int64/int32`；`jsonb`→`JSON`→Go `[]byte`（sqlc 默认 `json.RawMessage`）。
   - **可空列** → `guregu/null/v5`：`null.String`(text/varchar)、`null.Time`(datetime)、`null.Int`(bigint/int)、`null.Float`(double)。
3. **RETURNING 不可用**：sqlc MySQL 引擎不支持 `RETURNING`。统一改写：`INSERT/UPDATE` 用 `:exec`，需要回读整行的追加一个 `:one SELECT ... WHERE id = ?`（id 应用层已知）。
4. **执行纪律**：每个 Task 末尾 build + 相关测试 + commit；按 Conventional Commits，中文摘要。改 sqlc 注解后必须重跑 `make sqlc-generate`。
5. **不在 master 直接跑破坏性命令**；迁移命令通过 `make migrate-up/down`（docker 内）执行。

---

## 文件结构（创建 / 修改总览）

| 文件 | 动作 | 职责 |
|---|---|---|
| `go.mod` / `go.sum` | 改 | 加 go-sql-driver/mysql、guregu/null/v5；移除 pgx 直接依赖（最后一步） |
| `sqlc.yaml` | 改 | engine→mysql、schema→单基线、加类型 override |
| `internal/migrations/000001_baseline.up.sql` | 创建 | MySQL 8 单基线 schema（14 表） |
| `internal/migrations/000001_baseline.down.sql` | 创建 | DROP 全部表 |
| `internal/migrations/0000{02..30}_*.sql` | 删除 | 旧 PG 增量迁移（含 down） |
| `cmd/migrate/main.go` | 改 | postgres driver → mysql driver |
| `internal/store/store.go` | 改 | pgxpool → `*sql.DB`，WithTx 用 `*sql.Tx`，删死代码 `Pool()` |
| `internal/store/sqlc/**` | 重生成 | sqlc 输出（database/sql + 新类型） |
| `internal/store/queries/*.sql` | 改 | RETURNING/ON CONFLICT/JSON/cast 改写 |
| `internal/service/dbtype.go` | 创建（替换 `pgtype.go`） | string-UUID + guregu/null 转换 helper |
| `internal/service/pgtype.go` | 删除 | 被 dbtype.go 取代 |
| 37 个使用 `pgtype.*` 的 .go 文件 | 改 | 类型转换点替换（详见 Task 8 清单） |
| `config/manager.example.yaml`、`config/manager.yaml` | 改 | `database.url` 改 MySQL DSN |
| `Makefile`、`docker-compose.yml` | 改 | manager-postgres→manager-mysql；INTEGRATION_DATABASE_URL 改 mysql |

---

## Task 1：添加依赖与 sqlc 配置切换

**Files:**
- Modify: `go.mod`
- Modify: `sqlc.yaml`

- [ ] **Step 1：添加 MySQL 驱动与 null 库依赖**

Run:
```bash
go get github.com/go-sql-driver/mysql@latest
go get github.com/guregu/null/v5@latest
```
Expected: `go.mod` 出现这两行；`go-sql-driver/mysql` 与 `guregu/null/v5`。

- [ ] **Step 2：改写 sqlc.yaml 为 MySQL 引擎 + 单基线 + 类型 override**

`sqlc.yaml` 全文替换为：
```yaml
version: "2"
sql:
  - engine: mysql
    schema:
      - internal/migrations/000001_baseline.up.sql
    queries: internal/store/queries
    gen:
      go:
        package: sqlc
        out: internal/store/sqlc
        emit_json_tags: true
        emit_db_tags: true
        emit_empty_slices: true
        emit_interface: true
        overrides:
          # UUID 列存 CHAR(36)，Go 侧用 string（应用层生成）
          - db_type: "char"
            go_type: "string"
          - db_type: "char"
            nullable: true
            go_type: "github.com/guregu/null/v5.String"
          # 可空文本
          - db_type: "varchar"
            nullable: true
            go_type: "github.com/guregu/null/v5.String"
          - db_type: "text"
            nullable: true
            go_type: "github.com/guregu/null/v5.String"
          # 可空时间
          - db_type: "datetime"
            nullable: true
            go_type: "github.com/guregu/null/v5.Time"
          # 可空整数
          - db_type: "bigint"
            nullable: true
            go_type: "github.com/guregu/null/v5.Int"
          - db_type: "int"
            nullable: true
            go_type: "github.com/guregu/null/v5.Int"
          # 可空浮点（资源采样 cpu_percent 等）
          - db_type: "double"
            nullable: true
            go_type: "github.com/guregu/null/v5.Float"
```
注意：`sql_package: pgx/v5` 已删除（MySQL 引擎默认 database/sql）。`db_type` 名称以 sqlc 解析 MySQL DDL 的内部名为准；首次 `sqlc generate` 若报类型未覆盖，按报错补 override（如 `char` 长度形式 `char(36)`）。

- [ ] **Step 3：先不 generate（schema 文件尚未创建）**

本 Task 不跑 sqlc——基线 DDL 在 Task 2 创建后，Task 5 才 generate。本步仅确认配置语法。
Run: `go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0 version`
Expected: 打印 `v1.30.0`，不报 yaml 解析错。

- [ ] **Step 4：Commit**
```bash
git add go.mod go.sum sqlc.yaml
git commit -m "chore(db): 引入 MySQL 驱动与 guregu/null，sqlc 切到 mysql 引擎"
```

---

## Task 2：编写 MySQL 8 单基线 schema

**Files:**
- Create: `internal/migrations/000001_baseline.up.sql`
- Create: `internal/migrations/000001_baseline.down.sql`
- Delete: `internal/migrations/0000{01..30}_*.up.sql` 与 `*.down.sql`（保留新建的 000001_baseline）

> **关键修正点**（相对朴素翻译）：
> - **MySQL 不支持部分索引**。单列 `WHERE col IS NOT NULL` 的唯一索引退化为普通 `UNIQUE`（NULL 在唯一索引里互不冲突，语义等价）；复合 `(a,b) WHERE a IS NOT NULL` 也用普通 `UNIQUE(a,b)`（同理）；**带业务条件**（`WHERE deleted_at IS NULL` / `status<>'deleted'` / `scope_type='org'`）的唯一约束改用**STORED 生成列 + 唯一索引**。非唯一部分索引退化为普通索引。
> - **JSON 默认值**必须用表达式 `DEFAULT (...)`，不能写裸字面量。
> - **外键前向引用**：用 `SET FOREIGN_KEY_CHECKS` 包裹，避免建表顺序问题。

- [ ] **Step 1：写 `000001_baseline.up.sql`**

```sql
-- OC-Manager MySQL 8 基线 schema（由 30 个 PG 增量迁移合并而成）
-- 字符集 utf8mb4 / utf8mb4_0900_ai_ci，引擎 InnoDB。
SET FOREIGN_KEY_CHECKS = 0;

-- 组织租户表
CREATE TABLE organizations (
    id CHAR(36) PRIMARY KEY COMMENT '组织 ID',
    name VARCHAR(255) NOT NULL UNIQUE COMMENT '组织名称',
    status VARCHAR(50) NOT NULL DEFAULT 'active' COMMENT '组织状态',
    contact_name VARCHAR(255) NULL,
    contact_phone VARCHAR(50) NULL,
    remark TEXT NULL,
    newapi_user_id VARCHAR(255) NULL,
    credit_warning_threshold INT NULL,
    newapi_user_credentials_ciphertext TEXT NULL,
    code VARCHAR(32) NOT NULL UNIQUE COMMENT '组织代码，登录命名空间',
    newapi_username VARCHAR(255) NULL,
    assistant_version_ids JSON NOT NULL DEFAULT (JSON_ARRAY()) COMMENT '可用助手版本 ID allowlist',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    CONSTRAINT organizations_status_check CHECK (status IN ('active','disabled','deleted')),
    CONSTRAINT organizations_credit_warning_threshold_check CHECK (
        credit_warning_threshold IS NULL OR (credit_warning_threshold >= 0 AND credit_warning_threshold <= 100)),
    CONSTRAINT organizations_code_format_check CHECK (code REGEXP '^[a-z0-9]([a-z0-9-]{1,30}[a-z0-9])$'),
    KEY idx_organizations_status_name (status, name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 用户表（platform_username_key 生成列替代 PG 部分唯一索引 WHERE org_id IS NULL）
CREATE TABLE users (
    id CHAR(36) PRIMARY KEY,
    org_id CHAR(36) NULL COMMENT '所属组织（平台管理员为空）',
    username VARCHAR(255) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    display_name VARCHAR(255) NOT NULL,
    role VARCHAR(50) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    last_login_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL COMMENT '下线时间',
    platform_username_key VARCHAR(255) GENERATED ALWAYS AS (CASE WHEN org_id IS NULL THEN username END) STORED,
    CONSTRAINT users_role_check CHECK (role IN ('platform_admin','org_admin','org_member')),
    CONSTRAINT users_status_check CHECK (status IN ('active','disabled')),
    CONSTRAINT users_platform_org_check CHECK (
        (role = 'platform_admin' AND org_id IS NULL)
        OR (role IN ('org_admin','org_member') AND org_id IS NOT NULL)),
    CONSTRAINT fk_users_org_id FOREIGN KEY (org_id) REFERENCES organizations(id) ON DELETE CASCADE,
    UNIQUE KEY uk_users_org_username (org_id, username),
    UNIQUE KEY uk_users_platform_username (platform_username_key),
    KEY idx_users_org_role_status (org_id, role, status),
    KEY idx_users_active (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 运行节点表（spec-C 保留；workstream A 删除）
CREATE TABLE runtime_nodes (
    id CHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    agent_docker_endpoint VARCHAR(500) NULL,
    agent_file_endpoint VARCHAR(500) NULL,
    agent_tls_ca_cert TEXT NULL,
    agent_token_hash VARCHAR(255) NULL,
    agent_token_ciphertext TEXT NULL,
    agent_version VARCHAR(100) NULL,
    heartbeat_interval_seconds INT NOT NULL DEFAULT 30,
    last_heartbeat_at DATETIME(6) NULL,
    resource_snapshot_json JSON NULL,
    metadata_json JSON NULL,
    node_data_root VARCHAR(500) NULL,
    registered_at DATETIME(6) NULL,
    max_apps INT NULL,
    agent_id VARCHAR(255) NULL UNIQUE,
    last_probe_attempted_at DATETIME(6) NULL,
    last_probe_ok_at DATETIME(6) NULL,
    last_probe_failed_at DATETIME(6) NULL,
    last_probe_error VARCHAR(255) NULL,
    probe_failure_streak INT NOT NULL DEFAULT 0,
    probe_success_streak INT NOT NULL DEFAULT 0,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    CONSTRAINT runtime_nodes_status_check CHECK (status IN ('pending','active','unreachable','disabled','degraded')),
    CONSTRAINT runtime_nodes_heartbeat_interval_check CHECK (heartbeat_interval_seconds > 0),
    KEY idx_runtime_nodes_status_last_heartbeat (status, last_heartbeat_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 助手版本表（apps 外键引用，故先于 apps 建）
CREATE TABLE assistant_versions (
    id CHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT NOT NULL,
    system_prompt TEXT NOT NULL,
    image_id VARCHAR(255) NOT NULL,
    main_model VARCHAR(255) NOT NULL,
    routing_json JSON NOT NULL DEFAULT (JSON_OBJECT()),
    skills_json JSON NOT NULL DEFAULT (JSON_ARRAY()),
    revision INT NOT NULL DEFAULT 1,
    created_by CHAR(36) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    name_active_key VARCHAR(255) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN name END) STORED,
    CONSTRAINT assistant_versions_revision_check CHECK (revision > 0),
    CONSTRAINT fk_assistant_versions_created_by FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL,
    UNIQUE KEY uk_assistant_versions_name_active (name_active_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 应用表（owner_active_key / runtime_token_active_key 生成列替代部分唯一索引）
CREATE TABLE apps (
    id CHAR(36) PRIMARY KEY,
    org_id CHAR(36) NOT NULL,
    owner_user_id CHAR(36) NOT NULL,
    runtime_node_id CHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'draft',
    container_id VARCHAR(255) NULL,
    container_name VARCHAR(255) NULL,
    newapi_key_id VARCHAR(255) NULL,
    newapi_key_ciphertext TEXT NULL,
    api_key_status VARCHAR(50) NOT NULL DEFAULT 'pending',
    runtime_snapshot_json JSON NULL,
    runtime_snapshot_at DATETIME(6) NULL,
    restart_policy_json JSON NOT NULL
        DEFAULT (CAST('{"mode":"on_failure","max_per_window":5,"window_seconds":600}' AS JSON)),
    health_state_json JSON NULL,
    progress_current BIGINT NULL,
    progress_total BIGINT NULL,
    last_error_status VARCHAR(50) NULL,
    last_error_message TEXT NULL,
    runtime_image_ref VARCHAR(500) NOT NULL DEFAULT '',
    runtime_image_sha256 VARCHAR(255) NOT NULL DEFAULT '',
    newapi_key_name VARCHAR(255) NULL,
    version_id CHAR(36) NULL,
    applied_version_revision INT NOT NULL DEFAULT 0,
    applied_image_ref VARCHAR(500) NOT NULL DEFAULT '',
    runtime_token_hash VARCHAR(255) NULL,
    runtime_token_ciphertext TEXT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    owner_active_key CHAR(36) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN owner_user_id END) STORED,
    runtime_token_active_key VARCHAR(255) GENERATED ALWAYS AS (
        CASE WHEN runtime_token_hash IS NOT NULL AND deleted_at IS NULL THEN runtime_token_hash END) STORED,
    CONSTRAINT apps_status_check CHECK (status IN (
        'draft','pulling_runtime_image','pulling_image','syncing_image','preparing_runtime',
        'creating_container','starting','binding_waiting','binding_failed',
        'running','stopped','error','deleted')),
    CONSTRAINT apps_api_key_status_check CHECK (api_key_status IN ('pending','active','disabled','error')),
    CONSTRAINT apps_runtime_token_pair_check CHECK (
        (runtime_token_hash IS NULL AND runtime_token_ciphertext IS NULL)
        OR (runtime_token_hash IS NOT NULL AND runtime_token_ciphertext IS NOT NULL)),
    CONSTRAINT fk_apps_org_id FOREIGN KEY (org_id) REFERENCES organizations(id) ON DELETE CASCADE,
    CONSTRAINT fk_apps_owner_user_id FOREIGN KEY (owner_user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT fk_apps_runtime_node_id FOREIGN KEY (runtime_node_id) REFERENCES runtime_nodes(id) ON DELETE CASCADE,
    CONSTRAINT fk_apps_version_id FOREIGN KEY (version_id) REFERENCES assistant_versions(id) ON DELETE SET NULL,
    UNIQUE KEY uk_apps_owner_active (owner_active_key),
    UNIQUE KEY uk_apps_runtime_token_hash_active (runtime_token_active_key),
    KEY idx_apps_org_active_created (org_id, deleted_at, created_at DESC),
    KEY idx_apps_runtime_node_status (runtime_node_id, status),
    KEY idx_apps_newapi_key_id (newapi_key_id),
    KEY idx_apps_version_id (version_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 渠道绑定表（app_active_key 生成列替代 WHERE status<>'deleted' 唯一索引）
CREATE TABLE channel_bindings (
    id CHAR(36) PRIMARY KEY,
    app_id CHAR(36) NOT NULL,
    channel_type VARCHAR(50) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'unbound',
    bound_identity VARCHAR(255) NULL,
    channel_name VARCHAR(255) NULL,
    metadata_json JSON NULL,
    bound_at DATETIME(6) NULL,
    last_online_at DATETIME(6) NULL,
    last_error TEXT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    app_active_key CHAR(36) GENERATED ALWAYS AS (CASE WHEN status <> 'deleted' THEN app_id END) STORED,
    CONSTRAINT channel_bindings_status_check CHECK (status IN (
        'unbound','pending_auth','bound','failed','expired','unbound_by_user','deleted')),
    CONSTRAINT channel_bindings_channel_type_check CHECK (channel_type IN ('wechat')),
    CONSTRAINT fk_channel_bindings_app_id FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE,
    UNIQUE KEY uk_channel_bindings_app_active (app_active_key),
    KEY idx_channel_bindings_app_channel_status (app_id, channel_type, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 充值记录表
CREATE TABLE recharge_records (
    id CHAR(36) PRIMARY KEY,
    org_id CHAR(36) NOT NULL,
    operator_id CHAR(36) NOT NULL,
    credit_amount BIGINT NOT NULL,
    remark TEXT NULL,
    newapi_ref_id VARCHAR(255) NULL,
    status VARCHAR(50) NOT NULL,
    error_message TEXT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT recharge_records_credit_amount_check CHECK (credit_amount > 0),
    CONSTRAINT recharge_records_status_check CHECK (status IN ('succeeded','failed')),
    CONSTRAINT fk_recharge_records_org_id FOREIGN KEY (org_id) REFERENCES organizations(id) ON DELETE CASCADE,
    CONSTRAINT fk_recharge_records_operator_id FOREIGN KEY (operator_id) REFERENCES users(id) ON DELETE CASCADE,
    KEY idx_recharge_records_org_created (org_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 异步任务表
CREATE TABLE jobs (
    id CHAR(36) PRIMARY KEY,
    type VARCHAR(100) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    priority INT NOT NULL DEFAULT 0,
    run_after DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 5,
    payload_json JSON NOT NULL DEFAULT (JSON_OBJECT()),
    locked_by VARCHAR(255) NULL,
    locked_at DATETIME(6) NULL,
    last_error TEXT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    finished_at DATETIME(6) NULL,
    CONSTRAINT jobs_status_check CHECK (status IN ('pending','running','succeeded','failed','canceled')),
    CONSTRAINT jobs_attempts_check CHECK (attempts >= 0),
    CONSTRAINT jobs_max_attempts_check CHECK (max_attempts > 0),
    CONSTRAINT jobs_type_check CHECK (type IN (
        'app_initialize','app_start_container','app_stop_container','app_restart_container','app_delete',
        'channel_start_login','channel_check_binding','runtime_node_health_reconcile','runtime_refresh_status',
        'app_health_check','newapi_disable_key','newapi_restore_key','workspace_archive_cleanup')),
    KEY idx_jobs_status_run_after_priority (status, run_after, priority)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 审计日志表
CREATE TABLE audit_logs (
    id CHAR(36) PRIMARY KEY,
    actor_id CHAR(36) NULL,
    actor_role VARCHAR(50) NOT NULL,
    org_id CHAR(36) NULL,
    target_type VARCHAR(100) NOT NULL,
    target_id VARCHAR(255) NOT NULL,
    action VARCHAR(100) NOT NULL,
    result VARCHAR(50) NOT NULL,
    error_message TEXT NULL,
    ip_address VARCHAR(45) NULL,
    metadata_json JSON NULL,
    detail_message TEXT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT audit_logs_actor_role_check CHECK (actor_role IN ('system','platform_admin','org_admin','org_member')),
    CONSTRAINT audit_logs_result_check CHECK (result IN ('succeeded','failed')),
    CONSTRAINT fk_audit_logs_actor_id FOREIGN KEY (actor_id) REFERENCES users(id) ON DELETE SET NULL,
    CONSTRAINT fk_audit_logs_org_id FOREIGN KEY (org_id) REFERENCES organizations(id) ON DELETE CASCADE,
    KEY idx_audit_logs_org_created (org_id, created_at),
    KEY idx_audit_logs_target_created (target_type, target_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 刷新令牌表
CREATE TABLE refresh_tokens (
    id CHAR(36) PRIMARY KEY,
    user_id CHAR(36) NOT NULL,
    token_hash VARCHAR(255) NOT NULL UNIQUE,
    expires_at DATETIME(6) NOT NULL,
    revoked_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT fk_refresh_tokens_user_id FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    KEY idx_refresh_tokens_user_expires (user_id, expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- RAGFlow 数据集映射表（org_scope_key/app_scope_key 生成列替代部分唯一索引；remote 单列 WHERE 退化为普通 UNIQUE）
CREATE TABLE ragflow_datasets (
    id CHAR(36) PRIMARY KEY,
    scope_type VARCHAR(50) NOT NULL,
    org_id CHAR(36) NOT NULL,
    app_id CHAR(36) NULL,
    ragflow_dataset_id VARCHAR(255) NULL,
    name VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    last_error TEXT NULL,
    create_claim_token VARCHAR(255) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    org_scope_key CHAR(36) GENERATED ALWAYS AS (CASE WHEN scope_type = 'org' THEN org_id END) STORED,
    app_scope_key CHAR(36) GENERATED ALWAYS AS (CASE WHEN scope_type = 'app' THEN app_id END) STORED,
    CONSTRAINT ragflow_datasets_scope_type_check CHECK (scope_type IN ('org','app')),
    CONSTRAINT ragflow_datasets_status_check CHECK (status IN ('creating','active','deleting','failed')),
    CONSTRAINT ragflow_datasets_scope_app_check CHECK (
        (scope_type = 'org' AND app_id IS NULL) OR (scope_type = 'app' AND app_id IS NOT NULL)),
    CONSTRAINT fk_ragflow_datasets_org_id FOREIGN KEY (org_id) REFERENCES organizations(id) ON DELETE CASCADE,
    CONSTRAINT fk_ragflow_datasets_app_id FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE,
    UNIQUE KEY uk_ragflow_datasets_scope_identity (id, scope_type, org_id),
    UNIQUE KEY uk_ragflow_datasets_app_identity (id, scope_type, org_id, app_id),
    UNIQUE KEY uk_ragflow_datasets_org_unique (org_scope_key),
    UNIQUE KEY uk_ragflow_datasets_app_unique (app_scope_key),
    UNIQUE KEY uk_ragflow_datasets_remote_unique (ragflow_dataset_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- RAGFlow 文档元数据缓存表
CREATE TABLE ragflow_documents (
    id CHAR(36) PRIMARY KEY,
    dataset_id CHAR(36) NOT NULL,
    scope_type VARCHAR(50) NOT NULL,
    org_id CHAR(36) NOT NULL,
    app_id CHAR(36) NULL,
    ragflow_document_id VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    size_bytes BIGINT NOT NULL DEFAULT 0,
    mime_type VARCHAR(100) NULL,
    suffix VARCHAR(50) NULL,
    parse_status VARCHAR(50) NOT NULL DEFAULT 'queued',
    progress INT NOT NULL DEFAULT 0,
    last_error TEXT NULL,
    created_by VARCHAR(255) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    CONSTRAINT ragflow_documents_scope_type_check CHECK (scope_type IN ('org','app')),
    CONSTRAINT ragflow_documents_parse_status_check CHECK (parse_status IN ('queued','running','completed','failed','stopped')),
    CONSTRAINT ragflow_documents_progress_check CHECK (progress >= 0 AND progress <= 100),
    CONSTRAINT ragflow_documents_scope_app_check CHECK (
        (scope_type = 'org' AND app_id IS NULL) OR (scope_type = 'app' AND app_id IS NOT NULL)),
    CONSTRAINT fk_ragflow_documents_dataset_scope FOREIGN KEY (dataset_id, scope_type, org_id)
        REFERENCES ragflow_datasets(id, scope_type, org_id) ON DELETE CASCADE,
    CONSTRAINT fk_ragflow_documents_org_id FOREIGN KEY (org_id) REFERENCES organizations(id) ON DELETE CASCADE,
    CONSTRAINT fk_ragflow_documents_app_id FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE,
    UNIQUE KEY uk_ragflow_documents_dataset_remote (dataset_id, ragflow_document_id),
    KEY idx_ragflow_documents_scope (scope_type, org_id, app_id, created_at DESC),
    KEY idx_ragflow_documents_parse_status (parse_status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 节点资源采样表（spec-C 保留；workstream A 删除）
CREATE TABLE node_resource_samples (
    id CHAR(36) PRIMARY KEY,
    runtime_node_id CHAR(36) NOT NULL,
    sampled_at DATETIME(6) NOT NULL,
    cpu_percent DOUBLE NULL,
    memory_used_bytes BIGINT NULL,
    memory_total_bytes BIGINT NULL,
    disk_used_bytes BIGINT NULL,
    disk_total_bytes BIGINT NULL,
    network_rx_bytes BIGINT NULL,
    network_tx_bytes BIGINT NULL,
    instance_count INT NULL,
    last_error TEXT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT fk_node_resource_samples_node FOREIGN KEY (runtime_node_id) REFERENCES runtime_nodes(id) ON DELETE CASCADE,
    KEY node_resource_samples_node_time_idx (runtime_node_id, sampled_at DESC),
    KEY node_resource_samples_sampled_at_idx (sampled_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 实例资源采样表（spec-C 保留；workstream A 删除）
CREATE TABLE instance_resource_samples (
    id CHAR(36) PRIMARY KEY,
    app_id CHAR(36) NOT NULL,
    runtime_node_id CHAR(36) NOT NULL,
    container_id VARCHAR(255) NOT NULL,
    sampled_at DATETIME(6) NOT NULL,
    container_status VARCHAR(50) NULL,
    cpu_percent DOUBLE NULL,
    memory_used_bytes BIGINT NULL,
    memory_limit_bytes BIGINT NULL,
    disk_read_bytes BIGINT NULL,
    disk_write_bytes BIGINT NULL,
    network_rx_bytes BIGINT NULL,
    network_tx_bytes BIGINT NULL,
    last_error TEXT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT fk_instance_resource_samples_app FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE,
    CONSTRAINT fk_instance_resource_samples_node FOREIGN KEY (runtime_node_id) REFERENCES runtime_nodes(id) ON DELETE CASCADE,
    KEY instance_resource_samples_app_time_idx (app_id, sampled_at DESC),
    KEY instance_resource_samples_node_time_idx (runtime_node_id, sampled_at DESC),
    KEY instance_resource_samples_node_app_time_idx (runtime_node_id, app_id, sampled_at DESC),
    KEY instance_resource_samples_sampled_at_idx (sampled_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

SET FOREIGN_KEY_CHECKS = 1;
```

- [ ] **Step 2：写 `000001_baseline.down.sql`**
```sql
SET FOREIGN_KEY_CHECKS = 0;
DROP TABLE IF EXISTS instance_resource_samples;
DROP TABLE IF EXISTS node_resource_samples;
DROP TABLE IF EXISTS ragflow_documents;
DROP TABLE IF EXISTS ragflow_datasets;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS recharge_records;
DROP TABLE IF EXISTS channel_bindings;
DROP TABLE IF EXISTS apps;
DROP TABLE IF EXISTS assistant_versions;
DROP TABLE IF EXISTS runtime_nodes;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS organizations;
SET FOREIGN_KEY_CHECKS = 1;
```

- [ ] **Step 3：删除旧 PG 迁移文件**
```bash
cd internal/migrations
git rm 000001_init.up.sql 000001_init.down.sql 000002_*.sql 000003_*.sql 000004_*.sql 000005_*.sql 000006_*.sql 000007_*.sql 000008_*.sql 000010_*.sql 000011_*.sql 000012_*.sql 000013_*.sql 000014_*.sql 000015_*.sql 000016_*.sql 000017_*.sql 000018_*.sql 000019_*.sql 000020_*.sql 000021_*.sql 000022_*.sql 000023_*.sql 000024_*.sql 000025_*.sql 000026_*.sql 000027_*.sql 000028_*.sql 000029_*.sql 000030_*.sql
```
Expected: 仅剩 `000001_baseline.up.sql` / `000001_baseline.down.sql` 与 `migrations.go`。

- [ ] **Step 4：验证 DDL 可在真实 MySQL 8 建库（关键）**
```bash
docker run --rm -d --name ddlcheck -e MYSQL_ROOT_PASSWORD=root -e MYSQL_DATABASE=ocm -p 13310:3306 mysql:8.0
# 等待就绪
until docker exec ddlcheck mysqladmin ping -proot --silent 2>/dev/null; do sleep 2; done
docker exec -i ddlcheck mysql -uroot -proot ocm < internal/migrations/000001_baseline.up.sql
docker exec ddlcheck mysql -uroot -proot ocm -e "SHOW TABLES;"
docker rm -f ddlcheck
```
Expected: 无报错，`SHOW TABLES` 列出 14 张表。若报错（如生成列/CHECK/JSON 默认值语法），就地修正 up.sql 再重跑本步——**这是 DDL 正确性的硬验证，必须绿了才继续**。

- [ ] **Step 5：Commit**
```bash
git add internal/migrations/
git commit -m "feat(db): 新增 MySQL 8 单基线 schema，移除 PG 增量迁移

将 30 个 PostgreSQL 增量迁移合并为最终态的 MySQL 8 基线；部分唯一索引改用
STORED 生成列、JSON 默认值改表达式、外键创建用 FOREIGN_KEY_CHECKS 包裹。保留
runtime_nodes 与资源采样表（其删除属 workstream A）。"
```

---

## Task 3：migrate 命令切换到 MySQL driver

**Files:**
- Modify: `cmd/migrate/main.go:12`

- [ ] **Step 1：替换 database driver import**

`cmd/migrate/main.go` 把
```go
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
```
改为
```go
	_ "github.com/golang-migrate/migrate/v4/database/mysql"
```
其余不变（golang-migrate 的 mysql driver 接受 `mysql://user:pass@tcp(host:port)/db?...` 形式的 URL，由 `databaseURL` 提供）。

- [ ] **Step 2：编译**

Run: `go build ./cmd/migrate`
Expected: 编译通过。

- [ ] **Step 3：Commit**
```bash
git add cmd/migrate/main.go go.mod go.sum
git commit -m "chore(db): cmd/migrate 切换到 golang-migrate 的 mysql driver"
```

---

## Task 4：store.go 改写（pgxpool → database/sql）

**Files:**
- Modify: `internal/store/store.go`

- [ ] **Step 1：整体替换 store.go**
```go
package store

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/go-sql-driver/mysql"

	"oc-manager/internal/store/sqlc"
)

// Store 封装数据库连接和 sqlc 查询入口。
type Store struct {
	db      *sql.DB
	Queries *sqlc.Queries
}

// Open 用 MySQL DSN 创建连接池。databaseURL 形如
// "mysql://user:pass@tcp(host:3306)/ocm?parseTime=true&loc=UTC"；
// database/sql 的 mysql driver 需要去掉 "mysql://" 前缀的 DSN，故在此剥离。
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	dsn := databaseURL
	if len(dsn) > len("mysql://") && dsn[:len("mysql://")] == "mysql://" {
		dsn = dsn[len("mysql://"):]
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库连接失败: %w", err)
	}
	return New(db), nil
}

// New 用已有连接创建 Store，用于启动组装和测试注入。
func New(db *sql.DB) *Store {
	return &Store{db: db, Queries: sqlc.New(db)}
}

// Close 关闭数据库连接。
func (s *Store) Close() {
	if s == nil || s.db == nil {
		return
	}
	_ = s.db.Close()
}

// WithTx 在单个事务中执行 fn；fn 返回错误回滚，否则提交。
func (s *Store) WithTx(ctx context.Context, fn func(*sqlc.Queries) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启数据库事务失败: %w", err)
	}
	if err := fn(s.Queries.WithTx(tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交数据库事务失败: %w", err)
	}
	return nil
}
```
说明：删除了死代码 `Pool()`（全仓无调用点）。`sqlc.New(db)` 与 `Queries.WithTx(tx)` 是 sqlc MySQL 生成的接口，签名将在 Task 5 generate 后对齐。

- [ ] **Step 2：暂不编译**（sqlc 尚未重生成，编译会断；留到 Task 5 之后整体过编译）。

- [ ] **Step 3：Commit**
```bash
git add internal/store/store.go
git commit -m "refactor(db): store 改用 database/sql + mysql 驱动，删除死代码 Pool()"
```

---

## Task 5：重生成 sqlc 并确认类型基线

**Files:**
- Regenerate: `internal/store/sqlc/**`

- [ ] **Step 1：临时把含 RETURNING 的查询标记为待改（让 generate 能跑通）**

sqlc MySQL 引擎遇到 `RETURNING` 会报错。为先看到类型变化，可先跑一次 generate 观察报错清单（不致命，用于建立工作清单）：
Run: `make sqlc-generate`
Expected: 报告一批 “RETURNING not supported” / 类型未覆盖错误。**记录全部报错**作为 Task 6/Task 1-override 的工作清单。

- [ ] **Step 2：完成 Task 6 的查询改写后再次 generate**

（先做 Task 6，再回到这里）
Run: `make sqlc-generate`
Expected: 成功生成，无报错。`internal/store/sqlc/models.go` 中类型变为：UUID 列 `string`、可空列 `null.String/null.Time/null.Int/null.Float`、时间 `time.Time`、JSON `json.RawMessage`。

- [ ] **Step 3：Commit（generate 产物）**
```bash
git add internal/store/sqlc/
git commit -m "chore(db): 重生成 sqlc MySQL 代码（database/sql + guregu/null + string UUID）"
```

---

## Task 6：查询改写（RETURNING / ON CONFLICT / JSON / cast）

**Files（全部 Modify）:** `internal/store/queries/*.sql`

**统一改写模式：**

1. **`:one INSERT ... RETURNING *`** → 拆成两条：
```sql
-- name: CreateXxx :exec
INSERT INTO xxx (id, ...) VALUES (?, ...);

-- name: GetXxxByID :one
SELECT * FROM xxx WHERE id = ?;
```
service 层先 `CreateXxx`（传应用层生成的 id）再 `GetXxxByID(id)` 回读。

2. **`:one UPDATE ... RETURNING *`** → 改 `:exec` + 复用/新增对应 `:one SELECT`（按其 WHERE 主键回读）。

3. **`ON CONFLICT (...) DO NOTHING RETURNING *`** → `INSERT IGNORE`（`:exec`）+ 回读 SELECT。
   **`ON CONFLICT (...) DO UPDATE ...`** → `INSERT ... ON DUPLICATE KEY UPDATE ...`（`:exec`）+ 回读。

4. **JSON / cast / 关键字**：
   - `payload_json->>'app_id'` → `payload_json->>'$.app_id'`（MySQL path 语法）。
   - `jsonb_exists(col, $1)` → `JSON_CONTAINS(col, JSON_QUOTE(?))`（数组成员判断）。
   - `::text`/`::uuid`/`::timestamptz` cast 一律删除；`::bigint` 用 `CAST(x AS SIGNED)` 或删除。
   - `ANY(sqlc.arg(ids)::uuid[])` → `IN (sqlc.slice(ids))`。
   - `ILIKE` → `LIKE`；`a || b` 字符串拼接 → `CONCAT(a,b)`。
   - `count(*)::bigint` → `COUNT(*)`（MySQL 默认 BIGINT）。
   - `a.id::text = al.target_id` → `a.id = al.target_id`（CHAR(36) 与 VARCHAR 直接可比）。
   - `FOR UPDATE`、`CASE WHEN` 原样保留（MySQL 兼容）。

**完整改写清单（按文件，共 59 处 RETURNING）：**

> runtime_nodes.sql、resource_samples.sql 在 spec-C **保留并改写**（节点删除属 workstream A）。下方“:exec+回读”指上面的模式 1/2。

| 文件 | 需改 query | 改写 |
|---|---|---|
| `apps.sql` | CreateApp、SetAppStatus、SetAppContainer、SetAppNewAPIKey、SetAppRuntimeToken、SoftDeleteApp、SetAppRuntimeSnapshot、SetAppRestartPolicy、SetAppHealthState、SetAppProgress、ClearAppProgress、MarkAppFailed、UpdateAppRuntimeImage、SetAppAppliedVersion、SetAppVersion（15 处 RETURNING） | 全部 `:exec` + 回读 `GetApp :one`（已存在则复用）。`sqlc.embed(apps)` 保留 |
| `users.sql` | CreateUser、SetUserStatus、UpdateUserProfile、UpdateUserPassword、MarkUserLoggedIn（5） | `:exec` + `GetUserByID :one` 回读 |
| `organizations.sql` | CreateOrganization、SetOrganizationNewAPIUser、UpdateOrganizationProfile、SetOrganizationStatus、SoftDeleteOrganization、UpdateOrganizationCredentialsCiphertext（6） | `:exec` + `GetOrganization :one` 回读；`FOR UPDATE` 保留 |
| `ragflow_knowledge.sql` | 7 RETURNING + 2 ON CONFLICT | Create*DatasetMapping 用 `INSERT IGNORE` + 回读；其余 `:exec` + 回读；删 `::text`/`::uuid` cast；`ILIKE`→`LIKE`+`CONCAT` |
| `jobs.sql` | CreateJob、MarkJobRunning、MarkJobSucceeded、MarkJobFailed、RetryJob、RequeueJob（6） | `:exec` + `GetJob :one` 回读；`payload_json->>'app_id'`→`payload_json->>'$.app_id'`，删 `::text` |
| `assistant_versions.sql` | CreateAssistantVersion、UpdateAssistantVersion、UpdateAssistantVersionSkills、SoftDeleteAssistantVersion（4） | `:exec` + 回读；`jsonb_exists(assistant_version_ids, $1)`→`JSON_CONTAINS(assistant_version_ids, JSON_QUOTE(?))` |
| `channel_bindings.sql` | CreateChannelBinding、SetChannelBindingStatus、SetChannelBindingChallenge、MarkChannelBindingBound（4） | `:exec` + `GetChannelBindingByAppAndType :one` 回读 |
| `organizations.sql`/`apps.sql` 中 `assistant_version_ids` JSON 包含判断 | 同上 jsonb→JSON_CONTAINS | |
| `recharge_records.sql` | CreateRechargeRecord（1） | `:exec` + 回读；`SUM(...)::bigint`→`SUM(...)` |
| `refresh_tokens.sql` | CreateRefreshToken、RevokeRefreshToken（2） | `:exec` + 回读 |
| `audit_logs.sql` | CreateAuditLog（1） | `:exec` + 回读；删除 `a.id::text` cast |
| `runtime_nodes.sql` | Enroll*Insert/Update、UpdateRuntimeNodeHeartbeat、UpdateRuntimeNodeProbeSuccess/Failure、SetRuntimeNodeStatus（6） | `:exec` + `GetRuntimeNode :one` 回读 |
| `resource_samples.sql` | InsertNodeResourceSample、InsertInstanceResourceSample（2 RETURNING） | `:exec`（采样插入无需回读，去掉 RETURNING 即可）；`DISTINCT ON`→`ROW_NUMBER() OVER (PARTITION BY ... ORDER BY sampled_at DESC)` 子查询取 rn=1；`ANY(...::uuid[])`→`IN (sqlc.slice(...))`；`extract(epoch FROM x)`→`UNIX_TIMESTAMP(x)`、`to_timestamp(...)`→`FROM_UNIXTIME(...)`；`array_agg`→`GROUP_CONCAT` |
| `platform_overview.sql` | 0 RETURNING | 仅 `count(*)::bigint`→`COUNT(*)`（可选） |

**两个完整的 worked example（按此套用其余）：**

- [ ] **Step 1（示例 A：users.sql 的 CreateUser）**

把
```sql
-- name: CreateUser :one
INSERT INTO users (id, org_id, username, password_hash, display_name, role, status)
VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6)
RETURNING *;
```
改为
```sql
-- name: CreateUser :exec
INSERT INTO users (id, org_id, username, password_hash, display_name, role, status)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;
```
（id 由 service 用 `uuid.NewString()` 传入，见 Task 9。`GetUserByID` 若已存在则不重复定义。）

- [ ] **Step 2（示例 B：ragflow_knowledge.sql 的 ON CONFLICT）**

把
```sql
-- name: CreateRAGFlowOrgDatasetMapping :one
INSERT INTO ragflow_datasets (id, scope_type, org_id, name, status, create_claim_token)
VALUES (gen_random_uuid(), 'org', $1, $2, 'creating', sqlc.arg(create_claim_token)::text)
ON CONFLICT (org_id) WHERE scope_type = 'org' DO NOTHING
RETURNING *;
```
改为
```sql
-- name: CreateRAGFlowOrgDatasetMapping :exec
INSERT IGNORE INTO ragflow_datasets (id, scope_type, org_id, name, status, create_claim_token)
VALUES (?, 'org', ?, ?, 'creating', sqlc.arg(create_claim_token));

-- name: GetRAGFlowOrgDataset :one
SELECT * FROM ragflow_datasets WHERE scope_type = 'org' AND org_id = ?;
```
（`INSERT IGNORE` 命中 `uk_ragflow_datasets_org_unique` 生成列唯一约束时静默跳过，等价 PG 的 `DO NOTHING`。）

- [ ] **Step 3：按上表把所有文件改完**（每个文件改完后本地 `sqlc generate` 局部验证语法，最后统一 generate 见 Task 5 Step 2）。

- [ ] **Step 4：Commit（建议按文件/模块拆分多个 commit）**
```bash
git add internal/store/queries/
git commit -m "refactor(db): 查询改写适配 MySQL（去 RETURNING、ON CONFLICT→INSERT IGNORE、JSON/cast 调整）"
```

---

## Task 7：转换 helper（pgtype.go → dbtype.go）

**Files:**
- Create: `internal/service/dbtype.go`
- Delete: `internal/service/pgtype.go`

- [ ] **Step 1：写 dbtype.go**
```go
// Package service 的 dbtype.go 集中 service 层与 sqlc(MySQL) 生成类型之间的转换 helper。
// UUID 列在 DB 是 CHAR(36)、Go 侧是 string；可空列用 guregu/null。
package service

import (
	"time"

	"github.com/guregu/null/v5"
)

// strOrEmpty 把可空字符串转成 API 友好的普通 string（NULL → ""）。
func strOrEmpty(v null.String) string {
	if !v.Valid {
		return ""
	}
	return v.String
}

// nullStr 把普通 string 转成可空列写入值；空串视为 NULL。
func nullStr(s string) null.String {
	if s == "" {
		return null.String{}
	}
	return null.StringFrom(s)
}

// nullStrPtr 在“空串也要写入”的场景下，用指针区分 NULL 与空串。
func nullStrFromPtr(s *string) null.String {
	if s == nil {
		return null.String{}
	}
	return null.StringFrom(*s)
}

// nullTime / timeOrZero 处理可空时间列。
func nullTime(t time.Time) null.Time {
	if t.IsZero() {
		return null.Time{}
	}
	return null.TimeFrom(t)
}

func timeOrZero(v null.Time) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return v.Time
}

// nullInt / intOrZero 处理可空整数列。
func nullInt(i int64) null.Int { return null.IntFrom(i) }

func intOrZero(v null.Int) int64 {
	if !v.Valid {
		return 0
	}
	return v.Int64
}
```
说明：原 `uuidToString`/`uuidToOptionalString`/`parseUUID` 不再需要——UUID 已是 string，读写都是裸 string；调用点在 Task 8 直接删去这些转换。

- [ ] **Step 2：删除 pgtype.go**
```bash
git rm internal/service/pgtype.go
```

- [ ] **Step 3：暂不编译**（调用点未改完，编译断；Task 8 收口）。

- [ ] **Step 4：Commit**
```bash
git add internal/service/dbtype.go
git commit -m "refactor(db): 新增 dbtype 转换 helper，替代 pgtype.go"
```

---

## Task 8：逐文件替换 pgtype.* 调用点（37 文件）

**类型替换映射（机械套用）：**

| 旧（pgx/pgtype） | 新（MySQL 生成类型 / helper） |
|---|---|
| `pgtype.UUID`（字段类型） | `string` |
| 构造 `parseUUID(s)` / `pgtype.UUID{...}` | 直接用 `s`（string） |
| 读 `uuidToString(x)` / `uuidToOptionalString(x)` | 直接用 `x`（已是 string）；可空 UUID 用 `strOrEmpty(x)` |
| `pgtype.Text{String:s, Valid:true}` | `null.StringFrom(s)` 或 `nullStr(s)` |
| 读 `x.String` / `x.Valid` | `strOrEmpty(x)` |
| `pgtype.Timestamptz{Time:t, Valid:true}` | `null.TimeFrom(t)` 或 `nullTime(t)` |
| 读 `x.Time` | `timeOrZero(x)` |
| `pgtype.Int8{Int64:n, Valid:true}` / `Int4` | `null.IntFrom(n)` / `nullInt(n)` |
| 读 `x.Int64`/`x.Int32` | `intOrZero(x)` |
| `pgtype.Float8{...}` | `null.FloatFrom(f)` |
| import `github.com/jackc/pgx/v5/pgtype` | import `github.com/guregu/null/v5`（按需） |

**worked example（organization_service.go 片段，示意）：**
```go
// 旧：
org, err := q.CreateOrganization(ctx, sqlc.CreateOrganizationParams{
    Name: name,
    Code: code,
    ContactPhone: pgtype.Text{String: phone, Valid: phone != ""},
})
id := uuidToString(org.ID)

// 新：
newID := uuid.NewString()
if err := q.CreateOrganization(ctx, sqlc.CreateOrganizationParams{
    ID:           newID,
    Name:         name,
    Code:         code,
    ContactPhone: nullStr(phone),
}); err != nil { return ... }
org, err := q.GetOrganization(ctx, newID)
id := org.ID // 已是 string
```

**逐文件清单（每个文件：替换 → `go build ./...` → 跑该包测试 → commit）：**

> service 包（24）：`organization_service.go`、`app_service.go`、`auth_service.go`、`member_service.go`、`onboarding_service.go`、`recharge_service.go`、`audit_service.go`、`channel_service.go`、`knowledge_service.go`、`usage_service.go`、`workspace_service.go`、`hermes_cron.go`、`hermes_kanban.go`、`reconciler.go`、`probe_reconciler.go`、`runtime_node_service.go`、`runtime_operation_service.go`、`assistant_version_service.go`、`resource_sample_cleanup.go`、`resource_metrics_service.go`、`ragflow_parse_status_refresher.go`、`app_runtime_token.go`、`(dbtype.go 已建)`。
> worker 包（9）：`scheduler.go`、`worker.go`、`reaper/reaper.go`、`handlers/progress_reporter.go`、`handlers/channel_login.go`、`handlers/app_runtime_ops.go`、`handlers/newapi_key_status.go`、`handlers/app_health_check.go`、`handlers/runtime_refresh_status.go`、`handlers/app_initialize.go`。
> store 包（2）：`assistant_version_store.go`、`agent_token_store.go`。
> api/cmd（2）：`api/handlers/jobs.go`、`cmd/server/wiring.go`。

- [ ] **Step 1：按依赖顺序逐个文件替换**（建议先 store 包，再 service，再 worker/api/cmd）。每个文件改完：
```bash
go build ./... 2>&1 | head -30   # 看本文件相关编译错是否清零
```
- [ ] **Step 2：每个包改完跑该包测试**
```bash
go test ./internal/service/... ./internal/worker/... -count=1
```
Expected: 编译通过、单测通过（DB 相关集成测试在 Task 10 跑）。
- [ ] **Step 3：分包 Commit**
```bash
git add internal/store/ && git commit -m "refactor(db): store 包 pgtype 调用点改用 string/guregu null"
git add internal/service/ && git commit -m "refactor(db): service 包 pgtype 调用点改用 string/guregu null"
git add internal/worker/ internal/api/ cmd/ && git commit -m "refactor(db): worker/api/cmd pgtype 调用点改用 string/guregu null"
```

---

## Task 9：应用层 UUID 生成

**Files:** 所有调用 `Create*`（原 `gen_random_uuid()` 插入）的 service/worker 文件。

- [ ] **Step 1：在每个插入调用点生成并显式传 id**

模式：
```go
import "github.com/google/uuid"
...
newID := uuid.NewString()
err := q.CreateXxx(ctx, sqlc.CreateXxxParams{ID: newID, /* ...其余字段 */ })
// 需要回读：row, _ := q.GetXxxByID(ctx, newID)
```
插入点对应表（原 `gen_random_uuid()` 默认值的表）：organizations、users、apps、channel_bindings、recharge_records、jobs、audit_logs、refresh_tokens、assistant_versions、ragflow_datasets、ragflow_documents、runtime_nodes、node_resource_samples、instance_resource_samples。逐个 `Create*` 调用点补 `ID: uuid.NewString()`。

- [ ] **Step 2：编译 + 单测**
```bash
go build ./... && go test ./... -count=1
```
Expected: 通过。

- [ ] **Step 3：Commit**
```bash
git add -A && git commit -m "feat(db): 所有插入调用点改为应用层生成 UUID（CHAR(36)）"
```

---

## Task 10：MySQL 集成环境与集成测试

**Files:**
- Modify: `docker-compose.yml`（manager-postgres → manager-mysql）
- Modify: `Makefile`（INTEGRATION_DATABASE_URL）
- Modify: `config/manager.example.yaml`、`config/manager.yaml`（database.url）

- [ ] **Step 1：docker-compose 把 manager-postgres 换成 manager-mysql**
```yaml
  manager-mysql:
    image: mysql:8.0
    environment:
      MYSQL_ROOT_PASSWORD: ocm
      MYSQL_DATABASE: ocm
      MYSQL_USER: ocm
      MYSQL_PASSWORD: ocm
    command: ["--character-set-server=utf8mb4", "--collation-server=utf8mb4_0900_ai_ci"]
    ports: ["13306:3306"]
    volumes: ["./.local/data/manager-mysql:/var/lib/mysql"]
    healthcheck:
      test: ["CMD-SHELL", "mysqladmin ping -h localhost -uroot -p$$MYSQL_ROOT_PASSWORD --silent"]
      interval: 5s
      timeout: 5s
      retries: 20
```
（manager-api 的 `depends_on` 由 manager-postgres 改为 manager-mysql。）

- [ ] **Step 2：DSN 配置**

`config/manager.example.yaml` 与 `config/manager.yaml` 的 `database.url` 改为：
```
mysql://ocm:ocm@tcp(manager-mysql:3306)/ocm?parseTime=true&loc=UTC&charset=utf8mb4&multiStatements=true
```
说明：`parseTime=true` 让驱动把 DATETIME 扫成 `time.Time`；`loc=UTC` 统一 UTC；`multiStatements=true` 供 golang-migrate 跑含多语句的基线。

- [ ] **Step 3：Makefile 集成测试 URL**

把 `INTEGRATION_DATABASE_URL` 默认值由
`postgres://ocm:ocm@manager-postgres:5432/ocm?sslmode=disable`
改为
`mysql://ocm:ocm@tcp(manager-mysql:3306)/ocm?parseTime=true&loc=UTC&multiStatements=true`。

- [ ] **Step 4：起库 + 迁移 + 集成测试**
```bash
docker compose up -d manager-mysql
make migrate-up
make test            # 或 go test ./... 含集成测试（按现有 INTEGRATION_DATABASE_URL 约定）
```
Expected: 迁移成功建 14 表；集成测试全绿。失败则定位（多为类型扫描/NULL 处理/唯一约束差异），修复后重跑。

- [ ] **Step 5：Commit**
```bash
git add docker-compose.yml Makefile config/manager.example.yaml config/manager.yaml
git commit -m "chore(db): 本地与集成测试环境切换到 MySQL 8"
```

---

## Task 11：移除 pgx 残留依赖与最终验收

- [ ] **Step 1：确认无 pgx/pgtype 残留**
```bash
grep -rn "jackc/pgx\|pgtype\." internal/ cmd/ --include="*.go" | grep -v "_test.go"
```
Expected: 空输出（生成代码也应无 pgx）。

- [ ] **Step 2：go mod tidy**
```bash
go mod tidy && go build ./...
```
Expected: `go.mod` 不再含 `jackc/pgx`；编译通过。

- [ ] **Step 3：全量测试 + vet**
```bash
go vet ./... && go test ./... -count=1
```
Expected: 全绿。

- [ ] **Step 4：OpenAPI 同步检查**（类型可能影响响应结构）
```bash
make openapi-gen && make web-types-gen
git status --short    # 若 yaml/generated.ts 有变更，一并提交
```

- [ ] **Step 5：真实浏览器验收**（按项目交付规范，覆盖三角色）
登录 manager 后台（platform_admin `admin/admin123`、org `e2e-org-admin`/`e2e-org-member` 密码 `e2e-pass-123` 组织标识 `test-org`），过 golden path：组织/用户 CRUD、充值记录、应用列表、审计日志、知识库文件列表，核对网络请求状态码与 console 无报错。**涉及微信扫码绑定渠道的步骤，通知用户配合扫码。**

- [ ] **Step 6：Commit**
```bash
git add -A && git commit -m "chore(db): 移除 pgx 依赖，完成 Postgres→MySQL 迁移收尾"
```

---

## Self-Review（spec 覆盖核对）

- **schema（§6 全表）**：Task 2 单基线 14 表 ✅（含 generated column 替代部分唯一索引、JSON 默认值表达式、FK 顺序）。
- **gen_random_uuid → 应用层**：Task 9 ✅。
- **RETURNING(59)**：Task 6 模式 + 完整清单 ✅。
- **timestamptz→DATETIME(6) UTC**：Task 2 DDL + Task 10 DSN `parseTime/loc=UTC` ✅。
- **JSONB→JSON**：Task 2 DDL + Task 6 JSON 操作符改写 ✅。
- **唯一部分索引→生成列**：Task 2（platform_username/owner_active/runtime_token_active/channel app_active/assistant name_active/ragflow org_scope/app_scope）✅；单列 WHERE NOT NULL 退化为普通 UNIQUE（agent_id/ragflow_dataset_id/refresh token）✅。
- **CHECK 保留**：Task 2 ✅。
- **ON CONFLICT→INSERT IGNORE/ON DUPLICATE KEY**：Task 6（2 处）✅。
- **boolean→TINYINT(1)**：最终态无存活 boolean 列（均在 DROP 列中），无需处理；如后续新增按此映射。
- **sqlc engine mysql + go-sql-driver + golang-migrate mysql**：Task 1/3/4/5 ✅。
- **29 migration 压成单基线**：Task 2 ✅。
- **manager 与 new-api 全新库免 ETL**：全局前提（new-api 数据库切换属其部署配置，非本 manager 代码 spec；在 spec-D 处理 new-api 的 DSN）。

**遗留显式说明**：new-api 自身 Postgres→MySQL 属第三方服务部署配置（改 `SQL_DSN`），放 spec-D；本计划只覆盖 manager 自身代码与 schema。
