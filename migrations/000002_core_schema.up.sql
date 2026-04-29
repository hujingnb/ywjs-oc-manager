-- 核心业务表迁移。
-- 这些表覆盖平台、组织、成员、应用、运行节点、渠道绑定、异步任务和审计事实。

CREATE TABLE organizations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE,
    status text NOT NULL DEFAULT 'active',
    contact_name text NULL,
    contact_phone text NULL,
    remark text NULL,
    newapi_user_id text NULL,
    credit_warning_threshold integer NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz NULL,
    CONSTRAINT organizations_status_check CHECK (status IN ('active', 'disabled', 'deleted')),
    CONSTRAINT organizations_credit_warning_threshold_check CHECK (
        credit_warning_threshold IS NULL
        OR (credit_warning_threshold >= 0 AND credit_warning_threshold <= 100)
    )
);

COMMENT ON TABLE organizations IS '组织租户表，保存组织基础信息、new-api 账号映射和余额预警阈值。';
COMMENT ON COLUMN organizations.status IS '组织状态：active 表示可用，disabled 表示禁用，deleted 表示软删除。';
COMMENT ON COLUMN organizations.credit_warning_threshold IS '组织余额预警百分比阈值，空值表示不启用预警。';

CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id uuid NULL REFERENCES organizations(id),
    username text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    display_name text NOT NULL,
    role text NOT NULL,
    status text NOT NULL DEFAULT 'active',
    last_login_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_role_check CHECK (role IN ('platform_admin', 'org_admin', 'org_member')),
    CONSTRAINT users_status_check CHECK (status IN ('active', 'disabled')),
    CONSTRAINT users_platform_org_check CHECK (
        (role = 'platform_admin' AND org_id IS NULL)
        OR (role IN ('org_admin', 'org_member') AND org_id IS NOT NULL)
    )
);

COMMENT ON TABLE users IS '平台管理员、组织管理员和组织成员账号。';
COMMENT ON COLUMN users.org_id IS '平台管理员为空，组织管理员和组织成员必须归属一个组织。';
COMMENT ON COLUMN users.password_hash IS '密码 hash，不保存明文密码。';
COMMENT ON COLUMN users.role IS '账号角色：platform_admin、org_admin、org_member。';

CREATE TABLE organization_personas (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id uuid NOT NULL REFERENCES organizations(id),
    system_prompt text NOT NULL,
    conversation_rules text NULL,
    forbidden_rules text NULL,
    reply_style text NULL,
    allow_member_override boolean NOT NULL DEFAULT false,
    version integer NOT NULL,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT organization_personas_version_check CHECK (version > 0),
    CONSTRAINT organization_personas_org_version_unique UNIQUE (org_id, version)
);

COMMENT ON TABLE organization_personas IS '组织级 AI 人设版本表，当前生效版本取同组织最大 version。';
COMMENT ON COLUMN organization_personas.allow_member_override IS '是否允许成员应用使用 app_prompt 覆盖组织默认人设。';

CREATE TABLE runtime_nodes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL UNIQUE,
    status text NOT NULL DEFAULT 'pending',
    agent_docker_endpoint text NULL,
    agent_file_endpoint text NULL,
    agent_tls_ca_cert text NULL,
    agent_token_hash text NULL,
    bootstrap_token_hash text NULL,
    bootstrap_token_expires_at timestamptz NULL,
    agent_version text NULL,
    heartbeat_interval_seconds integer NOT NULL DEFAULT 30,
    last_heartbeat_at timestamptz NULL,
    resource_snapshot_json jsonb NULL,
    metadata_json jsonb NULL,
    node_data_root text NULL,
    registered_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT runtime_nodes_status_check CHECK (status IN ('pending', 'active', 'unreachable', 'disabled')),
    CONSTRAINT runtime_nodes_heartbeat_interval_check CHECK (heartbeat_interval_seconds > 0)
);

