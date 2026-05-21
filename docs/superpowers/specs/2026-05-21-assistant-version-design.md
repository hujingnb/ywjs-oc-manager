# 助手版本（Assistant Version）设计文档

> 设计日期：2026-05-21
> 关联调研：`docs/superpowers/plans/2026-05-19-hermes-agent-pull.md`（Hermes 智能路由调研报告）

---

## 一、背景与目标

当前 manager 把「模型、人设、知识库」配置散落在多个层级：

- 模型：`organizations.model_id`（组织单一模型，迁移 22 刚落地）→ 实例继承。
- 人设：`organization_personas` 表（组织级、版本化）+ `apps.persona_mode` / `apps.app_prompt`（实例级覆盖）。
- 镜像：配置文件 `hermes.runtime_image` 单一全局镜像。
- skill：仅由知识库文件自动派生（`resources/knowledge/{org,app}` → `kb-*` skill），无法独立装配。

这种分散导致：配置无法复用、无法统一治理、无法独立装配 skill、无法按场景做模型路由。

**目标**：引入「助手版本」——一个**平台级版本目录**，把「智能路由（主模型 + 8 个 auxiliary 槽位）、额外 skill 列表、内置提示词、使用镜像」打包成一个可复用、可命名、可版本化的整体。组织拿到版本 allowlist，实例绑定一个版本，所有相关配置都来自版本。

---

## 二、核心概念与决策

| 决策点 | 结论 |
|---|---|
| 模型归属 | **版本完全接管模型选择**。版本的智能路由定义主模型 + 全部 8 个 auxiliary 槽位。组织不再单独选模型，`organizations.model_id` 废弃。 |
| skill 存储 | 每个 skill 是一个 tar 包，存 manager 文件系统主副本；元信息存 `assistant_versions.skills_json`。 |
| 人设 | 去掉组织/实例级人设。版本带一个「内置提示词」文本字段（不叫"人设"，描述里提示"可填写助手人设、行为规则等"）。**保留**平台层 prompt（配置文件 `hermes.system_prompt_template`，所有版本共用）。 |
| 路由槽位 | 主模型 + 全部 8 个 auxiliary 槽位（vision / compression / web_extract / session_search / title_generation / approval / skills_hub / mcp），每个可选，留空走主模型。 |
| 版本生命周期 | 严格保护：版本被任何组织 allowlist 或实例引用时不可删除；组织不能从 allowlist 移除本组织实例正在使用的版本。 |
| 存量数据 | **不考虑存量**。迁移不做 backfill，旧表/旧列直接删除，本地 dev 实例直接重建。 |
| 关联关系存储 | 不建关联表。版本 skill、组织 allowlist 都用 jsonb 列保存。全特性只新增 `assistant_versions` 一张表。 |
| 版本管理权限 | 版本 CRUD 仅 `platform_admin`；组织 allowlist 由平台管理员在创建/编辑组织时设置；实例选版本由 `org_admin` 从本组织 allowlist 中选。 |

---

## 三、数据模型

### 3.1 新表 `assistant_versions`

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | uuid pk | 版本主键 |
| `name` | text | 版本名称，唯一（选择版本时展示）。唯一约束带 `WHERE deleted_at IS NULL` |
| `description` | text null | 详细描述 |
| `system_prompt` | text not null | 「内置提示词」文本，渲染进 SOUL.md 的版本层 |
| `image_id` | text not null | 引用配置文件 `hermes.runtime_images[].id` |
| `main_model` | text not null | 智能路由主模型（new-api 模型名） |
| `routing_json` | jsonb not null default `'{}'` | 8 个 auxiliary 槽位 → 模型名映射，空槽位省略 |
| `skills_json` | jsonb not null default `'[]'` | skill 元信息数组，每项 `{name, file_path, file_size, file_sha256}` |
| `revision` | integer not null default 1 | 内容修订号；影响容器的字段变更时 +1 |
| `created_by` | uuid references users(id) | 创建者 |
| `created_at` / `updated_at` | timestamptz | 时间戳 |
| `deleted_at` | timestamptz null | 软删除（严格保护下仅未被引用的版本可删） |

`routing_json` 形态示例：

```json
{
  "vision": "gpt-5.4",
  "compression": "deepseek-v4-flash",
  "title_generation": "qwen3.5:27b"
}
```

仅记录非空槽位；缺省槽位由 oc-entrypoint 渲染成 `provider: main`。

`skills_json` 形态示例：

