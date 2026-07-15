# AICC 客服对话、能力沙箱与全面验证 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 保留 Hermes 与客服 Skill 生态，同时将 AICC 收紧为只读知识/网络客服，并完成可解释意向线索、无状态续聊、渠道接口和全场景真实浏览器验收。

**Architecture:** `hermes-aicc` 镜像只向 api_server 注册客服白名单 toolset，并以内置执行守卫做第二次工具名校验；manager runtime API 再按 `app_type=aicc` 限制写能力。manager 数据库保存消息、摘要、来源、意向和状态，每轮向任意 AICC Pod 发送完整受控上下文，runtime 返回渠道无关响应信封。

**Tech Stack:** Go 1.22+、Gin、sqlc、MySQL、Python 3.13、Hermes tool registry、RAGFlow、Vue 3、TypeScript、Naive UI、Vitest、Playwright、本地 Chrome Stable、k3d/Kubernetes。

---

## Scope decomposition

该设计包含四个有依赖关系、可分别验收的阶段：

1. AICC runtime 能力沙箱与无状态安全基线。
2. 渠道无关回复信封、知识/网络来源和 manager 上下文。
3. 意向画像、非阻断留资、session 状态和管理端交互。
4. 全场景矩阵、故障注入、Chrome E2E 和最终报告。

必须按顺序执行；每个任务独立提交。现有
`docs/superpowers/plans/2026-07-14-aicc-stateless-runtime-app-type.md` 是用户未跟踪文件，
不得覆盖或纳入本计划提交。当前 `app_type=aicc`、`oc-bootstrap` 和无 `s3-sync` Pod
已落地，本计划只补幂等渲染、上下文真相源和客服能力裁剪。

## File structure

- `runtime/hermes/hermes-aicc/aicc_tools/`：知识工具与不可变工具执行策略。
- `runtime/hermes/hermes-aicc/skills/aicc-*/SKILL.md`：镜像内置客服 Skill。
- `runtime/hermes/hermes-aicc/patches/patch_aicc_tool_policy.py`：把执行守卫接入上游 Hermes。
- `internal/service/aicc_channel.go`：渠道无关输入轮次和响应信封。
- `internal/service/aicc_context.go`：从 manager 数据构建受限多轮上下文。
- `internal/service/aicc_response.go`：解析、校验回复来源与下一步动作。
- `internal/service/aicc_intent.go`：意向分析、邀请状态和显式字段合并。
- `internal/migrations/000037_aicc_conversation_intelligence.*.sql`：上下文、来源和意向表。
- `docs/testing/aicc-conversation-requirement-matrix.md`：本轮需求与测试唯一映射。
- `web/tests/e2e/aicc-conversation-*.spec.ts`：Chrome 真实业务旅程。

### Task 1: 建立本轮可追溯矩阵与失败基线

**Files:**
- Create: `docs/testing/aicc-conversation-requirement-matrix.md`
- Modify: `docs/testing/aicc-change-coverage.md`
- Modify: `internal/config/platform_prompt_test.go`
- Modify: `runtime/hermes/hermes-aicc/tests/test_render_config_yaml.py`

- [ ] **Step 1: 写入硬门槛矩阵**

矩阵逐行使用 `AICC-CAP-*`、`AICC-SRC-*`、`AICC-INT-*`、`AICC-STATE-*`、
`AICC-BOOT-*`、`AICC-CH-*` 和 `AICC-E2E-*`，每行包含需求、正向、拒绝、边界、
故障、恢复、并发、测试文件和结果列。首批行必须明确：

```markdown
| ID | 需求 | 正向 | 拒绝/边界 | 故障/恢复 | 自动化证据 | 结果 |
|---|---|---|---|---|---|---|
| AICC-CAP-001 | AICC 不暴露 terminal/process/execute_code | knowledge/web 可调用 | 伪造工具调用被拒绝 | policy 缺失时不 Ready | test_aicc_tool_policy.py | 未执行 |
| AICC-BOOT-001 | 任意新 Pod 可续聊 | Pod 删除后下一轮保留上下文 | 不读取本地 session | bootstrap 失败关闭 | aicc-conversation-runtime.spec.ts | 未执行 |
```

