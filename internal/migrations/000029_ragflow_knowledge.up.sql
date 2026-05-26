CREATE TABLE ragflow_datasets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    scope_type text NOT NULL CHECK (scope_type IN ('org', 'app')),
    org_id uuid NOT NULL REFERENCES organizations(id),
    app_id uuid NULL REFERENCES apps(id),
    ragflow_dataset_id text NULL,
    name text NOT NULL,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('creating', 'active', 'deleting', 'failed')),
    last_error text NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ragflow_datasets_scope_app_check CHECK (
        (scope_type = 'org' AND app_id IS NULL) OR (scope_type = 'app' AND app_id IS NOT NULL)
    )
);

CREATE UNIQUE INDEX ragflow_datasets_org_unique ON ragflow_datasets(org_id) WHERE scope_type = 'org';
CREATE UNIQUE INDEX ragflow_datasets_app_unique ON ragflow_datasets(app_id) WHERE scope_type = 'app';
CREATE UNIQUE INDEX ragflow_datasets_remote_unique ON ragflow_datasets(ragflow_dataset_id) WHERE ragflow_dataset_id IS NOT NULL;

CREATE TABLE ragflow_documents (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id uuid NOT NULL REFERENCES ragflow_datasets(id) ON DELETE CASCADE,
    scope_type text NOT NULL CHECK (scope_type IN ('org', 'app')),
    org_id uuid NOT NULL REFERENCES organizations(id),
    app_id uuid NULL REFERENCES apps(id),
    ragflow_document_id text NOT NULL,
    name text NOT NULL,
    size_bytes bigint NOT NULL DEFAULT 0,
    mime_type text NULL,
    suffix text NULL,
    parse_status text NOT NULL DEFAULT 'queued' CHECK (parse_status IN ('queued', 'running', 'completed', 'failed', 'stopped')),
    progress integer NOT NULL DEFAULT 0 CHECK (progress >= 0 AND progress <= 100),
    last_error text NULL,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ragflow_documents_scope_app_check CHECK (
        (scope_type = 'org' AND app_id IS NULL) OR (scope_type = 'app' AND app_id IS NOT NULL)
    ),
    CONSTRAINT ragflow_documents_unique_remote UNIQUE (dataset_id, ragflow_document_id)
);

CREATE INDEX ragflow_documents_scope_idx ON ragflow_documents(scope_type, org_id, app_id, created_at DESC);
CREATE INDEX ragflow_documents_parse_status_idx ON ragflow_documents(parse_status) WHERE parse_status IN ('queued', 'running');

ALTER TABLE apps ADD COLUMN runtime_token_hash text NULL;
ALTER TABLE apps ADD COLUMN runtime_token_ciphertext text NULL;

COMMENT ON TABLE ragflow_datasets IS 'oc-manager 组织/实例知识库到 RAGFlow dataset 的映射。';
COMMENT ON COLUMN ragflow_datasets.scope_type IS '映射归属范围：org 表示组织知识库，app 表示实例知识库。';
COMMENT ON COLUMN ragflow_datasets.ragflow_dataset_id IS 'RAGFlow 返回的 dataset ID，创建中或失败时可为空。';
COMMENT ON COLUMN ragflow_datasets.status IS 'dataset 生命周期状态：creating、active、deleting、failed。';
COMMENT ON TABLE ragflow_documents IS 'manager 展示文件列表所需的 RAGFlow document 元数据缓存。';
COMMENT ON COLUMN ragflow_documents.ragflow_document_id IS 'RAGFlow 返回的 document ID。';
COMMENT ON COLUMN ragflow_documents.parse_status IS 'manager 归一化后的解析状态：queued、running、completed、failed、stopped。';
COMMENT ON COLUMN ragflow_documents.created_by IS '创建来源标识，可为用户 ID 或 runtime app ID。';
COMMENT ON COLUMN apps.runtime_token_hash IS 'Hermes 调 manager runtime API 的 app 级 token hash。';
COMMENT ON COLUMN apps.runtime_token_ciphertext IS 'Hermes 调 manager runtime API 的 app 级 token 密文，使用 manager master key 加密。';
