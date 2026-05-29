# spec-B：app 数据模型（S3 + 启动回调 bootstrap）设计

> 状态：设计待评审（2026-05-29）。
> 父设计：`docs/superpowers/specs/2026-05-29-k8s-migration-design.md`（§5 Workstream B、D6/D7/D8/D15/D16/D17、§8、§10）。
> 本 spec 是 k8s 迁移 §9 拆分的 **Workstream B**，是 **spec-A（编排 k8s 化）的前置数据契约**。

## 1. 背景与目标

### 1.1 现状

manager 通过 **runtime-agent 节点的 file API** 读写 app 的所有文件，没有任何对象存储参与：

- 文件通道：`internal/integrations/agent/file_client.go` 的 `AgentFileClient`
  （`Upload`/`Download`/`Archive`/`Delete` + scope-aware 的 `InitAppDirs`/
  `UploadAppInputFile`/`ListWorkspace`/`DownloadWorkspaceFile`/`ClearAppSessions`），
  manager 经 HTTP 调 agent 的 `/v1/files/*`、`/v1/scopes/apps/<id>/*`。
- manifest 渲染：`internal/integrations/hermes/manifest.go`（`Manifest` 结构 +
  `MarshalManifestYAML`）在 manager 内存渲染；`internal/integrations/hermes/app_input.go`
  的 `WriteAppInput` 经 `AppInputWriter.WriteAppInputFile` 把 `resources/persona.md`、
  `resources/platform-rules.md`、`manifest.yaml` **写到节点 input 卷**。
- api_key：`internal/worker/handlers/app_initialize.go` 的 `ensureAPIKey` 经 new-api
  创建 sk- key，加密存 `apps.newapi_key_ciphertext`，明文经 manifest 下发。
- skill blob：`internal/service/skill_blob_store.go` 的 `FSSkillBlobStore` 存 manager
  本地 FS（`{root}/versions/{versionID}/skills/{name}.tar`），初始化时
  `pushVersionSkills`/`AssembleVersionInputData`（`app_initialize.go`）逐个读本地 tar
  经 file API 推到节点 `apps/<id>/input/resources/skills/`。
- per-app token：`internal/service/app_runtime_token.go` 的 `EnsureAppRuntimeToken`/
  `HashAppRuntimeToken`/`generateAppRuntimeToken`，存 `apps.runtime_token_hash`/
  `runtime_token_ciphertext`（SHA256 hash + master_key 加密密文），注入
  `manifest.knowledge.app_token` 给 hermes 的 `oc-kb` skill **出站**调 manager
  runtime/knowledge API（`GetAppByRuntimeTokenHash` 反查）。
- 路由：`internal/api/router.go:103-115` 仅 public / agent(`/api/v1/agent`、
  `/api/v1/runtime`) / user 三组，**无 `/internal/*` 组**。
- 配置：`internal/config/config.go` **无 `k8s.*` / `storage.s3.*` 字段**。

### 1.2 目标

把 app 数据从「manager↔agent 点对点 file API + 宿主本地盘」迁为
**「S3 持久化源 + pod 启动回调 manager 拉配置」**（父设计 D6/D7）：

- 文件持久源进标准 S3；pod 运行期临时盘（emptyDir）由 spec-A 落地。
- 新增 manager 内部端点 `GET /internal/apps/{id}/bootstrap`：DB 实时渲染 manifest
  （含 api_key）+ 签发 skills/restore 的预签名读 URL + app prefix 限定的 STS 写凭证。
- per-app token 三用统一为一把 **control token**（D8、§8）。
- **本 spec 只交付 manager 侧数据层**，全部可单测 / 对真实 MinIO 集成测。

### 1.3 现状调研结论（前置已确认）

- 仓库**完全没有** S3 / MinIO / aws-sdk / 预签名 / STS 任何代码——S3 抽象层从零建。
- 已有 per-app token 基础设施（hash + 加密 + 条件写库 + 按 hash 反查）可直接复用，
  token 统一无需新建一套加密机制。
- apps 表、manifest 渲染、app 初始化状态机均已就位，spec-B 主要在**存储引擎**与
  **pod 启动协议（bootstrap）**两层做新增，DB schema 基本不动。

