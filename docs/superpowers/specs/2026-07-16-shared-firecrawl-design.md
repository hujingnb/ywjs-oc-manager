# 共享 Firecrawl 网页提取服务设计

- 日期：2026-07-16
- 状态：待审阅
- 关联设计：`2026-07-15-aicc-conversation-capability-validation-design.md`

## 1. 目标

部署一个独立、无持久化的自建 Firecrawl 服务，为 AICC Hermes 与普通 Hermes 实例提供网页正文
提取能力。所有 Hermes 继续使用 DDGS 执行 `web_search`；只有 `web_extract` 请求发送到 Firecrawl。

这不是受控公网出口：Hermes 仍按各自既有网络策略访问搜索引擎和公网。Firecrawl 只接收要读取的
URL，抓取后返回 Markdown 正文，不承担域名白名单、审计代理或业务写操作。

## 2. 组件与命名空间

新增 `oc-firecrawl` namespace，独立于 `ocm`、`oc-apps` 和 `oc-aicc`。该 namespace 包含：

| 组件 | 副本与扩缩容 | 职责 | 存储 |
|---|---:|---|---|
| API | 1–3，HPA CPU/内存 | 接收 Hermes 的 Firecrawl API 请求 | 无状态 |
| scrape worker | 1–5，HPA CPU/内存 | 执行普通抓取任务 | 无状态 |
| extract worker | 1–5，HPA CPU/内存 | 执行正文提取任务 | 无状态 |
| NuQ worker / prefetch worker | 各 1–5，HPA CPU/内存 | 消费并预取 PostgreSQL 队列任务 | 无状态 |
| Playwright | 1–4，HPA CPU/内存 | 渲染动态网页、提取正文 | 无状态 |
| Redis | 固定 1 | 短期队列/限流状态 | `emptyDir` |
| RabbitMQ | 固定 1 | 任务消息队列 | `emptyDir` |
| PostgreSQL | 固定 1 | Firecrawl 运行中的任务/队列元数据 | `emptyDir` |

API、scrape/extract/NuQ worker、Playwright 必须分别配置 HPA，按 CPU 与内存阈值在表中上下限之间自动
扩缩；后续若集群提供队列 external metrics adapter，可在不改变接口的前提下为 worker 增加队列深度指标。
Redis、RabbitMQ、PostgreSQL 是单副本临时依赖，不作伪高可用声明。所有 Deployment 使用 `Recreate` 或安全
的单副本策略，数据库不挂 PVC、不接 S3。任一基础依赖重启会丢失排队与执行中的任务；Hermes 将该次
`web_extract` 视为失败并正常告知信息无法读取，不得编造正文或来源。

初始资源建议：API、NuQ worker 250m/512Mi（上限 1CPU/1Gi），scrape/extract worker、Playwright
500m/1Gi（上限 2CPU/2Gi）；实际并发由 Playwright 的 CPU/内存阈值和 HPA 收敛，初期以 2–5 个动态
页面并发为容量目标，再根据指标调整。

## 3. 网络与安全边界

Firecrawl Service 仅在 `oc-firecrawl` 集群内暴露。NetworkPolicy：

- ingress 只允许 `oc-aicc` 与 `oc-apps` namespace 的 Pod 访问 API 端口；Kubernetes label selector
  不支持 `app=app-*` 这类前缀匹配，应用身份仍由 Hermes 运行时 token 和工具策略约束；
- Firecrawl 内部组件只互相访问所需端口；
- API/worker/Playwright 出站仅允许 DNS、Firecrawl 内部依赖和公网 TCP 80/443；
- 禁止 Ingress、LoadBalancer、NodePort 与对外公开管理界面；队列管理 UI 不暴露。

Firecrawl 是供所有 Hermes 实例使用的完整网页能力服务，不在 Firecrawl 服务层禁止 `/interact`、
持久化浏览器 profile、登录、表单填写或写操作。权限在调用方 Hermes 收敛：AICC 仍只向模型暴露其
现有白名单，禁止 terminal、文件、进程、登录、表单、浏览器操作和任何写工具；普通 Hermes 维持其
既有权限边界，可使用 Firecrawl 支持的能力。

## 4. Hermes 配置

所有受支持 Hermes runtime variant 在启动 bootstrap 阶段一次性写入一致配置，并由 Deployment 为 Hermes
容器注入集群内服务地址：

```yaml
web:
  search_backend: ddgs
  extract_backend: firecrawl
```

同时注入 `FIRECRAWL_API_URL=http://firecrawl-api.oc-firecrawl.svc.cluster.local:3002`。启动配置还必须让
AICC 与普通 Hermes 的 API server 都向模型注册 `web_search`、`web_extract`：普通 Hermes 在既有 toolset
基础上追加 `web`，不得覆盖其它已启用 toolset；AICC 维持受限 `web` 白名单，不能因此取得操作工具。
不提供运行时开关、后台二次启用或单应用人工配置步骤。具体配置键以固定的 Hermes 上游版本实际 schema
为准，并由 renderer 测试断言。Firecrawl 服务地址是集群内非敏感配置；若固定上游版本要求 API key，
则使用 namespace 内 Secret，由所有 app Pod 以只读 env 注入同一服务凭证，绝不写入 manifest、日志或前端。

## 5. 失败处理、可观测性和验证

- Firecrawl 不可用、超时或重启时，Hermes 返回“无法读取该公开网页”，不伪造内容；AICC 保留来源
标注和企业知识优先级。
- 记录 API/worker/Playwright 的请求量、错误率、队列深度、并发、CPU/内存与 HPA 副本数；不得记录
网页正文、访客隐私内容或凭证。
- 单元测试覆盖 Kubernetes 资源、HPA、NetworkPolicy、无 PVC、所有 renderer variant 的 DDGS 搜索
与 Firecrawl 提取配置，以及 AICC 白名单未扩大。
- 本地 k3d 使用真实 Firecrawl 与 Chrome Stable 验证：普通 Hermes 和 AICC 都完成 `web_search`、
`web_extract`；动态网页并发压测证明 HPA 至少扩出一个计算副本，重启临时 PostgreSQL 后在飞任务失败
且后续新请求可恢复。

## 6. 非目标

- 不实现 Firecrawl 云服务、外部抓取代理、持久化网页归档、跨重启任务恢复或 Firecrawl 高可用。
- 不在本期改变 AICC 的对话、意向、来源审计或普通 Hermes 的其它工具策略；二者仅增加启动时的
  DDGS 搜索与 Firecrawl 提取配置。
