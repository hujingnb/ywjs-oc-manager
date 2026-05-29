# spec-A1 设计：app pod 运行时侧（restore/sync 脚本 + ops 镜像）

> 状态：设计待评审（2026-05-30）。
> 父设计：`docs/superpowers/specs/2026-05-29-k8s-migration-design.md`（§5.2 pod 结构、§5.5 单写者与丢失窗口、D15/D16/D17）。
> 本 spec 是 k8s 迁移 Workstream A 的前半——**A-pod**。Workstream A 按本轮决策拆为
> **spec-A1（pod 运行时侧，本文档）→ spec-A2（manager 编排侧）** 两个独立周期。
> 依赖契约：spec-B 的 bootstrap 端点（`docs/bootstrap-http-contract.md`）、spec-D 的 app-pod 契约
> （`deploy/k8s/contracts/app-pod.deployment.yaml`）、spec-E 的 oc-ops sidecar。

## 1. 背景与目标

### 1.1 现状

k8s 迁移后 app 不再跑在 runtime-agent 节点的 docker 容器里，而是 k8s pod（零 PVC，
emptyDir 为运行期临时盘，S3 为持久源）。spec-B 已交付 manager 侧数据层：

- bootstrap 端点 `GET /internal/apps/{id}/bootstrap`（Bearer control token）实时渲染
  manifest（含 api_key）+ 签发 skills/restore 的预签名读 URL + app prefix 限定的 STS 写凭证。
- S3 抽象层（标准 S3 协议）、control token 三用统一。

但**pod 侧没有任何东西去消费这些契约**——没有进程在 pod 启动时调 bootstrap 拉配置/恢复
数据，也没有进程把运行期产物同步回 S3。这正是 spec-A1 要补的运行时侧。

### 1.2 目标

为 app pod 提供运行时侧的数据搬运能力，全部打进一个专用 ops 镜像：

1. **initContainer（restore）**：启动时调 bootstrap 拉 manifest/resources 写入 `/opt/oc-input`、
   用预签名 URL 下载 skills（version 级、跨前缀，只能预签名），并用 bootstrap 返回的 STS 凭证
   `aws s3 sync`/`cp` 恢复 workspace/sessions/state.db（均在 `apps/<id>/` 前缀内）到 `/opt/data`
   （首启跳过）。
2. **sidecar（s3-sync）**：用 bootstrap 返回的 STS 凭证增量同步 `/opt/data/workspace` → S3
   + sqlite `.backup` 快照上传 + preStop 全量同步（优雅终止零丢失）。
3. **钉死 pod 内部契约**（env 变量名、emptyDir 路径、脚本入口、凭证续期方式），供 spec-A2
   渲染 pod spec 时对齐。

**本 spec 是纯运行时侧产物**（镜像 + 脚本），可独立构建、shellcheck、对真实 MinIO 集成测。

## 2. 范围与边界

### 2.1 本 spec 交付（已与用户确认）

1. 专用 **ops 镜像**（`runtime/ops/`）：alpine + aws-cli + sqlite3 + jq + curl，三个入口脚本。
2. **oc-restore**（initContainer 入口）、**oc-sync**（sidecar 入口）、**oc-presync**（preStop）。
3. **pod 内部契约**（§3）。
4. shellcheck + 对真实 MinIO + mock bootstrap 的集成测。

### 2.2 不在本 spec（归 spec-A2 / 保持原样）

- pod spec 渲染（initContainer/sidecar 定义、emptyDir 卷、imagePullSecrets、label/probe）。
- k8s Secret 创建与 `OC_CONTROL_TOKEN` 注入、`OC_BOOTSTRAP_URL` 的实际取值（按 manager 跑法）。
- KubernetesAdapter、docker→k8s 生命周期、节点概念删除、OcOpsResolver 真实寻址、创建流程
  ensure——**全归 spec-A2**。
- hermes 主容器镜像、oc-ops sidecar——**不动**（spec-D 契约 / spec-E 已定）。

### 2.3 关键取舍（已与用户确认）

