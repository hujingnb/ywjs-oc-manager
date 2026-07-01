# 对话语音输入设计

- 日期：2026-07-02
- 分支：`feat/conversation-voice-input`（本特性明确不在 `master` 上开发，覆盖 AGENTS.md「默认在 master」约定，因用户明确要求拉新分支）
- 范围：纯前端特性，**零后端改动、不上传录音**

## 背景与目标

给对话页输入框增加「语音输入」能力：用户按下麦克风录音，识别成文字后**填入草稿供编辑**，再手动发送。

现状调查结论：

- 对话页 `web/src/pages/apps/AppConversationsTab.vue`，用 Naive UI 的 `n-input` textarea 作输入框（第 172–179 行）。
- 发送/排队逻辑在 `onComposerSubmit`（第 481 行）+ `drainQueue`（第 514 行），排队模型在 `web/src/domain/messageQueue.ts`。
- 项目中**完全不存在**任何录音/语音识别代码或依赖。
- 技术栈：Vue 3.5 + Vite 7 + Naive UI 2.43 + Pinia + vue-i18n；**无 CSP 限制、无 Web Worker、无 CDN 约束**。
- 多模态已支持文件上传（`uploadConversationFile` + `ConversationPart[]`）。

## 关键决策（已与用户确认）

| 决策点 | 结论 | 取舍 |
|---|---|---|
| 识别位置 | **纯浏览器本地**（Transformers.js + whisper），录音不出浏览器 | 隐私最好、零后端；中文准确率受限于模型档位 |
| 模型档位 | **用户自选** tiny/base/small，带下载进度，可切换 | 让用户在准确率与下载体积间自行权衡 |
| 交互 | **点击切换录音**，结果**填入草稿**供编辑 | 最稳健、容错好，与现有发送/排队逻辑解耦 |
| 模型下载源 | **用户可选源站，默认国内镜像** `hf-mirror.com`；另有官方与自定义地址 | 大陆 `huggingface.co` 通常被墙/极慢；默认走国内镜像 |

已排除方案：后端 STT（与「录音不上传」冲突）、Web Speech API（Chrome 会把音频上传 Google，违背隐私前提，且 Firefox 不支持）。

## 总体架构与模块边界

新增前端 feature 目录 `web/src/features/voiceInput/`，内部严格分层，各单元单一职责、可独立测试：

| 单元 | 职责 | 依赖 |
|---|---|---|
| `audioDecode.ts` | 纯函数：录音 Blob → 16kHz 单声道 `Float32Array`（whisper 输入要求），用 `OfflineAudioContext` 重采样 | 浏览器 Web Audio |
| `voiceSettings.ts` | 源站清单 + 模型档位清单 + 两者的 `localStorage` 持久化读写 | 无 |
| `speechRecognizer.worker.ts` | **Web Worker**：按所选源站/档位加载 Transformers.js pipeline、下载模型（吐进度）、收 PCM 返回文本 | Transformers.js |
| `speechRecognizerClient.ts` | 主线程包装 worker 的 postMessage → Promise + 进度回调 | worker |
| `useVoiceRecorder.ts` | composable：`getUserMedia` + `MediaRecorder`，start/stop，出 Blob，管麦克风权限 | 浏览器 |
| `useVoiceInput.ts` | **编排器** composable：把录音器+识别器+设置串成一个状态机，暴露给组件 | 上面全部 |
| `VoiceInputButton.vue` | 麦克风按钮 + 状态视觉 | `useVoiceInput` |
| `ModelPickerPopover.vue` | 选下载源 + 模型档位 + 下载进度 | `useVoiceInput` |

**为什么用 Web Worker**：WASM/WebGPU 推理很重，跑在主线程会冻结整个界面（输入框卡死）。项目目前没有 worker，这是引入它的正当场景。

**接入点**：`AppConversationsTab.vue` 的 composer 区（现在 attach/send 按钮旁）加一个麦克风按钮；识别完成后把文本**追加到 `draft`**，不碰现有 `onComposerSubmit`/`drainQueue` 排队逻辑——两者完全解耦。

## 交互与 UI 状态

单个麦克风按钮驱动一个状态机：

```
idle →(点击)→ requesting-permission → recording（脉冲动效 + 计时秒数）
     →(再点击)→ decoding → transcribing（转圈）→ 回 idle 并把文本填入草稿
```

