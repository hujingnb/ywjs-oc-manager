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
         仅在已选中会话（currentId 非空）时才响应拖拽。 -->
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

      <!-- 待发送队列：任务进行中入队的消息，逐条串行自动发送；仅渲染当前会话的项。 -->
      <div v-if="queuedForCurrent.length" class="queue-panel">
        <div class="queue-header">
          <span class="queue-title">{{ t('apps.conversations.queueTitle') }}</span>
          <span v-if="sending" class="queue-generating">{{ t('apps.conversations.generating') }}</span>
        </div>
        <div
          v-for="item in queuedForCurrent"
          :key="item.id"
          class="queue-item"
          :class="{ 'queue-item--failed': item.status === 'failed' }"
          :data-test="`queued-${item.id}`"
        >
          <!-- 编辑态：内联 textarea + 可移除文件 tag + 保存/取消 -->
          <template v-if="editingId === item.id">
            <n-input
              v-model:value="editDraft"
              type="textarea"
              :autosize="{ minRows: 1, maxRows: 4 }"
            />
            <div v-if="editFiles.length" class="queue-files">
              <n-tag
                v-for="(f, i) in editFiles"
                :key="i"
                closable
                size="small"
                @close="removeEditFile(i)"
              >
                {{ f.name }}
              </n-tag>
            </div>
            <div class="queue-actions">
              <n-button size="tiny" type="primary" @click="saveEdit(item.id)">
                {{ t('apps.conversations.queueSave') }}
              </n-button>
              <n-button size="tiny" quaternary @click="cancelEdit">
                {{ t('apps.conversations.queueCancel') }}
              </n-button>
            </div>
          </template>
          <!-- 展示态：文本预览 + 只读文件 tag + 失败标记/重试/编辑/删除 -->
          <template v-else>
            <div class="queue-text">{{ item.text || '—' }}</div>
            <div v-if="item.files.length" class="queue-files">
              <n-tag v-for="(f, i) in item.files" :key="i" size="small">{{ f.name }}</n-tag>
            </div>
            <div class="queue-actions">
              <n-tag
                v-if="item.status === 'failed'"
                size="tiny"
                type="error"
                :bordered="false"
              >
                {{ t('apps.conversations.queueFailed') }}
              </n-tag>
              <n-button
                v-if="item.status === 'failed'"
                size="tiny"
                type="primary"
                @click="retryQueued(item.id)"
              >
                {{ t('apps.conversations.queueRetry') }}
              </n-button>
              <n-button size="tiny" quaternary @click="startEdit(item)">
                {{ t('apps.conversations.queueEdit') }}
              </n-button>
              <n-button size="tiny" quaternary type="error" @click="removeQueued(item.id)">
                {{ t('apps.conversations.queueRemove') }}
              </n-button>
            </div>
          </template>
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
        <!-- 第二行：文本框在左，附件 + 发送按钮一起靠右 -->
        <div class="composer-row">
          <n-input
            v-model:value="draft"
            type="textarea"
            :autosize="{ minRows: 2, maxRows: 5 }"
            :placeholder="t('apps.conversations.placeholder')"
            :disabled="!currentId"
            @keydown.enter.exact.prevent="onComposerSubmit"
          />
          <!-- 语音输入暂时关闭：功能保留待后续开启，勿删实现 -->
          <!-- <VoiceInputButton :disabled="!currentId" @text="appendVoiceText" /> -->
          <!-- 附件选择：用 label 包裹隐藏 input，点击 label 触发文件选择框 -->
          <label
            class="attach-button"
            :class="{ 'attach-button--disabled': !currentId }"
          >
            <input
              class="hidden-input"
              type="file"
              multiple
              :disabled="!currentId"
              @change="onPickFiles"
            />
            {{ t('apps.conversations.attach') }}
          </label>
          <n-button
            type="primary"
            :disabled="!currentId || (!draft.trim() && pendingFiles.length === 0)"
            data-test="send"
            @click="onComposerSubmit"
          >
            {{ sending ? t('apps.conversations.queueSend') : t('apps.conversations.send') }}
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
import { ref, reactive, computed, nextTick, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NInput, NModal, NSpace, NTag, useMessage } from 'naive-ui'
import * as api from '@/api/conversations'
import type { ConversationSession, ConversationPart } from '@/api/conversations'
import { isDialogueMessage, deriveSessionTitle } from '@/domain/conversation'
import {
  nextPending,
  removeById,
  prependFailed,
  setStatus,
  applyEdit,
  forSession,
  type QueuedMessage,
} from '@/domain/messageQueue'
import ConversationMessageView from './ConversationMessageView.vue'
// 语音输入暂时关闭：功能保留待后续开启，勿删实现
// import VoiceInputButton from '@/features/voiceInput/VoiceInputButton.vue'

