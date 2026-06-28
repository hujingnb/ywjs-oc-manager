<template>
  <!-- AppConversationsTab：实例会话管理 tab，左侧会话列表 + 右侧消息历史与输入框。 -->
  <div class="conversations-tab">
    <!-- 左侧：会话列表 -->
    <div class="sessions-col">
      <div class="sessions-header">
        <n-button
          size="small"
          type="primary"
          data-test="new-session"
          @click="onCreate"
        >
          {{ t('apps.conversations.new') }}
        </n-button>
      </div>
      <!-- 无会话时空状态提示 -->
      <div v-if="sessions.length === 0" class="empty-hint">
        {{ t('apps.conversations.empty') }}
      </div>
      <!-- 会话条目列表 -->
      <div
        v-for="s in sessions"
        :key="s.id"
        class="session-item"
        :class="{ active: currentId === s.id }"
        :data-test="`session-${s.id}`"
        @click="selectSession(s.id)"
      >
        <div class="session-main">
          <!-- source 标签 -->
          <n-tag size="tiny" :bordered="false" class="source-tag">{{ s.source }}</n-tag>
          <!-- 标题或 id 兜底 -->
          <span class="session-title">{{ s.title || s.id }}</span>
        </div>
        <!-- 操作按钮：重命名 / 删除。容器加 @click.stop 拦截，避免点按钮区误触选中。
             注意：actions 容器必须靠 .session-item 的 row 布局收窄到右侧，不能占满整行——
             否则会吞掉条目主体点击，导致「点会话切换经常无效、要点好几次」（见 CSS）。 -->
        <div class="session-actions" @click.stop>
          <n-button
            size="tiny"
            quaternary
            @click="startRename(s)"
          >
            {{ t('apps.conversations.rename') }}
          </n-button>
          <n-button
            size="tiny"
            quaternary
            type="error"
            @click="onDelete(s.id)"
          >
            {{ t('apps.conversations.delete') }}
          </n-button>
        </div>
      </div>
    </div>

    <!-- 右侧：消息历史 + 输入框。
         拖拽上传：拖文件到本区域触发高亮，松手后文件追加到 pendingFiles（与点击选择一致）。
         仅在已选中会话（currentId 非空）且未发送中（!sending）时才响应拖拽。 -->
    <div
      class="messages-col"
      :class="{ 'drag-active': dragActive }"
      @dragenter.prevent="onDragEnter"
      @dragover.prevent="onDragOver"
      @dragleave.prevent="onDragLeave"
      @drop.prevent="onDrop"
    >
      <!-- 消息列表 -->
      <div ref="msgListEl" class="msg-list">
        <div
          v-for="(msg, idx) in messages"
          :key="idx"
          class="msg-row"
          :class="msg.role"
        >
          <span class="role-label">{{ msg.role }}</span>
          <ConversationMessageView :message="msg" :app-id="props.appId" :session-id="currentId" />
        </div>
      </div>

      <!-- 输入区：仅在选中会话时启用 -->
      <div class="composer">
        <!-- 待发文件标签区：每个 tag 可关闭移除 -->
        <div v-if="pendingFiles.length" class="composer-files">
          <n-tag
            v-for="(f, i) in pendingFiles"
            :key="i"
            closable
            size="small"
            @close="removePendingFile(i)"
          >
            {{ f.name }}
          </n-tag>
        </div>
        <!-- 第二行：附件按钮 + 文本框 + 发送按钮 -->
        <div class="composer-row">
          <!-- 附件选择：用 label 包裹隐藏 input，点击 label 触发文件选择框 -->
          <label
            class="attach-button"
            :class="{ 'attach-button--disabled': !currentId || sending }"
          >
            <input
              class="hidden-input"
              type="file"
              multiple
              :disabled="!currentId || sending"
              @change="onPickFiles"
            />
            {{ t('apps.conversations.attach') }}
          </label>
          <n-input
            v-model:value="draft"
            type="textarea"
            :autosize="{ minRows: 2, maxRows: 5 }"
            :placeholder="t('apps.conversations.placeholder')"
            :disabled="!currentId || sending"
            @keydown.enter.exact.prevent="onSend"
          />
          <n-button
            type="primary"
            :disabled="!currentId || sending || (!draft.trim() && pendingFiles.length === 0)"
            :loading="sending"
            data-test="send"
            @click="onSend"
          >
            {{ sending ? t('apps.conversations.sending') : t('apps.conversations.send') }}
          </n-button>
        </div>
      </div>
    </div>

    <!-- 重命名弹窗 -->
    <n-modal
      v-model:show="renameVisible"
      preset="card"
      :title="t('apps.conversations.rename')"
      style="width: 360px; max-width: calc(100vw - 32px)"
      :mask-closable="true"
    >
      <n-input v-model:value="renameTitle" :placeholder="t('apps.conversations.rename')" />
      <template #footer>
        <n-space justify="end">
          <n-button @click="renameVisible = false">{{ t('common.actions.cancel') }}</n-button>
          <n-button type="primary" @click="submitRename">{{ t('common.actions.confirm') }}</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, nextTick, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NInput, NModal, NSpace, NTag, useMessage } from 'naive-ui'