- [ ] **Step 2: 固化当前不安全行为的失败测试**

在 `platform_prompt_test.go` 断言 AICC 不含 `skills_list` 强制规则；在
`test_render_config_yaml.py` 断言 AICC config 不含 `terminal`、`approvals`，且
`memory_enabled`、`user_profile_enabled` 均为 false。

- [ ] **Step 3: 运行基线并确认失败**

Run:
```bash
go test ./internal/config -run TestPlatformPrompts_Invariants -count=1
pytest -q runtime/hermes/hermes-aicc/tests/test_render_config_yaml.py
```

Expected: 两组测试均因当前提示词和 config 仍开放通用能力而 FAIL。

- [ ] **Step 4: 记录基线而不修改实现**

把失败命令、实际差异和对应矩阵 ID 写入矩阵“基线”段；不得把结果改成通过。

- [ ] **Step 5: 提交矩阵与失败测试**

```bash
git add docs/testing/aicc-conversation-requirement-matrix.md docs/testing/aicc-change-coverage.md internal/config/platform_prompt_test.go runtime/hermes/hermes-aicc/tests/test_render_config_yaml.py
git commit -m "test(aicc): 建立客服对话全场景验证基线" -m "以需求 ID 映射能力、来源、意向、状态、重启和浏览器场景，并固化当前开放通用工具的失败表现。"
```

### Task 2: 定义并执行 AICC 工具白名单

**Files:**
- Create: `runtime/hermes/hermes-aicc/aicc_tools/policy.py`
- Create: `runtime/hermes/hermes-aicc/aicc_tools/aicc_knowledge_tool.py`
- Create: `runtime/hermes/hermes-aicc/patches/patch_aicc_tool_policy.py`
- Create: `runtime/hermes/hermes-aicc/tests/test_aicc_tool_policy.py`
- Modify: `runtime/hermes/hermes-aicc/Dockerfile`
- Modify: `runtime/hermes/hermes-aicc/lib/manifest.py`
- Modify: `internal/service/bootstrap_service.go`
- Modify: `internal/service/bootstrap_service_test.go`

- [ ] **Step 1: 写 policy 失败测试**

```python
# 客服镜像只允许知识、只读网络、只读 Skill 查看、视觉和澄清。
ALLOWED = {"aicc_knowledge_search", "web_search", "web_extract",
           "skills_list", "skill_view", "vision_analyze", "clarify"}

def test_policy_rejects_every_non_allowlisted_tool():
    from aicc_tools.policy import authorize, filter_definitions
    all_definitions = [
        {"function": {"name": name}}
        for name in sorted(ALLOWED | {"terminal", "skill_manage"})
    ]
    assert authorize("web_search") is None
    assert [item["function"]["name"] for item in filter_definitions(all_definitions)] == sorted(ALLOWED)
    for name in ["terminal", "process", "execute_code", "read_file",
                 "write_file", "skill_manage", "browser_click", "cronjob"]:
        with pytest.raises(PermissionError, match="AICC_TOOL_FORBIDDEN"):
            authorize(name)
```

- [ ] **Step 2: 运行并确认模块缺失**

Run: `PYTHONPATH=runtime/hermes/hermes-aicc pytest -q runtime/hermes/hermes-aicc/tests/test_aicc_tool_policy.py`

Expected: FAIL with `ModuleNotFoundError: aicc_tools`。

- [ ] **Step 3: 实现不可变策略和知识工具**

`policy.py` 暴露不可变 `frozenset`、`filter_definitions(items)` 和
`authorize(name)`。AICC bootstrap manifest 固定下发
`capabilities: ["knowledge.read", "web.search", "skills.read", "vision.read"]`，manifest
解析器拒绝未知值；policy 将 capability 映射为具体工具名并只取镜像上限交集。知识工具使用
`OC_KB_RUNTIME_BASE_URL`、`OC_KB_APP_TOKEN` 向
`POST /api/v1/runtime/knowledge/search` 发送 `{"question": ..., "top_k": 8}`，只返回
JSON，不接受 URL、dataset ID 或写入参数；用 Hermes `registry.register` 注册到
`toolset="aicc"`。

