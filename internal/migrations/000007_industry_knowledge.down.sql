DROP TABLE assistant_version_industry_knowledge_bases;

-- 回滚到版本 6 前，旧 schema 无法表示 industry scope，需先清理行业库映射和文档缓存。
DELETE FROM ragflow_documents WHERE scope_type = 'industry';
DELETE FROM ragflow_datasets WHERE scope_type = 'industry';

ALTER TABLE ragflow_documents
    DROP INDEX uk_ragflow_documents_industry_name,
    DROP FOREIGN KEY fk_ragflow_documents_dataset_industry_scope,
    DROP FOREIGN KEY fk_ragflow_documents_industry_id,
    DROP FOREIGN KEY fk_ragflow_documents_dataset_scope,
    DROP FOREIGN KEY fk_ragflow_documents_dataset_app_scope,
    DROP FOREIGN KEY fk_ragflow_documents_org_id,
    DROP CHECK ragflow_documents_scope_target_check,
    DROP CHECK ragflow_documents_scope_type_check,
    DROP COLUMN industry_document_name_key,
    DROP COLUMN industry_document_base_key,
    DROP COLUMN industry_knowledge_base_id,
    MODIFY org_id CHAR(36) NOT NULL,
    ADD CONSTRAINT ragflow_documents_scope_type_check CHECK (scope_type IN ('org','app')),
    ADD CONSTRAINT ragflow_documents_scope_app_check CHECK (
        (scope_type = 'org' AND app_id IS NULL) OR (scope_type = 'app' AND app_id IS NOT NULL));

ALTER TABLE ragflow_datasets
    DROP INDEX uk_ragflow_datasets_industry_unique,
    DROP INDEX uk_ragflow_datasets_industry_identity,
    DROP FOREIGN KEY fk_ragflow_datasets_industry_id,
    DROP FOREIGN KEY fk_ragflow_datasets_org_id,
    DROP CHECK ragflow_datasets_scope_target_check,
    DROP CHECK ragflow_datasets_scope_type_check,
    DROP COLUMN industry_scope_key,
    DROP COLUMN industry_knowledge_base_id,
    MODIFY org_id CHAR(36) NOT NULL,
    ADD CONSTRAINT ragflow_datasets_scope_type_check CHECK (scope_type IN ('org','app')),
    ADD CONSTRAINT ragflow_datasets_scope_app_check CHECK (
        (scope_type = 'org' AND app_id IS NULL) OR (scope_type = 'app' AND app_id IS NOT NULL));

ALTER TABLE ragflow_datasets
    ADD CONSTRAINT fk_ragflow_datasets_org_id FOREIGN KEY (org_id) REFERENCES organizations(id);

ALTER TABLE ragflow_documents
    ADD CONSTRAINT fk_ragflow_documents_org_id FOREIGN KEY (org_id) REFERENCES organizations(id),
    ADD CONSTRAINT fk_ragflow_documents_dataset_scope FOREIGN KEY (dataset_id, scope_type, org_id)
        REFERENCES ragflow_datasets(id, scope_type, org_id) ON DELETE CASCADE,
    ADD CONSTRAINT fk_ragflow_documents_dataset_app_scope FOREIGN KEY (dataset_id, scope_type, org_id, app_id)
        REFERENCES ragflow_datasets(id, scope_type, org_id, app_id) ON DELETE CASCADE;

DROP TABLE industry_knowledge_bases;
