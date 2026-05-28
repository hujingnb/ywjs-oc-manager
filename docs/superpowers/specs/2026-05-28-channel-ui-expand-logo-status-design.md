# 实例渠道 Tab 扩展渠道、全彩 Logo 与状态合并设计

## 背景

实例详情页渠道 tab 已在 [`2026-05-26-channel-list-logo-design.md`](./2026-05-26-channel-list-logo-design.md)
中落地「左侧渠道列表 + 右侧当前渠道详情」布局，列出微信、企业微信、飞书、钉钉四个渠道，
其中仅微信可操作，其余置灰为「暂不支持」预告。当前实现存在三点待改进：

1. 预告渠道清单偏少，缺少 Telegram、WhatsApp 等海外主流渠道，无法完整表达产品的渠道规划边界。
2. 渠道 logo 用「品牌色圆角方块 + 白色汉字」（`微`/`企`/`飞`/`钉`）这种简化标识，
   辨识度低、不够正式。
3. 右侧详情区头部把「当前渠道」（渠道名）和「当前状态」拆成上下两块，信息密度低、占用纵向空间。

本次在不改动任何后端能力的前提下，扩展预告渠道清单、把 logo 升级为全彩官方品牌标识、
并把状态信息并入渠道名同一行。

## 目标

- 渠道列表在现有 4 个基础上新增 5 个预告渠道：Telegram、WhatsApp、Discord、Slack、Line，
  全部为 `supported: false` 的灰色「暂不支持」项。
- 渠道 logo 由「彩色方块 + 汉字」升级为「浅色底方块 + 全彩官方品牌 SVG」；
  未支持渠道的 logo 以灰度（grayscale）呈现。
- 右侧详情区头部把「当前状态」并入「当前渠道」那一行，呈现为「渠道名 · 状态」同行文字，
  去掉「当前渠道」「当前状态：」两个 kicker 文案。

## 非目标

- 不新增任何渠道的后端 adapter、API、状态机或绑定流程；不修改 `internal/domain/enums.go`、
  数据库 `channel_type` 约束、`channel_service.go` 或 Hermes runtime。
- 不修改 OpenAPI 契约（`openapi/openapi.yaml`）和前端生成类型（`web/src/api/generated.ts`）。
- 不引入远程图片依赖，不做运行时品牌资源下载；全部 logo 以本地内联 SVG 提供。
- 不改变微信渠道既有的登录、二维码展示、刷新、解绑和状态轮询逻辑。
- 不改变 `activeChannel` 固定为微信、`selectChannel` 对未支持渠道 no-op 的行为。

## 影响范围

仅前端，集中在：

- `web/src/pages/apps/AppChannelsTab.vue` —— 渠道清单、logo 渲染、头部布局。
- 新增 `web/src/pages/apps/ChannelLogo.vue`（或就近的 `components/`）—— 内联品牌 SVG 组件。
- `web/src/pages/apps/AppChannelsTab.spec.ts` —— 同步断言。

## 交互与视觉设计

沿用现有「左侧渠道列表 + 右侧当前渠道详情」布局。

### 渠道列表（左侧）

固定按以下顺序展示 9 个渠道：

| 顺序 | 渠道 | `type` | 是否支持 | 状态标签 | 描述文案 |
|---|---|---|---|---|---|
| 1 | 微信 | `wechat` | 是 | 已支持 | 扫码绑定后接收助手消息 |
| 2 | 企业微信 | `work_wechat` | 否 | 暂不支持 | 企业内部协作场景 |
| 3 | 飞书 | `feishu` | 否 | 暂不支持 | 团队消息与工作台场景 |
| 4 | 钉钉 | `dingtalk` | 否 | 暂不支持 | 组织通讯与审批场景 |
| 5 | Telegram | `telegram` | 否 | 暂不支持 | 海外即时通讯与 Bot 接入场景 |
| 6 | WhatsApp | `whatsapp` | 否 | 暂不支持 | 海外用户触达与客服场景 |
| 7 | Discord | `discord` | 否 | 暂不支持 | 社区与游戏玩家场景 |
| 8 | Slack | `slack` | 否 | 暂不支持 | 团队协作与工作流场景 |
| 9 | Line | `line` | 否 | 暂不支持 | 日本与东南亚用户场景 |

未支持渠道保持禁用：`disabled`、`aria-disabled="true"`、点击不切换详情、不发起任何请求。

### Logo 呈现

- 每个 logo 放在浅中性色（如 `#f5f6f7`）圆角方块内居中，列表项 36px 方块内约 22px logo，
  详情区 large 44px 方块内约 28px logo，保持列表对齐与视觉重量一致。
- 已支持渠道展示全彩官方品牌 SVG；未支持渠道对同一 SVG 施加 `filter: grayscale(1)` 并降低
  不透明度，呈灰度预告态。
- 注意：当前仅微信 `supported: true`，因此实际上**只有微信显示全彩**，其余 8 个（含已有的
  企业微信/飞书/钉钉）均为灰度。右侧详情区始终指向微信，故详情区 logo 恒为全彩。
- logo 取各品牌官方 brandmark 与官方配色（商标合规），以本地内联 SVG 提供，不依赖远程资源，
  便于单元测试稳定断言，也避免远程加载失败影响页面。

### 头部状态合并

详情区头部由「logo + `当前渠道` kicker + 渠道名」+ 下方单独「`当前状态：xxx`」一行，
合并为单行：

