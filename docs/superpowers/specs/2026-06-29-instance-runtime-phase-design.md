# 实例运行时就绪维度(runtime_phase)设计

**日期**:2026-06-29
**状态**:已通过 brainstorming,待评审
**作者**:hujing + Claude

## 1. 背景与问题

实例(app)三个渠道——微信(`wechat`)/企业微信(`work_wechat`)/飞书(`feishu`)——的登录(绑定)以及对话功能,运行时都打到 pod 内的 **oc-ops sidecar**(端口 8080)。当前判断「实例是否能发起渠道登录」只看单一字段 `app.status`:

- 前端 `AppChannelsTab.vue` 的闸门 `instanceReady` = `app.status ∈ {running, binding_waiting, binding_failed}`(`web/src/pages/apps/AppChannelsTab.vue:409`)。
- 后端守卫 `AppCanInitiateChannelAuth(status)` 同口径(`internal/domain/app_state_machine.go`)。

这个单字段同时承担了**两件本应正交的事**:

1. **业务生命周期**:实例处于哪个业务阶段(draft → 拉镜像 → 初始化 → 等绑定 → 运行 → 停止/删除)。
2. **运行时是否真能服务**:pod 此刻能不能接住请求。

平时两者重合(`running` 既是业务态也意味着能服务),但有三个窗口它们**分叉**,而当前模型表达不了:

- **解绑/升级重启**:已用业务态 `restarting` 打补丁(migration 000016),但只覆盖「解绑」一个触发源。
- **k8s 自发重启**:节点漂移、OOM、手动 rollout、宿主 `br_netfilter` 类问题——`app.status` 全程 `running`,pod 实际在重启,**没有任何标识**。
- **oc-ops 未就绪**:pod 已 Running、hermes 容器 Ready=true,但 **oc-ops sidecar 当前没有 readinessProbe**(`internal/integrations/k8sorch/render.go:136` 只给 hermes 容器配了 probe),uvicorn 还没起来。此时前端闸门按 `running` 放行,渠道登录打过去得 **502**,前端透出 cryptic「ocops: hermes cli failed」像 bug。

更要命的是:`orch.Status` 的 `Ready` 字段**只读 hermes 容器**(`internal/integrations/k8sorch/adapter.go:211`),完全不反映 oc-ops。reconciler 在 `running` 态只在 `IsTerminalBad` 才翻 `error`,`Ready=false` 的重启/未就绪窗口里 `app.status` 全程是 `running`,闸门照开。

业务态 `restarting` 本质就是「在单字段里塞运行时态」的第一块补丁——它只走了一步就开始疼:每多覆盖一个重启来源,就得往 enum 加一个状态 + 写 CHECK migration。

## 2. 目标与非目标

### 目标
- 修复「实例显示运行中但渠道发起/对话拿到 502」的就绪空窗(缺口 A)。
- 给实例增加**全局可观测**的「正在重启 / 未就绪」运行时标识,覆盖所有重启来源(解绑、升级、k8s 自发),后台/运维和前端都能看到(缺口 B)。
- 让 `Ready` 信号真实反映「oc-ops 也已就绪」,而非只看 hermes。
- 现有数据**平滑升级**:migration 纯增量,不卡实例、滚动部署期新旧代码并存安全、可回滚。

### 非目标
- 不重做业务状态机的语义,`app.status` 取值集合本次**不删减**。
- 不引入 k8s informer / watch 事件驱动(接受 reconciler 周期探测的 ~15s 延迟)。
- 不在本次清理业务态 `restarting`(留给后续 release)。
- 不调整 reconcile 间隔(保持 15s)。

## 3. 选型:双轴模型

业务态与运行时态是**正交**的两个维度,运行时就绪是个**横切**概念(几乎每个业务态都可能临时未就绪)。因此**新开一个独立维度** `runtime_phase` 描述「运行时此刻能不能服务」,`app.status` 保持业务生命周期语义不动。

对比与否决:
- **方案 B(继续往 app.status 加状态)**:状态爆炸(`running-not-ready`/`binding_waiting-not-ready`…)、丢失业务阶段信息(`restarting` 时不知本来是 running 还是 binding_waiting)、每个场景一次 CHECK migration。否决。
- **方案 C(纯前端探活)**:无全局可观测(与缺口 B 诉求直接相悖)、每客户端各探一次、后台不知情、治标不治本。否决。

