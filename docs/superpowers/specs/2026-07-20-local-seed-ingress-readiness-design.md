# 本地演示数据登录前 Ingress 就绪检查设计

## 背景

`make local-reset` 在 `local-init-models` 回填 secret 后会滚动重启 `manager-api`。虽然
`kubectl rollout status` 已确认新 Pod Ready，Traefik 对 Endpoint 变更的监听仍可能短暂滞后。
此时紧接着执行的 `local-seed-demo` 首次登录会收到 502，并让整条本地重建流程失败。

现场证据显示同一时间窗口内 Traefik 报告 `manager-api` 没有 Endpoint；稍后通过同一
Ingress 访问 `/healthz` 已返回 200，登录接口也恢复正常。因此问题属于入口层就绪竞态，
不是账号、数据库或登录 handler 错误。

## 方案选择

采用“登录前通过同一 Ingress 检查健康状态”的方案：创建平台 `ManagerAPI` 客户端后，先
GET `/healthz`，成功且响应 `status=ok` 后才发送一次登录 POST。

不采用以下方案：

- 不直接重试登录 POST。这样可以继续保持“写请求不盲目重放”的统一安全边界。
- 不在 Makefile 或模型初始化脚本中固定 sleep。固定等待无法证明 Traefik 已经完成路由同步，
  在慢机器上仍可能失败，在快机器上则徒增时间。

## 组件与数据流

1. `local_seed_demo.cli.main()` 创建指向 `http://ocm.localhost` 的平台客户端。
2. 客户端通过同一地址 GET `/healthz`，复用既有 GET 瞬时错误重试能力。
3. 就绪检查使用 60 秒绝对 deadline，确保请求 timeout、退避和后续尝试都受同一预算约束。
4. 仅当响应为 JSON 对象且 `status` 精确等于 `ok` 时继续。
5. CLI 随后只调用一次 `login("", "admin", "admin123")`，其余播种流程保持不变。

就绪检查应封装为客户端的明确方法，避免 CLI 读取客户端内部时钟或复制 deadline 计算规则。
默认调用保持现有 `ManagerAPI` 行为，不影响其他 GET/POST/PATCH。

## 错误处理与安全

- 502、503、504、429 和连接异常按既有 GET 策略有限退避；deadline 耗尽后失败。
- 401、404 等非瞬时状态立即失败，不掩盖路由或部署配置错误。
- 200 但 envelope 缺少 `status=ok` 时按响应契约冲突失败，不进入登录。
- CLI 继续通过现有安全输出边界隐藏响应体、token、密码和服务端自由文本。
- `KeyboardInterrupt` 与 `SystemExit` 继续向上传播。

失败后用户仍可在服务恢复后单独运行 `make local-seed-demo`，播种幂等语义不变。

## 测试与验收

- 客户端测试：健康检查遇到一次 502 后成功；确认退避并最终返回健康对象。
- 客户端测试：deadline 耗尽、永久 4xx、异常健康 envelope 均失败，且不会发起登录 POST。
- CLI 测试：严格断言健康检查发生在登录之前，健康失败时不登录、不运行 seeder。
- 回归：全部 `test_local_seed_demo_*.py`、`py_compile` 和 `git diff --check` 通过。
- 本地集成：滚动重启 `manager-api` 后立即运行 `make local-seed-demo`，确认不会因瞬时 502
  失败，并输出固定 `2/3/2/2` 汇总。

## 范围

本次只修改本地演示数据 HTTP 客户端、CLI 及其测试。不改变登录 API、Traefik 配置、
Kubernetes Deployment 策略或普通写请求的重试规则。