// appId 由路由 props: true 注入，标识当前实例。
const props = defineProps<{ appId: string }>()
const { t } = useI18n()
const message = useMessage()

// ─── 数据状态 ────────────────────────────────────────────────────────────────
const sessions = ref<api.ConversationSession[]>([])
const messages = ref<api.ConversationMessage[]>([])
// currentId 是当前选中的会话 id；空字符串表示未选中。
const currentId = ref('')
// autoTitleAttempted 记录本页内已尝试过自动命名的会话 id，避免每次打开会话重复 PATCH；
// 失败（如只读角色无重命名权限被拒 403）也计入，防止反复请求。纯内存、不持久化。
const autoTitleAttempted = new Set<string>()
// draft 是输入框当前文本。
const draft = ref('')
// sending 为 true 时表示流式发送进行中，禁用输入和发送按钮。
const sending = ref(false)
// pendingFiles 是用户已选但尚未发送的文件列表，发送时逐个上传后附入消息。
const pendingFiles = ref<File[]>([])
// dragActive 标记当前是否有文件正被拖拽到对话区域，为 true 时显示高亮边框。
const dragActive = ref(false)
// queue 是任务进行中入队的待发送消息，逐条串行自动发送；纯内存、不持久化。
const queue = ref<QueuedMessage[]>([])
// queueSeq 本地自增序号，用于生成队列项唯一 id（不依赖时间/随机源）。
let queueSeq = 0
// draining 防止 drainQueue 重入（如失败重试与自动消费并发）。
let draining = false
// queuedForCurrent 仅暴露当前会话的队列项，供面板按会话隔离渲染。
const queuedForCurrent = computed(() => forSession(queue.value, currentId.value))

// ─── 队列项内联编辑状态 ───────────────────────────────────────────────────────
// editingId 为正在编辑的队列项 id；null 表示无编辑态。
const editingId = ref<string | null>(null)
// editDraft / editFiles 是编辑态的文本与文件草稿，保存时写回队列项。
const editDraft = ref('')
const editFiles = ref<File[]>([])
// msgListEl 用于发送后滚动到底部。
const msgListEl = ref<HTMLElement | null>(null)

// ─── 重命名弹窗状态 ───────────────────────────────────────────────────────────
const renameVisible = ref(false)
const renameTitle = ref('')
const renameTargetId = ref('')

// appendVoiceText 把语音识别结果追加到 draft：非空草稿以空格拼接，保留用户已输入内容。
// 语音输入暂时关闭：功能保留待后续开启，勿删实现
// function appendVoiceText(text: string) {
//   draft.value = draft.value ? `${draft.value} ${text}` : text
// }

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

// onDragEnter 文件拖入对话区域时激活高亮；条件：已选会话且携带文件。
function onDragEnter(e: DragEvent) {
  if (!currentId.value || !hasFiles(e)) return
  dragActive.value = true
}

// onDragOver 持续拖拽经过时保持高亮并设置 dropEffect 为 copy，告知系统可放置。
function onDragOver(e: DragEvent) {
  if (!currentId.value || !hasFiles(e)) return
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
  if (!currentId.value) return
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
    // 消息就绪后尝试用首句自动命名空标题会话（新会话发完首句、旧会话点开时均在此收敛）。
    await maybeAutoTitle(sid)
  } catch (e) {
    message.error(e instanceof Error ? e.message : String(e))
  }
}

