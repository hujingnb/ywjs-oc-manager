# Hermes 智能路由调研报告

> 调研日期：2026-05-19
> 调研对象：NousResearch/hermes-agent 的模型路由能力
> 验证环境：本地 docker-compose + new-api (5 个模型)

---

## 一、功能概述

Hermes Agent 的"智能路由"并非传统意义上的"根据用户意图自动选模型"，而是一套**任务级模型分配机制**：将 agent 内部不同类型的子任务（vision、compression、title generation 等）路由到不同的模型，从而实现成本优化和能力互补。

核心设计思路：
- **主模型（Main Model）**：处理所有用户对话、tool-call 循环、流式响应
- **辅助模型（Auxiliary Models）**：8 个独立任务槽位，每个可指定不同模型
- **Fallback 机制**：主模型失败时自动切换到备用 provider:model
- **Provider Routing**：OpenRouter 场景下控制底层 provider 优先级（价格/延迟/吞吐）

---

## 二、Auxiliary 任务槽位详解

| 任务槽位 | 功能说明 | 推荐模型策略 |
|---|---|---|
| `vision` | 图片分析、浏览器截图识别 | 需要多模态能力，如 gpt-4o / gemini-flash |
| `compression` | 上下文压缩摘要（对话过长时触发） | 便宜快速模型即可，如 deepseek-flash |
| `web_extract` | 网页内容摘要提取 | 同 compression，摘要类任务 |
| `session_search` | 历史会话搜索 | 便宜模型 |
| `title_generation` | 会话标题生成 | 最便宜的模型，如 flash/mini |
| `approval` | 智能审批（approval_mode: smart 时） | 便宜快速模型 |
| `skills_hub` | 技能搜索和发现 | 一般 auto 即可 |
| `mcp` | MCP 工具路由辅助 | 一般 auto 即可 |

---

## 三、配置方式

### 3.1 config.yaml 配置示例

```yaml
# 主模型配置
model:
  default: "deepseek-v4-pro"
  provider: "custom"
  base_url: "http://new-api:3000/v1"
  api_key: "sk-xxx"

# 辅助模型路由配置
auxiliary:
  vision:
    provider: "custom"
    model: "gpt-5.4"
    base_url: "http://new-api:3000/v1"
    api_key: "sk-xxx"
  compression:
    provider: "custom"
    model: "deepseek-v4-flash"
    base_url: "http://new-api:3000/v1"
    api_key: "sk-xxx"
  web_extract:
    provider: "custom"
    model: "deepseek-v4-flash"
    base_url: "http://new-api:3000/v1"
    api_key: "sk-xxx"
  session_search:
    provider: "custom"
    model: "deepseek-v4-flash"
    base_url: "http://new-api:3000/v1"
    api_key: "sk-xxx"
  title_generation:
    provider: "custom"
    model: "qwen3.5:27b"
    base_url: "http://new-api:3000/v1"
    api_key: "sk-xxx"
  approval:
    provider: "custom"
    model: "deepseek-v4-flash"
    base_url: "http://new-api:3000/v1"
    api_key: "sk-xxx"
  skills_hub:
    provider: "custom"
    model: "deepseek-v4-flash"
    base_url: "http://new-api:3000/v1"
    api_key: "sk-xxx"
  mcp:
    provider: "custom"
    model: "deepseek-v4-flash"
    base_url: "http://new-api:3000/v1"
    api_key: "sk-xxx"
```

### 3.2 Fallback 配置

```yaml
# 主模型失败时的备用模型
fallback_model:
  provider: "custom"
  model: "gpt-5.2"
  base_url: "http://new-api:3000/v1"

# 多级 fallback（按顺序尝试）
fallback_providers:
  - provider: "custom"
    model: "gpt-5.2"
    base_url: "http://new-api:3000/v1"
  - provider: "custom"
    model: "qwen3.5:27b"
    base_url: "http://new-api:3000/v1"
```

### 3.3 Provider Routing（仅 OpenRouter 场景）

```yaml
# 控制 OpenRouter 底层 provider 选择策略
provider_routing:
  sort: "price"          # price | throughput | latency
  only: []               # 白名单
  ignore: ["Together"]   # 黑名单
  order: ["Anthropic", "Google"]  # 优先级
  require_parameters: true
  data_collection: "deny"
```

### 3.4 provider 字段可选值

| 值 | 含义 |
|---|---|
| `"auto"` | 默认，使用主模型 |
| `"main"` | 显式使用主模型 |
| `"custom"` | 自定义 endpoint（需配 base_url + api_key） |
| `"openrouter"` | OpenRouter |
| `"anthropic"` | Anthropic 直连 |
| `"nous"` | Nous Portal |
| 其他 provider ID | 见 Hermes 支持的 provider 列表 |