- [ ] **Step 4: 构建期补丁同时守住定义与执行**

`patch_aicc_tool_policy.py` 用稳定锚点在上游 `model_tools.py`：

```python
from aicc_tools.policy import authorize as authorize_aicc_tool
```

在 `registry.get_definitions` 后调用 `filter_definitions`，确保模型看不到
`skill_manage/terminal`；并在工具 dispatcher 进入 handler 前调用
`authorize_aicc_tool(function_name)`。补丁必须检查两个锚点各恰好替换一次，否则非零退出。
Dockerfile COPY `aicc_tools` 到
`/usr/local/lib/hermes-agent/aicc_tools`、知识工具到 `tools/`，然后执行补丁。

- [ ] **Step 5: 运行 runtime 测试并提交**

```bash
PYTHONPATH=runtime/hermes/hermes-aicc pytest -q runtime/hermes/hermes-aicc/tests/test_aicc_tool_policy.py
make aicc-runtime-inject-contract
git add runtime/hermes/hermes-aicc
git commit -m "feat(aicc): 内置客服工具能力白名单" -m "只向客服开放知识检索、只读网络、Skill 查看和视觉工具，并在实际 dispatcher 再次拒绝伪造调用。"
```

### Task 3: 裁剪客服镜像、配置与内置 Skill

**Files:**
- Create: `runtime/hermes/hermes-aicc/skills/aicc-customer-answer/SKILL.md`
- Create: `runtime/hermes/hermes-aicc/skills/aicc-safe-web-research/SKILL.md`
- Create: `runtime/hermes/hermes-aicc/skills/aicc-lead-analysis/SKILL.md`
- Modify: `runtime/hermes/hermes-aicc/Dockerfile`
- Modify: `runtime/hermes/hermes-aicc/renderer/render_config_yaml.py`
- Modify: `runtime/hermes/hermes-aicc/renderer/render_skills.py`
- Modify: `runtime/hermes/hermes-aicc/renderer/render_soul_md.py`
- Modify: `internal/config/platform_prompt.go`
- Test: `runtime/hermes/hermes-aicc/tests/test_render_*.py`

- [ ] **Step 1: 写镜像内容与 config 失败测试**

断言 `platform_toolsets.api_server == ["aicc", "web", "skills", "vision"]`，但实际工具集合
经 Task 2 policy 收紧；断言 memory false、无 terminal/approvals/web_publish，Skill 目录只含
三个 `aicc-*`，知识 Skill 不出现 `oc-kb add` 或 `execute_code`。

- [ ] **Step 2: 运行并确认失败**

Run: `pytest -q runtime/hermes/hermes-aicc/tests/test_render_config_yaml.py runtime/hermes/hermes-aicc/tests/test_render_skills.py runtime/hermes/hermes-aicc/tests/test_render_soul_md.py`

Expected: FAIL，报告 terminal、memory、publish 和通用 Skill 仍存在。

- [ ] **Step 3: 实现客服专用渲染**

配置只写模型、`platform_toolsets`、`web.backend: ddgs`、关闭 memory/user profile；
Dockerfile 安装 `ddgs`，删除上游通用 Skill 目录后复制三个只读 Skill。AICC
`render_skills.py` 不再解压 bootstrap skills、不生成 publish/add 指引；SOUL.md 删除
`oc-kb add` 和“其它信息源”开放表述。三个 Skill frontmatter 分别声明
`aicc_capabilities: [knowledge.read]`、`[web.search]` 和 `[]`，启动校验拒绝 Skill
声明超出 manifest capabilities。

- [ ] **Step 4: 收紧平台提示词**

`DefaultAICCSystemPromptTemplate` 明确企业知识优先、网络企业信息未经确认、只可讲解企业
产品步骤、不可执行操作；删除“调用 skills_list 检查所有技能”。

