# AICC 智能体删除运行时清理设计

## 目标

接待台删除 AICC 智能体时，保留会话、消息、线索和审计历史，同时删除该智能体关联隐藏 app 的 Kubernetes 运行时资源，避免 Deployment、Pod、Service、Secret、HPA 与 NetworkPolicy 残留。

## 现状与问题

当前 `AICCService.DeleteAgent` 仅软删除 `aicc_agents` 行。隐藏 app 和其 Kubernetes 资源没有进入删除流程，因此已删除智能体仍可能持续运行或 CrashLoopBackOff。

现有 `app_delete` worker 已具备按 app ID 幂等清理运行时资源的能力：删除 Kubernetes Deployment、Service、Secret、AICC HPA、AICC NetworkPolicy，禁用 app 的 new-api key，并清理私有 RAGFlow dataset。该任务还会软删除 apps 行；AICC 不持久化 S3 工作区，因此归档步骤为空操作。

## 方案

删除接口保持同步、资源回收保持异步：

1. 验证操作者有目标企业 AICC 管理权限，并读取未删除的智能体记录。
2. 软删除智能体记录，使公开链接立刻不可用，但不删除 AICC 会话、消息、线索和审计历史。
3. 通过隐藏 app 删除入口软删除关联 apps 行，并创建一个 `app_delete` 任务；该入口应通知现有任务队列，使资源回收尽快开始。
4. `app_delete` worker 复用既有幂等清理流程回收 Deployment、Service、Secret、HPA、NetworkPolicy、运行时 token 和私有 RAGFlow dataset。
5. 任一步创建隐藏 app 删除任务失败时，删除接口返回错误；不得把资源回收失败静默伪装为成功。

## 边界与一致性

- 删除的是运行时和隐藏 app，不删除 AICC 业务历史。新建同名智能体会拥有新的隐藏 app、公开链接和后续会话，不继承旧智能体的接待历史。
- 不直接在 HTTP 请求中调用 Kubernetes API。Kubernetes 删除、外部 token 和 RAGFlow 清理由 worker 负责，以获得重试和幂等保障。
- 重复删除同一智能体仍返回既有未找到语义；不会重复创建资源清理任务。
- 任务执行时资源不存在视为成功，保证已手工处理或部分失败后的重试安全。

## 影响文件

- `internal/service/aicc_service.go`：删除智能体时调用隐藏 app 删除入口。
- `internal/service/app_service.go`：提供创建 `app_delete` 任务的 AICC 隐藏 app 删除实现，复用既有 app 删除任务协议。
- `cmd/server/main.go`：为 AICC service 注入删除能力和任务通知器。
- `internal/service/aicc_service_test.go`、`internal/service/app_service_test.go`：覆盖删除任务创建、失败传播、幂等与历史保留语义。

## 验证

- 服务单元测试证明删除 AICC 智能体会软删除关联隐藏 app 并创建一次 `app_delete` 任务。
- 任务创建失败时接口返回错误且不记录成功审计。
- 现有 `app_delete` worker 测试继续证明 Kubernetes AICC 资源清理包含 HPA 与 NetworkPolicy。
- 运行受影响的 Go 服务与 worker 定向测试；本次不运行全量 E2E。