// maybeAutoTitle 在会话消息加载完成后，尝试用「用户发起的第一句话」自动补全空标题：
// 仅当该会话 title 为空、本页尚未尝试过、且能从当前消息派生出标题时才触发；
// 命中即调用已有的重命名接口回填 title，并就地更新左栏会话对象（响应式，无需整表刷新）。
// 自动命名属锦上添花：无论成功与否都先记入 autoTitleAttempted 防重复；失败（如只读角色
// 无重命名权限被拒 403）一律静默吞掉，不弹错误、不影响查看会话。
async function maybeAutoTitle(sid: string) {
  if (autoTitleAttempted.has(sid)) return
  const s = sessions.value.find((x) => x.id === sid)
  if (!s || s.title) return
  const title = deriveSessionTitle(messages.value)
  if (!title) return
  autoTitleAttempted.add(sid)
  try {
    await api.renameConversation(props.appId, sid, title)
    s.title = title
  } catch {
    // 静默：自动命名失败不影响查看会话（如无重命名权限）。
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

// sendMessage 以 SSE 流式发送一条消息（文本 + 文件），手动发送与队列消费共用：
//   1. 逐个上传 files 拿 file_id 组 ConversationPart[]（上传失败直接抛出）；
//   2. 乐观推入用户消息与空 assistant 占位；
//   3. 逐帧把 onDelta 追加到占位消息；
//   4. 成功后 refetch 保持一致。
// 关键：api.chatStream 从不 reject，失败只走 onError 回调后正常 resolve，故此处在 onError
// 里捕获错误信息，流结束后若有错误：移除刚才乐观推入的两条消息、抛出错误，让调用方（drainQueue /
// onComposerSubmit）感知失败并处理，避免失败消息残留在气泡里。
async function sendMessage(text: string, files: File[]) {
  if ((!text && files.length === 0) || !currentId.value) return

  sending.value = true

  // 逐个上传文件，拿到 file_id 组装 file parts；失败则复位 sending 并抛出。
  const fileParts: ConversationPart[] = []
  try {
    for (const f of files) {
      const meta = await api.uploadConversationFile(props.appId, currentId.value, f)
      fileParts.push({ type: 'input_file', file_id: meta.file_id, filename: meta.filename, mime: meta.mime })
    }
  } catch (e) {
    sending.value = false
    throw e instanceof Error ? e : new Error(String(e))
  }

  // 有文件时组装多模态 parts；纯文字时保持字符串，与旧行为兼容。
  const payload: string | ConversationPart[] =
    fileParts.length > 0
      ? [...(text ? [{ type: 'text' as const, text }] : []), ...fileParts]
      : text

  // 乐观推入用户消息与 assistant 占位（保存引用，失败时按引用精准移除）。
  const userMsg: api.ConversationMessage = { role: 'user', content: payload }
  messages.value.push(userMsg)
  const asst = reactive<api.ConversationMessage>({ role: 'assistant', content: '' })
  messages.value.push(asst)
  await scrollToBottom()

  // streamErr 捕获流内错误：chatStream 不 reject，靠 onError 回填。
  let streamErr: string | null = null
  try {
    await api.chatStream(props.appId, currentId.value, payload, {
      onDelta: (d) => {
        asst.content = (asst.content as string) + d
        void scrollToBottom()
      },
      onDone: () => {},
      onError: (m) => {
        streamErr = m
      },
    })
  } finally {
    sending.value = false
  }

  if (streamErr) {
    // 失败：移除刚才乐观推入的占位与用户消息，使该条不残留在气泡；抛出供调用方处理。
    const ai = messages.value.indexOf(asst)
    if (ai >= 0) messages.value.splice(ai, 1)
    const ui = messages.value.indexOf(userMsg)
    if (ui >= 0) messages.value.splice(ui, 1)
    throw new Error(streamErr)
  }

  // 成功后重新拉取消息，使列表与服务端状态一致（含 token/finish_reason 等完整字段）。
  await selectSession(currentId.value)
}

// onComposerSubmit 是发送按钮 / 回车的统一入口：
//   - 任务进行中（sending）：把当前草稿入队，不立即发送；
//   - 空闲：立即发送，成功后驱动 drainQueue 消费期间新入的队列项。
async function onComposerSubmit() {
  const text = draft.value.trim()
  const files = pendingFiles.value.slice()
  if ((!text && files.length === 0) || !currentId.value) return

  if (sending.value) {
    // 入队：生成唯一 id，绑定当前会话；清空草稿让用户感知已受理。
    queue.value = [
      ...queue.value,
      { id: `q${++queueSeq}`, sessionId: currentId.value, text, files, status: 'pending' },
    ]
    draft.value = ''
    pendingFiles.value = []
    return
  }

  // 空闲：立即发送。清空草稿后发送，失败则回填草稿并提示。
  draft.value = ''
  pendingFiles.value = []
  try {
    await sendMessage(text, files)
  } catch (e) {
    draft.value = text
    pendingFiles.value = files
    message.error(e instanceof Error ? e.message : String(e))
    return
  }
  await drainQueue()
}

// drainQueue 串行消费当前会话的 pending 队列：逐条取出发送，上一条流式跑完再发下一条。
// 任一条失败即停止（方案 A：停止并保留），把该条以 failed 放回队头供重试。
// draining 防重入；sending 为真时不启动（避免与在飞发送并发）。
async function drainQueue() {
  if (draining || sending.value) return
  draining = true
  try {
    for (;;) {
      const next = nextPending(queue.value, currentId.value)
      if (!next) return
      // 先移出队列：发送时它会经乐观更新变成真实用户气泡，从队列面板消失。
      queue.value = removeById(queue.value, next.id)
      try {
        await sendMessage(next.text, next.files)
      } catch (e) {
        queue.value = prependFailed(queue.value, next)
        message.error(e instanceof Error ? e.message : String(e))
        return
      }
    }
  } finally {
    draining = false
  }
}

// ─── 队列项操作 ───────────────────────────────────────────────────────────────
// startEdit 进入某队列项的内联编辑态，预填其文本与文件。
function startEdit(item: QueuedMessage) {
  editingId.value = item.id
  editDraft.value = item.text
  editFiles.value = item.files.slice()
}

// removeEditFile 在编辑态移除某个待发文件。
function removeEditFile(i: number) {
  editFiles.value.splice(i, 1)
}

// cancelEdit 放弃本次编辑，丢弃编辑草稿。
function cancelEdit() {
  editingId.value = null
}

// saveEdit 把编辑草稿写回队列项并退出编辑态。
function saveEdit(id: string) {
  queue.value = applyEdit(queue.value, id, editDraft.value.trim(), editFiles.value.slice())
  editingId.value = null
}

// removeQueued 从队列删除某项。
function removeQueued(id: string) {
  queue.value = removeById(queue.value, id)
  if (editingId.value === id) editingId.value = null
}

// retryQueued 把失败项改回 pending 并重新驱动消费。
async function retryQueued(id: string) {
  queue.value = setStatus(queue.value, id, 'pending')
  await drainQueue()
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

/* ─── 待发送队列面板 ──────────────────────────────
   位于消息列表与输入区之间，flex-shrink:0 不被压缩；内部自身可滚动，避免排多条时顶高布局。 */
.queue-panel {
  flex-shrink: 0;
  max-height: 30%;
  overflow-y: auto;
  padding: 8px 12px;
  border-top: 1px dashed var(--color-border, #e5e7eb);
  background: var(--color-bg-hover, #f5f5f5);
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.queue-header {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--color-text-secondary, #6b7280);
}

.queue-title {
  font-weight: 600;
}

.queue-generating {
  color: var(--color-brand-text, #8a3700);
}

/* 单条队列项：卡片式，纵向堆叠文本/文件/操作。 */
.queue-item {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 8px 10px;
  border: 1px solid var(--color-border, #e5e7eb);
  border-radius: 6px;
  background: var(--color-surface, #fff);
}

/* 失败项：红色左边框强调。 */
.queue-item--failed {
  border-left: 3px solid var(--color-error, #d03050);
}

.queue-text {
  font-size: 13px;
  color: var(--color-text-primary, #1f2329);
  white-space: pre-wrap;
  word-break: break-word;
}

.queue-files {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.queue-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
}

/* 输入区：固定在右侧底部。
   flex-shrink: 0 保证 composer 永不被压缩——上方 .msg-list(flex:1) 吸收所有剩余空间并自身滚动，
   composer 始终以完整高度钉在 .messages-col 底部，即使视口变矮或输入框 autosize 撑高也不下沉/不被挤出。 */
/* 纵向堆叠：第一行待发送文件标签（可选），第二行附件按钮+文本框+发送按钮。
   必须 column + stretch，否则 composer-files 与 composer-row 会横向并排、
   composer-row 被压成内容宽度（文本框塌成细条）。 */
.composer {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 8px;
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
