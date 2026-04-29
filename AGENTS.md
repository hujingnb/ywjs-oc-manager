# AGENTS.md

本文件规定本仓库内 AI agent 和协作者必须遵守的基础规范。若子目录存在更具体的 `AGENTS.md`，以更近层级的文件为准。

## 基本原则

- 保持改动聚焦，只修改与当前任务直接相关的文件。
- 不回滚、覆盖或重排他人已有改动，除非用户明确要求。
- 不做无关重构、无关格式化或批量机械改动。
- 修改代码前先理解现有结构、命名和测试习惯，并优先沿用项目既有模式。

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

- `summary` 必须简洁、具体，说明本次提交实际改变了什么。
- 不使用含糊描述，例如 `update`、`fix bug`、`changes`。
- 一次提交只表达一个清晰目的；无关改动拆分提交。

示例：

```text
feat(order): add order export endpoint
fix(auth): reject expired refresh token
test(user): cover password reset validation
docs: add local development notes
```

## 单元测试

- 修改业务逻辑、边界条件、错误处理或数据转换时，必须补充或更新对应单元测试。
- 修复 bug 时，优先添加能复现该 bug 的失败用例，再实现修复。
- 测试应覆盖正常路径、关键异常路径和重要边界条件。
- 不为了让测试通过而降低断言质量、跳过测试或删除有效测试。
- 提交前必须运行与改动相关的测试；若无法运行，必须在交付说明中写明原因和风险。

## 交付前检查

- 确认工作区中没有混入无关文件改动。
- 确认新增或修改的测试已经运行，或明确说明未运行原因。
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
