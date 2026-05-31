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

RAGFlow 统一通过 **new-api 网关**调用 embedding（与对话模型一致，便于计费、限流、
密钥集中管理）。环境差异只体现在 new-api 的 embedding 渠道上游：

```
RAGFlow ──/v1/embeddings──> new-api ──(渠道路由)──> 本地: Ollama  /  线上: 厂商 API
```

切换环境只需改 new-api 渠道，RAGFlow 与 manager 侧配置不变。

---

## 本地开发（k3d）：自托管 Ollama

本地不依赖任何外部厂商，用 Ollama 跑一个本地 embedding 模型。

1. **部署 Ollama**（已纳入 `make local-up`，或单独 apply）：

   ```bash
   kubectl apply -f deploy/k8s/local/ollama.yaml
   ```

2. **拉取 embedding 模型**（首次，约 1.2GB，经宿主代理下载）：

   ```bash
   kubectl -n ocm exec deploy/ollama -- ollama pull bge-m3
   ```

3. **在 new-api 后台加 embedding 渠道**（http://newapi.localhost → 渠道管理 → 添加渠道）：
   - 类型：`OpenAI`
   - 名称：`embedding-local`（任意）
   - 代理 / BaseURL：`http://ollama:11434`
   - 密钥：任意非空字符串（Ollama 不校验，填 `ollama` 即可）
   - 模型：`bge-m3`

4. **在 RAGFlow 配置默认 embedding 模型**（http://ragflow.localhost，用 manager 所用账号登录）：
   - 模型提供商 → 添加 `OpenAI-API-Compatible` 提供商
     - Base URL：`http://new-api:3000/v1`
     - API Key：填 manager 所用的 new-api 模型转发 token（或任一可用 token）
     - 模型名：`bge-m3`，模型类型：`embedding`
   - 系统模型设置 → 默认 embedding 模型选 `bge-m3`

5. **验证**：知识库上传一个文档 → 解析状态应变为「解析完成」。

> emptyDir 存储：Ollama pod 重建会重拉模型；如需免重拉，把 `ollama.yaml` 的
> `volumes.models` 改为 PVC。

---

## 线上 / 生产：接入 embedding 厂商

生产环境不部署 Ollama，改在 new-api 接入一个 embedding 厂商渠道。RAGFlow 与 manager
配置与本地完全一致（都指向 new-api），只需把 new-api 的 embedding 渠道上游换成厂商。

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