```json
[
  {"name": "weather", "file_path": "versions/<id>/skills/weather.tar", "file_size": 20480, "file_sha256": "ab12..."}
]
```

`file_path` 相对 manager 数据根目录；tar 字节存文件系统主副本。

**revision bump 规则**：只有「影响容器」的字段变更才 `revision += 1`——`system_prompt`、`image_id`、`main_model`、`routing_json`、skill 增删改。只改 `name` / `description` 不 bump。

### 3.2 `organizations` 改动

- **新增** `assistant_version_ids` jsonb not null default `'[]'`：该组织可用的版本 id 数组（allowlist）。
- **删除** `model_id` 列（org-single-model 被本特性取代）。

### 3.3 `apps` 改动

- **新增** `version_id` uuid not null references assistant_versions(id)：实例绑定的版本。
- **新增** `applied_version_revision` integer not null default 0：上次初始化/重启时使用的版本 `revision`。
- **新增** `applied_image_ref` text not null default `''`：上次实际拉取的镜像 ref。
- **删除** `model_id`、`persona_mode`、`app_prompt`、`model_synced` 列。

### 3.4 删除 `organization_personas` 表

整表删除，连同 persona service / handler / 前端页面一并移除。

### 3.5 变更检测（version_synced）

实例的「需重启」状态是计算值，不落库为布尔列：

```
version_synced = (apps.applied_version_revision == assistant_versions.revision)
              AND (apps.applied_image_ref      == 配置解析(assistant_versions.image_id).ref)
```

service 层在实例列表查询时 join `assistant_versions` 取 `revision` 与 `image_id`，再用启动配置把 `image_id` 解析成 `ref`，与 app 落库的两个 applied 字段比对。

两类变更都能触发「需重启」：

1. **版本被编辑**（提示词/路由/skill/改 image_id）→ `revision +1` → `applied_version_revision` 不匹配。
2. **配置文件镜像更新**——某 `image_id` 的 `ref` 被换成新构建 → `applied_image_ref` 不匹配。

---

## 四、配置文件：镜像列表

`hermes.runtime_image`（单字符串）改为 `hermes.runtime_images`（列表）：

```yaml
hermes:
  runtime_images:
    - id: "v2026.5.16"
      label: "Hermes v2026.5.16（当前）"
      ref: "crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-hermes:v2026.5.16-..."
    - id: "v2026.5.15"
      label: "Hermes v2026.5.15（旧版）"
      ref: "crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_app/oc-manager-hermes:v2026.5.15-..."
```

- `id`：管理员选定的稳定槽位标识，版本表 `image_id` 存它。
- `label`：前端 select 展示文案。
- `ref`：具体可拉取的镜像 tag。hermes tag 现已带提交号，每次构建唯一；重建后管理员把同一 `id` 下的 `ref` 换成新构建。

manager 提供只读 API 把镜像列表（`id` + `label`）暴露给平台管理员前端，供版本编辑表单的「使用镜像」select 使用。

配置加载时校验 `runtime_images` 非空、`id` 唯一、`ref` 非空。

### 4.1 第二个测试 variant

新建 `runtime/hermes/hermes-v2026.5.15/`：当前 `hermes-v2026.5.16/` 的近拷贝，改 `version.txt` 为 `v2026.5.15`，目录名与 version.txt 对齐（Makefile `.guard-hermes-version` 要求）。用于测试版本切换与跨 variant 数据迁移（`migrator/`）。

---

## 五、Manifest 契约 v2

manifest.yaml 是 manager 与 oc-entrypoint 的契约，两端都在本仓库，一起改。

```yaml
app:
  id:   <app uuid>
  name: <app name>
  model: <version.main_model>          # 主模型来自版本

routing:                               # 新增：8 个 auxiliary 槽位，空槽位省略
  vision: <model>
  compression: <model>
  web_extract: <model>
  session_search: <model>
  title_generation: <model>
  approval: <model>
  skills_hub: <model>
  mcp: <model>

credentials:
  openai:
    api_key:  <sk-...>
    base_url: <new-api base url, 不带 /v1>

resources:
  persona: resources/persona.md        # = version.system_prompt
  rules:
    platform: resources/platform-rules.md   # 仅保留平台层
  skills:                              # 新增：版本 skill tar 相对路径列表
    - resources/skills/weather.tar
    - resources/skills/calc.tar
```

相对 v1 的差异：