- [ ] **Step 5: 运行并提交**

```bash
go test ./internal/config -count=1
pytest -q runtime/hermes/hermes-aicc/tests
git add internal/config/platform_prompt.go internal/config/platform_prompt_test.go runtime/hermes/hermes-aicc
git commit -m "feat(aicc): 将运行时裁剪为客服专用镜像" -m "移除终端、发布、持久记忆和通用 Skill，只保留客服问答、网络研究与意向分析能力。"
```

### Task 4: manager 服务端拒绝 AICC 写能力并限制网络出口

**Files:**
- Modify: `internal/service/knowledge_service.go`
- Modify: `internal/service/knowledge_service_test.go`
- Modify: `internal/api/handlers/runtime_knowledge_test.go`
- Modify: `internal/integrations/k8sorch/render.go`
- Modify: `internal/integrations/k8sorch/render_test.go`
- Modify: `internal/integrations/k8sorch/testdata/deployment-aicc.golden.yaml`

- [ ] **Step 1: 写 AICC token 写入拒绝测试**

在 `knowledge_service_test.go` 用 `AppTypeAICC` app token 调用 `RuntimeAddFile`，断言
`require.ErrorIs(t, err, ErrAICCOperationForbidden)` 且 RAGFlow upload 调用数为 0；standard
token 保持成功。

- [ ] **Step 2: 写 Pod 安全上下文和出口测试**

断言 AICC Deployment 无 AWS/S3/publish 环境变量，`readOnlyRootFilesystem=true`、
`allowPrivilegeEscalation=false`、drop ALL capabilities，且只注入知识 API、模型和 web
backend 所需配置。

- [ ] **Step 3: 实现服务端类型检查**

`RuntimeAddFile` 解析 token 后若 `domain.IsAICCAppType(...)` 立即返回新定义的
`ErrAICCOperationForbidden`；不能依赖 handler 路由隐藏。RuntimeSearch 保持可用。

- [ ] **Step 4: 更新渲染和 golden 后验证**

Run:
```bash
go test ./internal/service ./internal/api/handlers ./internal/integrations/k8sorch -run 'Runtime(Knowledge|Add)|AICC' -count=1
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/service/knowledge_service* internal/api/handlers/runtime_knowledge_test.go internal/integrations/k8sorch
git commit -m "fix(aicc): 服务端拒绝客服运行时写操作" -m "即使容器内能力策略被绕过，AICC token 仍不能写知识库，并以受限容器安全上下文运行。"
```

### Task 5: 增加会话上下文、来源和意向持久化

**Files:**
- Create: `internal/migrations/000037_aicc_conversation_intelligence.up.sql`
- Create: `internal/migrations/000037_aicc_conversation_intelligence.down.sql`
- Modify: `internal/migrations/migrations_test.go`
- Modify: `internal/store/queries/aicc.sql`
- Generate: `internal/store/sqlc/aicc.sql.go`, `internal/store/sqlc/models.go`
- Test: `internal/store/sqlc/aicc_message_tasks_query_test.go`

- [ ] **Step 1: 写迁移守护测试**

断言迁移创建：

```sql
aicc_session_contexts(session_id UNIQUE, summary, summarized_through_message_id, summary_version)
aicc_message_sources(message_id, source_type, title, url, scope, reference_id, unconfirmed, retrieved_at)
aicc_session_intents(session_id UNIQUE, intent_level, fields_json, confidence_json,
                     evidence_json, analyzer_version, analyzed_message_id, invite_status)
```

并为 level、source type、invite status 添加 CHECK，为 session/message 添加级联外键。

- [ ] **Step 2: 运行迁移测试确认失败**

Run: `go test ./internal/migrations -run TestAICCConversationIntelligenceMigration -count=1`

Expected: FAIL，迁移文件不存在。

- [ ] **Step 3: 实现迁移和 sqlc 查询**

