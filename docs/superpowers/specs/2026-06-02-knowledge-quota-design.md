# 知识库空间容量配置 — 设计文档

- 日期：2026-06-02
- 状态：已确认，待实现
- 作者：hujing + Codex

## 背景与目标

当前企业知识库和实例知识库只限制单文件上传大小，不能限制一个知识库累计占用的空间。
本次需求是为两类知识库增加累计容量上限：

- **企业知识库**：由平台管理员设置空间大小。
- **实例知识库 / 个人知识库**：由企业管理员为单个实例设置空间大小，并且后续可编辑。

容量限制用于控制知识库文件累计占用，超出后拒绝继续上传；删除文件后释放容量。

## 术语与统计口径

- **企业知识库容量**：某个企业 `scope_type = 'org'` 的所有 `ragflow_documents.size_bytes` 之和。
- **实例知识库容量**：某个实例 `scope_type = 'app'` 且 `app_id = ?` 的所有 `ragflow_documents.size_bytes` 之和。
- **已用空间**：当前本地 document mapping 中对应 scope 的 `SUM(size_bytes)`。
- **剩余空间**：`quota_bytes - used_bytes`，展示时小于 0 按 0 处理。
- **容量单位**：后端统一保存字节数，前端以 GB 为主要输入单位展示。

解析失败、已停止、等待解析、解析中和已完成的文件都计入已用空间。只要文件仍在
RAGFlow 和 manager document mapping 中存在，就认为占用空间；删除文件后自然释放。

## 已确认语义

1. **所有企业和实例都必须有限制。** 不支持“不限制”。
2. **默认容量为 1GB。** migration 新增字段时默认写入 1GB；如果线上已存在空值或历史补丁产生空值，
   部署 SQL 必须统一更新为 1GB 后再启用 `NOT NULL` 约束。
3. **容量值必须为正整数。** 后端保存 `quota_bytes > 0`。
4. **允许把容量改到低于当前已用。** 已有文件不受影响，后续上传会被拒绝，直到删除文件释放空间。
5. **删除文件释放容量。** 容量统计基于当前 `ragflow_documents` 行，不做额外用量缓存。
6. **并发上传接受轻微 race。** 这是管理面低频场景，先不为强一致加行锁或事务锁。
7. **单文件上限仍保留 1GB。** 累计容量限制是在现有单文件硬上限之外新增的业务限制。

## 推荐方案

采用「容量配置跟随业务主体」的设计：

- `organizations` 表新增企业知识库容量字段。
- `apps` 表新增实例知识库容量字段。
- 知识库上传路径在 service 层用本地文件元数据做累计容量校验。

不把容量配置放到 `ragflow_datasets`，因为 dataset 是知识库映射资源，生命周期与业务主体不同；
也不单独新建配置表，因为当前只有一个容量字段，额外配置表会增加不必要的数据访问复杂度。

## 数据模型

### organizations

新增字段：

```sql
knowledge_quota_bytes BIGINT NOT NULL DEFAULT 1073741824
```

约束：

```sql
CHECK (knowledge_quota_bytes > 0)
```

平台管理员创建 / 编辑企业时设置该字段。存量企业 migration 后均为 1GB。

### apps

新增字段：

```sql
knowledge_quota_bytes BIGINT NOT NULL DEFAULT 1073741824
```

约束：

```sql
CHECK (knowledge_quota_bytes > 0)
```

新建实例默认 1GB。企业管理员可在实例知识库页面编辑单个实例的容量。

### ragflow_documents

沿用现有 `size_bytes` 字段做容量统计，不新增缓存字段。

新增 sqlc 查询：

```sql
-- name: SumRAGFlowDocumentsSizeByScope :one
SELECT COALESCE(SUM(size_bytes), 0)
FROM ragflow_documents
WHERE scope_type = ?
  AND org_id = ?
  AND (sqlc.narg(app_id) IS NULL OR app_id = sqlc.narg(app_id));
```

如实现时需要更清晰的类型，也可以拆成 `SumRAGFlowOrgDocumentsSize` 和
`SumRAGFlowAppDocumentsSize` 两个查询。

## 权限设计

权限谓词必须继续集中在 `internal/auth/authorizer.go`。

- **编辑企业知识库容量**：仅平台管理员可编辑，因为该容量属于企业级平台配置。
- **编辑实例知识库容量**：企业管理员可编辑本企业实例；平台管理员也可编辑，作为运维兜底；普通成员不可编辑。
- **读取容量信息**：与读取知识库权限一致。能看企业 / 实例知识库列表的用户，也能看到已用、上限和剩余空间。
- **上传校验**：不依赖前端权限。所有写入入口都必须在 service 层校验容量。

建议新增或复用权限函数：

- `CanUpdateOrgKnowledgeQuota(principal)`：仅平台管理员。
- `CanUpdateAppKnowledgeQuota(principal, appOrgID)`：平台管理员或本组织企业管理员。

## 接口设计

### 企业接口

企业知识库容量跟随现有企业资料接口：

- `POST /api/v1/organizations`
- `PATCH /api/v1/organizations/{orgId}`
- `GET /api/v1/organizations`
- `GET /api/v1/organizations/{orgId}`

DTO 增加：

```json
{
  "knowledge_quota_bytes": 1073741824
}
```

前端表单可用 GB 输入，但提交给后端时使用 bytes，避免后端猜测单位。

### 实例容量编辑接口

新增轻量接口：

```http
PATCH /api/v1/apps/{appId}/knowledge/quota
```

请求：

```json
{
  "quota_bytes": 1073741824
}
```