import * as api from '@/api/conversations'
import type { ConversationSession, ConversationPart } from '@/api/conversations'
import { isDialogueMessage } from '@/domain/conversation'
import ConversationMessageView from './ConversationMessageView.vue'

// appId 由路由 props: true 注入，标识当前实例。
const props = defineProps<{ appId: string }>()
const { t } = useI18n()
const message = useMessage()

// ─── 数据状态 ────────────────────────────────────────────────────────────────
const sessions = ref<api.ConversationSession[]>([])
const messages = ref<api.ConversationMessage[]>([])
// currentId 是当前选中的会话 id；空字符串表示未选中。
const currentId = ref('')
// draft 是输入框当前文本。
const draft = ref('')
// sending 为 true 时表示流式发送进行中，禁用输入和发送按钮。
const sending = ref(false)
// pendingFiles 是用户已选但尚未发送的文件列表，发送时逐个上传后附入消息。
const pendingFiles = ref<File[]>([])
// dragActive 标记当前是否有文件正被拖拽到对话区域，为 true 时显示高亮边框。
const dragActive = ref(false)
// msgListEl 用于发送后滚动到底部。
const msgListEl = ref<HTMLElement | null>(null)

// ─── 重命名弹窗状态 ───────────────────────────────────────────────────────────
const renameVisible = ref(false)
const renameTitle = ref('')
const renameTargetId = ref('')

// onPickFiles 处理 file input change 事件，把新选文件追加到 pendingFiles，并重置 input 值
// 允许再次选同名文件。
function onPickFiles(e: Event) {
  const input = e.target as HTMLInputElement
  if (input.files) pendingFiles.value.push(...Array.from(input.files))
  input.value = ''
}

// removePendingFile 从待发文件列表中按下标移除单个文件（对应 tag 关闭按钮）。
function removePendingFile(i: number) {
  pendingFiles.value.splice(i, 1)
}

// ─── 拖拽上传 ─────────────────────────────────────────────────────────────────
// hasFiles 检查拖拽事件的 dataTransfer 是否包含文件类型，用于区分拖链接、拖文本等无关场景。
function hasFiles(e: DragEvent): boolean {
  return !!e.dataTransfer && Array.from(e.dataTransfer.types || []).includes('Files')
}

// onDragEnter 文件拖入对话区域时激活高亮；条件：已选会话且未发送中且携带文件。
function onDragEnter(e: DragEvent) {
  if (!currentId.value || sending.value || !hasFiles(e)) return
  dragActive.value = true
}

// onDragOver 持续拖拽经过时保持高亮并设置 dropEffect 为 copy，告知系统可放置。
function onDragOver(e: DragEvent) {
  if (!currentId.value || sending.value || !hasFiles(e)) return
  dragActive.value = true
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy'
}