新增 `Get/UpsertAICCSessionContext`、`ListAICCContextMessages`、
`CreateAICCMessageSource`、`ListAICCMessageSources`、
`Get/UpsertAICCSessionIntent`、`ListAICCAnonymousIntentCandidates`。所有列表按
`created_at,id` 稳定排序。

- [ ] **Step 4: 生成并测试**

```bash
make sqlc-generate
go test ./internal/migrations ./internal/store/sqlc -count=1
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/migrations/000037_aicc_conversation_intelligence.* internal/migrations/migrations_test.go internal/store
git commit -m "feat(aicc): 持久化对话上下文来源与意向画像" -m "以 manager 数据库承载无状态续聊摘要、回复来源和匿名意向候选的唯一事实。"
```

### Task 6: 建立渠道无关轮次与无状态 Hermes 调用

**Files:**
- Create: `internal/service/aicc_channel.go`
- Create: `internal/service/aicc_context.go`
- Create: `internal/service/aicc_context_test.go`
- Modify: `internal/service/aicc_public_chat.go`
- Modify: `internal/service/aicc_public_chat_test.go`
- Modify: `internal/service/aicc_dispatcher.go`
- Modify: `internal/service/aicc_dispatcher_test.go`

- [ ] **Step 1: 写类型和上下文失败测试**

定义 `AICCInboundTurn`（TurnID、SessionID、Channel、Text、Locale、OccurredAt、Context）、
`AICCResponseSource`（Type、Title、URL、Scope、ReferenceID、Unconfirmed）和
`AICCResponseEnvelope`（Text、Sources、NextAction、Refusal、Fallback、AuditRef）。
测试仅选择最近 12 条原始消息，旧内容使用 `aicc_session_contexts.summary`，总字符不超过
12000，且只读取当前 session。

- [ ] **Step 2: 运行并确认类型不存在**

Run: `go test ./internal/service -run 'AICC(Context|InboundTurn)' -count=1`

Expected: FAIL with undefined types/functions。

- [ ] **Step 3: 实现上下文构建器**

`BuildAICCConversationContext` 使用 store 提供的摘要与稳定排序消息；访客文本使用明确 XML
边界标记，不把历史访客内容拼成 system instruction。超过限制时从最老消息开始裁剪。

- [ ] **Step 4: 改造 ChatAICC**

`AICCHermesChat.ChatAICC(ctx, turn)` 每个 TurnID 创建独立临时 Hermes session，prompt
包含 manager 上下文；不再按 `AICC <sessionID>` 查找或复用本地 session。dispatcher 在
租约内读取上下文并传递 `task.MessageID` 作为 TurnID。

- [ ] **Step 5: 测试 Pod 无本地历史语义并提交**

```bash
go test ./internal/service -run 'AICC(Context|PublicHermesChat|Dispatcher)' -count=1
git add internal/service/aicc_channel.go internal/service/aicc_context* internal/service/aicc_public_chat* internal/service/aicc_dispatcher*
git commit -m "refactor(aicc): 由 manager 提供无状态对话轮次" -m "每轮使用独立 Hermes session 和数据库上下文，任意新副本均可继续同一公开会话。"
```

### Task 7: 返回并校验带来源的响应信封

**Files:**
- Create: `internal/service/aicc_response.go`
- Create: `internal/service/aicc_response_test.go`
- Modify: `internal/service/aicc_dispatcher.go`
- Modify: `internal/service/aicc_types.go`
- Modify: `internal/store/aicc_public_runner.go`

- [ ] **Step 1: 写解析与政策失败测试**