COMMENT ON TABLE runtime_nodes IS '运行节点注册表，manager 通过节点 agent 执行 Docker 与文件操作。';
COMMENT ON COLUMN runtime_nodes.agent_docker_endpoint IS 'agent Docker 代理地址，manager 通过该地址访问节点 Docker 能力。';
COMMENT ON COLUMN runtime_nodes.agent_file_endpoint IS 'agent 文件 API 地址，manager 通过该地址访问节点工作目录和知识库目录。';
COMMENT ON COLUMN runtime_nodes.agent_token_hash IS '长期通信令牌 hash，明文令牌只保存在 agent 侧。';
COMMENT ON COLUMN runtime_nodes.bootstrap_token_hash IS '一次性注册令牌 hash，节点注册成功后清空。';
COMMENT ON COLUMN runtime_nodes.resource_snapshot_json IS 'agent 上报的 CPU、内存、磁盘、容器数量等资源快照。';

CREATE TABLE apps (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id uuid NOT NULL REFERENCES organizations(id),
    owner_user_id uuid NOT NULL REFERENCES users(id),
    runtime_node_id uuid NOT NULL REFERENCES runtime_nodes(id),
    name text NOT NULL,
    description text NULL,
    status text NOT NULL DEFAULT 'draft',
    persona_mode text NOT NULL DEFAULT 'org_inherited',
    app_prompt text NULL,
    container_id text NULL,
    container_name text NULL,
    newapi_key_id text NULL,
    newapi_key_ciphertext text NULL,
    api_key_status text NOT NULL DEFAULT 'pending',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz NULL,
    CONSTRAINT apps_status_check CHECK (
        status IN ('draft', 'initializing', 'binding_waiting', 'binding_failed', 'running', 'stopped', 'error', 'deleted')
    ),
    CONSTRAINT apps_persona_mode_check CHECK (persona_mode IN ('org_inherited', 'app_override')),
    CONSTRAINT apps_api_key_status_check CHECK (api_key_status IN ('pending', 'active', 'disabled', 'error'))
);

COMMENT ON TABLE apps IS '成员客户端应用表；第一版每个未删除成员账号最多拥有一个未删除应用。';
COMMENT ON COLUMN apps.runtime_node_id IS '应用所在运行节点，第一版创建应用前必须已有可用节点。';
COMMENT ON COLUMN apps.persona_mode IS '人设模式：继承组织人设或使用应用级覆盖提示词。';
COMMENT ON COLUMN apps.newapi_key_ciphertext IS 'new-api key 密文，使用 manager 的 security.master_key 加密。';
COMMENT ON COLUMN apps.deleted_at IS '软删除时间；业务记录不做物理删除。';

CREATE UNIQUE INDEX apps_owner_active ON apps(owner_user_id) WHERE deleted_at IS NULL;

CREATE TABLE channel_bindings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id uuid NOT NULL REFERENCES apps(id),
    channel_type text NOT NULL,
    status text NOT NULL DEFAULT 'unbound',
    bound_identity text NULL,
    channel_name text NULL,
    metadata_json jsonb NULL,
    bound_at timestamptz NULL,
    last_online_at timestamptz NULL,
    last_error text NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT channel_bindings_status_check CHECK (
        status IN ('unbound', 'pending_auth', 'bound', 'failed', 'expired', 'unbound_by_user', 'deleted')
    ),
    CONSTRAINT channel_bindings_channel_type_check CHECK (channel_type IN ('wechat'))
);

COMMENT ON TABLE channel_bindings IS '应用渠道绑定表，第一版仅支持 wechat，后续可扩展其他渠道。';
COMMENT ON COLUMN channel_bindings.bound_identity IS '渠道下的账号唯一标识，例如微信 wxid。';
COMMENT ON COLUMN channel_bindings.metadata_json IS '渠道特有元数据，例如二维码 payload、过期时间和登录 challenge。';

CREATE UNIQUE INDEX channel_bindings_app_active ON channel_bindings(app_id) WHERE status <> 'deleted';

CREATE TABLE recharge_records (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id uuid NOT NULL REFERENCES organizations(id),
    operator_id uuid NOT NULL REFERENCES users(id),
    credit_amount bigint NOT NULL,
    remark text NULL,
    newapi_ref_id text NULL,
    status text NOT NULL,
    error_message text NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT recharge_records_credit_amount_check CHECK (credit_amount > 0),
    CONSTRAINT recharge_records_status_check CHECK (status IN ('succeeded', 'failed'))
);

COMMENT ON TABLE recharge_records IS '组织点数充值记录，计费事实仍以 new-api 为准。';
COMMENT ON COLUMN recharge_records.credit_amount IS '充值点数，使用整数避免浮点误差。';
COMMENT ON COLUMN recharge_records.newapi_ref_id IS 'new-api 侧充值或余额调整引用 ID。';

