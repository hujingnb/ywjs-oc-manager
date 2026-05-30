# ops 镜像 Pod 内部契约

> **定位**：spec-A1 交付给 spec-A2 的权威 Pod 内部契约。  
> spec-A2 以本文档为唯一依据，将 ops initContainer / sidecar 渲染进 app pod spec。  
> 本文档与 `docs/bootstrap-http-contract.md`（bootstrap 端点契约）和  
> `deploy/k8s/contracts/app-pod.deployment.yaml`（spec-D Pod 契约）共同构成 spec-A 的完整契约集合。

---

## 1. 概述

`runtime/ops/` 提供专用 ops 镜像，承载 app pod 内两个辅助角色：

- **initContainer `restore`**：pod 启动时拉取配置、恢复工作区，确保 hermes 主容器启动前数据就绪。
- **sidecar `s3-sync`**：pod 运行期间持续将 hermes 写入 `/opt/data` 的产物增量同步回 S3；pod 停止前通过 preStop hook 触发最终全量同步，保证零丢失。

ops 镜像仅包含标准搬运工具（`aws-cli`、`sqlite3`、`jq`、`curl`、`bash`、`coreutils`），与 hermes  
主镜像（agent 运行时）完全解耦——agent 逻辑与运维搬运职责分离，互不依赖，可独立升级。

---

## 2. 镜像

### 2.1 构建目标

| Makefile target | 用途 | 推送目标 |
|---|---|---|
| `make build-ops-runtime` | 生产构建 | prod registry |
| `make local-build-ops` | 本地联调构建 | k3d 本地 registry |

> **注**：以上两个 target 由 **Task 9** 加入 Makefile，当前版本尚未提交。

### 2.2 镜像引用占位

Pod spec 中 ops 镜像引用固定以占位符 `<OPS_IMAGE_REF>` 表示，由 spec-A2 渲染时替换为实际镜像地址。

### 2.3 解耦原因

- hermes 镜像包含 LLM agent 运行时与 Python/Node 依赖，体积较大，发布周期独立。
- ops 镜像仅含 shell 脚本与标准 CLI 工具（alpine 基础层），升级互不干扰。
- 分离后 initContainer 拉取失败不影响 hermes 镜像本身，日志定界更清晰。

---

## 3. 共享卷

App pod 内使用两个 pod 级 `emptyDir` 卷，各容器按需挂载：

| 卷名 | 挂载路径 | 内容 | 读写关系 |
|---|---|---|---|
| `oc-input` | `/opt/oc-input` | `manifest.yaml`；`resources/persona.md`；`resources/platform-rules.md`；`resources/skills/*.tar` | initContainer **写**（oc-restore 落盘）；hermes 主容器**读**（加载配置） |
| `data` | `/opt/data` | `workspace/`（hermes 工作区）；`sessions/`（会话存档）；`state.db`（sqlite 状态库） | initContainer **写**（恢复数据）；hermes 主容器**读写**（正常运行）；sidecar `s3-sync` **读**（增量上传）；sidecar `oc-ops` **读**（spec-E，沿用 spec-D 契约） |

`oc-input` 卷是 spec-A1 新增的——spec-D 原契约只有 `data` 卷，spec-A2 渲染时须同时补充 `oc-input` 卷（见 §7）。

---

## 4. 环境变量契约

### 4.1 必需变量

| 变量名 | 来源 | 说明 |
|---|---|---|
| `OC_CONTROL_TOKEN` | Secret `app-<id>-token` 的 `control-token` 键（`secretKeyRef`） | per-app control token，以 `Bearer` 头调 bootstrap 端点鉴权；与 oc-ops 的 `OC_OPS_TOKEN` 指向**同一个** Secret key |
| `OC_BOOTSTRAP_URL` | spec-A2 渲染写入（字面量） | bootstrap 端点完整 URL，格式示例：`http://manager-api.oc-system.svc:<port>/internal/apps/<id>/bootstrap` |

两个变量均由三个脚本（`oc-restore`、`oc-sync`、`oc-presync`）通过 `require_env` 强制校验，缺失则脚本以非零退出。

### 4.2 可选调参变量

