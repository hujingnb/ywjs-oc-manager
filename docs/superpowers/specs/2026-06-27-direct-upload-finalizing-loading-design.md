# 直传 100% 后补齐「处理中」loading + 文案统一设计

- 日期：2026-06-27
- 范围：前端（`web/`），知识库文件上传进度反馈
- 类型：体验缺陷修复 + 文案统一

## 背景与问题

知识库文件上传分两条路径（`web/src/api/knowledgeUpload.ts`）：

- **大文件（≥8MB）走分片上传**：字节传到 100% 后调 `onFinalizing()`，store 置 `currentFinalizing=true`，弹窗显示「合并中…」+ 进度条转圈，等服务端 `complete`（合并分片 + 推送 RAGFlow）返回才算成功。**这条路径已经有 loading。**
- **小文件（<8MB）走直传**：单个 `octet-stream` XHR 请求。`xhr.upload.onprogress` 上报的是请求体上传进度，字节传到 100% 时请求体已发完，但服务端仍在同步把文件推给 RAGFlow，要等 `xhr.onload`（响应回来）才结束。**这中间的几秒，进度条停在 100% 显示「X MB / Y MB」，没有任何 loading 动画 —— 用户以为卡死。**

此外，现有「合并中…」文案是实现视角（用户不理解「合并」指什么），不够直观。

## 目标

1. 直传（<8MB）在字节传完、等待服务端处理期间，复用现有 finalizing 机制显示 loading（进度条转圈 + 文案 + 隐藏取消按钮），消除「卡在 100%」错觉。
2. 把 finalizing 阶段文案从实现视角的「合并中…」统一为用户视角的「处理中…」（英文 `Processing…`），两条路径共用。

非目标：

- 不改弹窗显示「成功」之后、文件在知识库列表里 RAGFlow 解析（queued/running）阶段的展示——那是列表页 5 秒轮询的独立机制，本次不动。
- 不重命名 `finalizing` / `currentFinalizing` / `isFinalizing` 等标识符（仅改展示文案与相关注释），避免无谓改名波及测试与多处引用。

## 方案（已选定方案 A）

直传探测「请求体已发完、开始等服务端」这一时刻，用 `xhr.upload.onload` 事件——浏览器在请求体发送完成时精确触发，是该时刻最权威的信号，优于依赖 `loaded===total` 终态进度事件的判断。

## 改动清单

### 1. `web/src/api/xhrUpload.ts`

- `XhrUploadOptions` 新增可选字段 `onUploadComplete?: () => void`，注释说明：请求体发送完成（字节已全部上传、等待服务端响应）时触发，调用方据此进入「处理中」反馈。
- 在 `xhr.send` 前绑定 `xhr.upload.onload = () => opts.onUploadComplete?.()`。
- 零回归：不传该回调的两处既有调用（分片 PUT、其它上传）行为不变。

### 2. `web/src/api/knowledgeUpload.ts`

- `directUpload` 新增 `onFinalizing?: () => void` 参数，向 `xhrUpload` 传 `onUploadComplete: onFinalizing`。
- `uploadKnowledgeFile` 把 `onFinalizing` 传给直传主路径调用；并传给「分片不可用（503）回退直传」分支的 `directUpload` 调用，保证回退场景也有 loading。
- 订正 `uploadKnowledgeFile` 顶部文档注释：直传现在也会在字节传完后触发 `onFinalizing`（删去「直传不调，直传的 100% 仍是上传中」的旧描述，改为说明直传在请求体发完、等服务端响应期间触发）。

### 3. i18n 文案

- `web/src/i18n/locales/zh/components.ts`：`finalizing: '合并中…'` → `finalizing: '处理中…'`，并订正其上方注释（不再局限「分片上传/合并阶段」，改为「上传字节已传完、服务端处理期间的提示」）。
- `web/src/i18n/locales/en/components.ts`：`finalizing: 'Merging…'` → `finalizing: 'Processing…'`，同步订正注释。

### 4. 注释订正（保持注释与行为一致，CLAUDE.md 要求）

- `web/src/stores/uploadProgress.ts`：`UploadSession.currentFinalizing` 字段注释、`RunnerContext.onFinalizing` 注释里「分片上传 complete 期间」「仅分片上传用，直传不调」改为涵盖直传（字节传完、服务端处理期间）。
- `web/src/components/UploadProgressModal.vue`：模板内「合并阶段」「合并中」相关注释与 `isFinalizing` 计算属性注释，文案描述更新为「处理中」，并说明两条路径都会进入此态。

### 5. 单元测试

- `web/src/api/xhrUpload.spec.ts`：新增用例——上传请求触发 `xhr.upload` 的 `load` 事件时，`onUploadComplete` 被调用一次；不传该回调时不报错。
- `web/src/api/knowledgeUpload.spec.ts`：
  - 新增「直传文件请求体发完后触发 `onFinalizing`」用例。
  - 「分片不可用回退直传」用例补充：回退后的直传同样会触发 `onFinalizing`。
- 视情况检查 `web/src/stores/uploadProgress.spec.ts` / `UploadProgressModal.spec.ts` 是否硬编码了「合并中…」文案断言，若有则改为「处理中…」。

每个新增测试方法/子用例都按项目规范补充相邻中文注释，说明覆盖的场景与边界。

## 数据流（直传，方案 A 后）

```
directUpload → xhrUpload(POST octet-stream)
  ├─ upload.onprogress: onProgress(loaded, total)  → store.currentLoaded（0→100%）
  ├─ upload.onload:      onUploadComplete()=onFinalizing() → store.currentFinalizing=true
  │                       → 弹窗：进度条转圈 + 「处理中…」+ 隐藏取消按钮
  └─ xhr.onload(2xx):    Promise resolve → store item.status='succeeded'
                          → 弹窗切到汇总视图「成功 N」
```

store / 弹窗 / 大文件分片路径无需新增状态，复用既有 `currentFinalizing` 链路。

## 验证

- `make web-test`（或前端单测命令）跑通新增/改动用例。
- 浏览器真机验证（CLAUDE.md 要求）：本地 k3d 环境，分别上传一个 <8MB 小文件与一个 ≥8MB 大文件，确认：
  1. 小文件进度到 100% 后出现「处理中…」转圈，取消按钮消失，随后切到「成功」；
  2. 大文件 finalizing 阶段文案为「处理中…」（不再是「合并中…」）；
  3. 中英文切换下文案分别为「处理中…」/`Processing…`。

## 影响范围与风险

- 仅前端；后端、API 契约、store 状态结构均不变（`onUploadComplete` 与 `onFinalizing` 都是可选新增）。
- `xhrUpload` 为三处上传共用，但新增回调可选且默认无操作，未传处行为不变，回归风险低。
