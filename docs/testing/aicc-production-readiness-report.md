# AICC 生产就绪严格验证报告

- 验证日期：2026-07-13
- 最终实现基线：`08fc4401`（报告提交前代码提交）
- 结论：**NO-GO，不可直接上线**

## 结论依据

除容量延迟门禁外，最终镜像已完成静态测试、真实浏览器、故障恢复，以及 `master -> 当前 -> 受控回退 -> 当前` 升级演练。正式容量测试成功率和会话隔离均达标，但 P95 为 24.502 秒，超过 15 秒上线阈值；因此不能上线。

## 已通过证据

| 验证层 | 结果 | 证据 |
|---|---|---|
| Go 全量测试 | PASS | `go test ./... -count=1` 全部包通过 |
| Hermes 测试 | PASS | 253 项通过 |
| 前端单元测试 | PASS | 105 个文件、729 个用例通过 |
| 类型、构建、API 契约 | PASS | `vue-tsc --noEmit`、Vite build、`make openapi-check` 通过 |
| 真实浏览器 | PASS | Chromium 合并执行 `aicc.spec.ts`、`aicc-access-i18n.spec.ts`、`aicc-knowledge.spec.ts`，11/11 通过 |
| 故障恢复 | PASS | Hermes、manager-api、RAGFlow、new-api、Redis、MySQL 故障与恢复均通过；重启后消息幂等 |
| 升级与回退 | PASS | `master c270b3d0 -> kefu 08fc4401`，27 -> 32；旧版本受控失败、基线库恢复至 27、最终恢复至 32 |

## 上线阻断

### 容量门禁失败

100 并发、30 分钟正式测试结果见 `docs/testing/evidence/aicc-load-formal-100x30m.json`：

- 总请求 `25,584`，成功 `25,584`，成功率 `100%`；
- 会话串写 `0`；
- P50 `5ms`，P95 `24,502ms`，P99 `26,776ms`；
- 门禁要求 P95 不超过 `15,000ms`，故 `AICC-LOAD-01 = FAIL`。

根因是 Hermes/模型上游在 100 并发下的有效吞吐不足。已验证扩大 CPU 和转发并发池不能改善 P95，相关无效调优已撤销，不能通过放宽阈值规避该问题。

## 本轮修复

- 升级回退脚本正确聚合 API/Web rollout 失败状态；
- 回退导入基线前重建数据库，避免新版外键残留；
- Traefik 与 `klipper-helm` 从 DaoCloud 预加载；
- `oc-ops` 就绪探针改为验证同 Pod api_server，避免 Hermes 重启后过早接流量。

## 达到 GO 前必须完成

解决 100 并发下 P95 延迟问题，并在相同门禁下重新运行 30 分钟容量测试，达到 P95 `<= 15s`。在此之前，其他通过项不能抵消容量失败。