// onDragLeave 仅当焦点真正离开容器（relatedTarget 不在容器内）才取消高亮，
// 避免鼠标在子元素间移动时高亮闪烁。
function onDragLeave(e: DragEvent) {
  const cur = e.currentTarget
  const rel = e.relatedTarget
  if (cur instanceof Node && rel instanceof Node && cur.contains(rel)) return
  dragActive.value = false
}

// onDrop 放置时把文件追加到 pendingFiles，与点击「附件」选择文件的行为完全一致。
function onDrop(e: DragEvent) {
  dragActive.value = false
  if (!currentId.value || sending.value) return
  const files = e.dataTransfer ? Array.from(e.dataTransfer.files) : []
  if (files.length) pendingFiles.value.push(...files)
}

// roleLabel 把消息 role 映射为本地化标签：user→用户、assistant→客服（AI 应答方），
// 其余 role（如 system）原样回显兜底，避免漏配时标签空白。
function roleLabel(role: string): string {
  if (role === 'user') return t('apps.conversations.roleUser')
  if (role === 'assistant') return t('apps.conversations.roleAssistant')
  return role
}

// ─── 数据加载 ─────────────────────────────────────────────────────────────────
// loadSessions 拉取当前实例的所有会话列表。
async function loadSessions() {
  try {
    sessions.value = await api.listConversations(props.appId)
  } catch (e) {
    message.error(e instanceof Error ? e.message : String(e))
  }
}

// selectSession 选中指定会话并加载其消息历史。
async function selectSession(sid: string) {
  currentId.value = sid
  try {
    // 只展示对话正文：过滤掉引擎的工具消息（role==='tool'）与仅含工具调用的空内容步骤，
    // 详见 isDialogueMessage。后端透传全量消息，是否展示由查看页决定。
    const all = await api.listMessages(props.appId, sid)
    messages.value = all.filter(isDialogueMessage)
    await scrollToBottom()
  } catch (e) {
    message.error(e instanceof Error ? e.message : String(e))
  }
}

// ─── 操作 ─────────────────────────────────────────────────────────────────────
// onCreate 新建会话并自动跳入。
async function onCreate() {
  try {
    const s = await api.createConversation(props.appId)
    await loadSessions()
    await selectSession(s.id)
  } catch (e) {
    message.error(e instanceof Error ? e.message : String(e))
  }
}

// startRename 打开重命名弹窗，预填当前标题。
function startRename(s: ConversationSession) {
  renameTargetId.value = s.id
  renameTitle.value = s.title ?? ''
  renameVisible.value = true
}

// submitRename 提交重命名并刷新列表。
async function submitRename() {
  if (!renameTargetId.value) return
  try {
    await api.renameConversation(props.appId, renameTargetId.value, renameTitle.value)
    renameVisible.value = false
    await loadSessions()
  } catch (e) {
    message.error(e instanceof Error ? e.message : String(e))
  }
}

// onDelete 删除指定会话；若当前选中则清空右侧面板。
async function onDelete(sid: string) {
  try {
    await api.deleteConversation(props.appId, sid)
    if (currentId.value === sid) {
      currentId.value = ''
      messages.value = []
    }
    await loadSessions()
  } catch (e) {
    message.error(e instanceof Error ? e.message : String(e))
  }
}

