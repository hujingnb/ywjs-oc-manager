-- name: CreateIndustryKnowledgeBase :exec
-- 创建平台级行业知识库；名称唯一性由未删除记录的生成列唯一键兜底。
INSERT INTO industry_knowledge_bases (id, name, created_by)
VALUES (?, ?, ?);

-- name: GetIndustryKnowledgeBase :one
-- 按 ID 读取未删除行业知识库，供管理面详情和后续权限校验使用。
SELECT *
FROM industry_knowledge_bases
WHERE id = ? AND deleted_at IS NULL;

-- name: GetIndustryKnowledgeBaseForUpdate :one
-- 在事务内锁定未删除行业知识库，序列化版本关联写入和行业库删除。
SELECT *
FROM industry_knowledge_bases
WHERE id = ? AND deleted_at IS NULL
FOR UPDATE;

-- name: GetIndustryKnowledgeBaseByName :one
-- 按名称读取未删除行业知识库，用于创建和重命名时做业务提示。
SELECT *
FROM industry_knowledge_bases
WHERE name = ? AND deleted_at IS NULL;

-- name: ListIndustryKnowledgeBases :many
-- 分页列出行业知识库，并统计行业 scope 下已缓存的 RAGFlow 文档数量。
SELECT ikb.*,
       CAST(COALESCE(COUNT(rd.id), 0) AS SIGNED) AS document_count
FROM industry_knowledge_bases ikb
LEFT JOIN ragflow_documents rd
  ON rd.scope_type = 'industry'
 AND rd.industry_knowledge_base_id = ikb.id
WHERE ikb.deleted_at IS NULL
  AND (sqlc.narg(keyword) IS NULL OR ikb.name LIKE CONCAT('%', sqlc.narg(keyword), '%'))
GROUP BY ikb.id
ORDER BY ikb.updated_at DESC, ikb.id DESC
LIMIT ? OFFSET ?;

-- name: CountIndustryKnowledgeBases :one
-- 统计行业知识库列表总数，过滤条件必须与 ListIndustryKnowledgeBases 保持一致。
SELECT count(*)
FROM industry_knowledge_bases
WHERE deleted_at IS NULL
  AND (sqlc.narg(keyword) IS NULL OR name LIKE CONCAT('%', sqlc.narg(keyword), '%'));

-- name: RenameIndustryKnowledgeBase :exec
-- 重命名未删除行业知识库；唯一约束负责拦截同名未删除记录。
UPDATE industry_knowledge_bases
SET name = ?, updated_at = now()
WHERE id = ? AND deleted_at IS NULL;

-- name: SoftDeleteIndustryKnowledgeBase :execrows
-- 软删除未被助手版本引用的行业知识库；删除后名称可被重新使用。
UPDATE industry_knowledge_bases AS ikb
SET deleted_at = now(), updated_at = now()
WHERE ikb.id = ? AND ikb.deleted_at IS NULL
  AND NOT EXISTS (
    SELECT 1
    FROM assistant_version_industry_knowledge_bases avikb
    JOIN assistant_versions av ON av.id = avikb.version_id
    WHERE av.deleted_at IS NULL
      AND avikb.industry_knowledge_base_id = ikb.id
  )
  AND NOT EXISTS (
    SELECT 1
    FROM organization_industry_knowledge_bases oikb
    WHERE oikb.industry_knowledge_base_id = ikb.id
  );

-- name: CountAssistantVersionsUsingIndustryKnowledgeBase :one
-- 统计仍被未删除助手版本引用的行业知识库，避免删除仍在使用的全局知识。
SELECT count(*)
FROM assistant_version_industry_knowledge_bases avikb
JOIN assistant_versions av ON av.id = avikb.version_id
WHERE av.deleted_at IS NULL
  AND avikb.industry_knowledge_base_id = ?;

-- name: CountOrganizationsUsingIndustryKnowledgeBase :one
-- 统计平台已授权该行业库的企业，防止删除仍可被 AICC 使用的行业库。
SELECT count(*)
FROM organization_industry_knowledge_bases
WHERE industry_knowledge_base_id = ?;

-- name: ListOrganizationIndustryKnowledgeBases :many
-- 列出企业已获授权且未删除的行业知识库，供 AICC 配置候选项和平台配置回显使用。
SELECT ikb.*
FROM organization_industry_knowledge_bases oikb
JOIN industry_knowledge_bases ikb ON ikb.id = oikb.industry_knowledge_base_id
WHERE oikb.org_id = ?
  AND ikb.deleted_at IS NULL
ORDER BY ikb.name ASC, ikb.id ASC;

-- name: ReplaceOrganizationIndustryKnowledgeBases :exec
-- 企业行业库授权使用整组替换，避免平台配置中的增删产生残留授权。
DELETE FROM organization_industry_knowledge_bases
WHERE org_id = ?;

-- name: AddOrganizationIndustryKnowledgeBase :execrows
-- 仅允许授权未删除的行业知识库；受影响行数为零表示提交了不存在的行业库。
INSERT INTO organization_industry_knowledge_bases (org_id, industry_knowledge_base_id)
SELECT sqlc.arg(org_id), ikb.id
FROM industry_knowledge_bases ikb
WHERE ikb.id = sqlc.arg(industry_knowledge_base_id)
  AND ikb.deleted_at IS NULL;

-- name: ReplaceAssistantVersionIndustryKnowledgeBases :exec
-- 替换助手版本行业知识库关联前先清空旧关联，由调用方在同一事务中重新插入。
DELETE FROM assistant_version_industry_knowledge_bases
WHERE version_id = ?;

-- name: AddAssistantVersionIndustryKnowledgeBase :execrows
-- 为助手版本追加一个行业知识库关联；只允许关联未删除行业库，复合主键保证同一版本不重复关联。
INSERT INTO assistant_version_industry_knowledge_bases (version_id, industry_knowledge_base_id)
SELECT sqlc.arg(version_id), ikb.id
FROM industry_knowledge_bases ikb
WHERE ikb.id = sqlc.arg(industry_knowledge_base_id) AND ikb.deleted_at IS NULL
FOR UPDATE;

-- name: ListIndustryKnowledgeBasesByAssistantVersion :many
-- 列出助手版本关联的未删除行业知识库，供发布配置和运行时检索范围使用。
SELECT ikb.*
FROM assistant_version_industry_knowledge_bases avikb
JOIN industry_knowledge_bases ikb ON ikb.id = avikb.industry_knowledge_base_id
WHERE avikb.version_id = ?
  AND ikb.deleted_at IS NULL
ORDER BY ikb.name ASC, ikb.id ASC;
