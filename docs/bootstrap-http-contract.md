# bootstrap HTTP 契约

> spec-B 交付。bootstrap 端点是 pod initContainer 启动时回调 manager 的**内部配置下发接口**
> （入站方向：pod → manager），只读无副作用。spec-A（pod 编排 k8s 化）负责将 control token
> 经 k8s Secret 注入 pod env，并在创建 pod 前 ensure api_key + control token + 发布版本。

## 1. 端点

```
GET /internal/apps/{id}/bootstrap
```

**路由组**：`/internal`，由 `RegisterBootstrapRoutes` 注册于顶层 gin.IRouter。该路由组**不挂**
用户认证中间件（JWT / session），鉴权逻辑完全由 handler 内联实现（control token 校验）。

**请求头**：

```
Authorization: Bearer <control token>
```

control token 是 per-app 三用令牌（由创建流程生成，经 AES 加密后入库），以明文形式由
pod env 持有，调用本端点时附在 Bearer 头。

## 2. 鉴权

鉴权流程在 `BootstrapHandler.Bootstrap` 中内联执行，分三步：

1. **提取 Bearer token**：复用 `bearerToken` 辅助函数解析 `Authorization` 头；缺失或格式
   不符（非 `Bearer <token>`）直接 401。
2. **hash 反查 app**：对 plain token 做 `SHA256` hash（`service.HashAppRuntimeToken`），
   调用 `ResolveByControlToken` 查 DB；hash 无匹配记录一律 401，**不区分** token 本身无效
   还是 app 不存在，避免泄露 app 存在性。
3. **校验 path id 一致**：比对 path `{id}` 与 token 所属 `app.ID`；不一致 401，防止持 app A
   的 token 横向拉取 app B 的配置。

三步全部通过后，handler 调用 `Build` 组装响应。

## 3. 错误码映射

| HTTP 状态 | 响应码 | 触发条件 |
|---|---|---|
| 400 | — | 不适用（本端点无 body 解析） |
| **401** | `UNAUTHORIZED` | 缺 Bearer token / token hash 反查失败 / path `{id}` 与 token 所属 app 不匹配 |
| **409** | `APP_NOT_READY` | `Build` 返回 `ErrAppNotReady`：app 缺 `api_key`（`newapiKeyCiphertext` 为 null）、缺 control token（`runtimeTokenCiphertext` 为 null）或尚无发布版本（`version_id` 为 null） |
| **500** | `INTERNAL` | `Build` 返回其他任何错误：解密失败 / 查询 DB 失败 / manifest 渲染失败 / S3 预签名失败 / STS 签发失败。handler **当前不细分**，统一映射 500 |
| **200** | — | 组装成功，body 为 `BootstrapResult` JSON |

> **注意**：handler 不返回 502。`Build` 中所有 `ErrAppNotReady` 之外的错误（包括 S3 / STS
> 故障）均以 500 `INTERNAL` 返回；文档须如实反映当前实现，不做 5xx 细分。

错误响应体：

```json
{"code": "APP_NOT_READY", "message": "app 未就绪"}
```

## 4. 响应体 schema（200）

成功时直接返回 `service.BootstrapResult` 序列化结果，无外层信封。

### 顶层字段

| 字段名 | 类型 | 描述 |
|---|---|---|
| `manifest_yaml` | string | 渲染后的 manifest YAML，含 `api_key`、persona/skills 相对路径、`knowledge.app_token` |
| `persona` | string | `resources/persona.md` 文本内容，由 hermes 模板渲染后输出，initContainer 写入 emptyDir |
| `platform_rule` | string | `resources/platform-rules.md` 文本内容，同上，initContainer 写入 emptyDir |
| `skills` | array | 各 skill tar 的下载信息（详见 §4.1），无 skill 时为空数组 |
| `restore` | object | 会话/工作区快照的预签名读 URL（详见 §4.2），首启时各字段省略 |
| `s3_write` | object | STS 临时写凭证（详见 §4.3），sidecar mc mirror 写入快照用 |

### 4.1 `skills` 数组元素

| 字段名 | 类型 | 描述 |
|---|---|---|
| `name` | string | skill 名称 |
| `rel_path` | string | pod 内目标相对路径，格式固定为 `resources/skills/<name>.tar` |
| `url` | string | 预签名 GET URL，有效期由 `cfg.PresignTTL` 决定 |

### 4.2 `restore` 对象

三个字段均为 `omitempty`：对应快照对象在 S3 中不存在时省略该字段。首启时三字段全部省略。

| 字段名 | 类型 | 描述 |
|---|---|---|
| `workspace_url` | string | 工作区目录归档的预签名读 URL |
| `state_db_url` | string | sqlite 状态数据库快照的预签名读 URL |
| `sessions_url` | string | 会话归档的预签名读 URL |

### 4.3 `s3_write` 对象

