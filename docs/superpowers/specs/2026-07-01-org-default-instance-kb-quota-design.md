# 企业级「个人知识库空间」默认配额 — 设计文档

- 日期：2026-07-01
- 状态：待实现

## 背景与目标

企业编辑页目前有「企业知识库空间 (GB)」字段，对应
`organizations.knowledge_quota_bytes`，限制**整个企业**知识库的总容量。

产品希望在企业配置里再加一个「个人知识库空间 (GB)」字段。经澄清，这里
的「个人知识库」指的就是**实例知识库**（`apps.knowledge_quota_bytes`，
每个成员的实例各自拥有的知识库）——每个企业成员对应一个实例，实例的知识
库即该成员的"个人知识库"。

现状：实例创建时不显式写 `knowledge_quota_bytes`，靠数据库默认值 **1GB**
（`000005_knowledge_quota` migration 设定）；之后平台管理员可在实例的知识
库页（`AppKnowledgeTab`）对单个实例单独调整上限。**目前没有任何企业级的
默认值在统一管理这个配额。**

**目标**：在企业配置里增加一个必填的「个人知识库空间 (GB)」，作为该企业
**新建实例**的知识库默认配额，替代当前写死的 1GB。

## 需求澄清结论

以下为与用户逐条确认的结论，作为设计的硬约束：

1. **语义**：企业级字段是"每个成员各自的上限"，即每个实例各自的配额（不是
   全企业共享的总池子）。
2. **与实例已有配额的关系（选 A）**：企业级字段**只作为新建实例的默认值**。
   已有实例的配额不受影响；平台管理员仍可在实例页对单个实例单独调整。
   → 不改动实例配额的读取 / 校验 / 编辑逻辑，改动面最小。
3. **留空语义**：**不允许留空，必填，且必须 > 0**。
   → 彻底绕开"无限制"语义，无需给实例配额系统引入"无限制"概念（实例配额
     现有 `CHECK (> 0)` 约束保持不变）。
4. **前端需要字段说明文案**（见下）。

## 非目标（明确不做）

- 不改动 `apps.knowledge_quota_bytes` 的 `CHECK (> 0)` 约束。
- 不改动实例知识库上传校验逻辑（`ensureKnowledgeQuotaAvailable`）。
- 不改动实例知识库页（`AppKnowledgeTab`）的单实例配额编辑入口。
- 不回填 / 不批量修改已有实例的配额。
- 不引入"无限制"配额概念。
- 不引入用户级（`users` 表）知识库配额字段——本需求完全通过"企业默认 →
  新实例继承"实现。

## 设计

### 1. 数据库层

`organizations` 表新增字段：

```sql
ALTER TABLE organizations
    ADD COLUMN default_app_knowledge_quota_bytes BIGINT NOT NULL
        DEFAULT 1073741824
        COMMENT '该企业新建实例的默认个人知识库空间上限（字节）';

ALTER TABLE organizations
    ADD CONSTRAINT organizations_default_app_kb_quota_positive
        CHECK (default_app_knowledge_quota_bytes > 0);
```

- 默认 1073741824 字节（1GB），与当前实例创建默认值一致 → **存量企业行为
  不变**（原来新实例就是 1GB）。
- 沿用现有 `knowledge_quota_bytes` 的字节存储 + `CHECK (> 0)` + 中文 COMMENT
  模式。
- 新 migration：`internal/migrations/000006_org_default_app_kb_quota.up.sql`
  / `.down.sql`；Down 删列与约束。
- 约束 / 字段命名以实际生成的 migration 校验为准，若与既有约束命名风格冲突
  以现有 000005 风格对齐。

### 2. 实例创建链路（核心行为变化）

- `internal/store/queries/apps.sql` 的 `CreateApp` INSERT 增加
  `knowledge_quota_bytes` 列，由参数传入（不再依赖 DB 默认）。
- `internal/service/app_service.go` 的实例创建逻辑：先读取所属企业的
  `default_app_knowledge_quota_bytes`，作为新实例 `knowledge_quota_bytes`
  写入。
- 已有实例、`SetAppKnowledgeQuota`、`AppKnowledgeTab` 编辑入口 **完全不动**。

### 3. sqlc 查询层

