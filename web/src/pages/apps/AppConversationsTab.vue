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

    <!-- 右侧：消息历史 + 输入框 -->
    <div class="messages-col">
      <!-- 消息列表 -->
      <div ref="msgListEl" class="msg-list">
        <div
          v-for="(msg, idx) in messages"
          :key="idx"
          class="msg-row"
          :class="msg.role"
        >
          <span class="role-label">{{ msg.role }}</span>
          <ConversationMessageView :message="msg" />
        </div>
      </div>

      <!-- 输入区：仅在选中会话时启用 -->
      <div class="composer">
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
          :disabled="!currentId || sending || !draft.trim()"
          :loading="sending"
          data-test="send"
          @click="onSend"
        >
          {{ sending ? t('apps.conversations.sending') : t('apps.conversations.send') }}
        </n-button>
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
import type { ConversationSession } from '@/api/conversations'
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
// msgListEl 用于发送后滚动到底部。
const msgListEl = ref<HTMLElement | null>(null)

// ─── 重命名弹窗状态 ───────────────────────────────────────────────────────────
const renameVisible = ref(false)
const renameTitle = ref('')
const renameTargetId = ref('')

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
    messages.value = await api.listMessages(props.appId, sid)
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

// onSend 以 SSE 流式发送消息：
//   1. 立即把用户消息推入列表（乐观更新）；
//   2. 插入空的 assistant 占位消息；
//   3. 逐帧把 onDelta 内容追加到占位消息的 content；
//   4. 完成后重新拉取该会话消息列表以确保一致性。
async function onSend() {
  const text = draft.value.trim()
  if (!text || !currentId.value || sending.value) return

  sending.value = true
  draft.value = ''

  // 乐观推入用户消息。
  messages.value.push({ role: 'user', content: text })
  // 用 reactive 包裹占位对象，确保 content 字段的变更触发视图更新。
  const asst = reactive<api.ConversationMessage>({ role: 'assistant', content: '' })
  messages.value.push(asst)
  await scrollToBottom()

  try {
    await api.chatStream(props.appId, currentId.value, text, {
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
  height: calc(100vh - 160px);
  min-height: 400px;
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

.msg-list {
  flex: 1;
  overflow-y: auto;
  padding: 12px 16px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.msg-row {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.msg-row.user .role-label {
  color: var(--color-brand-text, #8a3700);
}

.msg-row.assistant .role-label {
  color: var(--color-text-secondary, #6b7280);
}

.role-label {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
}

/* 输入区：固定在右侧底部 */
.composer {
  display: flex;
  gap: 8px;
  align-items: flex-end;
  padding: 10px 12px;
  border-top: 1px solid var(--color-border, #e5e7eb);
}

.composer :deep(.n-input) {
  flex: 1;
}
</style>
