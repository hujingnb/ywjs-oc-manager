# Test Comments Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the repository rule requiring scenario comments for every unit test method and subtest, then bring the current Go tests into compliance.

**Architecture:** This is a documentation and test-comment-only change. The implementation updates the root repository guidance and inserts adjacent Chinese comments before every uncommented `func Test...` and `t.Run(...)` without changing test behavior.

**Tech Stack:** Markdown, Go test files, shell static scan, `go test`.

---

### Task 1: Update Repository Guidance

**Files:**
- Modify: `AGENTS.md`

- [x] **Step 1: Add the unit-test comment rule**

Add this bullet to the `## 单元测试` section after the existing coverage expectations:

```markdown
- 每一个单元测试方法和子测试逻辑都必须有相邻中文注释，说明该测试覆盖的业务场景、边界条件或异常路径；文件级 `Package ...` 注释不能替代单个测试方法或子测试场景注释。
```

- [x] **Step 2: Check the documentation diff**

Run:

```bash
rtk git diff -- AGENTS.md
```

Expected: only the new bullet appears in the `## 单元测试` section.

### Task 2: Insert Missing Go Test Comments

**Files:**
- Modify: all `*_test.go` files with uncommented `func Test...` declarations or uncommented `t.Run(...)` calls.

- [x] **Step 1: Locate missing comments**

Run:

```bash
rtk python3 - <<'PY'
from pathlib import Path
import re
missing_funcs = []
missing_runs = []
for p in Path('.').rglob('*_test.go'):
    lines = p.read_text(encoding='utf-8').splitlines()
    for i, line in enumerate(lines):
        if re.match(r'^func Test\w+\(', line):
            j = i - 1
            while j >= 0 and lines[j].strip() == '':
                j -= 1
            if j < 0 or not lines[j].lstrip().startswith('//'):
                missing_funcs.append((str(p), i + 1, line.strip()))
        if re.search(r'\bt\.Run\s*\(', line):
            j = i - 1
            while j >= 0 and lines[j].strip() == '':
                j -= 1
            if j < 0 or not lines[j].lstrip().startswith('//'):
                missing_runs.append((str(p), i + 1, line.strip()))
print('missing_top_level_test_comments=', len(missing_funcs))
print('missing_t_run_comments=', len(missing_runs))
PY
```

Expected before edits: non-zero counts for top-level tests and subtests.

- [x] **Step 2: Add adjacent Chinese comments**

For every missing `func Test...`, add a `// TestXxx ...` comment immediately above the function. The comment must state the scenario being validated, such as permission rejection, happy path, boundary condition, error propagation, idempotency, parsing fallback, or external dependency guard.

For every missing `t.Run(...)`, add a Chinese comment immediately above the call. The comment must state the table row or subcase intent rather than only repeating the subtest name.

- [x] **Step 3: Keep behavior unchanged**

Do not edit assertions, helper functions, test inputs, expected values, package names, imports, or production code while inserting comments.

### Task 3: Verify Compliance

**Files:**
- Read: all `*_test.go`

- [x] **Step 1: Re-run the static scan**

Run:

```bash
rtk python3 - <<'PY'
from pathlib import Path
import re
missing_funcs = []
missing_runs = []
for p in Path('.').rglob('*_test.go'):
    lines = p.read_text(encoding='utf-8').splitlines()
    for i, line in enumerate(lines):
        if re.match(r'^func Test\w+\(', line):
            j = i - 1
            while j >= 0 and lines[j].strip() == '':
                j -= 1
            if j < 0 or not lines[j].lstrip().startswith('//'):
                missing_funcs.append((str(p), i + 1, line.strip()))
        if re.search(r'\bt\.Run\s*\(', line):
            j = i - 1
            while j >= 0 and lines[j].strip() == '':
                j -= 1
            if j < 0 or not lines[j].lstrip().startswith('//'):
                missing_runs.append((str(p), i + 1, line.strip()))
print('missing_top_level_test_comments=', len(missing_funcs))
print('missing_t_run_comments=', len(missing_runs))
if missing_funcs or missing_runs:
    raise SystemExit(1)
PY
```

Expected after edits:

```text
missing_top_level_test_comments= 0
missing_t_run_comments= 0
```

- [x] **Step 2: Run Go tests**

Run:

```bash
rtk go test ./...
```

Expected: all tests pass. If environment-gated integration tests skip or external dependency tests fail, record the exact failure and explain why comment-only changes do not affect that dependency.

- [x] **Step 3: Review changed files**

Run:

```bash
rtk git status --short
rtk git diff --stat
```

Expected: changes are limited to `AGENTS.md`, Go test comments, and this implementation plan.