## 2. 范围与边界

### 2.1 本 spec 交付（已与用户确认）

1. **S3 抽象层**：`internal/integrations/storage` 包，基于 **aws-sdk-go-v2 标准 S3
   协议**，提供对象读写（`PutObject`/`PresignGet`/`MovePrefix`/`DeletePrefix`）与
   **标准 STS `AssumeRole`** 签发 prefix 限定的临时写凭证（`STSIssuer`）。
2. **bootstrap 端点**：`GET /internal/apps/{id}/bootstrap`，Bearer control token
   鉴权，DB 实时渲染 manifest + 预签名 URL + STS 凭证。新建 `/internal/*` 路由组与
   token 中间件。
3. **token 三用统一**：`runtime_token` 列语义升级为 per-app **control token**，复用
   `app_runtime_token.go`；bootstrap / oc-kb / oc-ops 共用同一把。
4. **manifest 改造（新增渲染出口）**：bootstrap 端点复用现 manifest 渲染逻辑产出
   manifest 返回（不删旧的写 input 卷路径——删除在 spec-A 收口）。
5. **skill blob 迁 S3**：`SkillBlobStore` 接口 + S3 实现，版本发布时上传到
   `versions/<vid>/skills/`，读走预签名 URL。
6. **配置扩展**：新增 `storage.s3.*`（endpoint/bucket/region/credentials/STS role）。
7. **HTTP/数据契约文档** + 单测 + 对真实 MinIO 的 S3 集成测。

### 2.2 不在本 spec（归 spec-A / 保持原样）

- **pod 侧产物**：initContainer（restore）/ sidecar（mc mirror、sqlite `.backup`）
  脚本、镜像改造、pod spec 渲染、k8s Secret 注入 control token、Service DNS——**全归
  spec-A**（与 pod 编排同生共死，才能真实运行）。
- **删除旧通道**：runtime-agent file API、节点概念、`WriteAppInput` 的写卷路径——
  spec-B 只**新增** bootstrap/S3 路径，**不删**旧路径；删除在 spec-A 收口（避免 B 阶段
  manager 不可用）。
- **oc-ops 调用通道**：spec-E 已交付；control token 的 Secret 注入在 spec-A。
- **MySQL 迁移**：spec-C 已交付。

### 2.3 关键取舍（已与用户确认）

| # | 决策点 | 选择 | 理由 / 影响 |
|---|---|---|---|
| B1 | A/B 拆分 | **拆成 spec-B（数据层）→ spec-A（编排）两个独立周期**，本 spec 只做 B | B 是 A 的前置契约（pod 创建要调 bootstrap、要 S3 凭证）；各自更小、可独立单测 |
| B2 | spec-B 产物边界 | **纯 manager 侧数据层**；pod 侧脚本/镜像归 spec-A | bootstrap/S3 抽象是可独立单测的 manager 逻辑；pod 脚本依赖 A 的编排才能真跑 |
| B3 | S3 写凭证 | **标准 STS `AssumeRole` 临时凭证**（prefix 限定、短期、可续） | pod 零长期密钥、最小权限（父设计 §5.2 原意）；sidecar mc mirror 需凭证（预签名不适合批量） |
| B4 | S3 SDK / 协议 | **aws-sdk-go-v2，完全标准 S3 协议**，**不依赖 MinIO 私有协议** | 生产 vendor-neutral、可插拔任意云 OSS；本地 MinIO 兼容标准 S3 + STS AssumeRole |
| B5 | token 语义 | **统一为一把 per-app control token**，吸收现有 `runtime_token`（oc-kb 出站那把） | 同一 app 同一信任域，分多把无实际隔离收益；Secret 只放一把；复用现有加密/hash 基础设施 |
| B6 | 验证范围 | **单测（httptest）+ 对真实 MinIO 的 S3 集成测**；pod 完整闭环 + 三角色浏览器验证推迟到 A/B/D/E 合并 | pod 编排在 spec-A，B 阶段无 pod 可跑；但 S3/STS/预签名是外部协议交互，须对真实 MinIO 证明，不被 mock 掩盖 |