响应：

```json
{
  "app": {
    "...": "现有 AppResult 字段",
    "knowledge_quota_bytes": 1073741824
  }
}
```

该接口只修改实例知识库容量，不承担应用资料编辑、版本切换或运行时操作能力。

### 知识库列表响应

扩展 `KnowledgeListResult`：

```json
{
  "items": [],
  "total": 0,
  "used_bytes": 0,
  "quota_bytes": 1073741824,
  "remaining_bytes": 1073741824
}
```

`items` 和 `total` 保持现有语义。新增字段用于前端展示容量条和上传前本地拦截。

## Service 设计

容量校验放在 `KnowledgeService`，确保 handler、前端和 runtime token 路径都无法绕过。

### 企业知识库上传

`SaveOrgFile` 在获取 org dataset 后、上传 RAGFlow 前执行：

1. 读取组织 `knowledge_quota_bytes`。
2. 统计 org scope 已用空间。
3. 判断 `used_bytes + upload_size <= quota_bytes`。
4. 超出时返回 `ErrKnowledgeQuotaExceeded`。
5. 未超出时继续上传 RAGFlow、写入 document mapping、触发解析。

### 实例知识库上传

`SaveAppFile` 在读取 app 后执行同样校验：

1. 使用 app 的 `knowledge_quota_bytes`。
2. 统计该 app scope 已用空间。
3. 超出时返回 `ErrKnowledgeQuotaExceeded`。

### Runtime 写入

`RuntimeAddFile` 通过 app runtime token 写入当前实例知识库，也必须使用同一个 app 容量校验。
Hermes 工作目录产物不能绕过实例知识库容量限制。

### 无 Content-Length 请求

为避免上传到 RAGFlow 后才发现超限，知识库上传请求必须提供可用大小。
如果 handler 无法取得有效 `Content-Length`，返回 400，提示缺少文件大小信息。

## 错误设计

新增 sentinel error：

```go
ErrKnowledgeQuotaExceeded
```

HTTP 映射：

- 状态码：`409 Conflict`
- 错误码：`KNOWLEDGE_QUOTA_EXCEEDED`
- 文案：`知识库空间不足，剩余 120MB`

handler 层只做错误码映射；可展示文案由 service 构造安全、明确的业务错误信息。

## 前端设计

### 企业管理页

平台管理员创建 / 编辑企业表单增加：

- 字段：企业知识库空间
- 输入单位：GB
- 默认值：1
- 校验：必须大于 0

列表或详情中展示容量时使用统一字节格式化。

### 企业知识库页

展示：

- 已用空间
- 空间上限
- 剩余空间

上传按钮旁保留现有“单文件最大支持 1024MB”提示。
选择文件后，如果 `file.size > remaining_bytes`，前端直接提示空间不足；后端仍做最终校验。

### 实例知识库页

展示同样的容量信息，并增加“编辑空间”入口：

- 仅企业管理员和平台管理员可见。
- 弹窗输入 GB，提交为 bytes。
- 允许设置为小于当前已用；保存后页面展示“已用超过上限”状态，后续上传被拦截。

普通成员仍可查看容量，但不能编辑。

## OpenAPI 与生成类型

修改 handler 请求体、响应体或路由后必须运行：

```bash
make openapi-gen
make web-types-gen
```

同步提交：

- `openapi/openapi.yaml`
- `web/src/api/generated.ts`

不得手工编辑生成产物。

## 测试计划

### 后端单元测试

- 企业知识库：已用 + 上传大小小于上限，上传成功。
- 企业知识库：已用 + 上传大小等于上限，上传成功。
- 企业知识库：已用 + 上传大小超过上限，返回 `ErrKnowledgeQuotaExceeded`。
- 实例知识库：`SaveAppFile` 超限返回 `ErrKnowledgeQuotaExceeded`。
- Runtime 写入：`RuntimeAddFile` 超限返回 `ErrKnowledgeQuotaExceeded`。
- 解析失败 / 已停止文件仍计入 `SUM(size_bytes)`。
- 删除文件后 document mapping 消失，容量汇总下降。
- 编辑实例容量低于当前已用仍保存成功。
- 无有效 `Content-Length` 的上传请求返回 400。

### 前端测试

- 企业表单容量字段默认 1GB，提交 bytes。
- 企业知识库页展示已用 / 上限 / 剩余。
- 实例知识库页展示容量，并且企业管理员可打开编辑弹窗。
- 普通成员看不到编辑容量入口。
- 文件大小超过剩余空间时，前端提示空间不足，不发起上传会话。

### 浏览器验证

交付前必须使用真实浏览器验证：

1. 平台管理员创建或编辑企业知识库容量。
2. 企业知识库上传到接近上限。
3. 超出剩余空间的文件被拦截并提示。
4. 企业管理员进入实例知识库，编辑实例容量。
5. 实例知识库超限上传被拦截。
6. 删除文件后剩余空间恢复。

## 影响范围

- 数据库 migration、sqlc schema 与查询生成。
- 组织创建 / 编辑 / 查询接口与前端企业管理表单。
- 应用详情接口、实例知识库容量编辑接口与前端实例知识库页。
- 知识库列表响应、上传校验、runtime token 写入路径。
- OpenAPI 与前端生成类型。

## 不做

- 不做“不限制”选项。
- 不做按文件类型、解析状态或来源分别限额。
- 不做强一致容量锁。
- 不把容量同步写入 RAGFlow。
- 不做容量变更历史审计，沿用现有操作审计边界；如后续需要再单独设计。
