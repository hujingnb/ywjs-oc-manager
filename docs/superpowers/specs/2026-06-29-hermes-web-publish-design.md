# hermes 运行时发布静态站点(对外域名访问)设计

> 状态:设计已与用户对齐,待评审后转实现计划。
> 日期:2026-06-29

## 1. 背景与目标

允许 hermes 引擎在运行中"开发一个网站并发布",即:用户在对话里让 hermes 做一个站点,
hermes 产出静态产物后通过一个发布能力把它变成一个**公网可访问的带域名 HTTPS 站点**,
并告知用户"该站点 N 天后自动删除"。

该能力**不对所有企业开放**:由平台管理员在企业配置中按企业开关,并为该企业指定基础域名、
DNS provider 及凭证。DNS 解析需适配多家运营商(移动云 / 阿里云 / 华为云 / 腾讯云,接口可扩展)。

### 范围内
- 平台管理员按企业开通该能力 + 配置基础域名 / DNS provider / 凭证。
- 企业开通时一次性 provisioning:通配 DNS 解析 + ACME DNS-01 通配证书 + 一条通配 Ingress。
- hermes 侧发布能力(条件注入的 `oc-publish` skill),把 `/opt/data/workspace` 下的静态目录发布出去。
- 共享 site-server:按 Host 路由,从对象存储流式返回静态文件。
- **对同一站点反复修改**:同 slug 原地更新(URL 不变)、原子换版、republish 重置 TTL(见 §4.5)。
- 站点生命周期:默认 7 天 TTL、自动回收、手动下线、续期。
- 管理面:org admin 看/管本企业站点,平台管理员全局视图。

### 范围外(明确不做)
- **动态 web 服务**(Node/FastAPI 等运行中后端):本期只做**纯静态站点**。架构上为未来动态模式
  (独立 pod)预留,但本期不实现。
- 公网站点的访问鉴权:发布出去的静态站点**完全公开、无鉴权**(静态站点设计如此)。
- 自定义任意完整域名 / 域名归属校验:本期只支持 `<slug>.<企业基础域名>` 子域。

## 2. 关键设计决策(已与用户确认)

| 决策点 | 结论 | 理由 |
|---|---|---|
| 服务形态 | **纯静态站点** | 公网零代码执行、可重启不丢、TTL 好回收、最安全;"开发个网站"多数即静态/SPA |
| 域名方案 | **每企业通配子域** `<slug>.<base_domain>` | 子域瞬时生效(无逐站 DNS 传播延迟),一张通配证书覆盖全部 |
| HTTPS | **ACME DNS-01 通配证书,自动续期** | 通配证书必须走 DNS-01;这是引入 provider 适配层的核心动因 |
| 证书/DNS 技术选型 | **go-acme/lego v5 自建签发主流程** + provider 适配 | 见 §6 |
| 服务载体 | **平台共享 site-server + 每企业一条通配 Ingress** | 每次发布零 k8s 操作,秒级可达,无 k8s 资源爆炸 |
| 管理面 | 列表 + 手动下线 + 续期 | 公网内容失控时必须能即时下线(安全刚需) |
| v1 provider | cmcccloud / alidns / huaweicloud / tencentcloud | 接口一次性定好,其余增量加 |

### 2.1 为什么不直接集成 certimate
- certimate(`certimate-go/certimate`, MIT)是**独立自托管应用**(PocketBase + SQLite + React UI +
  workflow),对外是给人用的 Admin/REST,不是干净的签证 API——整体当服务跑、再远程驱动其 workflow
  耦合重,**否掉**。
- 但它的 DNS 层本质是 **「go-acme/lego 的 `challenge.Provider` 工厂集合」**,且核心能力在可 import 的
  `pkg/core/` 下。**lego v5 原生支持阿里云 / 华为云 / 腾讯云,但不支持移动云(cmcccloud)**——移动云是
  certimate 自己 fork 中国移动 ecloud SDK 实现的。**这是 certimate 对我们唯一不可替代的价值。**
- 因此:签发主流程自己用 lego;阿里/华为/腾讯用 lego 原生 provider;**移动云 vendor certimate 的
  `pkg/core/certifier/challengers/dns01/cmcccloud` 包**(连同它 fork 的 ecloud SDK + go.mod 的
  `replace` 指令,因为 Go 的 replace 对下游 import 方不生效,必须一并搬入)。

## 3. 端到端链路

