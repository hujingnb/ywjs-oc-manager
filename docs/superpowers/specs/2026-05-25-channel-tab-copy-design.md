# 实例渠道 Tab 文案修正设计

## 背景

实例详情页的渠道 tab 直接展示了部分后端状态原值，例如 `bound`。页面还会在没有二维码或验证码 challenge 时显示“尚未发起挑战”，该文案暴露了内部流程概念，用户难以判断当前到底是未绑定、已绑定，还是二维码正在生成。

## 目标

- 渠道状态在页面上使用中文业务文案展示，`bound` 展示为“已绑定”。
- 去掉“尚未发起挑战”这类内部术语空态；没有二维码或验证码时，由渠道 tab 的状态行和等待提示表达当前状态。
- 顺手检查同类状态原值回退，优先处理渠道绑定页内的 `pending_auth`、`failed`、`expired`、`unbound_by_user` 等状态。

## 范围

本次只调整前端展示层：

- `web/src/pages/apps/AppChannelsTab.vue`：渠道状态文案映射、challenge 展示条件。
- `web/src/components/AuthChallengeRenderer.vue`：无 challenge 时不再输出默认“尚未发起挑战”文案。
- 渠道相关单元测试：覆盖状态映射与无 challenge 展示边界。

不改后端状态机、API 契约、OpenAPI 产物或渠道绑定业务流程。

## 方案

在渠道 tab 内收敛渠道状态到中文文案的映射：

- `unbound`：未绑定
- `pending_auth`：等待扫码授权
- `bound`：已绑定
- `failed`：绑定失败
- `expired`：二维码已过期
- `unbound_by_user`：已解绑
- `deleted`：已删除

未知状态使用“未知状态：<原值>”，避免后端新增状态时前端静默误导。

`AuthChallengeRenderer` 只负责渲染真实 challenge：二维码、验证码、未知 challenge 类型。父组件在 `visibleChallenge` 为空时不再展示该组件，因此未绑定、已绑定或等待生成时不会出现“尚未发起挑战”。

## 测试

- 增加或更新渠道 tab / 纯函数测试，断言 `bound` 等状态映射为中文。
- 覆盖无 challenge 时不显示“尚未发起挑战”。
- 运行前端相关测试，优先运行受影响的 Vitest 用例。