## 4. 数据模型

`apps` 表新增一列(不动 `status` 及其 CHECK):

```
apps:
  status        = running        # 业务态(本次不动)
  runtime_phase = ready          # 新增:运行时态
```

### runtime_phase 取值(4 个,互斥,可从 orch.Status 推导)

| 值 | 含义 | 探测条件 | 前端展示 | 闸门 |
|---|---|---|---|---|
| `ready` | pod 所有关键容器(hermes + oc-ops)都 Ready,可服务。稳态。 | 改造后 `orch.Status.Ready == true` | 正常 | 放行(配合业务 allowlist) |
| `starting` | pod 存在但未 Ready,正常拉起瞬态(首启 / 重启后新 pod 初始化中)。k8s 预期自愈。 | pod 存在、`Ready == false`、`IsTerminalBad == false`,无重启窗口特征 | 徽标「正在启动」,提示「实例正在启动,请稍候」 | 关 |
| `restarting` | 一次重启动作导致的不可达窗口(解绑/升级/手动 rollout/k8s 驱逐)。常表现为 Recreate 空窗。 | ①主动 RolloutRestart 时同步置位;②reconciler 探到「Deployment 存在但 0 pod」或 RestartCount 增长 | 徽标「重启中」(warning),提示「实例正在重启,请稍候重试」 | 关 |
| `unknown` | 探测不到真实就绪度(k8s 查询失败 / reconciler 未跑过 / 新建未初始化)。 | `orch.Status` 出错或字段未初始化 | 提示「状态确认中」 | 关(保守不放行) |

**坏态不单列 phase**:pod 确定性坏死(CrashLoopBackOff / RestartCount ≥ 3 / pod 消失)已由现有逻辑翻成**业务态 `error`**(reconciler `running→error`)。职责划分:
- 坏态(需人工/重试介入)= 业务维度 `error`。
- 未就绪但会自愈(只需稍候)= 运行时维度 `starting`/`restarting`。

两者消费者不同,不在 `runtime_phase` 放 `bad`/`not_ready` 以免与业务 `error` 语义重叠。

### 就绪判定改造(前提)

`runtime_phase = ready` 必须真代表 oc-ops 也能服务,否则白改。两处改动:

1. **给 oc-ops sidecar 补 readinessProbe**(`render.go`,现在它没有),探 oc-ops `/health`。
2. **`adapter.go` 的 Ready 计算从「只读 hermes 容器」改成「hermes 且 oc-ops 容器都 Ready」**(`adapter.go:210-219` 遍历 ContainerStatuses,新增对 `oc-ops` 容器 Ready 的 AND)。

> 注意:`IsTerminalBad` 当前只看 hermes 的 RestartCount(`orchestrator.go:116`)。本次是否把 oc-ops 的 RestartCount 也纳入坏态判定,在实现计划里评估;最小改动是仅扩展 Ready 的 AND,坏态判定维持 hermes 口径(oc-ops 反复崩溃会让 Ready 长期 false → 停在 starting,可接受,后续按需收紧)。

## 5. 写入点:runtime_phase 何时变 ready / starting / restarting

### 快路径:初始化 worker 的 WaitReady(首启/重试)
`app_initialize.go` 的 `WaitReady` 循环本就在轮询 pod。**第一次探到 Ready 的那一刻**把业务态 `starting`→`binding_waiting`/`running` 的同时,顺手写 `runtime_phase = ready`。意义:首次拉起 / `error`→重试拉起,pod 一就绪立刻 `ready`,不必等 reconciler 的 15s。

### 稳态路径:reconciler Tick(每 15s,leader-only,`cmd/server/main.go:723`)
`AppStatusReconciler.Tick` 已扫 `running`/`binding_waiting` 实例并调 `orch.Status` 写 `runtime_snapshot_json`。在同一轮按探测结果归一写 `runtime_phase`:
- `Ready == true` → `ready`
- `Ready == false` 且非坏态 → `starting` / `restarting`(按是否 Recreate 空窗 / RestartCount 增长区分)
- 坏态 → 业务态翻 `error`(已有逻辑,runtime_phase 不抢)

这是**唯一能捕获 k8s 自发重启后恢复**的地方:节点漂移/OOM 导致 pod 重启,reconciler 探到不 Ready 写 `restarting`,pod 自愈后下一轮探到 Ready 写回 `ready`。