覆盖知识来源、公开网络、企业网络未确认、冲突、伪造 URL、无来源企业价格、操作完成声称和
非法 next action。企业网络来源缺 `unconfirmed=true` 必须返回
`ErrAICCResponsePolicy`。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/service -run AICCResponseEnvelope -count=1`

Expected: FAIL，解析器不存在。

- [ ] **Step 3: 实现严格 JSON 响应**

Hermes 最终回复必须为 `{"text":"","sources":[],"next_action":"none|offer_lead|ask_resolution","flags":{}}`。
`ParseAndValidateAICCResponse` 限制文本/来源数量和 URL scheme，只接受本轮工具审计中出现的
reference ID；第一次失败时 dispatcher 发送固定重写 prompt，第二次失败保存安全兜底。

- [ ] **Step 4: 在同一事务保存回复、来源和任务终态**

扩展 dispatcher tx store，把 assistant message、`aicc_message_sources` 和 completed 放在
一个事务；租约丢失时三者全部回滚。

- [ ] **Step 5: 测试并提交**

```bash
go test ./internal/service ./internal/store/sqlc -run 'AICC(Response|Dispatcher|MessageSource)' -count=1
git add internal/service/aicc_response* internal/service/aicc_dispatcher* internal/service/aicc_types.go internal/store
git commit -m "feat(aicc): 返回可校验的客服回复与来源" -m "回复、来源和下一步动作使用结构化信封，企业网络信息必须标记未经确认且与工具审计一致。"
```

### Task 8: 实现意向分析、匿名候选和一次性邀请

**Files:**
- Create: `internal/service/aicc_intent.go`
- Create: `internal/service/aicc_intent_test.go`
- Modify: `internal/service/aicc_dispatcher.go`
- Modify: `internal/service/aicc_service.go`
- Modify: `internal/service/aicc_types.go`
- Modify: `internal/store/queries/aicc.sql`

- [ ] **Step 1: 写业务状态失败测试**

覆盖 low/medium/high、字段证据归属、敏感字段拒绝、显式字段优先、high 首次
`not_invited→invited`、拒绝后不再邀请、提交后关联 `aicc_leads`、同一消息重复分析幂等。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/service -run 'AICCIntent|AICCLeadInvite' -count=1`

Expected: FAIL，intent service 不存在。

- [ ] **Step 3: 实现隔离分析调用和校验**

dispatcher 在回答前用独立 Hermes turn 调用 `aicc-lead-analysis`，只给当前 session 文本；
解析 level、fields、confidence、evidence，拒绝证据不属于访客消息或字段不在固定白名单的
输出。失败只记录，不阻断主回复。

- [ ] **Step 4: 合并留资**

`SubmitLeadValues` 不再作为发送 gate；提交时更新 `invite_status=submitted`，按现有 contact
hash upsert 正式 lead，并保留其它有证据的意向字段。拒绝接口只把当前 session 更新为
`declined`。

- [ ] **Step 5: 测试并提交**

```bash
go test ./internal/service ./internal/store/sqlc -run 'AICC(Intent|Lead)' -count=1
git add internal/service/aicc_intent* internal/service/aicc_dispatcher* internal/service/aicc_service.go internal/service/aicc_types.go internal/store
git commit -m "feat(aicc): 从自然对话生成可解释意向线索" -m "高意向会话形成匿名候选并只邀请一次，显式联系方式提交后再合并正式线索。"
```

### Task 9: 修正公开 API、session 状态与 OpenAPI