| 变量名 | 默认值 | 说明 |
|---|---|---|
| `OC_INPUT_DIR` | `/opt/oc-input` | oc-input emptyDir 在容器内的挂载路径 |
| `OC_DATA_DIR` | `/opt/data` | data emptyDir 在容器内的挂载路径（即 `HERMES_HOME`） |
| `OC_SYNC_INTERVAL` | `8`（秒） | sidecar workspace 增量同步间隔（`oc-sync` 主循环 `sleep` 周期） |
| `OC_SQLITE_INTERVAL` | `30`（秒） | sidecar sqlite 一致性备份最小间隔（`oc-sync` 内按时间判断是否触发） |
| `OC_CRED_SKEW` | `300`（秒） | STS 凭证提前续期秒数；凭证剩余有效期 < 该值时 sidecar 自动重调 bootstrap 换新凭证 |
| `OC_BOOTSTRAP_RETRIES` | `5` | bootstrap 拉取总请求次数（含首次）；指数退避，用尽则非零退出 |
| `OC_SYNC_ONCE` | `0` | 设为 `1` 时 oc-sync 跑一轮（含强制 sqlite 备份）后退出；**仅测试用，生产不设置** |

### 4.3 S3 参数来源说明

`endpoint`、`region`、`bucket`、`prefix` **不通过 env 注入**。  
脚本从 bootstrap 响应的 `s3_write` 对象解析，由 `oc-lib.sh` 的 `export_s3_env` 写入 shell 级变量后供 `aws_s3` 包装函数使用。  
STS 凭证（`access_key_id`、`secret_access_key`、`session_token`）同理，由 `write_aws_credentials` 写入 `~/.aws/credentials` 的 `ocsync` profile，不经 pod env 传递。

---

## 5. 容器角色与 command

### 5.1 initContainer `restore`

```yaml
initContainers:
  - name: restore
    image: "<OPS_IMAGE_REF>"
    command: ["oc-restore"]
    env:
      - name: OC_CONTROL_TOKEN
        valueFrom:
          secretKeyRef:
            name: app-<APP_ID>-token
            key: control-token
      - name: OC_BOOTSTRAP_URL
        value: "http://manager-api.oc-system.svc:<PORT>/internal/apps/<APP_ID>/bootstrap"
    volumeMounts:
      - { name: oc-input, mountPath: /opt/oc-input }
      - { name: data,     mountPath: /opt/data }
```

**行为**（`oc-restore` 脚本）：
1. `require_env OC_CONTROL_TOKEN OC_BOOTSTRAP_URL` 校验必需变量。
2. `fetch_bootstrap` 带 Bearer token 调 bootstrap，指数退避最多 `OC_BOOTSTRAP_RETRIES`（默认 5）次。
3. 将 `manifest_yaml` / `persona` / `platform_rule` 写入 `/opt/oc-input`（见 §6.1）。
4. 按 `skills[].rel_path` + `skills[].url` 下载各 skill tar（见 §6.2）。
5. 将 `s3_write` STS 凭证写入 `~/.aws/credentials ocsync` profile；从 `s3_write` 解析 S3 参数。
6. `aws s3 sync` 恢复 `apps/<id>/workspace/` 和 `apps/<id>/sessions/` 到 `/opt/data`（见 §6.3）。
7. 若 `apps/<id>/state.db` 存在则 `aws s3 cp` 下载，并清除本地 `-wal`/`-shm` 边车（见 §6.3）。

initContainer 完成后，hermes 主容器才会启动。

### 5.2 sidecar `s3-sync`

```yaml
containers:
  - name: s3-sync
    image: "<OPS_IMAGE_REF>"
    command: ["oc-sync"]
    lifecycle:
      preStop:
        exec:
          command: ["oc-presync"]
    env:
      - name: OC_CONTROL_TOKEN
        valueFrom:
          secretKeyRef:
            name: app-<APP_ID>-token
            key: control-token
      - name: OC_BOOTSTRAP_URL
        value: "http://manager-api.oc-system.svc:<PORT>/internal/apps/<APP_ID>/bootstrap"
    volumeMounts:
      - { name: data, mountPath: /opt/data }
```

**oc-sync 行为**（主循环）：
- 启动时立即调 bootstrap 拿 STS 凭证（`ensure_creds`）。
- 每 `OC_SYNC_INTERVAL`（默认 8s）循环：先 `ensure_creds`（凭证临近过期时自动续期），然后 `sync_workspace_up`（workspace 增量上传），每 `OC_SQLITE_INTERVAL`（默认 30s）触发一次 `backup_sqlite_up`（sqlite 一致性快照上传）。

