# ocops Contract

This directory is the canonical contract for the `ocops.server` HTTP control
plane bundled into each Hermes runtime variant.

## Versioning

- Contract version is `1.0`.
- Manager may consume compatible `1.x` contracts.
- Breaking changes require `2.0`; manager must reject unsupported major
  versions.
- Each Hermes runtime variant owns its `ocops` implementation and hides
  upstream Hermes CLI, file layout, and API differences.

## Transport

- `ocops.server` listens as an HTTP service, normally on port `8080`.
- Every route except `GET /healthz` requires
  `Authorization: Bearer ${OC_OPS_TOKEN}`.
- Non-streaming success responses return the domain payload directly as JSON.
- Business failures return:

```json
{"code":"BAD_REQUEST","message":"schedule is required"}
```

- Error responses must match `schema/common/error.schema.json`.
- SSE routes emit `data: <json>\n\n` frames. Business failures inside an SSE
  stream emit an `event: error` frame with the same `{code,message}` shape.

## Error Codes

| Code | Meaning |
|---|---|
| `BAD_REQUEST` | Invalid user input or unsafe path. |
| `UNAUTHORIZED` | Missing or invalid bearer token. |
| `NOT_FOUND` | Requested resource does not exist. |
| `UNSUPPORTED` | Runtime variant does not provide the requested capability. |
| `HERMES_CLI_FAILED` | Upstream Hermes command failed. |
| `INTERNAL` | Adapter could not parse, normalize, or complete the request. |

## Routes

Path parameters are documented in the path template. The Request column
documents query strings, JSON bodies, or multipart form fields when the route
accepts any.

### Core

| Method | Path | Request | Response |
|---|---|---|---|
| `GET` | `/healthz` | - | Plain text `ok`, unauthenticated. |
| `GET` | `/oc/info` | - | `schema/core/info.schema.json` |
| `GET` | `/oc/doctor` | - | `schema/core/doctor.schema.json` |

### Channels

| Method | Path | Request | Response |
|---|---|---|---|
| `GET` | `/oc/channels/{channel}/status` | - | `schema/channel/status.schema.json` |
| `POST` | `/oc/channels/{channel}/unbind` | - | `schema/channel/unbind.schema.json` |
| `POST` | `/oc/channels/{channel}/login` | - | SSE `schema/channel/login-event.schema.json` frames. |

### Cron

| Method | Path | Request | Response schema |
|---|---|---|---|
| `GET` | `/oc/cron/capabilities` | - | `schema/cron/capabilities.schema.json` |
| `GET` | `/oc/cron/status` | - | `schema/cron/status.schema.json` |
| `GET` | `/oc/cron/jobs` | query `schema/cron/request/list-query.schema.json` | `schema/cron/job.schema.json[]` |
| `POST` | `/oc/cron/jobs` | JSON `schema/cron/request/create-body.schema.json` | `schema/cron/job.schema.json` |
| `GET` | `/oc/cron/jobs/{id}` | - | `schema/cron/job.schema.json` |
| `PATCH` | `/oc/cron/jobs/{id}` | JSON `schema/cron/request/update-body.schema.json` | `schema/cron/job.schema.json` |
| `POST` | `/oc/cron/jobs/{id}/toggle` | JSON `schema/cron/request/toggle-body.schema.json` | `schema/cron/job.schema.json` |
| `POST` | `/oc/cron/jobs/{id}/run` | - | `schema/cron/job.schema.json` |
| `DELETE` | `/oc/cron/jobs/{id}` | - | `204 No Content` |
| `GET` | `/oc/cron/jobs/{id}/history` | - | `schema/cron/run-entry.schema.json[]` |
| `GET` | `/oc/cron/jobs/{id}/output` | query `schema/cron/request/output-query.schema.json` | `schema/cron/run-output.schema.json` |

### Kanban