> **B6 是对项目「所有新功能须真实浏览器/真实环境验证」要求的一次显式、有界偏离**，
> 仅限本迁移序列：spec-B 交付并单测 + MinIO 集成测的数据层，待 spec-A 把它接进 pod
> 编排后，与 A/B/D/E 一起做端到端 + 三角色真实浏览器验证。本 spec 不单独宣称
> 「pod 闭环已验证可用」。

## 3. 目标架构

```
                    app pod（spec-A 渲染并 apply；本 spec 只钉 manager 侧契约）
manager-api         ┌──────────────────────────────────────────────────────┐
┌──────────────────┐│ initContainer: 带 control token 调 bootstrap            │
│ /internal/apps/  ││   → 写 manifest 到 emptyDir /opt/oc-input               │
│  {id}/bootstrap  │◀───(Bearer control token)── 预签名 URL 拉 skills          │
│   handler        ││   → 预签名 URL restore workspace/sessions/state.db      │
│      │           ││ main: hermes（读 manifest，零改）                        │
│      ├ manifest 渲染（复用 manifest.go / app_input.go）                       │
│      ├ ObjectStore（aws-sdk-go-v2 标准 S3）─────────┐                        │
│      └ STSIssuer（标准 AssumeRole, prefix 限定）────┤  sidecar: 用 STS 凭证   │
└──────────────────┘                                 │   mc mirror → S3        │
                                              ┌───────▼────────┐└──────────────┘
                                              │ MinIO（本地 k3d）│ 标准 S3 端点
                                              │ / 云 OSS（prod） │
                                              └────────────────┘
```

- manager 只对 **标准 S3 + STS + MySQL** 说话；bootstrap 是新增的 **pod→manager** 入站路径。
- api_key **不进 S3 / 不落盘**：bootstrap 内存渲染，经认证通道交给 pod（D7）。
- pod 侧 initContainer/sidecar（虚线框内）是 **spec-A 产物**；spec-B 钉死它们消费的契约。

## 4. S3 数据布局（单 bucket + prefix）

```
<bucket>/
  versions/<versionID>/skills/<name>.tar     # version 级；write-once；manager 上传，pod 预签名只读
  apps/<appID>/workspace/...                 # app 级；sidecar mc mirror 读写
  apps/<appID>/sessions/...                  # app 级；会话
  apps/<appID>/state.db                      # app 级；sqlite 一致性快照（.backup 产物，spec-A 落地）
  apps/<appID>/archive/...                   # 删除归档（MovePrefix 目标）
```

- **STS 写凭证 prefix 限定到 `apps/<appID>/*`**：pod 只能写自己 app 的前缀，越权写
  其它 app / versions 被 S3 策略拒绝。
- **skills 是 manager 签发的预签名只读 URL**：pod 不持有写 skills 的权限（write-once
  由 manager 在版本发布时完成）。
- 对应父设计 §5.4 的 S3 数据分类：manifest/api_key 不进 S3；skills/workspace/
  sqlite/archive 进 S3；渲染产物（config.yaml/SOUL.md/env）不持久化。

## 5. bootstrap 端点契约

### 5.1 路由与鉴权

- `GET /internal/apps/{id}/bootstrap`。
- 新建 `/internal/*` 路由组（`router.go` 现无），独立 **control token 中间件**：读
  `Authorization: Bearer <token>` → SHA256 hash → `GetAppByRuntimeTokenHash` 反查 →
  校验 `{id}` 与 token 所属 app 一致；不匹配 / 缺失 → `401`。
- 与 agent 组（enrollment_secret）/ user 组（用户会话）隔离：bootstrap 是 app 实例
  自证身份的窄通道。

### 5.2 响应体（DB 实时渲染）