```
[logo] 微信 · 已绑定                    [发起登录] [刷新二维码] [解绑]
```

- 删除 `<p class="channel-title-kicker">当前渠道</p>` 和原独立的 `<p class="state-text">当前状态：…</p>`。
- 标题行为「渠道名（h3）+ 间隔点 `·` + 状态文字」，状态用次要文字色与渠道名区分；状态为纯文字，
  不使用彩色标签。
- 以下信息仍保留在头部下方，逐行展示（与现状一致）：
  - 已绑定身份：`已绑定：{bound_identity}`（仅 `bound_identity` 存在时）。
  - 最近错误：`最近错误：{error_message}`（danger 文案，仅 `error_message` 存在时）。
  - 等待挑战 / 二维码过期提示。
  - `AuthChallengeRenderer` 二维码或验证码渲染。

## 架构设计

### 渠道展示模型

复用现有 `ChannelDisplay` 纯前端模型，做两处调整：

- `type` 联合类型扩展为：
  `'wechat' | 'work_wechat' | 'feishu' | 'dingtalk' | 'telegram' | 'whatsapp' | 'discord' | 'slack' | 'line'`。
- 去掉 `logoText`（汉字）字段；logo 不再由文字驱动，改为按 `type` 取对应 SVG。
- 保留一个稳定的 class 钩子 `channel-logo--{type}`（如 `channel-logo--wechat`），用于 CSS 定位
  和单元测试断言；原 `logoClass`（`wechat`/`work-wechat` 等品牌色类）不再承担背景色职责，
  可并入该钩子或保留为 type 派生值。

`channels` 数组从 4 项扩为 9 项，新增 5 项均为 `supported: false`、`statusLabel: '暂不支持'`。

### Logo 组件（方案 A：内联 SVG 组件）

新增 `ChannelLogo.vue`，接收 `type` 与 `size`（或 large 布尔），内部按 `type` 渲染对应内联
`<svg>`。选择内联组件而非独立 `.svg` 资源文件的原因：

- 零额外网络请求，9 个 logo 不产生 9 个图片请求。
- 灰度与尺寸完全由 CSS（`filter`、`width/height`）控制，未支持态切换最简单。
- 与项目单文件组件习惯一致，类型安全，测试可直接断言 SVG 存在。

`AppChannelsTab.vue` 在列表项和详情区头部均通过 `ChannelLogo` 渲染，未支持态由父级根据
`supported` 加 `muted`/灰度 class。

### 编排逻辑不变

`channelType` 默认值仍为 `wechat`，`activeChannel` 仍始终落在微信，`selectChannel` 对
`supported: false` 渠道继续 no-op。新增渠道仅影响列表展示，不参与任何 API 参数或后端状态机。
后续若要真正接入某渠道，只需把对应配置改为 `supported: true` 并补齐后端 adapter / API，本次
不预留动态注册机制。

## 数据流

页面加载后流程与现状一致，仅列表条目数变化：

1. 左侧根据本地 9 项渠道配置同步渲染。
2. 右侧按现有逻辑调用 `useChannelProgressQuery(appId, 'wechat')` 查询微信进度。
3. 状态经 `formatChannelStatus(progress.status)` 映射为中文，渲染在头部「渠道名 · 状态」行。
4. 「发起登录 / 刷新二维码」仍调用微信渠道 `useBeginChannelAuth`；「解绑」仍调用
   `useUnbindChannel`。
5. 未支持渠道不触发任何查询、登录或解绑 mutation。

## 错误处理

- 微信渠道错误沿用现有 `progress.error_message` 与二维码过期提示，位置移到合并后头部的下方区域。
- 未支持渠道无后端请求，不产生接口错误；通过「暂不支持」「灰度 logo」「禁用」表达不可用原因。
- logo 为本地内联 SVG，不存在加载失败场景。

## 测试计划

更新 `web/src/pages/apps/AppChannelsTab.spec.ts`：

- 渠道数量断言由 `4` 改为 `9`；有序名单补充 Telegram、WhatsApp、Discord、Slack、Line。
- 已支持数仍为 `1`（微信）；未支持数由 `3` 改为 `8`，并断言新增项均 `aria-disabled="true"`、
  `disabled`、文案含「暂不支持」。
- logo 断言由旧的品牌色类（`.channel-logo.wechat` 等）改为新的稳定钩子
  （`.channel-logo--wechat` 等），并校验 SVG 渲染存在。
- 已绑定用例：原 `当前状态：已绑定` 断言改为断言详情区头部含 `微信 · 已绑定`；保留「不泄露
  后端原值（不出现裸 `bound`）」与「不显示 challenge 空态」的断言并相应调整文案前缀。
- 新增断言：Telegram / WhatsApp 等新渠道渲染且为未支持态；未支持渠道 logo 带灰度标记
  （class 或包裹 `muted`）。
- 运行受影响的前端 Vitest 用例（`npm run test` / `vitest`）；如改动 TS 类型，跑
  `npm run typecheck`。

## 交付前检查

- 渠道顺序、名称、描述文案与本设计表格一致。
- 全彩 logo 仅微信生效、其余灰度，详情区微信全彩，符合视觉预期（需用真实浏览器核对，
  curl/API 无法验证前端视觉）。
- 头部为「渠道名 · 状态」单行，绑定身份 / 错误 / 二维码等提示仍在下方正常展示。
- 不含后端、OpenAPI、生成类型改动，工作区无无关文件。
