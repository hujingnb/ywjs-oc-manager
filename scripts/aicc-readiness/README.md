# AICC 本地就绪验证工具

本目录的工具仅面向本项目本地 k3d 环境，用于严格上线门禁的故障、容量和升级验证。不要将公开 token、测试报告或此处的请求参数用于生产环境。

## 公开会话负载测试

`loadtest` 模拟匿名访客的完整公开客服链路：每个虚拟访客使用独立 HTTP client、不同的 `X-Forwarded-For` 地址、独立 AICC session，以及唯一文本消息。每轮依次创建会话、发送消息、读取会话镜像；读取结果缺失该访客唯一消息时记录为 `session_mismatches`。

工具默认使用 100 并发、持续 30 分钟、每个 HTTP 请求超时 30 秒。正式容量门禁需要让每个虚拟访客的 `X-Forwarded-For` 到达 manager；本地 Traefik 会按安全策略重写客户端伪造的代理头，因此先把 manager-api 转发到宿主机端口：

```bash
kubectl -n ocm port-forward svc/manager-api 18080:8080
```

再显式提供直连基础地址和已启用智能体的公开 token：

```bash
go run ./scripts/aicc-readiness/loadtest \
  -base-url http://127.0.0.1:18080 \
  -public-token "$AICC_PUBLIC_TOKEN" \
  -output /tmp/aicc-load-report.json
```

`ocm.localhost` 仍用于真实浏览器、Ingress、来源域名和挂件端到端验证。不要为压测放宽 Traefik 的可信代理范围；否则会让公网客户端伪造来源 IP，绕过匿名限流。

先以低负载冒烟，确认公开入口与 token 有效：

```bash
go run ./scripts/aicc-readiness/loadtest \
  -base-url http://ocm.localhost \
  -public-token "$AICC_PUBLIC_TOKEN" \
  -concurrency 2 \
  -duration 30s
```

正式门禁运行时，请同时另开终端采集 `ocm`、`oc-apps` 中 Pod 的资源、重启次数与 manager-api/Hermes 错误日志。报告不输出公开 token，包含：

- 总 HTTP 请求、成功请求和成功率；
- 全部 HTTP 请求的 P50/P95/P99 延迟（毫秒，最近秩算法）；
- 网络、超时、HTTP 状态码与协议响应错误分类；
- `session_mismatches`，用于识别会话串写或消息镜像不一致；
- 发生器进程起止时的 Go 内存、goroutine、CPU 和最大 RSS 快照。

本地容量门禁通过条件：成功率至少 `99.5`、P95 不高于 `15000` 毫秒、`session_mismatches` 为 `0`，且被测 Pod 无异常重启并在测试后资源回落。任何未满足项均为 `NO-GO`，不得以工具退出码为零替代这些判定。
