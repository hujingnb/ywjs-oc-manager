# Spec A：hermes 运行时文案接入原生 t() catalog

- 日期：2026-06-24
- 状态：设计已评审，待实现
- 所属拆分：国际化整改三件套之 **A**（A=hermes 运行时文案 / B=后端错误 i18n / C=manager 侧 locale 赋值·传播·实时展示）

## 背景

反馈：「hermes 的系统文案没有按照用户的语言进行输出」。

排查后确认：manager 的语言传参链路是通的——
`users.locale` →（创建实例时快照）`apps.locale` → bootstrap 注入 manifest `app.language`
→ renderer 写入 config.yaml 的 `display.language`。

真正的根因在 hermes 容器内的构建期补丁
`runtime/hermes/hermes-v2026.6.5/patches/patch_i18n_literals.py`：它把一批**未走
`t()` 的英文裸字符串**（关闭/重启通知、超时诊断、provider 错误回包、斜杠命令回复、
技能提示等）在镜像构建期**写死翻译成中文**，与运行期 `display.language` 无关。后果：

- `display.language=zh` 的实例：走 `t()` 的文案是中文、被补丁改过的裸字符串也是中文，一致。
- `display.language=en` 的实例：走 `t()` 的文案是英文、裸字符串仍是中文 → **中英混杂**。

`renderer/render_config_yaml.py:48-50` 的注释已自陈这是「已知局限，需另行改造该补丁」。

本 spec 解决该局限：把这批裸字符串接入 hermes **原生 `t()` i18n**，使其随
`display.language` 输出中/英，en 实例不再混杂。

## 目标与范围

**目标**：hermes 容器内用户可见的裸字符串（现补丁覆盖的约 263 条替换，分布在
`gateway/run.py` 与 `gateway/platforms/base.py`）改为走原生 `t()`，按实例
`display.language` 输出中/英；缺失语言自动回落英文。

**范围内**：现补丁已覆盖的那批字符串（微信渠道路径 + 所有适配器共用的
`base.py`）。

**范围外**：
- 平台专属（Telegram 话题 / Discord 语音 / `/platform` 多平台管理等）休眠渠道的文案。
  产品当前仅微信（`wechat`）一种渠道落地，其余 8 种为前端灰显占位、后端返回
  `CHANNEL_ADAPTER_MISSING`，终端用户看不到这些文案，按 YAGNI 不投入。
- manager 侧 locale 赋值策略（新成员随企业管理员、实例随成员）、语言变更传播、
  实例详情页实时展示当前语言——归 **Spec C**。
- 后端错误消息 i18n（`errors.go` 全中文硬编码、前端不按 code 查表）——归 **Spec B**。

## 关键事实（已在本地运行实例实测确认）

读取本地 hermes pod 内 `/usr/local/lib/hermes-agent/agent/i18n.py` 与 `locales/`
得到以下确定事实，作为方案依据：

1. **`t()` 契约**：`t(key, lang=None, **format_kwargs)`。
   - 按**点分 key** 查 `locales/<lang>.yaml`（YAML 可嵌套，加载时拍平成点分键空间）。
   - 用 `str.format(**format_kwargs)` 注入占位符。
   - 缺 key → 回落 `en.yaml` 同 key；en 也缺 → 返回 key 本身（**永不崩**，仅难看）。
   - `format` 失败（KeyError/IndexError/ValueError）→ 记 warning 并返回未格式化原串（降级不崩）。

2. **语言解析顺序**：`lang=` 显式参数 > `HERMES_LANGUAGE` 环境变量 >
   config.yaml 的 `display.language`（经 `hermes_cli.config.load_config()` 读取）> `"en"`。
   - **`t()` 自己从 config.yaml 读 `display.language`，无需 renderer / manager 任何改动**。
     renderer 已把 `app.language` 写进 `display.language`（`render_config_yaml.py:51`）。
   - 进程内 `@lru_cache` 缓存语言解析结果；`reset_language_cache()` 可在运行期失效
     （Spec C 若做热切语言会用到，本 spec 不涉及）。

3. **现有 catalog**：镜像已自带 16 个语言文件
   （`en/zh/zh-hant/ja/de/es/fr/tr/uk/af/ko/it/ga/pt/ru/hu`.yaml），覆盖 upstream
   **已经**走 `t()` 的那批文案。我们要接入的 263 条裸字符串**不在**这些 catalog 里。
   - 本 spec 只补 **en、zh** 两种；其余 14 种语言对这批 key 自动回落英文。