```
平台管理员开通企业能力(一次性)
  └─ manager:provider API 建 *.base_domain → ingress 公网 IP 通配解析
            ACME DNS-01 签 *.base_domain 通配证书 → 写 k8s TLS Secret
            建一条通配 Ingress *.base_domain (TLS) → site-server Service
            org_web_publish_config.status = ready

用户对话:"做个 XX 网站并发布"
  └─ hermes 在 /opt/data/workspace/<dir> 产出静态站点
       └─ 调 oc-publish ./<dir> [--slug xxx]
            └─ skill 打包目录 → 经 pod 已有的 manager runtime 通道(同 oc-kb:base + app token)
                 POST manager runtime /apps/<id>/sites
                   └─ manager:校验企业已开通 + 配额未满
                              分配 host = <slug>.base_domain (slug 缺省随机短串)
                              解包上传对象存储 published-sites/<siteID>/...
                              插入 published_sites 记录(expires_at = now + ttl_days)
                              返回 { url, expires_at }
            └─ hermes 告知用户:"已发布:https://<slug>.base_domain,7 天后(YYYY-MM-DD)自动删除。"

公网访问 https://<slug>.base_domain/...
  └─ DNS 通配解析 → ingress 公网 IP
       └─ 通配 Ingress(TLS 用通配证书 Secret) → site-server Service
            └─ site-server:Host 查注册表 → siteID → 从对象存储流式返回文件
```

## 4. 组件设计

### 4.1 企业配置(平台管理员)
- **新表 `org_web_publish_config`**(单独建表,避免把 provider 凭证塞进 `organizations`;所有表/字段带
  SQL COMMENT):
  - `org_id`(PK/FK)、`enabled`、`base_domain`、`dns_provider`(枚举:cmcccloud/alidns/huaweicloud/
    tencentcloud)、`dns_credentials_ciphertext`(复用 `auth.Cipher` 加密)、`site_ttl_days`(默认 7)、
    `max_sites`(默认 20)、`provisioning_status`(disabled/provisioning/ready/failed)、
    `provisioning_message`、`cert_secret_name`、时间戳。
  - **证书状态字段**(供 §8 页面展示给平台管理员与企业管理员):`cert_status`
    (none/issuing/issued/renewing/failed)、`cert_not_after`(到期时间,兼作续期巡检)、
    `cert_last_issued_at`、`cert_last_renewed_at`、`cert_message`(失败原因 / 最近一次结果摘要)。
- **manager 端**:企业配置页新增开关与配置区;开通触发一次性 provisioning(异步,走 worker/状态机,
  失败可重试),前端展示就绪/失败态。
- **权限**:开通/改配置属 `platform_admin`(沿用 `internal/auth/authorizer.go` 既有谓词,不在 handler/
  service 内联角色判断)。
- **前置约束(写入文档,非代码)**:
  - 企业基础域名的 DNS 必须托管在所选 provider,否则 API 写记录不生效。
  - 平台需配置自身 ingress 控制器的公网 IP(通配 A 记录指向它)。
  - 通配 Ingress 的 ingressClassName 跟随环境(本地 traefik / 线上对应 controller)。

### 4.2 hermes 发布能力

**边界**:hermes **不直接跟 site-server 打交道**。site-server 是下游、面向公网访客的服务组件;
hermes 用的是**发布能力**(`oc-publish` skill),site-server 只承接发布之后的公网流量。hermes 侧链路:

```
hermes(terminal 写文件) → oc-publish skill → manager runtime 发布端点 → 传 S3 + 插 DB 行
                                                                        ↓(site-server 轮询同步)
公网访客 ───────────────────────────────────────────────────→ site-server(读 S3 返回)
```

hermes 只管"发布"、拿到一个 URL;至于该 URL 如何经 DNS→Ingress→site-server 落回它传的 S3 前缀,
对 hermes 完全透明。

**`oc-publish` skill 形态**(与现有 `oc-kb` 完全同构,瘦客户端):
- `SKILL.md`:frontmatter(`name`/`description`)+ 用法说明,告诉 agent 这个能力是什么、何时用、怎么调;
  hermes 在对话里判断"用户要发布网站"时据此决定调用。
- 可执行入口(脚本):agent 实际执行的命令。

**调用约定**:
- hermes 先用本就具备的 `terminal.backend: local` 在 `/opt/data/workspace/<dir>` 产出静态产物
  (或跑构建得到 `dist/`)——常规文件/shell 操作,不属新能力。
- 调用:`oc-publish ./<dir> [--slug xxx]`;`--slug` 可省略,manager 缺省分配随机短串。
- skill 内部(对 hermes 透明):打包目录(大目录复用既有分片上传)→ 经 pod 已有的 manager runtime 通道
  (`oc-kb` 同款 base URL + app token 鉴权)`POST` manager 发布端点 → manager 校验开通+配额、分配 host、
  传 S3、插 `published_sites` 行 → 返回 `{ url, expires_at }` → skill 打到 stdout。
- hermes 拿到输出后在对话里回显:`已发布:https://<slug>.<base_domain>,7 天后(YYYY-MM-DD)自动删除。`

