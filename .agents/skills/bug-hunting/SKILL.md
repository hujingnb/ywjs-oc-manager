---
name: bug-hunting
description: >
  线上问题排查 skill。当用户报告生产/线上环境出现 bug、需要加调试日志定位原因、
  或收到日志后要分析根因时，立即调用本 skill。
  触发词：线上有问题、生产报错、帮我加日志、帮我排查、看看日志、日志分析、
  重现不了、排查一下、online bug、prod issue、debug log。
  不要等用户说"用 bug-hunting skill"——只要是排查线上问题的场景就主动触发。
---

# 线上问题排查（bug-hunting）

四个阶段，按实际进展选择入口：

| 阶段 | 触发时机 |
|------|---------|
| **A. 加日志** | 用户描述了问题，需要定位；或分析后日志不足 |
| **B. 分析日志** | 用户把日志拿回来了 |
| **C. 修复** | 根因已确认，实施代码修复 |
| **D. 清理** | 修复完成，删除全部调试日志并提交 |

流程：A → B → （日志不足？回到 A）→ C → D

---

## 阶段 A：添加调试日志

### 1. 先理解问题再动手

在加任何日志之前，明确：
- 用户描述的现象是什么？哪个接口/流程出了问题？
- 最可能出错的几个环节在哪里？
- 需要捕获哪些中间值才能定位？

如果不清楚，先问。

#### 日志数量

- 首轮排查：在可能出现问题的地方尽量添加日志，覆盖完整路径
- 后续轮次：根据已有信息，在缩小的范围内精准添加
- 避免在高频循环中无限制添加日志，防止日志爆炸

### 2. 日志规范

**格式**：`[hujingnb] <包名>:<函数名> - <描述>: <值>`

**级别**：统一用 error 级别（生产环境通常只开 warn/error，info 看不到）。

**日志函数**：本项目使用 `slog`。有 ctx 时用 `slog.ErrorContext(ctx, ...)`，无 ctx 时用 `slog.Error(...)`。

**每一行调试代码末尾都必须加 `// todo del`**，包括辅助变量、辅助序列化等。

### 3. 三种常见模式

**打印普通值**（有 ctx）：
```go
slog.ErrorContext(ctx, "[hujingnb] handler:Init - 收到请求", "appID", appID, "payload", payload) // todo del
```

**打印复杂结构**（需要辅助变量）：
```go
debugBytes, _ := json.Marshal(config) // todo del
slog.ErrorContext(ctx, "[hujingnb] handler:Init - 配置详情", "config", string(debugBytes)) // todo del
```

**打印循环中间值**：
```go
debugCount := len(items) // todo del
for i, item := range items {
    slog.ErrorContext(ctx, "[hujingnb] handler:Process - 处理项", "i", i, "total", debugCount, "item", fmt.Sprintf("%+v", item)) // todo del
}
```

**替换任何现有代码行**（无论是 return、io.Copy、函数调用还是任何其他语句，只要原始行被新代码替换，必须在替换行末尾加 `// todo del origin: <原始行完整内容>`）：
```go
// 示例 1：替换 return 语句
result, err := s.dao.Create(ctx, repo) // todo del origin: return s.dao.Create(ctx, repo)
slog.ErrorContext(ctx, "[hujingnb] service:Create - 创建结果", "result", result, "err", err) // todo del
return result, err // todo del

// 示例 2：替换 io.Copy/Discard 等语句
dockerLoadResp, _ := io.ReadAll(io.LimitReader(resp.Body, 8192)) // todo del origin: _, _ = io.Copy(io.Discard, resp.Body)
slog.Error("[hujingnb] foo:Bar - response", "body", string(dockerLoadResp)) // todo del

// 示例 3：替换 json.NewDecoder 流式解析
if err := json.Unmarshal(rawBody, &payload); err != nil { // todo del origin: if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
```
> **强制要求**：任何被删除/替换的原始行，必须以 `// todo del origin: <原始行>` 的形式保留在替换行末尾。清理阶段会把替换行整体恢复为 `origin:` 后面的内容。**不得直接删除原始行而不留 origin 注释**，否则清理时无法自动还原。

### 4. 加完日志后

- 检查：每行调试代码都有 `// todo del`；没有遗漏的 import
- 运行 `go build ./...` 确认编译通过
- **不提交、不 push**；根据修改的文件告知用户需要构建哪些镜像：
  - 改了 `cmd/server/` 或 `internal/` 下的文件 → 构建 **manager** 镜像
  - 改了 `runtime/agent/` 下的文件 → 构建 **agent** 镜像
  - 两者都改了 → 两个镜像都需要构建

---

## 阶段 B：分析日志

用户把 `[hujingnb]` 日志行拿回来后：

1. 按时间顺序梳理日志中的执行路径
2. 找到第一个异常值/缺失值/错误分支
3. 对照代码尝试确认根因

**判断日志是否充足**：
- 能明确指向某一行/某个条件 → 日志充足，进入**阶段 C 修复**
- 路径断了、关键值缺失、仍有多种可能 → 日志不足，说明缺哪些信息，回到**阶段 A** 补充日志

---

## 阶段 C：修复

根因确认后实施修复：

1. 描述根因和修复思路，与用户确认
2. 修改代码（只改与 bug 相关的逻辑，不做无关重构）
3. `go build ./...` 确认编译通过
4. 进入**阶段 D** 清理调试日志

---

## 阶段 D：清理调试日志

清理规则（**一行不能漏**）：

| 行的特征 | 操作 |
|---------|------|
| `// todo del origin: <code>` | 把整行替换为 `<code>`（还原原始代码） |
| `// todo del`（其余所有） | 整行删除 |

清理步骤：
1. 全局 grep `todo del` 确认所有待清理行
2. 先处理带 `origin:` 的行（还原），再删除其余 `// todo del` 行
3. 再次 grep `[hujingnb]` 和 `todo del`，确认为零
4. `go build ./...` 确认编译通过
5. commit message：`fix(<scope>): <修复内容的简短描述>`（遵循项目 Conventional Commits 规范）
6. **push**

---

## 注意事项

- **只加日志，不改业务逻辑**（修复在阶段 C 单独进行）
- **敏感信息脱敏**：不打印密码、token 等明文，可打印长度或脱敏值（如 `***`）
- **日志序号**：路径复杂时给日志加序号方便追踪，如 `[hujingnb][1]`、`[hujingnb][2]`
- **避免重复**：每轮添加日志后记住已加的位置，下一轮只补充空白区域，不重复添加
- **日志完整性**：分析时如果用户只贴了部分日志，主动询问是否有遗漏
- **新增 import**：调试新增的 import（如 `encoding/json`、`fmt`）也要标记 `// todo del`，清理时一并删掉
- **只改服务端**：调试日志只加在 manager/agent，不加在前端