| # | 决策点 | 选择 | 理由 / 影响 |
|---|---|---|---|
| A1.1 | A 拆分 | **spec-A 拆 A-pod（A1）→ A-manager（A2）**，本 spec 做 A1 | pod 运行时侧是 A 里唯一可独立构建/单测的部分（镜像工作，像 spec-E）；其余 manager 侧深度耦合 |
| A1.2 | 镜像 + 同步工具 | **专用 ops 小镜像（alpine + aws-cli + sqlite3 + jq + curl），initContainer 与 sidecar 共用；同步用标准 `aws s3 sync`** | hermes 主镜像不膨胀（agent 与运维搬运职责分离）；`aws s3 sync` 标准 S3、vendor-neutral、成熟增量同步，最贴 B4；代价是多一个聚焦镜像标签 |
| A1.3 | STS 凭证续期 | **sidecar 自调 bootstrap 续期**（env 控制 token，启动取一次、过期前刷新） | 与 initContainer 解耦、不靠 emptyDir 传凭证（避免陈旧）；复用 spec-B 幂等 bootstrap |
| A1.4 | 验证范围 | **shellcheck + 对真实 MinIO + mock bootstrap 的集成测**；完整 pod 闭环 + 三角色浏览器验证推迟到 A2 后的 A/B/D/E 合并 | pod 编排在 A2，本阶段无 pod 可跑；但 restore/sync/sqlite 是外部协议交互，须对真实后端证明（B6 同性质，不被 mock 掩盖）|

> A1.4 是对项目「真实环境验证」要求的一次显式、有界偏离，与 spec-B B6 / spec-E E4 同性质：
> 本 spec 交付并以真实 MinIO 集成测验证脚本逻辑，完整 pod 闭环待 A2 渲染 pod 后，与 A/B/D/E
> 一起做端到端 + 三角色浏览器验证。本 spec 不单独宣称「pod 闭环已验证可用」。

## 3. pod 内部契约（钉死，供 spec-A2 渲染）

### 3.1 共享卷