### 重启窗口闭环
解绑/升级主动 `RolloutRestart` 时**同步**置 `runtime_phase = restarting`(成因已知,不等探测);pod 重建完成后由稳态路径探到 Ready 写回 `ready`。

### 接受的延迟(如实记录)
- **主动重启**(解绑/升级):置 `restarting` 同步无延迟;恢复 `ready` 靠 reconciler,最多滞后一个 tick(~15s)。
- **k8s 自发重启**:从 pod 开始重启到标成 `restarting` 有最多 ~15s 探测延迟(周期轮询非事件驱动)。这意味着 pod 刚被杀的头 15s 内闸门可能仍按 `ready` 放行 → 仍可能撞一次 502。属现有 reconciler 周期模型固有特性,本次接受(主路径已无延迟,自发重启概率低)。

## 6. 闸门改造

### 后端守卫
`AppCanInitiateChannelAuth` 由「只看 status」改为**双维度 AND**:

```
canInitiate = AppCanInitiateChannelAuth(status) && runtime_phase == "ready"
```

非就绪返回现有 `ErrInstanceNotReady` → handler 409 + `MsgChannelInstanceNotReady`(中英,已有)。

### 前端闸门
`AppChannelsTab.vue` 的 `instanceReady`(`:409`)由:

```ts
const AUTH_READY_STATUSES = new Set(['running', 'binding_waiting', 'binding_failed'])
instanceReady = AUTH_READY_STATUSES.has(status)
```

改为:

```ts
instanceReady = AUTH_READY_STATUSES.has(status) && runtime_phase === 'ready'
```

三渠道按钮禁用条件(`:51/59/71/84`)与提示(`:97/136/158`)沿用 `instanceReady`,无需逐渠道改。提示文案按 `runtime_phase` 细化:`starting`→「正在启动」、`restarting`→「重启中」、`unknown`→「状态确认中」,替代/补充现有 `instanceNotReady`。

### API 契约
`runtime_phase` 需随 app 详情接口返回前端。改 handler 响应类型(`service.XxxResult` 或 dto),跑 `make openapi-gen` + `make web-types-gen` 同步 `openapi/openapi.yaml` 与 `web/src/api/generated.ts`。

## 7. 平滑升级

### 7.1 migration 纯增量(000019)
- **只 ADD `runtime_phase` 列** + 它自己的 CHECK(`ready`/`starting`/`restarting`/`unknown`),列带 `DEFAULT`。
- **不动** `apps_status_check`,存量任何 status 行都不违反约束 → migration 不会因存量数据失败。
- 列带 SQL COMMENT(项目规范:新建列必须带 COMMENT)。
- 旧 manager 二进制(滚动期)INSERT 不带该列也能写;旧代码 SELECT 显式列(sqlc),多一列直接忽略,不崩。
- down:`DROP COLUMN runtime_phase`;业务 CHECK 没动,回滚后旧代码完全不受影响。

### 7.2 存量行回填(乐观,reconciler 自愈)
升级后不能让所有 running 实例瞬间被闸门拦住(若默认 `unknown` 且闸门拦 unknown,会出现 ~15s 全员不可发起的回归窗)。回填按当前业务态乐观映射:

```sql
UPDATE apps SET runtime_phase =
  CASE status
    WHEN 'running'    THEN 'ready'        -- 乐观:正在跑的认为就绪
    WHEN 'restarting' THEN 'restarting'   -- 存量解绑重启窗
    ELSE 'unknown'                         -- 其余等 reconciler 探真
  END;
```

reconciler 下一个 tick(15s 内)用真实 pod 探测**自愈纠正**任何回填偏差。乐观回填 + 快速自愈 = 既不卡又最终正确。

### 7.3 业务态 restarting:本次保留,留后清理
现有业务态 `restarting`(migration 000016,解绑用)是 `runtime_phase` 的前身,最终应被收编,但**本次不删**:

- 滚动部署期**旧 manager 代码仍会**在解绑时写 `status='restarting'`;若本次就从业务 CHECK 删它,旧代码 UPDATE 会违反约束失败 → 不平滑。
- 本次:保留业务 `restarting` 不动;**新代码改为「解绑/重启时置 `runtime_phase='restarting'`、业务态保持 `running`」**;reconciler 现有 `convergeRestarting` **保留**,作为过渡期排水逻辑——把任何残留的业务态 `restarting` 行(旧代码/存量写的)收敛回 `running`。
- **后续清理 release**(确认线上再无业务 `restarting` 行后)才从业务 CHECK 删 `restarting` 值、移除 `convergeRestarting` 及相关 store 方法。

## 8. 受影响文件清单(参考)

| 模块 | 文件 | 改动 |
|---|---|---|
| migration | `internal/migrations/000019_*.{up,down}.sql` | 新增 runtime_phase 列 + CHECK + 回填 |
| enum/校验 | `internal/domain/enums.go` | 新增 `RuntimePhase*` 常量与校验集合 |
| 状态机/守卫 | `internal/domain/app_state_machine.go` | `AppCanInitiateChannelAuth` 改双维度;新增 phase 转移校验(如需) |
| pod 就绪计算 | `internal/integrations/k8sorch/adapter.go` | Ready = hermes AND oc-ops |
| pod 渲染 | `internal/integrations/k8sorch/render.go` | oc-ops sidecar 补 readinessProbe |
| reconciler | `internal/service/app_status_reconciler.go` | Tick 写 runtime_phase;保留 convergeRestarting 排水 |
| 初始化 worker | `internal/worker/handlers/app_initialize.go` | WaitReady 就绪时写 runtime_phase=ready(快路径) |
| 解绑/升级 | channel/升级 service(RolloutRestart 调用处) | 同步置 runtime_phase=restarting |
| sqlc 查询 | `internal/store/sqlc/apps.sql*` | 新增 SetAppRuntimePhase / 读出 runtime_phase |
| handler/dto | app 详情 handler + dto | 响应带 runtime_phase |
| OpenAPI | `openapi/openapi.yaml`、`web/src/api/generated.ts` | `make openapi-gen` + `make web-types-gen` 重新生成 |
| 前端闸门 | `web/src/pages/apps/AppChannelsTab.vue` | instanceReady 双维度;phase 化提示文案 |
| 前端 i18n | `web/src/i18n/locales/{zh,en}/...` | starting/restarting/unknown 文案(中英) |

## 9. 测试要点

- **domain 单测**:`AppCanInitiateChannelAuth` 双维度 truth table(每个 status × 每个 runtime_phase);runtime_phase 校验集合。
- **adapter 单测**:Ready 计算——hermes ready 但 oc-ops 未 ready → Ready=false;两者皆 ready → true;补充现有 `adapter_test.go` 的 ContainerStatuses 用例。
- **reconciler 单测**:各 orch.Status 输入 → 期望写入的 runtime_phase(ready/starting/restarting/unknown);坏态仍走 running→error 不被 runtime_phase 抢。
- **migration 测试**:回填 CASE 正确性;CHECK 拒非法值;down 干净。
- **前端**:instanceReady 组合逻辑单测;按 phase 渲染对应徽标/提示。
- **浏览器端到端**(CLAUDE.md 硬性):用 `kubectl scale deploy app-<id> --replicas=0` 冻结看 `starting`/`restarting`/`unknown` 各徽标与闸门禁用 + 提示;scale 回 1 看 reconciler 收敛回 `ready` 后闸门放行;三渠道(微信/企业微信/飞书)在未就绪态发起均被挡、就绪后可发起;三角色(platform_admin/org_admin/org_member)权限口径不变。

## 10. 风险与缓解

| 风险 | 缓解 |
|---|---|
| 升级后 running 实例被闸门误拦 | 乐观回填 `running→ready` + reconciler 15s 自愈 |
| k8s 自发重启头 ~15s 仍可能撞 502 | 接受(主动重启路径已同步置位无延迟);后续如需可上 informer |
| oc-ops probe 抖动致 Ready 翻 false 误标 restarting | probe 设合理 FailureThreshold(参考 hermes 的 6 次);starting/restarting 仅影响提示与闸门,不翻业务态,代价低 |
| 业务态 restarting 与 runtime_phase 双写漂移 | 新代码不再写业务 restarting,仅 reconciler 排水残留;清理 release 彻底移除 |
| 滚动部署期新旧代码并存 | migration 纯增量 + 业务 CHECK 不动 + 列带 default,新旧代码均合法 |
