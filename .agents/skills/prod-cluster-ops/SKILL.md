---
name: prod-cluster-ops
description: >-
  连接并操作本项目（oc-manager）的线上 k8s 生产集群的唯一正确方式与硬性安全边界。
  只要涉及"线上/生产集群"的任何 kubectl 操作——查 pod、看日志、apply、滚动重启、
  发版部署、排查线上故障、改 secret 生效——都必须先读本 skill。线上是与十几个其他业务
  共享的多租户集群，本项目只拥有 ocm 与 oc-apps 两个 namespace，碰错 namespace 会砸到
  别人的生产，所以连接方式和命名空间隔离不是建议而是铁律。当用户说"线上""生产""prod"
  "ywjskubectl""部署到集群""线上报错""看下线上 pod"时，主动使用本 skill，即使用户没点名。
  也包括用浏览器登录线上 manager 后台（ai.ywjs.com）只读查看页面数据的场景。
---

# 线上集群操作（oc-manager prod）

本项目的线上环境跑在一个**与十几个其他业务团队共享的 k8s 集群**上。本 skill 规定连接这个
集群的唯一正确方式，以及一条不可逾越的安全边界：**只动本项目自己的 namespace**。

## 0. 操作前必读：这是共享集群

线上集群里除了本项目的 `ocm` / `oc-apps`，还有 `drug-shop-master`、`workno-master`、
`market-master`、`ywjs-com-master`、`haodun-master`、`leadpass-master`、`fw-master`、
`tls`、`util`、`vela-system`、`kube-system` 等十几个**别的业务的 namespace**。

在这种集群上，一条 `kubectl delete`、一次 `-A` 的批量写、一个搞错 namespace 的
`rollout restart`，伤的是别人的线上业务，且往往不可逆。所以下面两件事必须像呼吸一样自然：
**(1) 永远用对 kubeconfig；(2) 永远显式指定且只指定 ocm / oc-apps。**

## 1. 连接：用对 kubeconfig（最容易犯的错）

线上集群**不在默认 kubeconfig 里**，必须显式指定：

```bash
kubectl --kubeconfig ~/dir/ywjs/kube/kubeconfig.json <命令>
```

用户终端里这一串有别名 `ywjskubectl`（定义在 `~/.bashrc`），但**别名在非交互 shell
里不展开**，所以你在 Bash 工具里必须写全 `kubectl --kubeconfig ~/dir/ywjs/kube/kubeconfig.json`。

> ⚠️ **致命混淆**：直接敲 `kubectl`（不带 --kubeconfig）连的是**本地 k3d 联调集群**
> （默认 context `k3d-ocm`），不是线上！两套集群 namespace 同名（都有 ocm），pod 名也像，
> 极易把"线上排查"做到本地、或把"本地验证"误当线上。**每次开始线上操作前，先确认：**
>
> ```bash
> kubectl --kubeconfig ~/dir/ywjs/kube/kubeconfig.json config current-context
> ```
>
> 反过来，本地联调用默认 `kubectl`（见项目 AGENTS.md 的本地 k3d 约定），不要混用。

## 2. 两条铁律

### 铁律一：命名空间只能是 ocm 或 oc-apps

本项目在线上只拥有这两个 namespace，**所有命令都必须显式 `-n ocm` 或 `-n oc-apps`**：

| namespace | 职责 | 里面有什么 |
|---|---|---|
| `ocm` | 控制面 + 自管中间件 | manager-api、manager-web、new-api、ragflow、elasticsearch、`ocm-secrets`、`acr-pull` |
| `oc-apps` | app pod（Hermes 实例）运行区 | manager 渲染出的 app pod、`acr-pull` |

- ❌ 不对**任何**其它 namespace 做读或写——它们是别的业务，不属于本项目。
- ❌ 不增删改 cluster-scoped 资源（node、pv、storageclass、clusterrole、crd 等），这些是
  集群级、影响所有租户；只读查看可以，改动交给集群管理员。
