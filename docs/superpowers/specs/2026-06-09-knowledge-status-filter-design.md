# 知识库文件列表按解析状态筛选 — 设计

## 目标

在三个知识库文件列表页加一个「解析状态」下拉筛选，让用户能只看某一解析状态的文件
（如只看「解析失败」）。

## 范围

三个页面：
- 企业知识库 `web/src/pages/knowledge/OrgKnowledgePage.vue`
- 实例知识库 `web/src/pages/apps/AppKnowledgeTab.vue`
- 行业知识库 `web/src/pages/platform/IndustryKnowledgePage.vue`

纯前端改动。后端 service（`ListOrg`/`ListApp`/`ListIndustryFiles`）、handler（读 `c.Query("status")`）
和 SQL（`parse_status` narg 过滤）**均已支持** `status`，本次不动后端、不重新生成 OpenAPI。

## 现状

- org/app 的查询 hook（`web/src/api/hooks/useKnowledge.ts` 的 `useOrgKnowledgeQuery`/`useAppKnowledgeQuery`）
  **已经透传 `status`**：选项类型含 `status`，请求参数按 `...(status ? { status } : {})` 拼接。
- 行业的查询 hook 在另一文件 `web/src/api/hooks/useIndustryKnowledge.ts` 的
  `useIndustryKnowledgeFilesQuery`，**同样已经支持 `status`**：选项类型 `IndustryKnowledgeFileListQueryOptions`
  含 `status?`，`buildIndustryKnowledgeFileListQuery` 按 `...(status ? { status } : {})` 拼接，hook 也已传
  `status: options.status?.value`。即三个 hook 都已就绪，页面把 `status` 传进去即可，无需改 hook。
- 三个页面各自重复定义了 `parseStatusLabel`（状态→中文文案）和 `parseTagType`（状态→标签颜色）。
- 解析状态取值：`queued`(等待解析) / `running`(解析中) / `completed`(已完成) /
  `failed`(解析失败) / `stopped`(已停止)。

## 改动设计

### 1. 新增共享模块 `web/src/domain/parseStatus.ts`

收敛目前三页重复的逻辑，统一导出：
- `PARSE_STATUS_LABELS: Record<string, string>` —— 上述 5 个状态的中文文案；
- `parseStatusLabel(status: string): string` —— 未知值原样透出（沿用现有兜底行为）；
- `parseStatusTagType(status): 'success' | 'warning' | 'error' | 'default'` —— 沿用现有映射
  （completed→success，queued/running→warning，failed/stopped→error，其它→default）；
- `PARSE_STATUS_FILTER_OPTIONS: { label: string; value: string }[]` —— 下拉选项，按
  queued/running/completed/failed/stopped 顺序，label 取对应中文文案。

三个页面改为 import 该模块，删除各自重复的 `parseStatusLabel`/`parseTagType` 本地定义。

### 2. 每个页面加状态下拉

在现有筛选行（文件名输入框旁）加：

```vue
<n-select
  v-model:value="status"
  :options="PARSE_STATUS_FILTER_OPTIONS"
  clearable
  placeholder="全部状态"
  style="width: 160px"
/>
```

- 新增 `const status = ref<string | null>(null)`（`null`/清空＝不过滤，即全部状态）；
- 把 `status`（规范化为 `string | undefined`）传入该页查询 hook 的 `status` 选项；
- 把 `status` 加入该页触发刷新/翻页的 watch，使切换状态时回到第 1 页并重新查询，
  与现有 `keyword` 的处理方式一致。

clearable 清空时 `n-select` 置 `null`，经现有 `...(status ? ...)` / `stringOrUndefined`
归一后请求不带 `status` 参数，等价于「全部状态」，无需显式「全部」选项。

### 3. 查询 hook 无需改动

org/app（`useKnowledge.ts`）与行业（`useIndustryKnowledge.ts`）的列表 hook 选项类型与请求参数拼接
**均已支持 `status`**，页面把 `status` 传入选项即可生效，本次不改任何 hook。

## 测试

- 共享模块 `parseStatus.ts` 加单测，覆盖 `parseStatusLabel` 已知/未知值、`parseStatusTagType` 各分支、
  `PARSE_STATUS_FILTER_OPTIONS` 内容。
- 三页改动以「跑各自既有 `*.spec.ts` 防回归」（确认删本地重复函数、改名不破坏渲染）+ Task 5 的
  **真实浏览器逐页验证**（按 AGENTS.md 新功能必须真实浏览器验证）共同覆盖筛选行为：选状态→列表过滤、
  分页回到第 1 页、请求带 `status`；clearable 清空→恢复全部。
  之所以不为每页新增筛选单测：三页 spec 的 mock 较重，新增筛选单测性价比低，而筛选属用户可见 UI 行为，
  真实浏览器验证更可靠且为本项目硬性要求。

## 不做

- 不改后端、不改 SQL、不重新生成 OpenAPI（`status` 已在 API 契约内）。
- 不做与本筛选无关的页面重构（除上述三页重复函数的收敛外）。
- 暂不加「按状态排序」「多选状态」等未要求的能力（YAGNI）。