**oc-presync 行为**（preStop hook，exec 模式）：
- 调 bootstrap 取最新凭证 → 做一次 `sync_workspace_up` + `backup_sqlite_up`，完成后退出。
- 与 oc-sync 主循环**并发安全**：`backup_sqlite_up` 使用 `mktemp` 唯一临时文件，两者同时调用不会相互覆盖。

---

## 6. 恢复/同步机制

### 6.1 manifest 与 resources

bootstrap 响应中的 `manifest_yaml`、`persona`、`platform_rule` 直接写入 `/opt/oc-input`：

| 源字段 | 写入路径 |
|---|---|
| `manifest_yaml` | `/opt/oc-input/manifest.yaml` |
| `persona` | `/opt/oc-input/resources/persona.md` |
| `platform_rule` | `/opt/oc-input/resources/platform-rules.md` |

`manifest.yaml` 含 `api_key` 与 `knowledge.app_token`（均为明文）——仅落 pod 本地临时盘，**不上传 S3**，pod 销毁即消失（`emptyDir` 语义）。

### 6.2 skills：预签名 URL 下载

skills tar 位于 S3 的 `versions/<vid>/skills/*` 路径，跨 `apps/<id>/` 前缀，STS 写凭证（限定前缀 `apps/<id>/`）无读权限。  
因此 skills 采用 bootstrap 响应的 `skills[].url` 预签名 GET URL 下载，写入 `/opt/oc-input/resources/skills/<name>.tar`。

下载逻辑：
```
for skill in response.skills[]:
    curl -fsS skill.url -o /opt/oc-input/{skill.rel_path}
```

### 6.3 app 数据：STS 凭证 + aws s3 sync/cp

`workspace/`、`sessions/`、`state.db` 均在 S3 的 `apps/<id>/` 前缀内，bootstrap 的 `s3_write` STS 凭证可读写该前缀。

**恢复（oc-restore）**：

| 数据 | S3 路径 | 本地路径 | 方式 |
|---|---|---|---|
| workspace 目录树 | `apps/<id>/workspace/` | `/opt/data/workspace/` | `aws s3 sync`（增量下载） |
| sessions 目录树 | `apps/<id>/sessions/` | `/opt/data/sessions/` | `aws s3 sync`（增量下载） |
| sqlite 状态库 | `apps/<id>/state.db` | `/opt/data/state.db` | `aws s3 cp`（单文件下载）+ 清 `-wal`/`-shm` |

首启时 `apps/<id>/` 前缀为空：`aws s3 sync` 返回 0（空操作），`state.db` 不存在则跳过——行为完全幂等。

**同步（oc-sync / oc-presync）**：

| 数据 | S3 路径 | 本地路径 | 方式 |
|---|---|---|---|
| workspace 目录树 | `apps/<id>/workspace/` | `/opt/data/workspace/` | `aws s3 sync`，排除 `node_modules/*`、`.git/*`、`*.tmp` |
| sqlite 状态库 | `apps/<id>/state.db` | 临时文件（`mktemp`）→ 上传后删除 | `sqlite3 .backup`（一致性快照）+ `aws s3 cp` |

> **无 `--delete`**：workspace 同步**故意不加** `--delete`，避免将本地临时删除传播到 S3 持久存储，防止误删历史数据。

### 6.4 STS 凭证续期

- 凭证有效期由 bootstrap 响应的 `s3_write.expires_at`（RFC3339）决定。
- sidecar `oc-sync` 每轮循环调 `needs_refresh`：若 `expires_at - now < OC_CRED_SKEW`（默认 300s），则自动重调 bootstrap 换新凭证，无需 pod 重启。
- oc-presync（preStop）每次独立调 bootstrap 拿最新凭证，不依赖 oc-sync 的凭证状态。

### 6.5 `restore.*_url` 字段废弃说明（对 spec-A1）

bootstrap 响应的 `restore.workspace_url` / `sessions_url` / `state_db_url` 三个预签名读 URL 字段对 spec-A1 **废弃，不使用**：

- 预签名 URL 指向单对象，无法恢复前缀镜像目录（workspace 是多文件目录树，sessions 同理）。
- spec-A1 改用 `s3_write` STS 凭证做 `aws s3 sync` 前缀级恢复，覆盖所有历史文件。