- 麦克风按钮旁有个小「模型」入口（caret/齿轮）打开 `ModelPickerPopover`。
- **首次点击麦克风且未选过模型** → 自动弹出模型选择 → 选源站+档位 → 下载进度条（`downloading N%`）→ 缓存完成后才开始录音。
- 已选模型后再点直接录音；模型已被浏览器缓存（Transformers.js 走 Cache API），**同一浏览器同一源站+档位只下一次**。
- 想换源站或档位 → 从模型入口重选 → 触发对应模型下载。

## 模型选择、下载源与缓存

模型选择 popover 里放**两个控件**：

- **下载源**（默认国内）：
  - `国内镜像 hf-mirror.com`（默认）
  - `HuggingFace 官方 huggingface.co`
  - `自定义地址`（用户填自己的镜像/CDN URL）
- **模型档位**：`Xenova/whisper-tiny`(~40MB) / `whisper-base`(~80MB) / `whisper-small`(~250MB)，量化权重（dtype q8/fp16）压体积。

实现要点：

- Transformers.js `env.allowLocalModels=false`；`env.remoteHost` 在 worker 初始化时按所选源站动态设置。
- 优先 WebGPU（`device:'webgpu'`），不支持则退回**单线程 WASM**——刻意不用多线程 WASM，因为那需要 COOP/COEP 跨源隔离头，会和从镜像跨域拉模型冲突，也会牵动整站部署。单线程慢一些但零部署风险。
- 所选源站 + 档位存 `localStorage`，跨会话记住；换源/换档位各自缓存独立（浏览器 Cache API 按 URL 区分）。

**风险记录**：默认国内镜像依赖第三方站点（hf-mirror.com）可用性；镜像限速或下线时，用户可切换官方源或填自定义地址。

## 录音 → 识别数据流

1. 点麦克风 → 确认模型就绪（否则先走下载）→ `getUserMedia({audio})` → `MediaRecorder` 录制。
2. 再点 → 停录得 Blob → `audioDecode` → 16kHz 单声道 Float32。
3. postMessage 给 worker → `pipeline(audio, {task:'transcribe', language})` → 文本。
4. `language` 默认跟随当前 UI 语言（zh→中文 / en→英文，提升准确率），可回退自动检测。
5. 文本追加进 `draft`（草稿非空则以空格/换行拼接），光标交回用户编辑。

## 错误处理与边界

- 麦克风权限被拒 → toast 提示 + 回 idle。
- 非安全上下文（http 非 localhost，`getUserMedia` 不可用）→ 按钮禁用 + 提示（生产是 https，安全）。
- 浏览器不支持 `MediaRecorder`/`AudioContext`/WASM → 特性检测，直接隐藏按钮。
- 模型下载失败（镜像挂/断网）→ 错误 toast + 可重试，保留上一个可用档位。
- 自定义地址不可用/格式错 → 下载失败 toast + 允许改回默认源重试。
- 未识别到语音（空结果）→ toast「未识别到语音」，不填草稿。
- 录音与「任务进行中消息排队」互不影响：语音只填草稿，发送时机仍由用户/现有队列决定。

## 国际化

在 `web/src/i18n/locales/{zh,en}/apps/conversations.ts` 加 `voice.*` 一组键：开始/停止录音、录音中、识别中、下载模型 N%、选择模型、下载源标签（国内镜像/官方/自定义）、三档标签+体积、权限被拒、不支持、未识别到语音、下载失败、切换模型/源站。

## 明确不做（YAGNI）

- 不做实时流式边说边出字（只做录完再识别）。
- 不做后端改动、不上传录音。
- 不做波形可视化（只脉冲点 + 计时）。
- 不做识别后自动发送（whisper 有错词率，必须留人工校对）。
- 不做语音播放/TTS。

## 测试策略

- `audioDecode.ts`、`voiceSettings.ts` 为纯逻辑单元，补 vitest 单测（正常路径 + 边界：空音频、非法自定义 URL、持久化读写默认值）。
- `useVoiceInput.ts` 状态机迁移做单测（idle→recording→transcribing→idle、权限拒绝、下载失败分支）。
- 识别/录音涉及浏览器 API，交付前用**真实浏览器**做全流程功能验证（选源站+档位→下载进度→录音→识别填草稿→编辑发送；权限拒绝；换源），不以 curl 替代。

## 实施说明

- 全程在 `feat/conversation-voice-input` 分支开发。
- 新增依赖 `@huggingface/transformers`（Transformers.js），仅前端 `web/package.json`。
- 不涉及 handler/OpenAPI/生成类型改动，无需 `make openapi-gen` / `web-types-gen`。
