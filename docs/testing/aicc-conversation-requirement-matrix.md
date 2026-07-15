# AICC 客服对话需求与验证矩阵

本矩阵是本轮 AICC 客服能力沙箱、对话、意向线索、无状态运行时和渠道扩展的唯一可追溯来源。每条需求均同时列出正向、拒绝或边界、故障恢复、并发或隔离验证；未实现项不得标记为通过。

## 结果定义

- `BASELINE-FAIL`：已写入期望行为测试，当前实现按预期失败。
- `PENDING`：尚未实现或尚未运行对应验证。
- `PASS`：实现完成，自动化和所需浏览器验证均通过。
- `BLOCKED`：外部依赖或环境阻塞，必须记录原因和风险。

## 需求矩阵

| ID | 需求 | 正向 | 拒绝/边界 | 故障/恢复 | 并发/隔离 | 自动化证据 | 结果 |
|---|---|---|---|---|---|---|---|
| AICC-CAP-001 | AICC 不暴露 terminal、process、execute_code、文件读写、cron、kanban、发布和外部写工具 | `knowledge.read`、`web.search`、Skill 查看，以及仅接收 manager 验证的当前轮次图片理解可用 | 伪造或未声明工具调用返回 `AICC_TOOL_FORBIDDEN`；图片不得开放文件系统或历史附件读取 | policy 缺失、损坏或审计失败时不 Ready | 多 Pod 使用相同不可变工具集合 | `test_aicc_tool_policy.py`、镜像契约 | BASELINE-FAIL |
| AICC-CAP-002 | AICC 不强制枚举全部 Skill，只能选择审核通过的客服 Skill | 白名单 Skill 可被发现和查看 | 普通或未审核 Skill 不可见且不可调用 | manifest 声明越权 capability 时启动失败关闭 | 企业启用项只能缩小镜像上限 | `platform_prompt_test.go`、`test_render_skills.py` | BASELINE-FAIL |
| AICC-CAP-003 | capability 同时经过镜像、Skill、运行时和 manager API 四层校验 | 合法只读检索完成 | 非法工具、伪造 Skill 和 AICC token 写请求均拒绝 | Broker 或 manager 校验不可用时拒绝执行 | 并发请求不能借用其他 Skill 或 app 权限 | `test_aicc_tool_policy.py`、`knowledge_service_test.go` | PENDING |
| AICC-CAP-004 | 普通 Hermes 应用保留现有通用能力 | standard app 保持 terminal、S3 和原有 Skill | AICC 配置不得回退为 standard 配置 | AICC 校验失败不自动降级为普通运行时 | 两类 app 配置和 token 不串用 | Go/Python 回归、部署契约 | PENDING |
| AICC-SRC-001 | 企业知识按客服、企业、授权行业顺序优先回答 | 各层命中时返回正确企业资料 | 无命中、模糊命中和恶意知识内容不被当作事实 | 知识库失败时明确说明，不能伪装为企业事实 | 不同企业和客服检索范围隔离 | 知识 service、RAGFlow 集成、Chrome 问答 | PENDING |
| AICC-SRC-002 | 知识不足时可只读搜索公开网络 | 通用信息附来源链接、标题和检索时间 | 禁止登录、表单、上传、HTTP 写请求、内网和云元数据地址 | 搜索无结果、超时和失效链接安全降级 | 并发搜索不串 URL、缓存或引用 | web tool 单测、SSRF 测试、Chrome | PENDING |
| AICC-SRC-003 | 网络企业信息必须标为未经企业确认 | 企业知识缺失时显示未确认标识和来源 | 不得将公开网页摘要宣称为企业已确认事实 | 来源提取或标识失败时不展示未校验回答 | 多来源引用只关联当前消息 | response validator、Chrome 来源标签 | PENDING |
| AICC-SRC-004 | 企业知识和网络冲突时以企业知识为准 | 回复企业知识并说明重大差异 | 不得用网络覆盖企业价格、政策或承诺 | 冲突判定失败时保守回答无法确认 | 同一会话多轮改问不污染既有来源 | service 单测、评测集、Chrome | PENDING |
| AICC-INT-001 | 仅从访客明确表达提取销售意向画像和消息证据 | 提取产品、场景、预算、周期、企业、行业、角色、顾虑和联系方式 | 不推断年龄、性别、收入等敏感属性；证据不得来自助手或其他会话 | 分析失败不阻断回答，恢复后幂等重试 | 两访客并发不串画像、证据或置信度 | `aicc_intent_test.go`、属性测试 | PENDING |
| AICC-INT-002 | 意向等级可随对话升级、降级、更正和撤回 | low、medium、high 正反例正确更新 | 求职、投诉、售后和媒体咨询不得误判高意向 | 模型输出字段冲突或低置信度时保持空值 | 同会话乱序和重复任务不倒退最新状态 | 意向评测集、任务幂等测试 | PENDING |
| AICC-INT-003 | 高意向只自然邀请一次留资 | 高意向无联系信息时展示邀请和可选卡片 | 拒绝或忽略后不重复追问；中低意向不主动索要 | 邀请任务失败可重试但不重复展示 | 多标签或重复消息只产生一次邀请 | service/API/Chrome 留资旅程 | PENDING |
| AICC-INT-004 | 匿名意向候选可在后续留资时合并正式线索 | 匿名候选可查看会话和证据；提交后按联系方式合并 | 格式错误、跨企业联系方式和重复提交受控处理 | 写入部分失败回滚，不产生孤立重复线索 | 同联系方式跨 session 并发去重 | sqlc/service、Chrome 后台闭环 | PENDING |
| AICC-INT-005 | 显式留资优先于模型推断 | 表单联系方式覆盖模型同字段 | 不覆盖其他有证据的历史画像 | 合并冲突记录审计并可重试 | 多来源更新按幂等键去重 | service 单测、迁移测试 | PENDING |
| AICC-STATE-001 | 首次咨询不被留资表单阻断 | 隐私同意后可直接发消息 | 仅必需隐私同意可阻断；配置 required 只约束提交表单 | 表单组件加载失败不影响基础问答 | 刷新、多标签不创建空会话 | PublicAICC Vitest、Chrome | PENDING |
| AICC-STATE-002 | session 解决状态只由访客明确确认 | `unknown` 可转 `resolved` 或 `unresolved` | 单条回答质量反馈不得改变会话状态 | 状态写入失败显示可重试且不假成功 | 重复点击和并发更新幂等 | `aicc_public_service_test.go`、Chrome | PENDING |
| AICC-STATE-003 | 已确认 session 收到新问题时重置为 unknown | resolved/unresolved 后新消息重新进入待确认 | 非消息操作不得重置状态 | 消息持久化失败不改变状态 | 同 session 顺序在多 worker 下保持 | service/worker 并发测试 | PENDING |
| AICC-STATE-004 | 前端在合适时机询问会话是否解决 | 多轮、结束意图或离开前展示确认动作 | 不在首条、处理中或安全拒绝后强迫确认 | 动作解析失败时继续对话 | 刷新后同一提示状态一致 | response action、Chrome Stable | PENDING |
| AICC-BOOT-001 | 任意全新 Pod 能使用 manager 上下文续聊 | 删除 Pod 后下一轮保持受控摘要和近期消息 | 不读取本地 Hermes session、profile 或长期 memory | bootstrap 失败关闭，恢复后可从 manager 重新启动 | 同 session 落到不同 Pod 不丢上下文 | `aicc_context_test.go`、runtime Chrome | PENDING |
| AICC-BOOT-002 | AICC 启动是幂等的无状态渲染 | 同镜像与 manifest 重复启动得到等价配置 | 半成品目录、未知本地状态均不能影响结果 | 临时渲染失败不留下可被读取半成品 | 多副本同时 bootstrap 不需分布式锁 | entrypoint/renderer 测试 | PENDING |
| AICC-BOOT-003 | AICC 禁用跨 session memory 与 user profile | 当前 session 受 manager 输入控制 | config 中不能启用 memory、profile、workspace session 恢复 | 配置缺失或非法时不 Ready | 访客、客服和企业之间零记忆泄漏 | `test_render_config_yaml.py`、隔离集成 | BASELINE-FAIL |
| AICC-BOOT-004 | AICC 不保存或恢复 S3 运行时数据 | 容器仅使用 bootstrap manifest 与内置 Skill | 无 S3 credential、restore、sync 或 preStop 保存 | bootstrap 凭证失败安全终止 | 扩缩容不依赖共享磁盘或 S3 状态 | k8s render、Pod 生命周期测试 | PENDING |
| AICC-CH-001 | 输入轮次与响应信封渠道无关 | web、widget 通过 adapter 交给同一服务 | 渠道不可伪造内部 capability 或动作 | adapter 失败返回渠道安全错误 | 两渠道 session/token 不串用 | `aicc_channel_test.go`、契约测试 | PENDING |
| AICC-CH-002 | 留资邀请和解决确认以结构化动作交付 | web 渲染卡片/确认按钮 | 动作不能由模型任意伪造或绕过策略 | 解析失败使用安全文本兜底 | 同动作幂等消费一次 | response action、Vue Vitest | PENDING |
| AICC-CH-003 | 预留语音客服 adapter，不提前绑定供应商 | mock ASR/TTS 与打断契约可通过 | 语音不得获得额外工具、记忆或隐私权限 | ASR/TTS/通话失败保留会话一致性 | 语音和网页并发会话隔离 | voice adapter mock、契约测试 | PENDING |
| AICC-E2E-001 | 公开页和挂件在 Chrome Stable 中完成企业问答与来源展示 | 知识命中、网络补充、来源标签可见 | 操作请求和提示词攻击被拒绝 | 检索、模型、网络失败可恢复 | 两个独立 browser context 不串信息 | `aicc-conversation-security.spec.ts`（`chrome-headed`） | BLOCKED：RAGFlow CrashLoopBackOff；知识单层/组合/冲突/网络来源另需 `OCM_AICC_KNOWLEDGE_FIXTURE=1` 的固定三层语料，当前 seed-e2e 未提供。 |
| AICC-E2E-002 | Chrome Stable 验证意向、匿名候选、留资合并和后台证据 | 高意向留资卡、匿名候选、正式线索和证据可见 | 拒绝留资后仍可问答且不重复邀请 | 分析失败恢复后不重复建线索 | 多标签并发提交不重复 | `aicc-conversation-intent.spec.ts`（`chrome-headed`） | BLOCKED：同一 runtime 前置不可用；测试已实现但未将 skip 伪记为通过。 |
| AICC-E2E-003 | Chrome Stable 验证 session 状态、刷新、重试、移动端和挂件 | 状态流转、刷新恢复、移动端无横向溢出 | 未确认不计为未解决；控制台无内部错误 | 删除 Pod 后可续聊，失败回复可重试 | 多浏览器 context 与多 Pod 会话隔离 | `aicc-conversation-runtime.spec.ts`（`chrome-headed`） | BLOCKED：RAGFlow/runtime 不可用；四类故障恢复另需可控的一次性 `OCM_AICC_FAULT_INJECTION=1` injector，当前本地未提供。 |

## Task 1 失败基线

本节只记录当前实现与目标客服约束的差异；Task 1 不修改生产实现使其通过。基线命令的实际输出在执行后补充到下表。

| 命令 | 对应 ID | 当前预期差异 | 实际结果 |
|---|---|---|---|
| `go test ./internal/config -run TestPlatformPrompts_Invariants -count=1` | AICC-CAP-002 | AICC 提示词缺少审核客服 Skill 白名单，并仍强制调用 `skills_list` 和回退通用能力 | `FAIL`：`TestPlatformPrompts_Invariants/AICC` 缺少两项白名单规则，并仍含强制 `skills_list` 及两项通用能力回退表述；退出码 1。 |
| `pytest -q runtime/hermes/hermes-aicc/tests/test_render_config_yaml.py` | AICC-CAP-001、AICC-BOOT-003 | config 仍包含 terminal、approvals，且 memory 和 user profile 均为 true | `FAIL`：10 项中 4 项失败，分别检测到 `terminal`、`approvals`、`memory_enabled=true` 和 `user_profile_enabled=true`；退出码 1。 |
