# 日志规范

manager-api 统一使用标准库 `log/slog`，输出经 `internal/log` 包装（脱敏 + trace_id 自动注入）。

## 字段命名

- 字段 key 一律使用 `internal/log` 中的 `Key*` 常量，不要写裸字符串字面量。
- 错误统一用 `log.Err(err)`（key 固定为 `error`），不要再写 `slog.String("err", ...)` 之类的变体。
- trace_id 由 `requestIDHandler` 从 ctx 自动注入，业务代码不手填；务必用 `*Context` 系列方法
  （`slog.InfoContext` / `WarnContext` / `ErrorContext`）并传 `ctx`，否则丢 trace_id。

## 级别原则

- **Debug**：正常流程的细粒度追踪（如外部调用成功）。生产默认不输出。
- **Info**：正常业务里程碑（如 HTTP access log、后台任务完成）。
- **Warn**：可恢复 / 不阻塞主流程的异常（如外部清理失败但主业务已成功、可重试错误）。
- **Error**：不可恢复或导致数据不一致、需人工介入的错误。

## 约束

- 外部依赖调用（new-api / RAGFlow）的请求元数据由 `internal/integrations/httplog` 的 RoundTripper
  统一记录，service 层不要重复记录成功调用与请求细节，只在需要业务上下文时补充。
- HTTP 请求由 access log 中间件统一记录，handler 不要逐个再记一遍请求行。
- 不记录请求 / 响应 body；不打印 token、密码、密钥（脱敏 writer 是兜底，不是允许打印的理由）。

## 配置

- `LOG_LEVEL`：debug / info / warn / error，默认 info。生产排故可临时调 debug。
- `LOG_FORMAT`：json（默认，容器/ELK）/ text（本地调试人眼友好）。