CREATE TABLE jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    type text NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    priority integer NOT NULL DEFAULT 0,
    run_after timestamptz NOT NULL DEFAULT now(),
    attempts integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL DEFAULT 5,
    payload_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    locked_by text NULL,
    locked_at timestamptz NULL,
    last_error text NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz NULL,
    CONSTRAINT jobs_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'canceled')),
    CONSTRAINT jobs_attempts_check CHECK (attempts >= 0),
    CONSTRAINT jobs_max_attempts_check CHECK (max_attempts > 0),
    CONSTRAINT jobs_type_check CHECK (
        type IN (
            'app_initialize',
            'app_start_container',
            'app_stop_container',
            'app_restart_container',
            'app_delete',
            'channel_start_login',
            'channel_check_binding',
            'knowledge_sync_node',
            'runtime_node_health_reconcile',
            'runtime_refresh_status',
            'app_health_check',
            'newapi_disable_key',
            'newapi_restore_key',
            'workspace_archive_cleanup'
        )
    )
);

COMMENT ON TABLE jobs IS '异步任务事实表；Redis 只负责分发，任务状态以本表为准。';
COMMENT ON COLUMN jobs.run_after IS '任务最早可执行时间，用于失败重试和延迟调度。';
COMMENT ON COLUMN jobs.payload_json IS '任务载荷，handler 必须按类型做结构化校验。';
COMMENT ON COLUMN jobs.locked_by IS '当前领取任务的 worker 实例 ID。';

CREATE TABLE audit_logs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id uuid NULL REFERENCES users(id),
    actor_role text NOT NULL,
    org_id uuid NULL REFERENCES organizations(id),
    target_type text NOT NULL,
    target_id text NOT NULL,
    action text NOT NULL,
    result text NOT NULL,
    error_message text NULL,
    ip_address inet NULL,
    metadata_json jsonb NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT audit_logs_actor_role_check CHECK (actor_role IN ('system', 'platform_admin', 'org_admin', 'org_member')),
    CONSTRAINT audit_logs_result_check CHECK (result IN ('succeeded', 'failed'))
);

COMMENT ON TABLE audit_logs IS '审计日志表，记录关键操作、结果和错误信息；普通业务 API 不允许修改或删除。';
COMMENT ON COLUMN audit_logs.actor_id IS '操作者用户 ID；系统任务产生的审计可为空。';
COMMENT ON COLUMN audit_logs.target_id IS '目标资源 ID；知识库文件可使用 org:{id}:filename 或 app:{id}:filename 编码。';
COMMENT ON COLUMN audit_logs.metadata_json IS '审计扩展元数据，例如文件大小、MIME、上传者等。';

CREATE TABLE refresh_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id),
    token_hash text NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE refresh_tokens IS '登录刷新令牌表，只保存 token hash，不保存明文令牌。';
COMMENT ON COLUMN refresh_tokens.revoked_at IS '令牌撤销时间；为空且未过期时可用于刷新登录态。';

CREATE INDEX users_org_role_status_idx ON users(org_id, role, status);
CREATE INDEX organizations_status_name_idx ON organizations(status, name);
CREATE INDEX apps_org_owner_status_idx ON apps(org_id, owner_user_id, status);
CREATE INDEX apps_runtime_node_status_idx ON apps(runtime_node_id, status);
CREATE INDEX apps_newapi_key_id_idx ON apps(newapi_key_id);
CREATE INDEX channel_bindings_app_channel_status_idx ON channel_bindings(app_id, channel_type, status);
CREATE INDEX runtime_nodes_status_last_heartbeat_idx ON runtime_nodes(status, last_heartbeat_at);
CREATE INDEX jobs_status_run_after_priority_idx ON jobs(status, run_after, priority);
CREATE INDEX audit_logs_org_created_idx ON audit_logs(org_id, created_at);
CREATE INDEX audit_logs_target_created_idx ON audit_logs(target_type, target_id, created_at);
CREATE INDEX recharge_records_org_created_idx ON recharge_records(org_id, created_at);
CREATE INDEX refresh_tokens_user_expires_idx ON refresh_tokens(user_id, expires_at);
CREATE INDEX organization_personas_org_version_idx ON organization_personas(org_id, version DESC);
