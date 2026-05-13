# AGENTS.md

本文件规定本仓库内 AI agent 和协作者必须遵守的基础规范。若子目录存在更具体的 `AGENTS.md`，以更近层级的文件为准。

## 基本原则

- 保持改动聚焦，只修改与当前任务直接相关的文件。
- 不回滚、覆盖或重排他人已有改动，除非用户明确要求。
- 不做无关重构、无关格式化或批量机械改动。
- 修改代码前先理解现有结构、命名和测试习惯，并优先沿用项目既有模式。
- 除非用户明确提出特殊要求，否则所有代码和文档改动都直接在当前 `master` 分支完成。
- 除非用户明确提出特殊要求，否则所有任务都不要切换或创建 git worktree。

## 本地调试账号

- new-api 管理员：`admin` / `admin123`
- manager 平台管理员：组织标识留空，`admin` / `admin123`
- manager 测试组织：组织标识 `test-org`
- manager 测试组织管理员：组织标识 `test-org`，`test-org` / `test-org123`
- manager 组织成员：组织标识 `test-org`，`test-org-user1` / `test-org-user1`

## 权限校验

- 角色 / 资源权限谓词（platform_admin / org_admin / org_member 三层判断）必须放在
  `internal/auth/authorizer.go`，service 包不再定义本地 `canX` 函数。
- 新增权限规则时优先扩展现有 `Can*` 函数，避免在 handler 或 service 内联写
  `if principal.Role == "..."` 判断；如确需新增，提交 PR 时请说明设计取舍。

## OpenAPI 同步

- API 契约由 swag 注解扫描生成 `openapi/openapi.yaml`，前端类型由
  `make web-types-gen` 从 yaml 生成 `web/src/api/generated.ts`。两个文件都
  入 git，提交时必须保持同步。
- 修改 handler 函数签名 / 请求体 / 响应类型 / 路由后，必须跑 `make openapi-gen`
  + `make web-types-gen`，把变更连同代码一起提交。
- `make openapi-check` 用于本地校验：跑 `make openapi-gen` 后 git 工作区应保持
  干净，否则说明 yaml 未跟随代码更新。
- 新增 handler 时，请求体类型放 `internal/api/handlers/dto.go` 并导出大写命名；
  响应仍用 `service.XxxResult`（swag 跨包扫描）。
- 不要手工编辑 `openapi/openapi.yaml` 与 `web/src/api/generated.ts`——它们是
  生成产物。

## 测试断言

- 新增 / 重构单元测试一律使用 `github.com/stretchr/testify` 的 `assert` /
  `require`：错误检查用 `require.NoError` / `require.Error` /
  `require.ErrorIs`；等值断言用 `assert.Equal`（顺序：expected 在前，与
  原 `t.Errorf("got %v want %v")` 顺序相反）；后续依赖此值不能继续时用
  `require.*` 让 fail 立即停止。
- stdlib `t.Fatalf` / `t.Errorf` 仅在极个别 helper / table-driven 复合
  格式化场景保留，不再做新增。

## users.deleted_at 语义

- `users.deleted_at` 字段语义为「下线时间戳」（即 `status=disabled` 时由
  SQL 自动设置 `deleted_at = NOW()`，重新启用时清空）。**与
  `organizations.deleted_at`「真删除时间」语义不同**。
- 查询活跃用户：`WHERE deleted_at IS NULL`，走 `users_active_idx` 部分索引。
- 真软删除场景（如未来要做「彻底下线、不可恢复」）用 `SoftDeleteUser`
  query：仅设置 `deleted_at`，不动 `status`。

## Commit Message

- 使用 Conventional Commits 格式：

```text
<type>(optional-scope): <summary>
```

- 常用 `type`：
  - `feat`: 新功能
  - `fix`: 缺陷修复
  - `test`: 测试相关
  - `docs`: 文档相关
  - `refactor`: 不改变行为的重构
  - `chore`: 构建、依赖、工具或杂项维护