4. **复杂占位符可处理**：补丁里的 f-string 含 `.format` 无法直接处理的复杂表达式
   （`{len(already)}`、`{platform_name.title()}`、`{'s' if prev != 1 else ''}`、
   `{provider or 'openrouter'}`、`{result['error']}`、`{m!r}` 等）。
   解法：在**调用点求值后作为命名 kwarg 传入**，catalog 里只放简单 `{name}`。
   - 复数 / 语法的中英差异天然解决：en catalog 写 `{s_suffix}`、zh catalog 省略即可，
     `str.format` 忽略未引用的多余 kwarg。

## 方案选型

**方案 1（采用）：接入 hermes 原生 `t()` catalog。**
裸字符串改为 `t("oc.key", kw=expr…)`，中英译文进 `locales/en.yaml`、`zh.yaml`。
完全走 upstream 机制；en 干净；多语言可扩展近乎零成本；译文集中在 YAML、可维护；
语言解析 / 热更新由 `t()` 负责。代价是每处改造非纯机械（起 key、占位符提 kwarg、
写两份 catalog）。

**方案 2（否决）：补丁内联中英条件表达式**
`f"en {x}"` → `(f"中文 {x}" if _lang=="zh" else f"en {x}")`。仅支持中/英二元、
与 upstream 多语言机制重复、中译内联难维护、generated 代码冗长。在已确认 `t()`
可用后无实质优势。

## 架构与组件

### (a) OC catalog 翻译源（新增文件，入 git）

`runtime/hermes/hermes-v2026.6.5/locales/oc_overlay.yaml`——**唯一翻译事实源**，
`oc.*` 命名空间，每条 leaf 中英并排，便于维护与对照：

```yaml
oc:
  run:
    timeout_last_activity:
      en: "Last activity: {last_desc} ({secs_ago:.0f}s ago, iteration {iter_n}/{iter_max})."
      zh: "最近活动:{last_desc}(距今 {secs_ago:.0f} 秒, 迭代 {iter_n}/{iter_max})。"
    provider_auth_failed:
      en: "⚠️ Provider authentication failed. Check the configured credentials; raw provider details are in the gateway logs."
      zh: "⚠️ 模型服务商鉴权失败。请检查所配置的凭证；原始服务商错误详情见 gateway 日志。"
  base:
    # base.py 来源的文案
    ...
```

- 译文直接迁移自现 `patch_i18n_literals.py` 的 `REPLACEMENTS_RUN` / `REPLACEMENTS_BASE`。
- 占位符统一为简单 `{name}`，format spec（如 `:.0f`）留在 catalog 串里。
- 单文件含双语，避免 en/zh 两份漂移。

### (b) 构建期合并脚本（新增）

`runtime/hermes/hermes-v2026.6.5/patches/merge_oc_locales.py`（命名沿用 patch 风格）：
- 读 `oc_overlay.yaml`，把每条的 `en` / `zh` 分别深合并进镜像内 upstream
  `/usr/local/lib/hermes-agent/locales/en.yaml`、`zh.yaml` 的 `oc:` 顶层块。
- **幂等**：已合并（目标已含该 key 且值相同）则跳过。
- **fail-loud 冲突检测**：若 upstream 已存在 `oc` 顶层键、或目标 key 已存在且值不同
  → 抛错中断构建（防止静默覆盖 upstream）。

### (c) 改造 `patch_i18n_literals.py`

从「`str.replace(英文原串 → 中文内联)`」改为「`str.replace(原 f-string 表达式
→ t("oc.key", kw=expr…) 调用)`」：

- **占位符提 kwarg**：
  `f"Last activity: {_last_desc} ({_secs_ago:.0f}s ago, iteration {_iter_n}/{_iter_max})."`
  → `t("oc.run.timeout_last_activity", last_desc=_last_desc, secs_ago=_secs_ago, iter_n=_iter_n, iter_max=_iter_max)`
- **拼接消息按完整逻辑消息重组为单个 key**：现补丁把一条用户可见长消息拆成多个
  片段分别 `str.replace`（因受限于 `str.replace` 只能改内部文本）；迁移到 `t()` 时
  应**对照 upstream 源码把同一条消息合并成一个 key**，而不是逐片段建 key。这是实现期
  最重的活，需逐条阅读 `run.py` / `base.py` 的消息拼装结构。
- **保留现补丁三大约定**：
  - fail-loud：anchor（原英文表达式）在源码中不存在、且替换后形式也不存在 → 抛错中断构建。
  - 幂等：替换后形式已存在 → 跳过。
  - 顺序：当一条 anchor 是另一条的子串时，长串排前，避免子串先命中切出病句。