// onSend 以 SSE 流式发送消息，支持纯文本与多模态（文件+文字）：
//   1. 先逐个上传 pendingFiles 拿到 file_id，组 ConversationPart[]；
//   2. 立即把用户消息推入列表（乐观更新）；
//   3. 插入空的 assistant 占位消息；
//   4. 逐帧把 onDelta 内容追加到占位消息的 content；
//   5. 完成后重新拉取该会话消息列表以确保一致性。
// 注意：useMessage() 返回的通知 API 已命名为 `message`，发送消息内容使用 `payload` 避免遮蔽。
async function onSend() {
  const text = draft.value.trim()
  const files = pendingFiles.value.slice()
  // 文字与文件都为空时不发送。
  if ((!text && files.length === 0) || !currentId.value || sending.value) return

  sending.value = true
  // 立即清空草稿与待发文件，让用户感知操作已受理。
  draft.value = ''
  pendingFiles.value = []

  // 逐个上传文件，拿到 file_id 组装 file parts。
  let fileParts: ConversationPart[] = []
  try {
    for (const f of files) {
      const meta = await api.uploadConversationFile(props.appId, currentId.value, f)
      fileParts.push({ type: 'input_file', file_id: meta.file_id, filename: meta.filename, mime: meta.mime })
    }
  } catch (e) {
    message.error(e instanceof Error ? e.message : String(e))
    sending.value = false
    return
  }

  // 有文件时组装多模态 parts；纯文字时保持字符串，与旧行为兼容。
  const payload: string | ConversationPart[] =
    fileParts.length > 0
      ? [...(text ? [{ type: 'text' as const, text }] : []), ...fileParts]
      : text

  // 乐观推入用户消息。
  messages.value.push({ role: 'user', content: payload })
  // 用 reactive 包裹占位对象，确保 content 字段的变更触发视图更新。
  const asst = reactive<api.ConversationMessage>({ role: 'assistant', content: '' })
  messages.value.push(asst)
  await scrollToBottom()

  try {
    await api.chatStream(props.appId, currentId.value, payload, {
      onDelta: (d) => {
        asst.content = (asst.content as string) + d
        void scrollToBottom()
      },
      onDone: () => {},
      onError: (m) => {
        message.error(m)
      },
    })
    // 流结束后重新拉取消息，使列表与服务端状态一致（包含 token/finish_reason 等完整字段）。
    await selectSession(currentId.value)
  } catch (e) {
    message.error(e instanceof Error ? e.message : String(e))
  } finally {
    sending.value = false
  }
}

// scrollToBottom 在 DOM 更新后把消息列表滚到最底部。
async function scrollToBottom() {
  await nextTick()
  if (msgListEl.value) {
    msgListEl.value.scrollTop = msgListEl.value.scrollHeight
  }
}

onMounted(loadSessions)
</script>

<style scoped>
.conversations-tab {
  display: grid;
  grid-template-columns: 260px 1fr;
  gap: 12px;
  /* 填满父级详情页为本 tab 分配的 1fr 行（见 AppDetailPage 的 .app-detail-root--fill），
     不再用 100vh 魔法数字。min-height: 0 允许本容器收缩到内容高度以下，
     从而让右侧 .msg-list 自身滚动，避免把溢出顶到外层 layout 产生整页滚动条。 */
  height: 100%;
  min-height: 0;
}

/* 小屏折叠为单列 */
@media (max-width: 900px) {
  .conversations-tab {
    grid-template-columns: 1fr;
  }
}