- 唯一可接受的跨 ns 只读：偶尔 `get ns` / `get pv` 这类全局列举用于了解环境，但**绝不**基于
  其结果去碰别的 ns。

### 铁律二：读操作可直接执行，写操作只输出命令、由用户自行执行

把命令分成两类，区别对待——这是本项目对线上变更的把关方式：

- **读操作**（`get`/`list`/`describe`/`logs`/`events`/`top`、`config current-context` 等一切
  不改变集群状态的命令）：可以**直接执行**（仍只限 ocm/oc-apps）。排查故障、查看状态就靠它们，
  自动化读取能快速定位问题，这是这个 skill 日常的主力。
- **写操作 / 变更类**（`apply`/`create`/`replace`/`patch`/`edit`/`delete`/`rollout restart`/
  `scale`/`set image`，以及 `make prod-deploy-*`、`make update-config` 这类内部封装了写的命令）：
  **绝不自己执行**。把完整、可直接复制粘贴的命令**输出给用户，由用户亲自执行**。
  - 为什么：这是共享生产集群，一次更新/创建/删除可能不可逆、可能波及别的业务。把"按下回车"
    这一步交给人，让真正动线上的决定权始终在用户手里。
  - 输出写命令时：带全 `--kubeconfig` 和 `-n ocm`/`-n oc-apps`，让用户复制即跑；并用一句话说明
    它会改变什么、预期结果是什么，方便用户判断后执行。执行后若需我继续排查，用户会把输出贴回来。

## 3. 常用只读操作（可直接执行）

这些都是读操作，按铁律二可由你直接执行。把 `KC=~/dir/ywjs/kube/kubeconfig.json` 设好，命令更短：

```bash
KC=~/dir/ywjs/kube/kubeconfig.json
kubectl --kubeconfig $KC -n ocm get pods -o wide          # 看控制面/中间件 pod
kubectl --kubeconfig $KC -n oc-apps get pods              # 看 app pod
kubectl --kubeconfig $KC -n ocm logs -l app=manager-api --tail=50   # 按 label 看日志
kubectl --kubeconfig $KC -n ocm describe pod <pod>        # 排查调度/拉镜像/挂卷
kubectl --kubeconfig $KC -n ocm get events --field-selector involvedObject.name=<pod> | tail
```

排查线上故障时，先用只读命令定位（pod 状态、events、日志），确认根因后再做最小写操作——
不要一上来就重启/删 pod 掩盖问题。

## 4. 浏览器查看线上页面数据（只读）

除了 kubectl，也可以直接用浏览器登录线上 manager 后台，核对"用户在界面上实际看到什么"
（知识库解析状态、用户 / 版本 / 用量、组织数据等）。这是排查时与查 DB / 看日志互补的视角。

- 地址：<https://ai.ywjs.com/>
- 平台管理员账号：用户名 `admin`、密码 `qb4Tx7TtIz!xN#`、**组织标识留空**（留空即平台
  管理员，可见全平台数据）。

> 本 skill 位于 gitignored 的 `.agents/`，不入版本库，故此处保存的生产口令不会泄漏到 git；
> 但它仍是**真实生产凭证**，绝不要拷到任何入库文件、提交信息、日志或对外消息里。

**只读铁律同样适用于浏览器**：登录后**只看、不动**。允许浏览页面、翻列表、看详情、查状态、
截图取证；**禁止任何会改变线上状态的界面操作**——不新建 / 编辑 / 删除 / 上传 / 下线 / 充值 /
改配置 / 点任何提交按钮。界面上一次误点和一条 `kubectl delete` 后果一样。用 chrome-devtools
浏览器工具时，导航、快照、截图、读取网络响应都可以，但**不要填表提交、不要点触发写操作的按钮**。

## 5. 写操作模板：输出给用户执行，不要自己跑