结论：spec-A2 渲染 wiring 时**不依赖** `restore.*_url`；manager 侧可按需在后续迭代清理该字段。

---

## 7. spec-A2 渲染待办

spec-A2 在 spec-D 现有契约（`deploy/k8s/contracts/app-pod.deployment.yaml`）基础上追加以下内容：

### 7.1 待办清单

1. **新增 `oc-input` emptyDir 卷**：volumes 列表加 `{ name: oc-input, emptyDir: {} }`。
2. **新增 initContainer `restore`**：ops 镜像，command `["oc-restore"]`，挂 `oc-input` + `data`，注入 `OC_CONTROL_TOKEN` + `OC_BOOTSTRAP_URL`。
3. **新增 sidecar `s3-sync`**：ops 镜像，command `["oc-sync"]`，lifecycle.preStop exec `["oc-presync"]`，挂 `data`，注入同上两个必需变量。
4. **hermes 主容器** 补充挂载 `oc-input`（只读），以读取 initContainer 写入的 manifest/resources。
5. `OC_BOOTSTRAP_URL` 由 spec-A2 渲染时计算（manager svc ClusterIP/DNS + port + app id 拼接）。
6. `OC_CONTROL_TOKEN` 经 `secretKeyRef` 从 `app-<id>-token` Secret 的 `control-token` 键注入——与 `oc-ops` 的 `OC_OPS_TOKEN` 指向同一 Secret key，Secret 本身由 spec-A 创建流程 ensure。

### 7.2 示意 pod spec 片段（与 spec-D 契约的 diff）

以下为 spec-D 契约基础上 spec-A2 须追加/修改的 YAML 片段（非完整 Deployment，仅展示增量）：

```yaml
# spec-D 原 volumes（仅 data）→ 新增 oc-input
volumes:
  - name: data
    emptyDir: {}          # spec-D 已有
  - name: oc-input        # ★ spec-A2 新增
    emptyDir: {}

# spec-D 无 initContainers → spec-A2 新增
initContainers:
  - name: restore                # ★ spec-A2 新增
    image: "<OPS_IMAGE_REF>"     # Task 9 提供构建 target
    command: ["oc-restore"]
    env:
      - name: OC_CONTROL_TOKEN
        valueFrom:
          secretKeyRef:
            name: app-<APP_ID>-token
            key: control-token   # 与 oc-ops 的 OC_OPS_TOKEN 同一把
      - name: OC_BOOTSTRAP_URL
        value: "http://manager-api.oc-system.svc:<PORT>/internal/apps/<APP_ID>/bootstrap"
    volumeMounts:
      - { name: oc-input, mountPath: /opt/oc-input }
      - { name: data,     mountPath: /opt/data }

containers:
  # hermes 主容器（spec-D 已有，补充 oc-input 挂载）
  - name: hermes
    image: "<HERMES_IMAGE_REF>"
    env:
      - name: HERMES_HOME
        value: /opt/data
    volumeMounts:
      - { name: data,     mountPath: /opt/data }
      - { name: oc-input, mountPath: /opt/oc-input, readOnly: true }  # ★ spec-A2 新增

  # oc-ops 第二容器（spec-D 已有，不变）
  - name: oc-ops
    image: "<OC_OPS_IMAGE_REF>"
    ports:
      - containerPort: 8080
    env:
      - name: OC_OPS_TOKEN
        valueFrom:
          secretKeyRef:
            name: app-<APP_ID>-token
            key: control-token
    volumeMounts:
      - { name: data, mountPath: /opt/data }

  # s3-sync sidecar（spec-D 无 → spec-A2 新增）
  - name: s3-sync                # ★ spec-A2 新增
    image: "<OPS_IMAGE_REF>"
    command: ["oc-sync"]
    lifecycle:
      preStop:
        exec:
          command: ["oc-presync"]
    env:
      - name: OC_CONTROL_TOKEN
        valueFrom:
          secretKeyRef:
            name: app-<APP_ID>-token
            key: control-token
      - name: OC_BOOTSTRAP_URL
        value: "http://manager-api.oc-system.svc:<PORT>/internal/apps/<APP_ID>/bootstrap"
    volumeMounts:
      - { name: data, mountPath: /opt/data }
```

> **★** 标注的行/块为 spec-A2 在 spec-D 基础上的新增内容；未标注部分沿用 spec-D 原契约不变。