```jsonc
{
  "manifest": { /* 现有 Manifest 结构（hermes/manifest.go），含： */
    "app": {"id","name","model"},
    "credentials": {"openai": {"api_key": "<明文 sk->", "base_url": "..."}},  // 不落 S3/盘
    "resources": {"persona","rules","skills": ["resources/skills/<name>.tar", ...]},
    "knowledge": {"runtime_base_url", "app_token": "<control token>"},        // oc-kb 出站用
    "routing": {"<alias>": "<model>"}
  },
  "skills": [ {"name": "...", "url": "<预签名 GET, versions/<vid>/skills/<name>.tar>"} ],
  "restore": {                                  // 首启为空对象 / 缺省字段
    "workspace_url": "<预签名 GET, apps/<id>/workspace 归档>",
    "state_db_url":  "<预签名 GET, apps/<id>/state.db>",
    "sessions_url":  "<预签名 GET, apps/<id>/sessions 归档>"
  },
  "s3_write": {                                 // 标准 STS AssumeRole 产物
    "endpoint": "...", "region": "...", "bucket": "...",
    "prefix": "apps/<id>/",
    "access_key_id": "...", "secret_access_key": "...", "session_token": "...",
    "expires_at": "<RFC3339>"                   // pod 可在过期前重调 bootstrap 续期
  }
}
```

- **续期**：bootstrap 幂等可重复调用；STS 凭证过期前 pod（sidecar，spec-A）重调拿新
  凭证。manager 侧无状态，每次现签。
- **首启判定**：`restore` 字段按 S3 是否存在对应对象决定是否给出 URL；不存在则省略，
  pod 跳过 restore。

### 5.3 错误映射

| 场景 | HTTP | body |
|---|---|---|
| token 缺失 / 不匹配 / 与 {id} 不符 | 401 | `{"code":"UNAUTHORIZED",...}` |
| app 不存在 / 已软删 | 404 | `{"code":"NOT_FOUND",...}` |
| S3 / STS 签发失败 | 502 | `{"code":"STORAGE_UNAVAILABLE",...}` |
| 渲染内部错误 | 500 | `{"code":"INTERNAL",...}` |

## 6. token 统一（一把 per-app control token）

- **物理列保留** `apps.runtime_token_hash` / `runtime_token_ciphertext`（避免改 MySQL
  基线 + sqlc 重生成的大范围 churn），**语义升级**为 per-app **control token**，三用合一：
  1. pod→manager **bootstrap** 拉配置（本 spec 新增的校验路径）；
  2. pod→manager **oc-kb** 调 knowledge API（现有，`manifest.knowledge.app_token`）；
  3. manager→pod **oc-ops** 调命令（spec-E 的 `OC_OPS_TOKEN`，Secret `control-token`
     键，Secret 注入在 spec-A）。
- 复用 `app_runtime_token.go` 的 `EnsureAppRuntimeToken`/`HashAppRuntimeToken`/
  条件写库与并发冲突回读，**不新建加密机制**。
- 代码/文档注释统一标注该列为「control token（三用）」；可选的物理重命名
  （`control_token_*`）**不做**，仅语义升级，理由见上（churn 不值）。

## 7. S3 抽象层接口（`internal/integrations/storage`）

```go
// ObjectStore：标准 S3 对象读写（aws-sdk-go-v2，endpoint 指 MinIO/云 OSS）
type ObjectStore interface {
    PutObject(ctx context.Context, key string, r io.Reader, size int64) error
    PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
    MovePrefix(ctx context.Context, srcPrefix, dstPrefix string) error // 删除归档：复制后删
    DeletePrefix(ctx context.Context, prefix string) error
}

// STSIssuer：标准 STS AssumeRole，签发限定到 appPrefix 的临时写凭证
type STSIssuer interface {
    AssumeAppRole(ctx context.Context, appPrefix string, ttl time.Duration) (TempCredentials, error)
}

type TempCredentials struct {
    AccessKeyID, SecretAccessKey, SessionToken string
    ExpiresAt time.Time
}
```

- 实现用 `github.com/aws/aws-sdk-go-v2/service/s3`（含 `s3.PresignClient`）+
  `.../service/sts`；`BaseEndpoint` 指向配置的 S3 端点，path-style 寻址（MinIO 友好）。
- `AssumeAppRole` 用标准 `AssumeRole`，policy 动态注入限定 `Resource` 为
  `arn:aws:s3:::<bucket>/<appPrefix>*`（标准 IAM policy 语法，MinIO 兼容）。

## 8. manifest / skill 改造

- **manifest**：抽出现 `WriteAppInput` 内的渲染逻辑为可复用的「渲染出 `Manifest` +
  附属 resources」纯函数（现状渲染与「写 input 卷」耦合在 `app_input.go`）；bootstrap
  端点调它产出 manifest 返回。**旧的写 input 卷路径保留**（spec-A 删）。
