-- 企业可使用的行业知识库由平台管理员显式授权；AICC 智能体只能从该集合中选择。
CREATE TABLE organization_industry_knowledge_bases (
    org_id CHAR(36) NOT NULL,
    industry_knowledge_base_id CHAR(36) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (org_id, industry_knowledge_base_id),
    CONSTRAINT fk_org_industry_knowledge_org
        FOREIGN KEY (org_id) REFERENCES organizations(id) ON DELETE CASCADE,
    CONSTRAINT fk_org_industry_knowledge_base
        FOREIGN KEY (industry_knowledge_base_id) REFERENCES industry_knowledge_bases(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
