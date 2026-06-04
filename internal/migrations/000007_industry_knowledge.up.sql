-- 行业知识库是平台级全局资源，名称只在未删除记录中唯一。
CREATE TABLE industry_knowledge_bases (
    id CHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    created_by VARCHAR(255) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    name_active_key VARCHAR(255) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN name END) VIRTUAL,
    UNIQUE KEY uk_industry_knowledge_bases_name_active (name_active_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 行业库不归属企业，因此 org_id 需要允许 NULL；org/app 的原有约束通过新 CHECK 保留。
ALTER TABLE ragflow_datasets
    DROP CHECK ragflow_datasets_scope_type_check,
    DROP CHECK ragflow_datasets_scope_app_check,
    MODIFY org_id CHAR(36) NULL,
    ADD COLUMN industry_knowledge_base_id CHAR(36) NULL AFTER app_id,
    ADD COLUMN industry_scope_key CHAR(36)
        GENERATED ALWAYS AS (CASE WHEN scope_type = 'industry' THEN industry_knowledge_base_id END) VIRTUAL,
    ADD CONSTRAINT ragflow_datasets_scope_type_check CHECK (scope_type IN ('org','app','industry')),
    ADD CONSTRAINT ragflow_datasets_scope_target_check CHECK (
        (scope_type = 'org' AND org_id IS NOT NULL AND app_id IS NULL AND industry_knowledge_base_id IS NULL)
        OR (scope_type = 'app' AND org_id IS NOT NULL AND app_id IS NOT NULL AND industry_knowledge_base_id IS NULL)
        OR (scope_type = 'industry' AND org_id IS NULL AND app_id IS NULL AND industry_knowledge_base_id IS NOT NULL)
    ),
    ADD CONSTRAINT fk_ragflow_datasets_industry_id
        FOREIGN KEY (industry_knowledge_base_id) REFERENCES industry_knowledge_bases(id),
    ADD UNIQUE KEY uk_ragflow_datasets_industry_identity (id, scope_type, industry_knowledge_base_id),
    ADD UNIQUE KEY uk_ragflow_datasets_industry_unique (industry_scope_key);

ALTER TABLE ragflow_documents
    DROP CHECK ragflow_documents_scope_type_check,
    DROP CHECK ragflow_documents_scope_app_check,
    MODIFY org_id CHAR(36) NULL,
    ADD COLUMN industry_knowledge_base_id CHAR(36) NULL AFTER app_id,
    ADD COLUMN industry_document_base_key CHAR(36)
        GENERATED ALWAYS AS (CASE WHEN scope_type = 'industry' THEN industry_knowledge_base_id END) VIRTUAL,
    ADD COLUMN industry_document_name_key VARCHAR(255)
        GENERATED ALWAYS AS (CASE WHEN scope_type = 'industry' THEN name END) VIRTUAL,
    ADD CONSTRAINT ragflow_documents_scope_type_check CHECK (scope_type IN ('org','app','industry')),
    ADD CONSTRAINT ragflow_documents_scope_target_check CHECK (
        (scope_type = 'org' AND org_id IS NOT NULL AND app_id IS NULL AND industry_knowledge_base_id IS NULL)
        OR (scope_type = 'app' AND org_id IS NOT NULL AND app_id IS NOT NULL AND industry_knowledge_base_id IS NULL)
        OR (scope_type = 'industry' AND org_id IS NULL AND app_id IS NULL AND industry_knowledge_base_id IS NOT NULL)
    ),
    ADD CONSTRAINT fk_ragflow_documents_industry_id
        FOREIGN KEY (industry_knowledge_base_id) REFERENCES industry_knowledge_bases(id),
    ADD CONSTRAINT fk_ragflow_documents_dataset_industry_scope
        FOREIGN KEY (dataset_id, scope_type, industry_knowledge_base_id)
        REFERENCES ragflow_datasets(id, scope_type, industry_knowledge_base_id) ON DELETE CASCADE,
    ADD UNIQUE KEY uk_ragflow_documents_industry_name (industry_document_base_key, industry_document_name_key);

CREATE TABLE assistant_version_industry_knowledge_bases (
    version_id CHAR(36) NOT NULL,
    industry_knowledge_base_id CHAR(36) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (version_id, industry_knowledge_base_id),
    CONSTRAINT fk_av_industry_version
        FOREIGN KEY (version_id) REFERENCES assistant_versions(id) ON DELETE CASCADE,
    CONSTRAINT fk_av_industry_base
        FOREIGN KEY (industry_knowledge_base_id) REFERENCES industry_knowledge_bases(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
