# OpenClaw 集成契约（git 内副本）

完整契约（含 POC 原始日志样本与决策依据）：
`docs/superpowers/specs/2026-05-02-openclaw-integration-contract.md`（gitignored）。

本副本同步关键代码相关约定，便于离线审阅。

## 上游版本

- npm 包：`openclaw`（github.com/openclaw/openclaw）
- 锁定版本：见 `runtime/openclaw/version.txt`
- Sprint 0 实测使用 `2026.4.29`（content commit `a448042c`）
- 微信插件：`@tencent-weixin/openclaw-weixin@2.3.1`（来自 clawhub）
- 安装方式：上游官方 `install.sh --no-onboard --no-prompt`

## 容器入口

| 项 | 值 |
|---|---|
| ENTRYPOINT | （无；CMD 直接 exec） |
| CMD | `openclaw gateway --allow-unconfigured --bind lan` |
| 监听端口 | 18789（HTTP；含 Control UI / `/healthz` / `/readyz`） |
| HEALTHCHECK | `curl -fsS http://127.0.0.1:18789/healthz`，期待 200 + `{"ok":true,"status":"live"}` |
| start-period | 60s（plugin 加载约 11s + buffer） |

## 微信登录命令（manager docker exec 调用）

```text
[openclaw, channels, login, --channel, openclaw-weixin, --verbose]
```

**没有 `--json` 标志**——上游 `channels login` 不支持。manager parser 通过 stdout 文本扫描识别协议事件。

参考实现：`internal/integrations/channel/wechat_runner.go`。

## 二维码协议

stdout 真实输出（按行）：

```text
[plugins] loading <name> from /root/.openclaw/...   ← noise
（118 plugins 加载约 11s）
[plugins] loaded 118 plugin(s) (70 attempted) in 11035.8ms
正在启动...
用手机微信扫描以下二维码，以继续连接：
（21 行 unicode block 字符 ASCII QR）
若二维码未能显示或无法使用，你可以访问以下链接以继续：
https://liteapp.weixin.qq.com/q/<id>?qrcode=<token>&bot_type=3
正在等待操作...
```

manager parser 黄金用例：`internal/integrations/openclaw/parser_test.go`。

事件类型映射：

| 上游 stdout 行 | manager event.Type |
|---|---|
| 含 `qrcode=<token>` 的 URL 行 | `qrcode`（QRCode 字段 = 整 URL） |
| `正在等待操作` / `等待扫描` / `扫描成功，请在手机上确认` | `pending` |
| `已将此 OpenClaw 连接到微信。` / `Connected this OpenClaw to WeChat` | `bound`（**stdout 不携带 wxid**；service 层须读 plugin state 补 bound_identity） |
| `二维码已过期` / `已失效` | `expired` |
| `认证失败` / `登录失败` / `Error:` | `failed`（Error 字段 = 整行） |
| 其它（plugin loading / ASCII QR / 中文提示行） | unparsable（调用方 skip） |

二维码默认有效期：5 分钟（上游无显式 `expires_at`）。

## 挂载约定（Sprint 1 容器创建会用）

| 容器内 | 节点路径 | 模式 | 说明 |
|---|---|---|---|
| `/knowledge/org` | `apps/{app_id}/../orgs/{org_id}/knowledge` | ro | 组织级知识库 |
| `/knowledge/app` | `apps/{app_id}/knowledge` | ro | 应用级知识库 |
| `/workspace` | `apps/{app_id}/workspace` | rw | OpenClaw 输出 |
| `/state` | `apps/{app_id}/state` | rw | OpenClaw 内部状态 |
| `/logs` | `apps/{app_id}/logs` | rw | 可选日志 |

注意：上游 OpenClaw 默认 workspace = `/home/node/.openclaw/workspace` 或 `/root/.openclaw/workspace`（取决于 USER）。
manager 容器创建时通过环境变量或 `openclaw config set agents.defaults.workspace /workspace` 改默认目录。

知识库映射方式（OpenClaw 无原生 knowledge 概念）Sprint 1 决策；候选：system prompt 注入小文件 + bind mount 大文件让 agent 自己 file read。

## 微信账号标识获取（实测）

`channels login` 完成后**stdout 不携带 wxid / userId**。真实账号信息持久化在：

```text
/root/.openclaw/openclaw-weixin/accounts.json   # 列表：[ "<account-name>" ]
/root/.openclaw/openclaw-weixin/accounts/<account-name>.json
{
  "token": "<sensitive>",
  "savedAt": "2026-05-02T15:00:22.500Z",
  "baseUrl": "https://ilinkai.weixin.qq.com",
  "userId": "<openid>@im.wechat"
}
```

manager service 层收到 bound 事件后必须做以下之一：

1. 通过 docker exec 跑 `openclaw channels list --json`（待验证是否输出 JSON）
2. 经 agent 文件 API 读 `accounts/<account-name>.json` 取 `userId`
3. （首选 1，2 作为 fallback）

绑定后 `openclaw channels list` 输出的关键行：
```text
- openclaw-weixin default: configured, enabled
```
仅说明 enabled，不含 userId。所以方案 2（读 plugin state 文件）是更可靠的来源。

`channel_bindings.bound_identity` 写入的就是 `userId`（如 `o9cq800xszCM8jyoS9YpRKpvAN9c@im.wechat`）。

## 必需环境变量（容器创建时由 manager 注入）

| 变量 | 用途 |
|---|---|
| `OPENAI_API_KEY` | new-api 给应用创建的 api_key（OpenClaw 内置 `openai@^6.34.0` SDK 识别） |
| `OPENAI_BASE_URL` | new-api base URL + `/v1`（如 `http://new-api:3000/v1`） |
| `OPENCLAW_WORKSPACE_DIR` | manager 假设的 workspace 路径（建议 `/workspace`），需配合 `agents.defaults.workspace` 配置 |
| `OPENCLAW_KNOWLEDGE_ORG_DIR` | `/knowledge/org` |
| `OPENCLAW_KNOWLEDGE_APP_DIR` | `/knowledge/app` |
| `OPENCLAW_DISABLE_BONJOUR` | `1`（容器场景禁用 mDNS 广播，docker 默认推荐） |

## 已知风险（Sprint 0 输出，留 v1 文档）

1. **wechat plugin manifest 缺 `channelConfigs` 元数据**：上游 plugin 自身 bug，不影响 `channels login` 命令本身可执行；`channels list` 不显示 wechat 是表象，可忽略。
2. **CLI 每次 exec 重新加载 118 个 plugin（约 11s）**：manager BeginAuth 必须放宽 timeout，建议 30s+。
3. **wechat 渠道有合规风险**：私人微信号扫码登录是逆向协议，非腾讯官方授权。商用 v1 文档需声明"微信渠道不在 SLA 范围"。