- **skill blob**：`FSSkillBlobStore` 抽象为 `SkillBlobStore` 接口（`PutSkill`/`OpenSkill`/
  `DeleteSkill` + 新增 `PresignSkill`），新增 **S3 实现**上传到
  `versions/<vid>/skills/<name>.tar`；版本发布时上传（现 `PutSkill` 调用点）。
  `versions.skills_json` 的 `file_path` 语义从「本地相对路径」变「S3 key」（复用列，
  **无 schema 变更**）。

## 9. DB schema 变更

- **基本无变更**：
  - control token 复用现有 `runtime_token_*` 列（仅语义/注释升级）。
  - app 的 S3 prefix 由 `appID` 约定推导（`apps/<id>/`），**不存列**。
  - skill 的 S3 key 复用 `versions.skills_json.file_path`。
- 若实现中发现需记录「S3 已上传/已归档」标志位，再按需补列（实现计划评估，默认不加）。

## 10. 验证策略（B6）

- **Go 单测（httptest / 表驱动）**：
  - bootstrap 端点：401（token 缺失/不符/跨 app）、404（app 不存在）、正常体
    （manifest 字段、skills/restore URL 形态、s3_write 字段齐全）。
  - control token 中间件：hash 反查、跨 app 拒绝。
  - manifest 渲染纯函数：api_key/persona/rules/routing/app_token 正确装配。
- **对真实 MinIO 的 S3 集成测**（本地 k3d MinIO，标准 S3 协议）：
  - `PutObject` → `PresignGet` 下载内容一致；`MovePrefix`/`DeletePrefix` 生效。
  - `STSIssuer.AssumeAppRole` 签发的临时凭证：能写 `apps/<id>/*`、**越权写其它 prefix
    被拒**（证明 prefix 限定真生效）。
  - 集成测用构建标签 / 环境变量门控（无 MinIO 时跳过并在交付说明标注）。
- **不做**：pod initContainer/sidecar 真实拉取与同步、端到端、浏览器走查——推迟到
  A/B/D/E 合并后统一验证（B6）。

## 11. 风险与权衡

| 风险 | 说明 | 缓解 |
|---|---|---|
| S3 抽象在 B 阶段无 pod 消费 | bootstrap/STS 的真实使用方（initContainer/sidecar）在 spec-A | B6：对真实 MinIO 集成测证明协议可用；契约文档固定形态供 A 落地 |
| token 语义升级影响 oc-kb 现有路径 | `manifest.knowledge.app_token` 复用同一把 | 物理列不变、机制不变，仅语义；oc-kb 行为不变（仍是这把 token） |
| STS prefix 限定跨厂商差异 | 标准 AssumeRole + IAM policy，MinIO 兼容；云 OSS 可能有方言 | B4 锁定标准协议；集成测验证 MinIO；生产切换在部署期单独验证 |
| api_key 经 bootstrap 明文返回 | 必要（hermes 需明文）；但走认证通道、不落盘/S3 | control token 鉴权 + 仅内网 `/internal` 通道；api_key 不进 S3（D7） |
| manager 成为 pod 启动硬依赖 | pod 起/重启强依赖 bootstrap 在线 | manager-api 多副本 HA（父设计 §5.3、§10，spec-A/D 落地） |
| 续期窗口 | STS 凭证过期 pod 需重调 bootstrap | bootstrap 幂等可重调；sidecar 过期前续（spec-A）；manager 侧无状态 |

## 12. 待 spec-A（本 spec 不做，契约已钉）

- pod spec 渲染：initContainer 调 bootstrap、写 emptyDir、预签名 restore；sidecar
  `mc mirror` + sqlite `.backup`（用 bootstrap 返回的 STS 凭证 + prefix）。
- k8s Secret 注入 control token（`app-<id>-token` 的 `control-token` 键）。
- 删除 runtime-agent file API、节点概念、`WriteAppInput` 写卷旧路径。
- `OcOpsResolver` 真实 Service DNS 寻址（spec-E 占位）。
- A/B/D/E 合并后端到端 + 三角色真实浏览器验证（吸收 B6 推迟项）。