- 需引入 `from agent.i18n import t`（确认 `run.py` / `base.py` 的 import 区，补丁顺带
  注入 import；若已导入则跳过）。

### (d) 一致性守卫（新增，构建期）

校验 patch 引入的每个 `t("oc.X")` 的 key，在 `oc_overlay.yaml` 中都同时有 `en`
和 `zh`；反之 overlay 里每个 key 都应被 patch 用到。任一不满足 → 中断构建。
防止「patch 用了某 key 但 overlay 没译文」或「overlay 有死译文」的漂移。
实现上可由 `merge_oc_locales.py` 或一个独立小脚本完成（patch 维护一份 key 清单，
或从 patch 的替换表与 overlay 取交集比对）。

## 构建集成（Dockerfile）

在 `runtime/hermes/hermes-v2026.6.5/Dockerfile` 中，patch 应用阶段顺序：

1. `install.sh`（已存在）安装 upstream hermes-agent 到 `/usr/local/lib/hermes-agent/`。
2. **`merge_oc_locales.py`**：合并 OC catalog 进 upstream `en.yaml` / `zh.yaml`。
3. **改造版 `patch_i18n_literals.py`**：裸字符串 → `t("oc.…")`。
4. `patch_api_server_reload.py`（已存在，不变）。
5. **一致性守卫**校验通过。

合并脚本与 patch 全部 fail-loud，任一失败 docker build 中断、不缓存坏层。

## 测试与验证

### 构建期 / 单元
- 镜像构建成功；合并后 `en.yaml` / `zh.yaml` 含全部 `oc.*` key；一致性守卫通过。
- pytest（沿用 `runtime/hermes/hermes-v2026.6.5/tests/`）：
  - `merge_oc_locales` 的深合并、幂等、冲突 fail-loud。
  - key 一致性（patch ↔ overlay）。
  - 对几条代表性 key，构造最小 `en.yaml` / `zh.yaml` + config，断言 `t()` 在
    `display.language=zh` 出中文、`=en` 出英文、缺 key 回落英文。

### 真实对话验证（CLAUDE.md 强制，不可用 curl 替代）
本地 k3d（`make local-up` / `build-hermes-runtime`，注意 memory 记录的 `NO_CACHE=1`
与按版本精确匹配两坑）起两个实例：`display.language=zh` 与 `=en`。经真实微信渠道对话，
逐条触发并截图核对：

- 智能体超时诊断
- provider 鉴权失败 / 请求被拒回包
- `/reset`、关闭 / 重启（drain）通知
- 技能提示、配对流程、后台任务、会话过大等

验收：zh 实例全中文、en 实例全英文，**零中英混杂**；触发未翻 key 时回落英文而非 key 路径。

## 风险与缓解

- **拼接消息重组**（最大风险）：现补丁逐片段替换，迁移到单 key 需逐条对照 upstream
  源码理解消息拼装，漏片段 / 语序错会出病句。缓解：实现期逐条核对 `run.py` /
  `base.py`；真实对话验证逐条触发兜底。
- **upstream 升级 anchor 失配**：与现状同风险，fail-loud 中断构建即暴露；优于现状的是
  译文留在 `oc_overlay.yaml`，升级时不丢，只需重对 anchor。
- **catalog key 冲突**：`oc.` 命名空间规避 + 合并脚本冲突检测。
- **kwarg 类型 / format spec**：如 `{secs_ago:.0f}` 需 float；传错类型 `t()` 记
  warning 返回未格式化串（降级不崩），验证期覆盖代表性条目。
- **构建缓存坑**：沿用 memory「hermes 构建两大坑」——`NO_CACHE=1`、按版本精确匹配
  避免撞陈旧 install.sh 缓存。

## 交付物清单

- 新增 `runtime/hermes/hermes-v2026.6.5/locales/oc_overlay.yaml`
- 新增 `runtime/hermes/hermes-v2026.6.5/patches/merge_oc_locales.py`
- 改造 `runtime/hermes/hermes-v2026.6.5/patches/patch_i18n_literals.py`
- 修改 `runtime/hermes/hermes-v2026.6.5/Dockerfile`（合并步骤 + 顺序 + 守卫）
- 新增 / 更新 `runtime/hermes/hermes-v2026.6.5/tests/` 相关用例
- 更新 `render_config_yaml.py:48-50` 注释（移除「已知局限」描述，改述为已走 catalog）

注：旧的 `hermes-v2026.5.16` variant 是否同步改造，取决于线上是否仍在用该版本；
若需带走补丁，按 memory「hermes 文案中文化：variant 升级需带走补丁」处理（本 spec
以 v2026.6.5 为准，5.16 同步与否在实现前确认）。