- `internal/store/queries/organizations.sql`：
  - `CreateOrganization`：插入 `default_app_knowledge_quota_bytes`。由于是
    必填且 > 0，service 层保证传入合法正值；SQL 侧直接写入即可（可保留
    `COALESCE(NULLIF(..., 0), 1073741824)` 兜底默认，但校验以 service 为准）。
  - `UpdateOrganizationProfile`：更新该列；沿用现有"nil/0 时保留旧值"的
    `COALESCE(NULLIF(...), default_app_knowledge_quota_bytes)` 模式。
  - 各 `Get*` / `List*` 查询返回新列（`SELECT *` 的查询自动包含）。
- 重新生成 `internal/store/sqlc/*.go`（models、organizations.sql.go）。

### 4. Service 层

- `internal/service/organization_service.go`：
  - `OrganizationInput` 增加 `DefaultAppKnowledgeQuotaBytes *int64`。
  - `OrganizationResult` 增加 `DefaultAppKnowledgeQuotaBytes int64`。
  - `CreateOrganization`：校验该字段**必填（非 nil）且 > 0**，不合法返回
    参数错误（区别于企业知识库的"nil 用默认"）。
  - `UpdateOrganization`：给定值时校验 > 0 并更新；未给定时保留旧值。
- 复用 `internal/service/knowledge_quota.go` 中的 `validateKnowledgeQuotaBytes`
  做 > 0 校验；"必填"逻辑（非 nil 检查）在 organization service 内处理。

### 5. Handler / DTO 层

- `internal/api/handlers/dto.go`：
  - `CreateOrganizationRequest` 增加
    `DefaultAppKnowledgeQuotaBytes *int64 json:"default_app_knowledge_quota_bytes"`。
  - `OrganizationRequest`（更新用）同上。
- `internal/api/handlers/organizations.go` 的 `toCreateOrganizationInput` /
  `toOrganizationInput` 透传新字段。

### 6. 前端

- `web/src/pages/platform/OrganizationsPage.vue`：
  - 在「企业知识库空间 (GB)」附近新增「个人知识库空间 (GB)」输入框。
  - GB ↔ Bytes 转换复用现有 `quotaGBToBytes` / `quotaBytesToGB` /
    `editQuotaBytesForPayload` 同款逻辑（为新字段各加一份对应处理）。
  - **必填校验**（不允许空 / 非正数）。
  - 创建表单默认值显示 **1（GB）**。
  - 输入框下方显示**字段说明文案**：
    - 中文：该企业新建实例的默认个人知识库空间上限。仅对之后新建的实例
      生效，不影响已有实例；平台管理员仍可在实例中单独调整。
    - 英文：Default personal knowledge base quota for new instances in this
      organization. Applies only to instances created afterward; existing
      instances are unaffected and can still be adjusted individually.
- `web/src/api/hooks/useOrganizations.ts`：`OrganizationFormPayload` /
  `OrganizationUpdatePayload` 增加 `default_app_knowledge_quota_bytes?: number`。
- i18n：`web/src/i18n/locales/{zh,en}/platform.ts` 增加标签
  「个人知识库空间 (GB)」/「Personal Knowledge Base Quota (GB)」及上述说明
  文案 key。

### 7. OpenAPI / 类型同步

- 修改 handler 签名 / 请求体后跑 `make openapi-gen` + `make web-types-gen`，
  生成产物（`openapi/openapi.yaml`、`web/src/api/generated.ts`）随代码一起
  提交；`make openapi-check` 工作区应干净。

## 测试

- **service（organization）**：
  - 创建企业：缺省该字段 → 参数错误（必填）。
  - 创建企业：该字段为 0 / 负数 → 参数错误。
  - 创建企业：合法正值 → 正确落库并回读。
  - 更新企业：给定合法值 → 更新；未给定 → 保留旧值。
- **service（app 创建）**：
  - 新建实例继承所属企业的 `default_app_knowledge_quota_bytes`（用非 1GB 的
    企业值验证确实来自企业设置，而非 DB 默认）。
  - 已有实例配额不受企业设置变更影响（回归性质，视现有测试结构补充）。
- 断言统一用 testify `assert` / `require`；每个测试与子用例带中文注释说明
  覆盖场景。

## 交付前验证

- `make openapi-check` 干净。
- 相关 Go 单测通过。
- **真实浏览器**验证（平台管理员）：
  - 创建企业时该字段必填、默认 1GB、说明文案可见。
  - 编辑企业时可修改该值并保存回读一致。
  - 在该企业下新建实例，其知识库配额等于企业设置值（非 1GB）。
  - 已有实例配额不变；实例页单独编辑入口仍可用。

## 影响范围

- 新增一个企业级配置字段与一条 migration；实例创建路径读取企业默认值。
- 存量数据：企业默认 1GB，行为与现状一致；不触碰任何已有实例。