- `summary` 必须使用中文，简洁、具体地说明本次提交实际改变了什么。
- commit message 不应只有一行简短摘要；第一行写中文简短摘要，空一行后的正文应补充必要的背景、实现细节、影响范围或测试说明。
- 不使用含糊描述，例如 `update`、`fix bug`、`changes`。
- 提交必须按照业务 / 功能边界拆分：一次提交只表达一个清晰目的，相关文件应作为同一业务或功能变更一起提交；不要为了“一个文件一个提交”而拆碎提交。
- 无关业务、无关功能或可独立评审的改动必须拆分提交。

示例：

```text
feat(order): 增加订单导出接口

为筛选后的订单列表增加 CSV 导出能力。

导出逻辑复用现有查询条件，并返回与订单列表一致的可见字段。

fix(auth): 拒绝已过期的刷新令牌
test(user): 覆盖密码重置校验
docs: 增加本地开发说明
```

## 单元测试

- 修改业务逻辑、边界条件、错误处理或数据转换时，必须补充或更新对应单元测试。
- 修复 bug 时，优先添加能复现该 bug 的失败用例，再实现修复。
- 测试应覆盖正常路径、关键异常路径和重要边界条件。
- 每一个单元测试方法和子测试逻辑都必须有相邻中文注释，说明该测试覆盖的
  业务场景、边界条件或异常路径；文件级 `Package ...` 注释不能替代单个
  测试方法或子测试场景注释。
- table-driven 测试的每一条用例数据都必须有相邻或行尾中文注释，说明该条
  数据覆盖的输入组合、边界条件或期望结果；即使用例包含 `name` 字段，也
  需要补充注释说明对应测试场景。
- 不为了让测试通过而降低断言质量、跳过测试或删除有效测试。
- 提交前必须运行与改动相关的测试；若无法运行，必须在交付说明中写明原因和风险。

## 注释

- 新增或重构的代码必须补充必要的中文注释。
- 所有逻辑都必须配备详细的中文注释，范围至少包括包、文件、方法、结构体、字段、
  常量、参数、代码段等；注释应说明业务意图、边界条件、异常原因或非显而易见的
  实现约束，避免只重复代码表面含义。
- 注释应说明业务意图、边界条件、异常原因或非显而易见的实现约束。
- 不添加重复代码表面含义的注释。
- 保留第三方接口名、协议名、错误码、命令、配置键和代码标识的英文原文。
- 注释应说明业务意图、跨模块约束、兼容性原因或不直观的实现细节，避免重复代码字面含义。

## 交付前检查

- 确认工作区中没有混入无关文件改动。
- 确认新增或修改的测试已经运行，或明确说明未运行原因。
- 所有新功能开发完成后，必须调用浏览器进行全面功能验证；若发现问题，必须先修复并重新验证，直到功能正常后再交付。
- 确认文档、注释和命名与实际行为一致。
- 确认没有提交密钥、令牌、私有地址或临时调试代码。


# CLAUDE.md

Behavioral guidelines to reduce common LLM coding mistakes. Merge with project-specific instructions as needed.

**Tradeoff:** These guidelines bias toward caution over speed. For trivial tasks, use judgment.

## 1. Think Before Coding

**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:
- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them - don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

## 2. Simplicity First

**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Ask yourself: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

## 3. Surgical Changes

**Touch only what you must. Clean up only your own mess.**

When editing existing code:
- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it - don't delete it.

When your changes create orphans:
- Remove imports/variables/functions that YOUR changes made unused.
- Don't remove pre-existing dead code unless asked.

The test: Every changed line should trace directly to the user's request.

## 4. Goal-Driven Execution

**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:
- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:
```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

Strong success criteria let you loop independently. Weak criteria ("make it work") require constant clarification.

---

**These guidelines are working if:** fewer unnecessary changes in diffs, fewer rewrites due to overcomplication, and clarifying questions come before implementation rather than after mistakes.