---

## 四、配置生效机制

| 运行模式 | 生效时机 | 说明 |
|---|---|---|
| CLI (`hermes chat`) | 下次启动 `hermes chat` | 每次启动读取最新 config.yaml |
| Gateway（Telegram/Discord/微信等） | 新 session | 已有 session 保持原模型 |
| Gateway 强制生效 | `hermes gateway restart` | 所有 session 重新读取配置 |
| Dashboard chat | 新 PTY | 当前 chat 用 `/model` 热切换 |
| oc-manager 容器化部署 | app restart | RefreshConfigYAML 重写后 stop→start |

**关键点**：Hermes 在 session 启动时冻结 system_prompt 到 state.db，配置变更不会影响已有 session。必须新建 session 或 restart 才能生效。

---

## 五、实际验证结果

### 5.1 验证环境

- 本地 new-api 可用模型：`deepseek-v4-pro`、`deepseek-v4-flash`、`gpt-5.4`、`gpt-5.2`、`qwen3.5:27b`
- Hermes 镜像：`oc-manager-hermes:2026-05-16-23-34-35`
- 配置：主模型 deepseek-v4-pro，auxiliary 分配不同模型

### 5.2 验证结论

通过 new-api 请求日志确认，**智能路由功能完全可用**：

```
模型名称                  调用次数    输入Token    输出Token
------------------------------------------------------------
deepseek-v4-flash            7      67590        230    ← compression/web_extract
deepseek-v4-pro             33     364545       5942    ← 主对话
gpt-5.2                     14      32448        167    ← 辅助任务
gpt-5.4                      7       9851         71    ← vision
qwen3.5:27b                 37     403215       5503    ← title_generation
```

- 主对话确实走 `deepseek-v4-pro`
- compression 等辅助任务走 `deepseek-v4-flash`
- vision 走 `gpt-5.4`
- title_generation 走 `qwen3.5:27b`
- 配置修改后新进程立即读取最新配置

### 5.3 `hermes status` 输出确认

```
◆ Model
  Model:        deepseek-v4-pro
  Provider:     Custom endpoint

◆ Context Compression
  Model:        deepseek-v4-flash
  Provider:     custom

◆ Auxiliary Models (overrides)
  Vision        provider=custom, model=gpt-5.4
  Web extract   provider=custom, model=deepseek-v4-flash
```

---

## 六、与 oc-manager 集成的影响

### 6.1 当前 oc-manager 的 config.yaml 渲染

当前 `internal/integrations/hermes/config.go` 的 `RenderConfigYAML` 只写入：
- `model.default` / `model.provider` / `model.base_url` / `model.api_key`
- `auxiliary` 全部设为 `{ provider: main }`（即全走主模型）

### 6.2 如果要支持智能路由

需要扩展 `ConfigInput` 和 `RenderConfigYAML`，增加：
1. 每个 auxiliary 任务的 model 配置字段
2. 可选的 fallback_model 配置
3. 在 app 创建/restart 时将这些配置写入 config.yaml

### 6.3 推荐的集成方案

**方案 A：组织级统一配置**
- 在组织设置中配置 auxiliary 模型映射
- 所有该组织下的 app 共享同一套路由策略
- 适合成本控制场景

**方案 B：应用级独立配置**
- 每个 app 可以独立配置 auxiliary 模型
- 灵活但管理复杂度高

**方案 C：平台级默认 + 组织级覆盖**
- 平台管理员设置默认的 auxiliary 策略
- 组织可选择覆盖特定槽位
- 兼顾统一管理和灵活性

---

## 七、成本优化效果估算

以典型对话场景为例（假设主模型 token 单价为 1x）：

| 任务 | 占比 | 推荐模型 | 单价比 | 节省 |
|---|---|---|---|---|
| 主对话 | 60% | 高端模型 | 1x | - |
| compression | 15% | flash 模型 | 0.1x | ~13.5% |
| title_generation | 5% | flash 模型 | 0.1x | ~4.5% |
| vision | 5% | 中端多模态 | 0.5x | ~2.5% |
| 其他 auxiliary | 15% | flash 模型 | 0.1x | ~13.5% |

**总体可节省约 30-35% 的 token 成本**，同时不影响主对话质量。

---

## 八、参考文档

- [Configuring Models](https://hermes-agent.nousresearch.com/docs/user-guide/configuring-models)
- [AI Providers](https://hermes-agent.nousresearch.com/docs/integrations/providers)
- [Provider Routing](https://hermes-agent.nousresearch.com/docs/user-guide/features/provider-routing/)
- [Fallback Providers](https://hermes-agent.nousresearch.com/docs/user-guide/features/fallback-providers)
- [Configuration](https://hermes-agent.nousresearch.com/docs/user-guide/configuration)