前缀限定的 STS 临时写凭证，sidecar 仅可写入 `apps/<id>/` 前缀内的对象。

| 字段名 | 类型 | 描述 |
|---|---|---|
| `endpoint` | string | S3 兼容存储访问地址（来自 `cfg.Endpoint`） |
| `region` | string | 存储桶区域（来自 `cfg.Region`） |
| `bucket` | string | 存储桶名称（来自 `cfg.Bucket`） |
| `prefix` | string | 写权限限定前缀，格式 `apps/<id>/` |
| `access_key_id` | string | STS 颁发的临时 Key ID |
| `secret_access_key` | string | STS 颁发的临时密钥 |
| `session_token` | string | STS 颁发的会话令牌 |
| `expires_at` | string (RFC3339) | 临时凭证过期时间 |

### 示例响应

```json
{
  "manifest_yaml": "version: \"2\"\napp:\n  id: app-01j9zz\n  name: 财务助理\n...",
  "persona": "你是财务部专属助理，专注于...",
  "platform_rule": "【平台规则】\n禁止输出任何...",
  "skills": [
    {
      "name": "weather",
      "rel_path": "resources/skills/weather.tar",
      "url": "https://s3.example.com/bucket/skills/weather.tar?X-Amz-Signature=..."
    }
  ],
  "restore": {
    "workspace_url": "https://s3.example.com/bucket/apps/app-01j9zz/workspace?X-Amz-Signature=...",
    "state_db_url": "https://s3.example.com/bucket/apps/app-01j9zz/state.db?X-Amz-Signature=..."
  },
  "s3_write": {
    "endpoint": "https://s3.example.com",
    "region": "cn-east-1",
    "bucket": "oc-runtime",
    "prefix": "apps/app-01j9zz/",
    "access_key_id": "ASIA...",
    "secret_access_key": "wJalrX...",
    "session_token": "FQoGZXIvYXdzE...",
    "expires_at": "2026-05-29T10:30:00Z"
  }
}
```

> 首启时 `restore` 字段整体省略（无快照），`sessions_url` 同理。

## 5. 语义约定

- **只读无副作用**：`Build` 不创建 new-api key、不生成 token、不写 DB。所有数据来自 DB 现有记录，
  api_key 与 control token 由 spec-A 创建流程提前 ensure。
- **幂等可重复调用**：STS 凭证过期前 pod 可重调本端点以获取新凭证（如 pod 重启续期）。预签名
  URL 同样幂等——同一快照对象可反复签名，S3 对象内容不变。
- **首启 restore 省略**：`presignRestore` 对每个 S3 对象做存在性检查，不存在则跳过；首启时
  workspace/state.db/sessions 三个对象均不存在，`restore` 字段中所有 URL 均省略。
- **敏感数据不落盘**：`api_key`（以 `manifest_yaml` 内 `openai.api_key` 字段形式返回）与
  `control token`（以 `manifest.knowledge.app_token` 形式返回）仅经认证通道以明文返回，
  **不落 S3 / 本地盘**；DB 是唯一真相源，存储加密密文。

## 6. spec-A 对齐点

以下接入行为由 **spec-A（pod 编排 k8s 化）** 落地，bootstrap 端点本身（spec-B）不包含：

- **control token 注入**：创建 pod 时，manager 将 control token 写入 k8s Secret
  `app-<id>-token`（键名 `control-token`），再由 pod spec envFrom/secretKeyRef 注入
  pod env（env var 名待 spec-A 确定）。initContainer 读取该 env var 用于调用本端点。
- **initContainer 行为**：
  1. 用 env var 中的 control token 调 `GET /internal/apps/{id}/bootstrap`。
  2. 将 `manifest_yaml` 写入 emptyDir manifest 路径。
  3. 将 `persona` / `platform_rule` 写入 emptyDir 对应路径。
  4. 按 `skills[].rel_path` 下载各 skill tar（用 `skills[].url` 预签名 GET）。
  5. 若 `restore.workspace_url` / `state_db_url` / `sessions_url` 存在，从 S3 拉取快照恢复。
- **sidecar mc mirror 行为**：sidecar 读取 `s3_write` 中的 endpoint/region/bucket/prefix
  与 STS 临时凭证，配置 mc client 将 emptyDir 快照目录持续 mirror 到 S3。
- **endpoint/bucket/region 来源**：三者均来自 manager 配置 `storage.s3.*`，由 `BootstrapConfig`
  携带，pod 侧不需要独立感知 S3 配置。
- **创建流程先决条件**：spec-A 在建 pod 前须 ensure（若缺失则主动创建并加密入库）：
  1. `newapiKeyCiphertext`（new-api api_key 密文）
  2. `runtimeTokenCiphertext`（control token 密文）
  3. `version_id`（至少存在一个已发布版本）
  否则 bootstrap 返回 409 `APP_NOT_READY`，pod 无法正常启动。
