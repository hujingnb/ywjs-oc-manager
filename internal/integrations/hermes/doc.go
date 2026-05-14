// Package hermes 提供 manager 与 Hermes Agent runtime 镜像的协议封装。
//
// 该包替代 internal/integrations/openclaw,承担以下职责:
//   - prompt.go: 渲染 SOUL.md(Hermes 启动时注入 system prompt 的 agent identity 文档)
//   - config.go: 渲染 config.yaml(model provider)与 .env(凭证)
//   - skills.go: 渲染知识库文档为 Hermes skills 目录(SKILL.md frontmatter + 正文)
//   - wechat_runner.go: docker exec 调用 oc-weixin-login.py + stdcopy 分流 stdout/stderr
//
// 所有 Render* 函数均返回字节内容,不直接写文件;真正落盘由 worker handler 负责。
package hermes
