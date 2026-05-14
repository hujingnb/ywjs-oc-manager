# Hermes 集成契约

本文件汇总 manager 与 Hermes Agent 上游(NousResearch/hermes-agent)集成的关键约定,
便于离线审阅。

## 上游版本

- 上游仓库:`https://github.com/NousResearch/hermes-agent`
- 安装方式:`curl -fsSL https://hermes-agent.nousresearch.com/install.sh | bash -s -- --skip-setup`
- 锁定版本:见 `runtime/hermes/version.txt`

## 容器入口

| 项 | 值 |
|---|---|
| ENTRYPOINT | `tini -g -- hermes` |
| CMD | `gateway run` |
| 监听端口 | 无,Hermes gateway 出站长轮询 iLink API |
| HEALTHCHECK | `/usr/local/bin/oc-healthcheck`(内部 `hermes gateway status`,退出码 0 = healthy) |
| start-period | 60s |

## 容器内目录约定

- `HERMES_HOME=/opt/data` —— Hermes 主数据目录(挂载点)
- `/opt/data/config.yaml` —— model provider + auxiliary 配置(manager 写入)
- `/opt/data/.env` —— 凭证(OPENAI_API_KEY + WEIXIN_*)
- `/opt/data/SOUL.md` —— agent identity / system prompt(manager 写入)
- `/opt/data/skills/kb-<scope>-<slug>/SKILL.md` —— 知识库映射(manager 写入)
- `/opt/data/workspace/` —— agent 工作目录(Hermes 自动)
- `/opt/data/sessions/` —— 会话记录(Hermes 自动)
- `/opt/data/logs/` —— 日志(Hermes 自动)

## 微信渠道扫码

`/usr/local/bin/oc-weixin-login` 由 manager 通过 docker exec 调用:
- stdout 单行 JSON: `{"account_id":"<hex>@im.bot","token":"<...>","base_url":"<...>","user_id":"<...>"}`
- stderr 单行 URL: `https://liteapp.weixin.qq.com/q/<id>?qrcode=<token>&bot_type=3`
- exit code 0 = 登录成功;exit 2 = 超时或失败

manager 端实现位置:`internal/integrations/hermes/wechat_runner.go`
