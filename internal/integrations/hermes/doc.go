// Package hermes 提供 manager 端与 hermes 容器之间的中性集成：
//   - Manifest 结构与 YAML 序列化（manifest.go）
//   - 写入 input/manifest.yaml + resources/*.md 的 WriteAppInput（app_input.go）
//   - 占位符替换工具 RenderRuleText / RenderPersonaText（prompt.go）
//   - 通过 docker exec 触发容器对外命令的 RunInfo / RunDoctor / RunChannelLogin
//     等封装（commands.go）
//   - 微信渠道扫码命令的 docker exec 流式封装 WeixinRunner（wechat_runner.go）
//
// 所有 hermes 内部 schema（config.yaml / SOUL.md / skills/kb-*）的渲染
// 已下沉到镜像内 oc-entrypoint；manager 只写「中性输入」。
package hermes