**Files:**
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/api/handlers/public_aicc.go`
- Modify: `internal/api/handlers/public_aicc_test.go`
- Modify: `internal/service/aicc_public_service.go`
- Modify: `internal/service/aicc_public_service_test.go`
- Generate: `openapi/openapi.yaml`, `web/src/api/generated.ts`

- [ ] **Step 1: 写状态机和去反馈失败测试**

断言普通消息不受 required lead field 阻断；删除公开 feedback 路由；第二条非拒答后返回
`next_action=ask_resolution`；`resolved/unresolved` 收到新访客消息时原子重置
`unknown`。

- [ ] **Step 2: 运行确认当前行为失败**

Run: `go test ./internal/service ./internal/api/handlers -run 'AICC.*(LeadRequired|Feedback|Resolution)' -count=1`

Expected: FAIL，当前仍有 feedback 路由和留资 gate。

- [ ] **Step 3: 实现 API**

移除 `SubmitFeedback` 公开接口与路由；增加
`POST /sessions/:sessionToken/lead-invitation/decline`；消息状态结果携带 sources 与
next_action。发送新消息事务内将已确认 session 重置 unknown。

- [ ] **Step 4: 生成契约并验证**

```bash
make openapi-gen
make web-types-gen
make openapi-check
go test ./internal/api/handlers ./internal/service -run AICC -count=1
```

Expected: PASS，生成文件与注解同步。

- [ ] **Step 5: 提交**

```bash
git add internal/api/handlers internal/service/aicc_public_service* openapi/openapi.yaml web/src/api/generated.ts
git commit -m "feat(aicc): 改为非阻断留资与显式会话解决状态" -m "移除单条回复反馈，新增拒绝留资动作，并在新问题进入时重置已确认会话状态。"
```

### Task 10: 更新访客页与意向后台

**Files:**
- Modify: `web/src/domain/aicc.ts`
- Modify: `web/src/api/hooks/useAICC.ts`
- Modify: `web/src/pages/aicc/PublicAICCChatPage.vue`
- Modify: `web/src/pages/aicc/PublicAICCChatPage.spec.ts`
- Modify: `web/src/pages/aicc/AICCLeadsPage.vue`
- Modify: `web/src/pages/aicc/AICCLeadsPage.spec.ts`
- Modify: `web/src/i18n/locales/zh/aicc.ts`
- Modify: `web/src/i18n/locales/en/aicc.ts`

- [ ] **Step 1: 写组件失败测试**

覆盖初始输入可用、source 标签、未确认网络标记、offer_lead 卡片、暂时不用、第二条回复后的
解决卡片、继续提问收起、匿名候选证据和移动端无横向溢出。

- [ ] **Step 2: 运行确认失败**

Run: `cd web && npm test -- --run src/pages/aicc/PublicAICCChatPage.spec.ts src/pages/aicc/AICCLeadsPage.spec.ts`

Expected: FAIL，当前页面仍前置 lead gate 且无来源/匿名候选。

- [ ] **Step 3: 实现公开页**

按响应信封渲染来源和 next action；页头使用次要“结束本次咨询”；不再渲染单条 feedback。
留资表单只在 `offer_lead` 后展开，decline 后继续输入。

- [ ] **Step 4: 实现后台**

线索页分“匿名意向候选”和“联系人线索”，显示 level、产品、预算、采购时间、顾虑和可点击
证据；不把推断字段伪装为访客填写。

- [ ] **Step 5: 测试并提交**

```bash
cd web && npm test -- --run src/pages/aicc/PublicAICCChatPage.spec.ts src/pages/aicc/AICCLeadsPage.spec.ts
cd ..
git add web/src
git commit -m "feat(aicc): 优化客服留资来源与会话结果交互" -m "访客先自由咨询，高意向后再选择留资；后台区分匿名候选、显式联系人和对应证据。"
```

### Task 11: 固化幂等启动与语音 adapter 出口

**Files:**
- Modify: `runtime/hermes/hermes-aicc/oc-entrypoint.py`
- Modify: `runtime/hermes/hermes-aicc/tests/test_entrypoint_integration.py`
- Modify: `runtime/hermes/hermes-aicc/CONTRACT.md`
- Create: `internal/service/aicc_channel_test.go`

- [ ] **Step 1: 写脏目录和 channel 契约失败测试**

entrypoint 测试覆盖空目录、重复启动、完整残留、半成品和非法 policy；channel 测试覆盖
web_link/web_widget/mock voice 归一化、未知渠道拒绝，以及 `offer_lead` 映射不改变 capability。

- [ ] **Step 2: 运行确认失败**

```bash
pytest -q runtime/hermes/hermes-aicc/tests/test_entrypoint_integration.py
go test ./internal/service -run AICCChannel -count=1
```

Expected: 至少半成品原子替换和 channel adapter 用例 FAIL。

- [ ] **Step 3: 实现幂等 staging**

每次删除受管 staging，渲染到 `/opt/data/.aicc-render-<pid>`，完整校验后用原子 rename 替换
config/SOUL/skills；不读取 migrator 或 `.oc-state.json` 决定行为。失败清 staging 并退出 1。

- [ ] **Step 4: 实现 mock voice adapter**

adapter 只把 transcript 映射为 `AICCInboundTurn`，把 `text/sources/next_action` 映射为 mock
事件；不增加 ASR、TTS、音频字段或供应商配置。

- [ ] **Step 5: 测试并提交**

```bash
pytest -q runtime/hermes/hermes-aicc/tests
go test ./internal/service -run AICCChannel -count=1
git add runtime/hermes/hermes-aicc internal/service/aicc_channel*
git commit -m "feat(aicc): 保证客服镜像幂等启动并预留语音适配" -m "运行时从任意空或脏目录确定性重建，渠道响应保持与网页组件和语音供应商解耦。"
```

### Task 12: 完成全场景 Chrome 验收与报告

**Files:**
- Create: `web/tests/e2e/aicc-conversation-security.spec.ts`
- Create: `web/tests/e2e/aicc-conversation-intent.spec.ts`
- Create: `web/tests/e2e/aicc-conversation-runtime.spec.ts`
- Modify: `web/playwright.config.ts`
- Modify: `web/tests/e2e/aicc/helpers.ts`
- Modify: `docs/testing/aicc-conversation-requirement-matrix.md`
- Create: `docs/testing/aicc-conversation-validation-report.md`

- [ ] **Step 1: 配置本地 Chrome Stable headed 项目**

`playwright.config.ts` 新增 `chrome-headed` project：`channel: "chrome"`、
`headless: false`、trace/video/screenshot on-first-retry；保留 Chromium 快速项目。

- [ ] **Step 2: 实现安全、来源和隔离 E2E**

真实页面覆盖知识单/组合/冲突、网络未确认、命令/文件/建站/登录/多轮注入、两个
BrowserContext 跨访客隔离、域名/隐私/限流/图片/token。所有拒绝场景断言无未授权工具审计。

- [ ] **Step 3: 实现意向和状态 E2E**

覆盖 low/medium/high、误判负例、升级/降级、一次邀请、拒绝、匿名候选、后续实名合并、
字段证据、解决/未解决/新消息重置、中英文和移动挂件。

- [ ] **Step 4: 实现重启、故障与三轮运行**

通过页面完成首轮后删除本地 `oc-aicc` Pod，等待 Ready 后继续提问；注入 RAGFlow、搜索、
模型超时和队列失败并验证恢复。执行：

```bash
cd web
kubectl config use-context k3d-ocm
for run in 1 2 3; do
  npx playwright test --project=chrome-headed tests/e2e/aicc-conversation-*.spec.ts