**能力的"有/无" = skill 的"注入/不注入"**(落地"不对所有人开放"):
- 由 `render_skills` 按 manifest 的 `web_publish` 段**条件注入**——企业未开通时 manifest 无该段,
  `oc-publish` 不渲染进 hermes,对话里 hermes 压根不知道有发布这回事。
- **manifest 注入**:`internal/integrations/hermes/app_input.go` 在企业开通时往 manifest 写 `web_publish`
  段(base_domain、runtime endpoint、app token)。
- skill/hermes 不持有任何集群级或 provider 凭证,只有 per-app token;鉴权、配额、资源分配全在 manager 侧。

**两个 variant**:`oc-publish` skill 需同时在 `hermes-v2026.6.5` 与 `hermes-v2026.5.16` 渲染器中落地
(随能力开关条件注入)。

### 4.3 site-server(新组件,平台共享)
- 无状态小 Go 服务,平台级单一 Deployment(随 app 一起部署在 apps 命名空间)。
- 职责:
  - 接收来自通配 Ingress 的请求,按 `Host` 头查注册表 → siteID。
  - 未知 / 已下线 / 已过期 Host → 404。
  - 从对象存储 `published-sites/<siteID>/` 流式返回对应文件:目录 / 根路径回退 `index.html`、
    正确 content-type、合理缓存头。
- **注册表**:Host → siteID 映射从 manager 同步,**采用 site-server 主动轮询 manager 内部端点**
  (每 5–10s 拉一次本表活跃站点列表)的方式,而非 manager 推送。理由:实现最简单、无需处理 site-server
  多副本 / 重启补偿;一致性窗口几秒,对"发布后立即访问"足够(站点非毫秒级敏感);manager 重启不影响
  site-server 已缓存的路由。manager 侧提供一个内部(集群内、带鉴权)端点返回 `host → {siteID,
  s3_prefix, status}` 列表。
- **安全**:NetworkPolicy 限制出网只允许到对象存储;资源 limits;只读单一 bucket/前缀;不持有任何
  集群级或其他企业凭证。
- **隔离取舍(记录)**:本期共享单实例。若未来需更强隔离,可演进为每企业独立 site-server pod;接口
  设计不应阻断该演进。

### 4.4 对象存储
- 复用项目既有对象存储(EOS / S3 兼容)。站点产物存 `published-sites/<siteID>/<version>/`(见 §4.5 换版)。
- 回收时删除整个 `published-sites/<siteID>/` 前缀。

### 4.5 站点更新(反复修改 / update-in-place)
支持对同一站点反复迭代:hermes 改完再发,URL 不变。

- **身份 = slug,企业基础域名内稳定**:`oc-publish ./dir --slug blog` 首次创建
  `blog.<base_domain>`;之后对同一 slug 再发布即**原地更新**(同 host、同 URL、同 `published_sites`
  行,只换内容)。hermes "改改重新发"即 `--slug blog` 再跑一次。
- **归属校验**:slug 在企业基础域名内唯一(等价于 host 唯一)。站点归属创建它的 app:
  - 同 `app_id` + 同 slug → 更新(放行)。
  - 不同 app 想占用已存在的 slug → 返回 `slug 已占用` 错误,**不允许跨 app 覆盖**。
- **原子换版**:更新时上传到新版本前缀 `published-sites/<siteID>/<version>/`,**整目录传完**后再把
  DB 行的当前版本指针(`current_version` / `s3_prefix`)切过去;site-server 下次轮询(几秒内)才切到新版本。
  - 目的:避免访客在上传中途看到半更新站点;切换是单行 DB 更新,天然原子。
  - 旧版本前缀在切换后异步 GC(可保留最近 1 个版本用于快速回退,实现细节计划阶段定)。
- **TTL 重置**:每次 republish 把 `expires_at` 重置为 `now + site_ttl_days`——正在迭代的站点不应中途
  过期。(若未来要"修改不续命",改这一条即可。)
- **首发 vs 更新对 hermes 透明**:`oc-publish` 调用形态一致;manager 按 (org, slug, app 归属) 判定是
  创建还是更新,返回同样的 `{ url, expires_at }`。

## 5. 数据模型

### `published_sites`(新表,带 COMMENT)
- `id`(siteID)、`org_id`、`app_id`(归属,update-in-place 校验用)、`host`(唯一)、`slug`、
  `current_version`(当前版本标识)、`s3_prefix`(指向当前版本目录 `published-sites/<siteID>/<version>/`)、
  `status`(active/disabled/expired)、`size_bytes`、`created_at`、`expires_at`、`updated_at`。
- 索引:`host` 唯一(site-server 路由,也即 slug 在企业域内唯一);`(org_id, status)`(列表);
  `expires_at`(reaper 扫描)。