| Method | Path | Request | Response schema |
|---|---|---|---|
| `GET` | `/oc/kanban/capabilities` | - | `schema/kanban/capabilities.schema.json` |
| `GET` | `/oc/kanban/boards` | - | `schema/kanban/board.schema.json[]` |
| `GET` | `/oc/kanban/tasks` | query `schema/kanban/request/task-list-query.schema.json` | `schema/kanban/task.schema.json[]` |
| `POST` | `/oc/kanban/tasks` | JSON `schema/kanban/request/create-body.schema.json` | `schema/kanban/task-detail.schema.json` |
| `GET` | `/oc/kanban/tasks/{id}` | query `schema/kanban/request/board-query.schema.json` | `schema/kanban/task-detail.schema.json` |
| `GET` | `/oc/kanban/tasks/{id}/runs` | query `schema/kanban/request/board-query.schema.json` | `schema/kanban/run.schema.json[]` |
| `GET` | `/oc/kanban/stats` | query `schema/kanban/request/board-query.schema.json` | `schema/kanban/stats.schema.json` |
| `POST` | `/oc/kanban/tasks/{id}/comment` | JSON `schema/kanban/request/comment-body.schema.json` | `schema/kanban/task-detail.schema.json` |
| `POST` | `/oc/kanban/tasks/{id}/complete` | JSON `schema/kanban/request/complete-body.schema.json` | `schema/kanban/task-detail.schema.json` |
| `POST` | `/oc/kanban/tasks/{id}/block` | JSON `schema/kanban/request/block-body.schema.json` | `schema/kanban/task-detail.schema.json` |
| `POST` | `/oc/kanban/tasks/{id}/unblock` | JSON `schema/kanban/request/board-body.schema.json` | `schema/kanban/task-detail.schema.json` |
| `POST` | `/oc/kanban/tasks/{id}/archive` | JSON `schema/kanban/request/board-body.schema.json` | `schema/kanban/task-detail.schema.json` |
| `POST` | `/oc/kanban/tasks/{id}/reassign` | JSON `schema/kanban/request/reassign-body.schema.json` | `schema/kanban/task-detail.schema.json` |
| `POST` | `/oc/kanban/tasks/{id}/reclaim` | JSON `schema/kanban/request/board-body.schema.json` | `schema/kanban/task-detail.schema.json` |
| `GET` | `/oc/kanban/watch` | query `schema/kanban/request/board-query.schema.json` | SSE `schema/kanban/event.schema.json` frames. |

### Skills

| Method | Path | Request | Response |
|---|---|---|---|
| `GET` | `/oc/skills` | - | `schema/skills/list.schema.json` |
| `POST` | `/oc/skills` | multipart `schema/skills/request/install-multipart.schema.json` | `schema/skills/action.schema.json` |
| `POST` | `/oc/skills/reload` | - | `schema/skills/reload.schema.json` |
| `DELETE` | `/oc/skills/{name}` | - | `schema/skills/action.schema.json` |

### Conversations

会话端点：oc-ops 做带 per-app token 的透传，将请求转发到同 pod 内的 hermes
api_server（`/api/sessions/*`）。会话数据不在 oc-ops 落地。

| Method | Path | Request | Response |
|---|---|---|---|
| `GET` | `/oc/conversations` | query `source`, `limit`, `offset` | 会话列表数组 |
| `POST` | `/oc/conversations` | JSON body（`source`/`title`） | 新建会话对象，`201 Created` |
| `GET` | `/oc/conversations/{sid}/messages` | - | 历史消息数组 |
| `POST` | `/oc/conversations/{sid}/chat` | JSON body（`message`） | 单轮续聊 assistant 回复对象 |
| `DELETE` | `/oc/conversations/{sid}` | - | `204 No Content` |

## Schema Layout

- `schema/common/*.schema.json` defines shared payloads such as errors.
- `schema/core/*.schema.json` defines info and doctor payloads.
- `schema/channel/*.schema.json` defines channel binding and login payloads.
- `schema/cron/*.schema.json` defines cron domain payloads.
- `schema/kanban/*.schema.json` defines kanban domain payloads.
- `schema/skills/*.schema.json` defines skill management payloads.
- `schema/*/request/*.schema.json` defines query strings, JSON bodies, or
  multipart form fields accepted by mutating and filtered routes.
- `envelope.schema.json` files are retained for backward compatibility with
  earlier CLI-era tests and docs, but `ocops.server` HTTP responses use direct
  payloads plus HTTP status codes.
