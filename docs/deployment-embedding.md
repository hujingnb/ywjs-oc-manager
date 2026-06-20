# 知识库 Embedding 模型部署指南

## 为什么需要单独配置 embedding

知识库（RAGFlow）的工作链路是：

```
上传文档 → 切块 → 每块用 embedding 模型算向量 → 存入向量库
提问 → 问题算向量 → 检索最相似的块 → 连同问题喂给对话模型生成回答
```

其中「算向量」这一步必须用 **embedding 模型**，它和对话模型（如 deepseek-v4-pro）是
两类完全不同的模型：

| | 对话 / Chat 模型 | Embedding 模型 |
|---|---|---|
| 输入 → 输出 | 文字 → 文字 | 文字 → 数值向量（如 1024 维） |
| API 端点 | `/v1/chat/completions` | `/v1/embeddings` |
| 例子 | deepseek-v4-pro、gpt-4o | bge-m3、text-embedding-3-small |

**deepseek 系列只提供对话模型，没有 embedding 端点**（`/embeddings` 返回 404），
因此知识库必须额外接入一个 embedding 模型来源，否则 RAGFlow 解析文档时报
`No default embedding model is set`、文档「解析失败」。

## 架构约定

RAGFlow 的 embedding / 对话模型在其控制台「模型提供商」配置，可走 **new-api 网关**
（便于计费、限流、密钥集中管理）或直连厂商。本地与线上都接 **厂商 API**，不再自托管模型：

```
RAGFlow ──/v1/embeddings──> 厂商 API（如 SiliconFlow BAAI/bge-m3）
RAGFlow ──/v1/chat/...   ──> 厂商 API（如 DeepSeek）/ 经 new-api 网关
```

本地与线上配置一致，切换环境只需换厂商 Key，RAGFlow 与 manager 侧逻辑不变。

---

## 本地开发（k3d）：自动初始化

本地**不再自托管 Ollama**（已移除），与线上一样接厂商 API。**模型与 token 已自动化**：
`make local-reset`（或首次 `make local-up`）会清空有状态数据、把 new-api 与 RAGFlow
**重新初始化**——模型渠道 / 提供商清空、二者的管理 token 失效；这套重建过去靠人工点 UI，
现已由 `scripts/local-init-models.py` 自动完成。

只需在仓库根目录 `.env` 填好厂商 key（见 `.env.example`）：

```
DEEPSEEK_API_KEY=<DeepSeek API Key>      # new-api DeepSeek 渠道 + RAGFlow chat
SILICONFLOW_API_KEY=<SiliconFlow API Key># RAGFlow embedding，BAAI/bge-m3
```

`make local-up` 末尾会自动跑该脚本（幂等），完成：

- **new-api**（`http://newapi.localhost`，`admin/admin123`）：初始化向导、开自用模式、
  建 DeepSeek 渠道（模型 `deepseek-chat … deepseek-v4-pro-max`）、生成 admin 系统访问令牌。
- **RAGFlow**（`http://ragflow.localhost`，`admin@ragflow.io/admin`）：SiliconFlow
  `BAAI/bge-m3`（embedding，max_tokens 8192）+ DeepSeek `deepseek-v4-pro`/`deepseek-v4-flash`
  （chat），设默认 embedding=bge-m3、默认 LLM=deepseek-v4-pro，生成外部 API key。
- 把两个**随机** token 回填 `deploy/k8s/local/secret.yaml`
  （`newapi.admin_token` / `ragflow.api_key`），`kubectl apply` + 重启 manager-api 生效。
- 自检：new-api 系统令牌可用 + DeepSeek 渠道连通测试通过、RAGFlow api_key 可调外部 API。

> RAGFlow 模型配置走 MySQL 直写（其模型管理 API 藏在 RSA 登录 + session JWT 之后，
> 脚本化困难；key/token 在 DB 明文存储，镜像已锁 `v0.25.6`，DB 写安全），其余走官方 HTTP API。

补好 `.env` 后可单独重跑 `make local-init-models`（幂等，不产生重复渠道/模型）。
缺 `.env` 或任一 key 为空时该步**自动跳过**（exit 0），不阻断组件启动；补好 key 再单独跑即可。
`make local-stop` / `local-start` 不重置数据，无需重做。

> 真实厂商 key 只入本地 `.env`（gitignored），由脚本读进内存、只发给 new-api 请求体 /
> 写进 RAGFlow DB，**绝不写入任何 git 跟踪文件**（脚本、secret.yaml、文档，见文末「安全约束」）。
> 每次重建后 `secret.yaml` 的 `admin_token`/`api_key` 两行会随新实例变化（git 工作区显示脏，
> 属预期，不必提交）。
> 本地 RAGFlow / new-api pod 经宿主 clash 代理（`host.k3d.internal:7890`）出站访问厂商 API，
> 见 `deploy/k8s/local/{ragflow,new-api}.yaml` 的 `HTTP(S)_PROXY`。

---

## 线上 / 生产：接入 embedding 厂商

生产环境同样接 embedding 厂商。RAGFlow 与 manager 配置与本地完全一致，只需把厂商 Key
换成生产 Key。

### 可选厂商

| 厂商 | 推荐模型 | BaseURL | 说明 |
|---|---|---|---|
| **硅基流动 SiliconFlow** | `BAAI/bge-m3` | `https://api.siliconflow.cn/v1` | 注册送额度，bge-m3 常驻免费；中文效果好，与本地同模型，迁移无感 |
| **OpenAI** | `text-embedding-3-small` / `-large` | `https://api.openai.com/v1` | 稳定、英文强；small 性价比高，large 精度高 |
| **阿里百炼 DashScope** | `text-embedding-v3` | `https://dashscope.aliyuncs.com/compatible-mode/v1` | 阿里云生态、国内直连、中文好 |
| **智谱 BigModel** | `embedding-3` | `https://open.bigmodel.cn/api/paas/v4` | 国内直连、中文好 |
| **自托管** | `bge-m3` 等 | 自建 TEI / Ollama / vLLM 的 `/v1` | 数据不出域、无单价；需自备 GPU/CPU 资源 |

> 选型建议：优先选与本地一致的 **bge-m3**（硅基流动 / 自托管），换环境零差异、维度一致，
> 避免「本地建库与线上检索向量维度不匹配」。若已用 OpenAI 生态可选 `text-embedding-3-small`。

### 配置步骤（生产）

1. 在 new-api 后台「渠道管理」添加渠道：
   - 类型 `OpenAI`、BaseURL 填厂商地址（见上表）、密钥填**厂商 API Key**、模型填厂商模型名。
2. RAGFlow「模型提供商」加 OpenAI-API-Compatible 提供商，Base URL 指向 new-api 的 `/v1`，
   模型名与渠道一致，类型 `embedding`；「系统模型设置」选为默认 embedding 模型。
3. （重要）**embedding 模型一经选定不要中途更换**：已入库文档的向量按当时模型维度生成，
   换模型需重建（重新解析）全部知识库文档，否则检索失效。

### 安全约束

- embedding 厂商 Key 与其它密钥一样，**只放进 `deploy/k8s/prod/secret.yaml`（已 gitignore）
  或密钥管理系统，绝不入 git**。
- new-api 渠道密钥落在 new-api 自身数据库，不经 manager 仓库。