- 新增 `routing` 段（8 槽位）。
- 新增 `resources.skills`（版本 skill tar 列表）。
- **移除** `resources.rules.organization` 与 `resources.rules.application`（组织层一直为空，应用层来自已删除的 `app_prompt`）。

---

## 六、Hermes 镜像（oc-entrypoint）改动

针对 `runtime/hermes/hermes-v2026.5.16/`（以及新建的 `hermes-v2026.5.15/`）：

### 6.1 `lib/manifest.py`

- 解析新增的 `routing` 段（dict，可为空）。
- 解析新增的 `resources.skills` 列表（list[str]，可为空）。
- 去掉 `resources.rules.organization` / `resources.rules.application` 必填校验，仅保留 `platform`。

### 6.2 `renderer/render_config_yaml.py`

把 manifest `routing` 渲染进 `auxiliary` 全部 8 个槽位：槽位在 `routing` 中有值则 `{provider: custom, model: <model>, base_url, api_key}`，无值则 `{provider: main}`。base_url / api_key 复用 `credentials.openai`。

### 6.3 `renderer/render_soul_md.py`

SOUL.md 结构简化为：内置 header（语言要求）+ 平台层（`resources/platform-rules.md`）+ 版本 persona（`resources/persona.md`）+ 知识库 always-on 摘要。去掉组织层、应用层两段。

### 6.4 `renderer/render_skills.py`：隐藏标记文件机制

不靠目录名前缀区分 skill 来源（tar 内部目录名不可控）。改为：oc-entrypoint 安装的每个 skill 目录内放一个隐藏标记文件 `.oc-managed`（小 JSON，记 `source: "version-skill" | "knowledge"`、`installed_at`）。

每次 render 流程：

1. 扫 `data/skills/*/`，**删除所有含 `.oc-managed` 的目录**。
2. 重新渲染知识库 `kb-*` skill（保留现有逻辑），每个目录补写 `.oc-managed`（`source: knowledge`）。
3. 解压 manifest `resources.skills` 列出的每个版本 skill tar 到 `data/skills/` 下，每个目录补写 `.oc-managed`（`source: version-skill`）。
4. 镜像内置 skill 没有标记 → 永不触碰。

效果：版本切换、删 skill 都自动正确——上一次安装的版本 skill 全被清掉再整体换新；知识库 skill 保留；内置 skill 保留。

### 6.5 skill tar 解压安全

解压时校验 tar 条目路径不越界（不含 `..`、不含绝对路径、不含符号链接逃逸），防止 skill tar 写到 `skills/` 之外。

---

## 七、Manager 后端

### 7.1 版本 service / handler

新增 `internal/service/assistant_version_service.go` 与 `internal/api/handlers/assistant_versions.go`：

- 列表 / 详情 / 创建 / 编辑 / 删除（`platform_admin`）。
- skill tar 上传 / 删除：multipart 上传，校验合法 tar、大小上限（建议 10 MiB）、tar 内含 `SKILL.md`；写 manager 文件系统主副本，更新 `skills_json`。
- 创建/编辑时用 `ModelCatalogService` 校验 `main_model` 与 `routing_json` 中的每个模型名都存在于 new-api 实时模型列表。
- 校验 `image_id` 存在于配置 `runtime_images`。
- 编辑时按 §3.1 规则决定是否 bump `revision`。
- 删除前做严格保护检查：被任何组织 allowlist 或任何实例引用则拒绝。

### 7.2 权限

`internal/auth/authorizer.go` 新增谓词：

- `CanManageAssistantVersion(principal)`：仅 `platform_admin`。
- `CanViewAssistantVersion(principal)`：`platform_admin` 看全部；`org_admin` 只读本组织 allowlist 内版本（创建实例时选用）。

### 7.3 组织 service 改动

- 创建/编辑组织时接收 `assistant_version_ids`（多选），校验每个 id 存在且未删除，写入 `organizations.assistant_version_ids`。
- 移除 `model_id` 相关逻辑。
- 编辑 allowlist 时：若要移除的版本正被本组织某实例使用，拒绝（严格保护）。

### 7.4 实例创建流程改动

`onboarding_service` / 实例创建链路：

- 接收 `version_id`，校验它在该组织的 `assistant_version_ids` 内，写入 `apps.version_id`。
- 移除 persona / 模型相关入参。

### 7.5 实例初始化 / 重启

`internal/worker/handlers/app_initialize.go` 与 restart 刷新链路：