done
```

Expected: 三轮全部 PASS，控制台无未处理错误，未授权操作和跨租户泄漏均为 0。

- [ ] **Step 5: 运行全量验证并提交报告**

```bash
go test ./internal/... ./cmd/server -count=1
pytest -q runtime/hermes/hermes-aicc/tests
cd web && npm test -- --run && npm run typecheck && npm run build
cd ..
make openapi-check
git diff --check
```

更新矩阵结果为真实命令与证据路径；报告列出 100% 映射/状态/capability/故障恢复、意向
precision≥90%、recall≥85%、三轮 Chrome 结果及人工 Chrome 复核。然后提交：

```bash
git add web/playwright.config.ts web/tests/e2e docs/testing
git commit -m "test(aicc): 完成客服对话全场景真实浏览器验收" -m "覆盖能力沙箱、来源、意向、会话状态、Pod 重建、故障恢复和跨访客隔离，并记录三轮本地 Chrome 证据。"
```

## Final self-check

- [ ] 逐节对照设计文档 1—15，矩阵中每条要求均指向上述任务和测试。
- [ ] 所有 Go 新测试和子测试都有相邻中文场景注释，table case 每行有中文说明。
- [ ] OpenAPI 两个生成文件只由命令生成，未手工编辑。
- [ ] 工作区只包含本计划任务相关改动，用户未跟踪计划文件保持原样。
- [ ] 未连接生产集群，所有 kubectl 均显式使用本地 k3d context 与 `ocm/oc-aicc` namespace。