| 挂载路径 | 用途 | 容器可见性 |
|---|---|---|
| `/opt/oc-input` | manifest.yaml + resources/{persona,platform-rules}.md + resources/skills/*.tar | initContainer 写、hermes 读 |
| `/opt/data` | HERMES_HOME：workspace、sessions、sqlite（state.db 等） | initContainer 恢复写、hermes 读写、sidecar 同步读 |

两者均为 pod 级 emptyDir（A2 在 pod spec 声明），各容器共享挂载。

### 3.2 脚本读取的环境变量

| env | 含义 | A2 注入来源 |
|---|---|---|
| `OC_CONTROL_TOKEN` | per-app control token，作 Bearer 调 bootstrap | k8s Secret `app-<id>-token` 的 `control-token` 键（与 oc-ops 同一把） |
| `OC_BOOTSTRAP_URL` | manager bootstrap 端点完整 URL | A2 渲染（如 `http://manager-api.oc-system.svc:<port>/internal/apps/<id>/bootstrap`；本地 manager 跑法不同则填对应可达地址） |

S3 endpoint/region/bucket/prefix **不走 env**——脚本从 bootstrap 响应的 `s3_write` 字段解析
（单一真相源、少配置）。

### 3.3 容器角色（A2 据此渲染）

- initContainer `restore`：ops 镜像，command `oc-restore`。
- sidecar `s3-sync`：ops 镜像，command `oc-sync`，preStop hook → `oc-presync`。
- hermes 主容器 + oc-ops sidecar：沿用 spec-D 契约 / spec-E，不在本 spec。

## 4. oc-restore（initContainer，恢复）

**恢复机制按数据所在前缀分流（必须，非偏好）**：
- **skills 是 version 级**（S3 key `versions/<vid>/skills/<name>.tar`），**不在 `apps/<id>/`
  前缀内**——pod 的 STS 凭证（policy 仅授权 `apps/<id>/*`）读不到，**只能用 manager 签发的
  预签名 URL** 下载（bootstrap `skills[].url`）。
- **workspace/sessions/state.db 在 `apps/<id>/*` 内**——STS 凭证含 GetObject/ListBucket 读权限，
  **用 STS 凭证 `aws s3 sync`/`cp` 下载**（与 sidecar 的前缀镜像上传对称）。

> 故 bootstrap 响应里 spec-B 的 `restore.workspace_url`/`sessions_url`/`state_db_url` **预签名
> 字段对 A1 无用**——单对象预签名 URL 无法恢复前缀镜像的目录数据。spec-B 已建该字段，无害；
> spec-A2 在落地时标注其废弃。A1 不消费 `restore` 字段，改用 `s3_write` STS 凭证恢复 app 数据。

执行步骤：

1. 带 `Authorization: Bearer $OC_CONTROL_TOKEN` curl `$OC_BOOTSTRAP_URL` 取 bootstrap JSON；
   失败按指数退避重试有限次（manager 多副本 HA，但 pod 起得早时可能瞬时不可达）；最终失败则
   initContainer 非零退出（k8s 自动重试 pod）。
2. jq 解析响应：
   - `manifest_yaml` → `/opt/oc-input/manifest.yaml`
   - `persona` → `/opt/oc-input/resources/persona.md`
   - `platform_rule` → `/opt/oc-input/resources/platform-rules.md`
3. 遍历 `skills[]`：curl 预签名 `url` → `/opt/oc-input/<rel_path>`（rel_path 形如
   `resources/skills/<name>.tar`，由 bootstrap 给出）。
4. 从 `s3_write` 写 STS 凭证（aws credentials 文件）+ 解析 endpoint/region/bucket/prefix。
5. 用 STS 凭证恢复 app 数据（首启时前缀/对象不存在 → sync 空操作、cp 跳过，干净处理）：
   - `aws s3 sync s3://<bucket>/<prefix>workspace/ /opt/data/workspace --endpoint-url <ep>`
   - `aws s3 sync s3://<bucket>/<prefix>sessions/ /opt/data/sessions --endpoint-url <ep>`
   - state.db：`aws s3 ls` 判存在 → `aws s3 cp s3://<bucket>/<prefix>state.db /opt/data/state.db`，
     并按 §5 清理 `state.db-wal`/`state.db-shm`（干净重开）。
6. 恢复为覆盖式幂等；pod 重启时 initContainer 重跑，行为一致。

> initContainer 与 sidecar 都调 bootstrap：initContainer 取 manifest + skills 预签名 + s3_write
> STS 凭证（读 app 数据）；sidecar 取 s3_write STS 凭证（写 app 数据）并续期。两者共用 §6.1 凭证
> 解析/写入逻辑（放 oc-lib.sh）。

> api_key 随 manifest 落 `/opt/oc-input/manifest.yaml`（emptyDir，pod 本地临时盘，**不进 S3**）；
> 符合 spec-B D7「api_key 不落 S3/盘持久层、DB 为唯一真相源」。

## 5. sqlite 处理（参考父设计 §5.5 / D16）

- **restore（oc-restore）**：放置 `state.db` 后**删除同目录的 `state.db-wal` / `state.db-shm`**，
  保证 hermes 干净重开 WAL 库；**绝不分别下载 -wal/-shm**（时点不一致会损坏）。
- **backup（oc-sync）**：`sqlite3 /opt/data/<state.db 路径> ".backup /tmp/snap.db"` 出一致性
  快照（对运行中的 live DB 安全）→ 上传为 S3 的 `state.db` key；**绝不分别上传 -wal/-shm**。
- state.db 在 `/opt/data` 下的确切相对路径，在实现计划核对 hermes 源（`scopes.go`）后钉死；
  S3 key 用 spec-B 约定的 `apps/<id>/state.db`（由 bootstrap `s3_write.prefix` + `state.db` 拼）。

## 6. oc-sync（sidecar）+ STS 凭证续期

### 6.1 凭证续期

- 维护本地凭证状态（写 `~/.aws/credentials` 的临时凭证 profile，含 `aws_session_token`）。
- 缺失或临近过期（剩余 < 5m，按 bootstrap 返回的 `s3_write.expires_at` 判断）时：curl bootstrap
  → jq `s3_write` → 重写凭证文件，并解析 `endpoint`/`region`/`bucket`/`prefix` 供 aws-cli 使用。

### 6.2 同步循环

1. 确保凭证新鲜（§6.1）。
2. 增量同步 workspace：
   `aws s3 sync /opt/data/workspace s3://<bucket>/<prefix>workspace/ --endpoint-url <endpoint>
   --exclude "node_modules/*" --exclude ".git/*" --exclude "*.tmp"`
   （排除可重建大目录，父设计 §5.5）。
3. sqlite 快照（§5，节流：约每 30s）→ 上传 state.db。
4. sleep（workspace 同步节奏约 8s）。

> 节流默认值（workspace ~8s、sqlite ~30s、临近过期阈值 5m）作为初值，实现计划落为脚本常量/env，
> 后续可调；丢失窗口语义见父设计 D17（仅硬 kill 丢上次快照后的增量，已接受）。

### 6.3 oc-presync（preStop）

pod preStop hook 调用：一次全量 `aws s3 sync` + sqlite 快照上传——优雅终止零丢失（D17）。

## 7. 验证策略（A1.4）

- **shellcheck**：静态检查 oc-restore / oc-sync / oc-presync 全部脚本。
- **集成测（对本地 k3d 真实 MinIO + 一个 mock bootstrap HTTP 服务，环境门控；脚本在 ops 镜像
  容器内跑）**：
  - **oc-restore**：mock bootstrap 返回 canned JSON（manifest_yaml/persona/platform_rule、指向
    MinIO 预置 version 级对象的 skills 预签名 URL、s3_write STS 凭证）；MinIO 的 `apps/<id>/`
    前缀预置 workspace 对象 + state.db → 断言 `/opt/oc-input` 落盘、skills 下载到位、workspace
    经 STS sync 下载到 `/opt/data/workspace`、state.db 恢复后无 -wal/-shm。
  - **oc-restore 首启**：MinIO `apps/<id>/` 前缀为空 → 断言 workspace sync 空操作、state.db cp
    干净跳过、不报错。
  - **oc-sync**：预置 workspace 文件 + state.db → 跑一轮 → 断言 MinIO 出现 workspace 对象与
    state.db；凭证刷新路径被走到（mock bootstrap 提供可用于真实 MinIO 的 s3_write 凭证）。
- **不做**：完整 pod（initContainer/sidecar 真在 pod 内编排运行）、端到端、三角色浏览器走查
  ——推迟到 A2 渲染 pod 后的 A/B/D/E 合并验证。

## 8. 风险与权衡

| 风险 | 说明 | 缓解 |
|---|---|---|
| 脚本无 pod 编排无法端到端验 | initContainer/sidecar 真实运行依赖 A2 | A1.4：对真实 MinIO 集成测证明脚本逻辑；契约固定供 A2 渲染 |
| STS 凭证过期窗口 | 续期失败则同步中断 | 临近过期提前刷新；bootstrap 幂等可重调；manager HA |
| sqlite live backup 一致性 | 运行中备份 | 用 `.backup` API（对 live DB 安全），不碰 -wal/-shm |
| workspace 大目录同步成本 | node_modules 等高频变更 | `--exclude` 可重建大目录；`aws s3 sync` 仅传变更 |
| `aws s3 sync --delete` 误删 | 远端删本地已删文件 | restore 后本地是完整态再开同步；preStop 全量；本设计同步循环默认不加 --delete（仅新增/更新），删除归档由 manager 侧（A2/spec-B MovePrefix）管，避免 sidecar 误删持久数据 |
| ops 镜像 aws-cli 体积 | alpine + aws-cli 偏大 | 聚焦镜像、按需精简；与 hermes 镜像解耦不影响 agent |

> 注：§6.2 同步命令上文示例含 `--exclude` 但**不含 `--delete`**——sidecar 只做新增/更新镜像，
> 不向 S3 传播本地删除，避免 workspace 临时清理或 restore 中间态导致持久数据被误删；S3 侧的
> 删除/归档由 manager 控制（spec-B `MovePrefix`/`DeletePrefix`，spec-A2 编排）。

## 9. 待 spec-A2（本 spec 不做，契约已钉）

- 渲染 pod spec：引用 `<OPS_IMAGE_REF>`，声明 initContainer `restore`（cmd oc-restore）、sidecar
  `s3-sync`（cmd oc-sync + preStop oc-presync）、`/opt/data` 与 `/opt/oc-input` emptyDir 卷。
- 注入 `OC_CONTROL_TOKEN`（Secret `app-<id>-token` 的 `control-token` 键）与 `OC_BOOTSTRAP_URL`
  （按 manager 在集群内/宿主的跑法渲染可达地址）。
- KubernetesAdapter、docker→k8s 生命周期、节点概念删除、OcOpsResolver 真实寻址、创建流程
  ensure（api_key/control token/version）。
- A/B/D/E 合并后的端到端 + 三角色真实浏览器验证（吸收 A1.4 推迟项）。
