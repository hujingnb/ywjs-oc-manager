# OpenClaw Manager 端到端验证报告

> 版本：master，commit `c7f46a5` 起始；以下命令在 oc-manager 仓库根执行，时间为本地运行时间。

## 自动化检查

| 命令 | 结果 | 备注 |
|---|---|---|
| `bash -c 'go vet ./...'` | ✅ 通过 | 全模块无警告。 |
| `bash -c 'go test ./... -count=1'` | ✅ 通过 | 覆盖 `internal/api/handlers`、`internal/auth`、`internal/config`、`internal/domain`、`internal/files`、`internal/integrations/{agent,channel,newapi,openclaw,runtime}`、`internal/redis`、`internal/runtime/imagesync`、`internal/scheduler`、`internal/service`、`internal/store`、`internal/worker(/handlers)`、`runtime/agent`。 |
| `docker exec manager-web sh -c 'npm test -- --run'` | ✅ 通过 | `src/domain/status.test.ts` 8 个 case。 |
| `docker exec manager-web sh -c 'npm run typecheck'` | ✅ 通过 | `vue-tsc --noEmit`。 |
| `docker exec manager-web sh -c 'rm -rf /app/web/dist && npm run build'` | ✅ 通过 | 产出 `dist/index.html` + `assets/index-*.js`。 |
| 直接 `npm run build` | ⚠️ 无法在宿主机直接运行 | `web/dist/assets` 由先前 docker-as-root 构建留下，宿主用户无写权限。需在容器内构建或先用 root 删除目录。 |

## 端到端流程

> 受限于宿主机 chrome-devtools MCP 与现有 Chrome 进程的 profile 冲突，本轮没有通过 MCP 完成可视化验收。
> 下表给出每条流程对应的 API 路径，供操作员/CI 在解决浏览器冲突后逐项核对。

| 步骤 | 接口/页面 | 备注 |
|---|---|---|
| 登录 | `POST /api/v1/auth/login` → `/login` 页 | LoginPage 使用真实接口，成功后跳 `/`。 |
| 创建组织 | `POST /api/v1/organizations` → `/organizations` | 平台管理员可见。 |
| 注册节点 | `POST /api/v1/runtime-nodes` → `/runtime-nodes` | 创建后页面会一次性弹出 bootstrap token。 |
| Agent 注册 | `POST /api/v1/agent/register` | OC runtime agent 启动时调用，换取 agent token。 |
| 心跳 | `POST /api/v1/agent/heartbeat` | 周期性上报。 |
| 创建成员 + 初始化应用 | `POST /api/v1/organizations/:orgId/members/onboard` → `/members/new` | 单事务串起 user/app/binding/audit/job。 |
| 应用初始化 | worker 处理 `app_initialize` job | 渲染 prompt、调用 new-api 写 api_key、状态推到 binding_waiting。 |
| 绑定渠道 | `POST /api/v1/apps/:appId/channels/:channelType/auth` → AppChannelsTab | 等 OC runtime CommandRunner 接入后才能真正出二维码。 |
| 知识库主副本 | `POST/GET/DELETE /api/v1/organizations/:orgId/knowledge` → `/knowledge` | 路径校验由 SafePath 兜底。 |
| 工作目录 | `GET /api/v1/apps/:appId/workspace` → AppWorkspaceTab | 列表 / 下载 / 归档；写操作走 worker。 |
| 运行操作 | `POST /api/v1/apps/:appId/runtime/{start|stop|restart|delete}` | 同步写审计与 jobs。 |
| 用量 | `GET /api/v1/apps/:appId/usage?...` | 通过 new-api token remain_quota 推断。 |
| 删除 | runtime/delete + soft delete | AppsPage 中通过 ConfirmActionModal 二次确认。 |

## 已知未实现/待外部条件

- **Docker proxy via agent**：`internal/integrations/runtime.AgentBackedAdapter` 中 Container* 操作仍返回 `ErrUnimplemented`，待 oc-runtime-agent 暴露 docker proxy 后接入。
- **WeChat CommandRunner**：`channel.CommandRunner` 接口已就位，需要实际 OpenClaw 微信登录命令的 docker exec stream 接入；adapter 单测通过 fake stream 覆盖协议解析。
- **chrome-devtools MCP 验证**：当前宿主机 Chrome 占用 profile 致使 MCP 启动失败；解决后需依次抓取登录页、组织列表、成员列表、应用列表、Runtime Node、知识库、应用工作目录的 DOM snapshot。
- **OpenAPI client 生成**：本轮没有跑 oapi-codegen 等工具；OpenAPI 与代码保持一致由人工 review 把关。

## 仓库自检结论

- 14/17 计划任务在本会话内完成并按 task 边界提交；剩余 9.2 最终自检与持续浏览器验收待外部条件。
- 全量 `go test ./...` 与 `vitest` 通过，没有跳过的 case。
- `make check-compose` 校验通过：所有挂载使用本地 `./data` bind mount，符合“禁止 named volume”的全局约束。