按铁律二，下面都是**变更类命令**——你的任务是把参数填好、**输出给用户并说明作用**，由用户自己
执行，而不是你来按回车。

```bash
KC=~/dir/ywjs/kube/kubeconfig.json
# 滚动重启某个 Deployment（改了它依赖的配置后）。rollout status 是只读等待，可由你执行确认。
kubectl --kubeconfig $KC -n ocm rollout restart deploy/<name>
kubectl --kubeconfig $KC -n ocm rollout status  deploy/<name> --timeout=180s
```

**apply secret 的特例**：`deploy/k8s/prod/secret.yaml` 是多文档，含 `ocm-secrets`(ocm)、
`acr-pull`(ocm) 和 `acr-pull`(oc-apps) 三个对象，**各自声明了 namespace**。所以 apply 它时
**不要带 `-n`**，让每个对象落到自己声明的 ns；带了 `-n ocm` 反而会和声明 oc-apps 的对象冲突报错：

```bash
kubectl --kubeconfig $KC apply -f deploy/k8s/prod/secret.yaml   # 不带 -n，对象自带 namespace
```

该文件里的对象**只属于 ocm / oc-apps**，不会动到别的 ns——但它仍是写操作，照样输出给用户执行。

## 6. 发版 / 配置更新：用 Makefile（封装好 kubeconfig 与 ns，但仍由用户执行）

项目 Makefile 的 `##@ 生产部署 (k8s)` 分组已经把 `--kubeconfig` 和 `-n ocm` 封装进去。这些命令
**封装了构建推送 + 写集群**，按铁律二同样**输出给用户执行、不要自己跑**；日常发版推荐用它们而非
手敲 kubectl：

| 命令 | 作用 |
|---|---|
| `make build-api-image` / `build-web-image` / `build-hermes-image` / `build-ops-runtime` | 构建并推送镜像到 ACR |
| `make prod-deploy-api` / `prod-deploy-web` / `prod-deploy-manager` | 构建推送 + `set image` 滚动更新 manager-api/web |
| `make prod-deploy-hermes` / `prod-deploy-ops` | 构建推送 + 把新镜像 ref 写回 secret.yaml + `update-config` |
| `make update-config` | apply secret.yaml + 重启 manager-api 使新配置生效 |

注意 `update-config` 重启的是 **manager-api**。如果你改的是 secret 里 **其他服务**的配置
（如 ragflow 的 ES/Redis、new-api 连接串），要 apply secret 后**单独重启那个服务**，
而不是 `update-config`：

```bash
kubectl --kubeconfig $KC apply -f deploy/k8s/prod/secret.yaml
kubectl --kubeconfig $KC -n ocm rollout restart deploy/ragflow   # 例：改了 ragflow 的配置
```

## 7. 凭证与文件纪律

- `deploy/k8s/prod/secret.yaml` 含**真实生产凭证**（DB/Redis/ES 口令、master_key、ACR 凭证、
  S3 长期 key），**已被 gitignore，绝不提交**。改它只改目标字段，不动其他凭证。
- 改完 secret 要生效必须 apply + 重启对应服务（进程不热重载配置）。
- 入库的是 `deploy/k8s/prod/*.yaml`（manifest，镜像 ref / 域名 / 资源，不含真值）和
  `secret.example.yaml`（占位模板）。

## 8. 一句话自检清单

每次对线上动手前，默念四条：

1. **kubeconfig 对不对？**——是 `--kubeconfig ~/dir/ywjs/kube/kubeconfig.json`，不是裸 `kubectl`。
2. **namespace 对不对？**——只 `ocm` / `oc-apps`，命令里显式写出来了。
3. **是读还是写？**——读操作可自己执行；**写 / 变更类一律只输出命令给用户执行**，不自己按回车
   （浏览器后台同理：只看不点写操作）。
4. **写命令给全了吗？**——输出给用户的写命令带全 `--kubeconfig` 和 `-n`、一句话说清作用，复制即可跑。