- 一个 slug 一行:反复修改只更新该行(换 `current_version`/`s3_prefix`、刷 `expires_at`/`updated_at`),
  不新增行。

## 6. 证书与 DNS provider 适配层

- **`internal/integrations/dnsprovider/`**:定义统一 `Provider` 接口:
  - DNS-01 challenge:写 / 删 `_acme-challenge` TXT 记录(供 lego 签发回调)。
  - 解析记录:写 / 删通配 A 记录 `*.base_domain → ingress IP`。
  - 四个实现:**alidns / huaweicloud / tencentcloud 用 lego 原生 provider**;**cmcccloud vendor
    certimate 的 `pkg/core/certifier/challengers/dns01/cmcccloud`**(+ fork 的 ecloud SDK +
    go.mod replace,落地前 `grep -ri ecloud` 二次确认 lego v5.2.2 确实无原生移动云)。
- **每企业一张通配证书,manager 全权托管**:每个企业一个基础域名,只维护**一张 `*.base_domain`
  通配证书**。平台管理员配置 provider 凭证(即把该域名的 DNS 权限交给 manager)后,**manager 自行负责
  证书的签发与续签,全程自动、无需任何手工上传或干预**。
- **证书签发**:go-acme/lego v5 自建签发 + 续期主流程;证书写 `kubernetes.io/tls` Secret 供通配
  Ingress 引用。
- **续期**:定时任务在证书到期前自动续签(`cert_not_after` 巡检)。
- **状态持久化并在页面体现**:证书的当前状态(未签发 / 签发中 / 已签发 / 续签中 / 失败)、到期时间、
  最近签发/续签时间、失败原因落 `org_web_publish_config`(见 §4.1),并通过管理面 API 暴露给
  **平台管理员与企业管理员**(见 §8)。

## 7. 生命周期与回收

- **scheduler 新增 reaper job**:扫描 `expires_at < now` 且 active 的记录 → 置 `expired` → 删对象存储
  前缀;site-server 注册表同步后即停服。
- **手动下线**:置 `disabled` → site-server 立即 404 + 删对象存储前缀。
- **续期**:延长 `expires_at`(+ttl_days)。
- 通配证书续期独立于站点生命周期(企业级,§6)。

## 8. 管理面(API + 前端)

- **org admin**:列出本企业已发布站点(URL / 状态 / 到期时间 / 大小)、手动下线、续期;
  **只读查看本企业证书状态**(域名、`cert_status`、到期时间、最近签发/续签时间、失败原因)。
- **平台管理员**:全局视图 + 企业能力开通配置;查看各企业证书状态,并可触发**手动重试签发/续签**
  (失败兜底)。
- **证书状态面板(两角色均可见)**:展示通配域 `*.base_domain`、当前证书状态(未签发/签发中/已签发/
  续签中/失败)、到期时间与最近签发/续签时间、失败原因;让"manager 自动托管证书"这件事在页面上可见、
  可观测。企业管理员只读,平台管理员可重试。
- 新增 manager handler / service / 路由;请求体类型入 `dto.go` 导出大写命名,响应用 `service.XxxResult`;
  改动后跑 `make openapi-gen` + `make web-types-gen` 保持契约同步。

## 9. 安全小结

- 发布站点**公网完全公开、无鉴权**(明确写入用户可见文案与文档)。
- 仅静态产物;失控内容靠手动下线即时生效兜底。
- site-server NetworkPolicy 收敛出网;只读单一前缀;不持集群级凭证。
- provider 凭证加密入库(`auth.Cipher`),不落明文 / 不进 git。

## 10. 待实现计划阶段细化的点

- provisioning 状态机与失败重试的 worker 编排细节。
- 分片上传在 oc-publish 路径上的复用边界。
- cmcccloud vendor 的具体目录布局与 go.mod replace 迁移清单。
- 配额 / 单站大小上限的具体执行点。
- 站点版本 GC / 回退保留策略(保留几个历史版本、何时清理旧版本前缀)。

## 11. 实现拆分建议(供 writing-plans 参考)

1. **DNS provider 适配层 + 证书签发**(`dnsprovider` 接口 + 四 provider + lego 签发/续期 + k8s TLS
   Secret 写入)——相对独立,可先行并单测。
2. **企业能力开通 + provisioning**(`org_web_publish_config` 表 + 平台管理员配置 UI + 一次性建通配
   解析/证书/Ingress 的状态机)。
3. **site-server 组件**(Go 服务 + 镜像 + 部署 + NetworkPolicy + Host 路由 + S3 流式返回)。
4. **发布链路**(`published_sites` 表 + manager runtime 发布端点 + `oc-publish` skill 双 variant +
   manifest `web_publish` 段条件注入)。
5. **生命周期与管理面**(reaper job + 续期 + org/平台管理 API + 前端列表/下线/续期 + OpenAPI 同步)。