/* ─── 左侧会话列表 ─────────────────────────────── */
.sessions-col {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--color-border, #e5e7eb);
  border-radius: 6px;
  overflow: hidden;
  background: var(--color-surface, #fff);
}

.sessions-header {
  padding: 10px 12px;
  border-bottom: 1px solid var(--color-border, #e5e7eb);
}

.empty-hint {
  padding: 16px 12px;
  color: var(--color-text-secondary, #6b7280);
  font-size: 13px;
}

/* 行布局：标题区 .session-main 占满左侧（flex:1）作为主点击区，操作按钮收在右侧、
   只占自身宽度——避免 actions 容器（带 @click.stop）铺满整行吞掉条目点击。 */
.session-item {
  display: flex;
  flex-direction: row;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 12px;
  cursor: pointer;
  border-bottom: 1px solid var(--color-divider, #f0f0f0);
  transition: background 0.15s;
}

.session-item:hover {
  background: var(--color-bg-hover, #f5f5f5);
}

.session-item.active {
  background: var(--color-brand-bg, #fff5ee);
}

.session-main {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 1;
  min-width: 0;
}

.source-tag {
  flex-shrink: 0;
  font-size: 10px;
}

.session-title {
  font-size: 13px;
  color: var(--color-text-primary, #1f2329);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.session-actions {
  display: flex;
  gap: 4px;
  flex-shrink: 0;
}

/* ─── 右侧消息区 ──────────────────────────────── */
.messages-col {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--color-border, #e5e7eb);
  border-radius: 6px;
  overflow: hidden;
  background: var(--color-surface, #fff);
}

/* 拖拽进入对话区域时的高亮反馈：品牌色虚线边框 + 极淡背景，不影响布局尺寸。 */
.messages-col.drag-active {
  border-color: var(--color-brand, #ff6a00);
  outline: 2px dashed var(--color-brand, #ff6a00);
  outline-offset: -2px;
  background: rgba(255, 106, 0, 0.03);
}

.msg-list {
  flex: 1;
  overflow-y: auto;
  padding: 12px 16px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

/* 单条消息：纵向「角色标签 + 气泡」，并按角色左右分栏。
   max-width 限制气泡宽度、留出对侧空白凸显方向；align-self 决定整条靠左/靠右。 */
.msg-row {
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-width: 78%;
}
/* 用户消息：整条靠右，标签与气泡右对齐 */
.msg-row.user {
  align-self: flex-end;
  align-items: flex-end;
}
/* 客服(assistant)消息：整条靠左 */
.msg-row.assistant {
  align-self: flex-start;
  align-items: flex-start;
}

.role-label {
  font-size: 11px;
  font-weight: 600;
}
.msg-row.user .role-label {
  color: var(--color-brand-text, #8a3700);
}
.msg-row.assistant .role-label {
  color: var(--color-text-secondary, #6b7280);
}

/* 消息气泡：正文容器（ConversationMessageView 的 .message-view）上色与圆角。
   用户用品牌橙底白字、客服用浅灰底深字，明确区分双方；靠发送侧的上角收小指向角色。 */
.msg-row :deep(.message-view) {
  padding: 8px 12px;
  border-radius: 12px;
  font-size: 14px;
  line-height: 1.6;
}
.msg-row.user :deep(.message-view) {
  background: var(--color-brand, #ff6a00);
  color: #fff;
  border-top-right-radius: 4px;
}
.msg-row.assistant :deep(.message-view) {
  background: var(--color-bg-hover, #f5f5f5);
  color: var(--color-text-primary, #1f2329);
  border-top-left-radius: 4px;
}
/* 客服气泡内 markdown 代码块改白底，避免与浅灰气泡同色糊在一起 */
.msg-row.assistant :deep(.markdown-body code),
.msg-row.assistant :deep(.markdown-body pre) {
  background: #fff;
}

/* 输入区：固定在右侧底部。
   flex-shrink: 0 保证 composer 永不被压缩——上方 .msg-list(flex:1) 吸收所有剩余空间并自身滚动，
   composer 始终以完整高度钉在 .messages-col 底部，即使视口变矮或输入框 autosize 撑高也不下沉/不被挤出。 */
.composer {
  display: flex;
  gap: 8px;
  align-items: flex-end;
  padding: 10px 12px;
  border-top: 1px solid var(--color-border, #e5e7eb);
  flex-shrink: 0;
}

.composer-files {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding: 0 0 6px;
}

/* 将附件按钮+文本框+发送按钮横向排列 */
.composer-row {
  display: flex;
  gap: 8px;
  align-items: flex-end;
}

.composer-row :deep(.n-input) {
  flex: 1;
}

/* 附件按钮：label 包裹 hidden file input，点击触发系统文件选择框 */
.attach-button {
  display: inline-flex;
  align-items: center;
  padding: 0 10px;
  height: 34px;
  border: 1px solid var(--color-border, #e5e7eb);
  border-radius: 4px;
  cursor: pointer;
  font-size: 13px;
  color: var(--color-text-secondary, #6b7280);
  white-space: nowrap;
  flex-shrink: 0;
  user-select: none;
  transition: background 0.15s;
}

.attach-button:hover {
  background: var(--color-bg-hover, #f5f5f5);
}

.attach-button--disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

/* 隐藏 file input 自身；点击包裹的 label 触发弹框 */
.hidden-input {
  display: none;
}
</style>