- 加载 app → 加载 `assistant_versions` 行 → 解析 `image_id` 为镜像 `ref`、取 `routing`、`system_prompt`、`skills`。
- 镜像 ref 不再来自全局 `cfg.RuntimeImage`，改为按版本 `image_id` 解析。
- `BuildAppInputData` / `WriteAppInput`：写 manifest v2（含 `routing` + `resources.skills`）、`resources/persona.md`（= 版本 `system_prompt`）、`resources/platform-rules.md`（= 配置 `system_prompt_template`，保留）。
- 推送版本 skill tar 到 `apps/<id>/input/resources/skills/<name>.tar`。
- 知识库推送逻辑不变。
- 成功后写回 `apps.applied_version_revision = version.revision`、`apps.applied_image_ref = 解析后的 ref`。

### 7.6 实例切换版本

实例详情页新增「切换版本」动作：改 `apps.version_id`（校验在组织 allowlist 内）→ 因 `applied_version_revision` 与新版本 `revision` 不匹配，实例自动进入「需重启」态 → 用户点重启后，按 §7.5 重新初始化生效（重启会重拉镜像、重写 input、oc-entrypoint 重渲染）。

---

## 八、前端

### 8.1 助手版本管理页（新增，平台管理员）

- 版本列表：名称、描述、镜像、主模型、修订号、引用情况。
- 创建/编辑表单字段：
  - **名称**：唯一值。
  - **描述**：详细描述。
  - **内置提示词**：textarea，helper 文案「可填写助手人设、行为规则等」。
  - **使用镜像**：select，选项来自配置 `runtime_images`（`label` 展示、`id` 提交）。
  - **主模型**：select，选项来自 new-api 模型列表。
  - **智能路由**：8 个 auxiliary 槽位，每个一个 select（选项来自 new-api 模型列表），留空表示走主模型。
  - **skill 列表**：tar 包上传组件，可上传多个、可删除单个。

### 8.2 组织创建/编辑

- 新增「可用助手版本」多选（allowlist）。
- 移除模型 select。

### 8.3 实例创建

- 新增「助手版本」单选，选项为该组织 allowlist 内的版本。
- 移除人设、模型相关配置项。

### 8.4 实例列表

- `version_synced == false` 时展示「需重启更新」提示（复用现有 `model_synced` 提示 UI 与交互）。

### 8.5 实例详情

- 新增「切换版本」动作。

### 8.6 移除

- 删除 `web/src/pages/org/PersonaPage.vue` 及相关路由、store、API 调用。

---

## 九、OpenAPI 同步

涉及新增 handler、请求体、响应类型、路由，按仓库规范执行 `make openapi-gen` + `make web-types-gen`，把 `openapi/openapi.yaml` 与 `web/src/api/generated.ts` 连同代码一起提交。新增请求体类型放 `internal/api/handlers/dto.go` 并导出大写命名，响应用 `service.XxxResult`。

---

## 十、测试要点

- 版本 service：CRUD、唯一名校验、模型名校验、image_id 校验、revision bump 规则、严格保护（被引用时拒删/拒移除）。
- skill 上传：合法 tar、大小上限、缺 `SKILL.md`、路径越界拒绝。
- 变更检测：`version_synced` 在「版本被编辑」「配置镜像 ref 更新」「切换版本」三种场景下的计算正确性。
- oc-entrypoint（pytest）：`render_config_yaml` 渲染 8 槽位、`render_soul_md` 仅平台层 + persona、`render_skills` 的 `.oc-managed` 标记清理与重装、版本切换后旧 skill 被清、知识库 skill 与内置 skill 保留、tar 解压路径越界防护。
- 端到端：用 v2026.5.16 与 v2026.5.15 两个版本走完整版本切换 + 重启更新流程，真实浏览器验证。

---

## 十一、实施分期（供 writing-plans 参考）

1. DB 迁移（建 `assistant_versions`、改 `organizations` / `apps`、删 `organization_personas`）+ 版本 CRUD 后端 + 版本管理前端页。
2. 配置镜像列表 + 镜像列表只读 API + manifest 契约 v2 + oc-entrypoint 全部改动 + 新建 `hermes-v2026.5.15` variant。
3. 组织 allowlist + 实例绑定版本 + 创建流程改造。
4. 实例初始化/重启写入版本数据 + `version_synced` 检测 + 重启刷新 + 切换版本动作。
5. 移除 persona / 组织 model 的清理（service / handler / 前端页面）。
