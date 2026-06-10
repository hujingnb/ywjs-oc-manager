<template>
  <!-- SkillTicketPanel：企业成员「定制技能」工单面板，自包含（无需 appId）。
       工单列表 + 提交需求弹窗 + 详情抽屉（对话流 / 附件 / 回复）；
       delivered 工单可「去安装」，由父组件（SkillManager）切到市场定制筛选。 -->
  <div class="skill-ticket-panel">
    <!-- 顶部工具栏：提交新需求入口。 -->
    <div class="ticket-toolbar">
      <n-button type="primary" size="small" @click="showSubmit = true">+ 提交需求</n-button>
    </div>

    <!-- 工单列表：加载态 / 错误态 / 数据表。 -->
    <div v-if="ticketsQuery.isLoading.value" class="state-text">加载中…</div>
    <p v-else-if="ticketsQuery.error.value" class="state-text danger">
      工单查询失败：{{ ticketsQuery.error.value?.message }}
    </p>
    <n-data-table
      v-else
      :columns="columns"
      :data="tickets"
      size="small"
      :bordered="false"
      :row-key="(row: SkillTicket) => row.id"
    />

    <!-- 提交需求弹窗：仅标题 + 描述（附件在详情抽屉补传）。 -->
    <n-modal
      v-model:show="showSubmit"
      preset="card"
      title="提交定制技能需求"
      class="ticket-submit-modal"
      :style="{ width: '520px' }"
    >
      <n-form>
        <n-form-item label="标题">
          <n-input v-model:value="submitTitle" placeholder="一句话说明需要什么技能" />
        </n-form-item>
        <n-form-item label="描述">
          <n-input
            v-model:value="submitDescription"
            type="textarea"
            :autosize="{ minRows: 3, maxRows: 8 }"
            placeholder="详细描述使用场景、输入输出、期望行为等"
          />
        </n-form-item>
      </n-form>
      <template #footer>
        <div class="ticket-modal-footer">
          <n-button @click="showSubmit = false">取消</n-button>
          <n-button
            type="primary"
            :loading="submitMut.isPending.value"
            :disabled="!submitTitle.trim()"
            @click="onSubmit"
          >
            提交
          </n-button>
        </div>
      </template>
    </n-modal>

    <!-- 详情抽屉：仿 SkillDetailDrawer，placement=right，含头部 / 对话流 / 附件 / 回复框。 -->
    <n-drawer :show="detailOpen" :width="480" placement="right" @update:show="onDrawerToggle">
      <n-drawer-content :title="detail?.title ?? '工单详情'" closable>
        <div v-if="detailQuery.isLoading.value" class="state-text">加载中…</div>
        <p v-else-if="detailQuery.error.value" class="state-text danger">详情查询失败</p>
        <div v-else-if="detail" class="ticket-detail">
          <!-- 头部：状态徽章 + 报价；rejected 显示拒绝原因与重新提交提示。 -->
          <div class="ticket-detail-head">
            <n-tag :type="statusTag(detail.status).type" size="small" :bordered="false">
              {{ statusTag(detail.status).label }}
            </n-tag>
            <span class="ticket-detail-quote">报价 {{ yuan(detail.quote_amount_cents) }}</span>
          </div>
          <div v-if="detail.status === 'rejected' && detail.reject_reason" class="ticket-reject">
            <p class="ticket-reject-reason">拒绝原因：{{ detail.reject_reason }}</p>
            <p class="ticket-reject-hint">补充说明后将重新提交。</p>
          </div>

          <!-- 需求描述。 -->
          <p v-if="detail.description" class="ticket-detail-desc">{{ detail.description }}</p>

          <!-- 对话流：按 author_user_id 是否本人左右区分气泡。 -->
          <div class="ticket-comments">
            <strong>沟通记录</strong>
            <div v-if="!comments.length" class="state-text">暂无沟通记录</div>
            <div
              v-for="c in comments"
              :key="c.id"
              class="ticket-comment"
              :class="isMine(c.author_user_id) ? 'mine' : 'theirs'"
            >
              <div class="ticket-comment-bubble">{{ c.body }}</div>
            </div>
          </div>

          <!-- 附件区：列表 + 每条下载；原生 file input 上传。 -->
          <div class="ticket-attachments">
            <strong>附件</strong>
            <div v-if="attachmentsQuery.isLoading.value" class="state-text">加载中…</div>
            <div v-else-if="!attachments.length" class="state-text">暂无附件</div>
            <ul v-else class="ticket-attachment-list">
              <li v-for="att in attachments" :key="att.id" class="ticket-attachment-item">
                <span class="ticket-attachment-name">{{ att.file_name }}</span>
                <n-button size="tiny" @click="onDownloadAttachment(att)">下载</n-button>
              </li>
            </ul>
            <!-- 原生 file input：选中即上传当前工单。 -->
            <input
              ref="fileInput"
              type="file"
              class="ticket-file-input"
              @change="onPickFile"
            />
            <n-button
              size="small"
              :loading="uploadMut.isPending.value"
              @click="triggerFileInput"
            >
              上传附件
            </n-button>
          </div>

          <!-- 回复框：任何状态可发，delivered/rejected 发送即重开（hook 自动 invalidate）。 -->
          <div class="ticket-reply">
            <n-input
              v-model:value="replyText"
              type="textarea"
              :autosize="{ minRows: 2, maxRows: 5 }"
              placeholder="补充说明或回复…"
            />
            <n-button
              type="primary"
              size="small"
              :loading="commentMut.isPending.value"
              :disabled="!replyText.trim()"
              @click="onSendReply"
            >
              发送
            </n-button>
          </div>

          <!-- delivered 工单抽屉显「去安装」，跳市场定制筛选。 -->
          <div v-if="detail.status === 'delivered'" class="ticket-detail-actions">
            <n-button type="primary" @click="onGoInstall(detail.custom_skill_name)">去安装</n-button>
          </div>
        </div>
      </n-drawer-content>
    </n-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, h, ref } from 'vue'
import {
  NButton,
  NDataTable,
  NDrawer,
  NDrawerContent,
  NForm,
  NFormItem,
  NInput,
  NModal,
  NTag,
  useMessage,
  type DataTableColumns,
} from 'naive-ui'

import type { SkillTicket, SkillTicketAttachment } from '@/api'
import {
  downloadSkillTicketAttachment,
  useAddSkillTicketComment,
  useMySkillTicketsQuery,
  useSkillTicketAttachmentsQuery,
  useSkillTicketDetailQuery,
  useSubmitSkillTicket,
  useUploadSkillTicketAttachment,
} from '@/api/hooks/useSkillTickets'
import { useAuthStore } from '@/stores/auth'

// goInstall：delivered 工单「去安装」时上抛技能名，父组件（SkillManager）切到市场定制筛选并定位。
const emit = defineEmits<{ goInstall: [name: string | undefined] }>()

const auth = useAuthStore()
const message = useMessage()

// selectedId：当前打开详情抽屉的工单 ID；undefined 表示未打开（详情/附件 query 据此 enabled）。
const selectedId = ref<string | undefined>()
// detailOpen：抽屉显隐，与 selectedId 联动（关闭时清空 selectedId 让 query 失效）。
const detailOpen = ref(false)
// showSubmit：提交需求弹窗显隐。
const showSubmit = ref(false)
// 提交表单字段。
const submitTitle = ref('')
const submitDescription = ref('')
// 回复框文本。
const replyText = ref('')
// fileInput：原生文件选择器引用，用于程序化触发与读取选中文件。
const fileInput = ref<HTMLInputElement | null>(null)

// 工单列表 / 详情 / 附件 query，以及提交 / 评论 / 上传 mutation。
const ticketsQuery = useMySkillTicketsQuery()
const detailQuery = useSkillTicketDetailQuery(selectedId)
const attachmentsQuery = useSkillTicketAttachmentsQuery(selectedId)
const submitMut = useSubmitSkillTicket()
const commentMut = useAddSkillTicketComment(selectedId)
const uploadMut = useUploadSkillTicketAttachment(selectedId)

// tickets / detail / comments / attachments 解包，列表与抽屉模板共用。
const tickets = computed<SkillTicket[]>(() => ticketsQuery.data.value ?? [])
const detail = computed(() => detailQuery.data.value ?? null)
const comments = computed(() => detail.value?.comments ?? [])
const attachments = computed<SkillTicketAttachment[]>(() => attachmentsQuery.data.value ?? [])

// statusTag 把工单状态映射为徽章颜色 + 中文文案。
// pending 默认 / processing 警告 / delivered 成功 / rejected 错误；未知状态原样回显。
function statusTag(s: string): { type: 'default' | 'warning' | 'success' | 'error'; label: string } {
  return (
    (
      {
        pending: { type: 'default', label: '待处理' },
        processing: { type: 'warning', label: '制作中' },
        delivered: { type: 'success', label: '已交付' },
        rejected: { type: 'error', label: '已拒绝' },
      } as const
    )[s] ?? { type: 'default', label: s }
  )
}

// yuan 把报价（单位分）格式化为「¥x.xx」；null/undefined 显示「—」（尚未报价）。
function yuan(cents?: number | null): string {
  return cents == null ? '—' : `¥${(cents / 100).toFixed(2)}`
}

// fmtTime 把 ISO 时间字符串格式化为本地可读时间；空值显示「—」。
function fmtTime(v?: string): string {
  if (!v) return '—'
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString()
}

// isMine 判断评论作者是否当前登录用户，用于气泡左右区分。
function isMine(authorUserId?: string): boolean {
  return Boolean(authorUserId && authorUserId === auth.user?.id)
}

// openDetail 由「查看」按钮触发：记录工单 ID 并打开抽屉（详情/附件 query 随 selectedId 自动拉取）。
function openDetail(row: SkillTicket) {
  selectedId.value = row.id
  detailOpen.value = true
  // 切换工单时清空上一单残留的回复草稿。
  replyText.value = ''
}

// onDrawerToggle 同步抽屉显隐：关闭时清空 selectedId 让详情/附件 query 失效，避免下次打开闪现旧数据。
function onDrawerToggle(show: boolean) {
  detailOpen.value = show
  if (!show) selectedId.value = undefined
}

// onGoInstall 上抛技能名给父组件并关闭抽屉。
function onGoInstall(name: string | undefined) {
  emit('goInstall', name)
  onDrawerToggle(false)
}

// onSubmit 提交新工单：成功后提示、关闭弹窗、清空表单（hook 已 invalidate 列表自动刷新）。
async function onSubmit() {
  const title = submitTitle.value.trim()
  if (!title) return
  try {
    await submitMut.mutateAsync({ title, description: submitDescription.value.trim() })
    message.success('需求已提交')
    showSubmit.value = false
    submitTitle.value = ''
    submitDescription.value = ''
  } catch (err) {
    message.error(err instanceof Error ? err.message : '提交失败')
  }
}

// onSendReply 发送评论：任何状态可发，delivered/rejected 发送即触发后端重开（hook 自动 invalidate 刷新状态）。
async function onSendReply() {
  const body = replyText.value.trim()
  if (!body) return
  try {
    await commentMut.mutateAsync({ body })
    replyText.value = ''
  } catch (err) {
    message.error(err instanceof Error ? err.message : '发送失败')
  }
}

// triggerFileInput 程序化点开原生文件选择器（隐藏 input 复用为上传入口）。
function triggerFileInput() {
  fileInput.value?.click()
}

// onPickFile 选中文件即上传当前工单；上传完清空 input 值以便再次选同名文件触发 change。
async function onPickFile(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  try {
    await uploadMut.mutateAsync(file)
    message.success('附件已上传')
  } catch (err) {
    message.error(err instanceof Error ? err.message : '上传失败')
  } finally {
    input.value = ''
  }
}

// onDownloadAttachment 带鉴权下载附件并触发浏览器保存；失败 toast，不抛到组件外。
async function onDownloadAttachment(att: SkillTicketAttachment) {
  if (!selectedId.value) return
  try {
    await downloadSkillTicketAttachment(selectedId.value, att)
  } catch (err) {
    message.error(err instanceof Error ? err.message : '下载失败')
  }
}

// columns 定义工单列表表格列：标题 / 状态徽章 / 报价 / 更新时间 / 操作。
const columns: DataTableColumns<SkillTicket> = [
  // 标题列。
  { title: '标题', key: 'title', render: (row) => row.title },
  // 状态徽章列。
  {
    title: '状态',
    key: 'status',
    render: (row) => {
      const tag = statusTag(row.status)
      return h(NTag, { type: tag.type, size: 'small', bordered: false }, { default: () => tag.label })
    },
  },
  // 报价列：单位分→元，未报价显示「—」。
  { title: '报价', key: 'quote', render: (row) => yuan(row.quote_amount_cents) },
  // 更新时间列。
  { title: '更新时间', key: 'updated_at', render: (row) => fmtTime(row.updated_at) },
  // 操作列：「查看」打开详情抽屉；delivered 额外给「去安装」。
  {
    title: '操作',
    key: 'actions',
    render: (row) => {
      const viewBtn = h(
        NButton,
        { size: 'small', text: true, type: 'primary', onClick: () => openDetail(row) },
        { default: () => '查看' },
      )
      if (row.status !== 'delivered') return viewBtn
      const installBtn = h(
        NButton,
        { size: 'small', text: true, type: 'success', onClick: () => onGoInstall(row.custom_skill_name) },
        { default: () => '去安装' },
      )
      return h('div', { style: 'display: flex; gap: 12px' }, [viewBtn, installBtn])
    },
  },
]
</script>

<style scoped>
/* 顶部工具栏：提交需求按钮右对齐。 */
.ticket-toolbar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 12px;
}

/* 提交弹窗底部按钮区。 */
.ticket-modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

/* 详情头部：状态徽章 + 报价同行。 */
.ticket-detail-head {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
}
.ticket-detail-quote {
  font-size: 13px;
  color: var(--color-text-secondary, #888);
}

/* 拒绝原因块：醒目提示重新提交方式。 */
.ticket-reject {
  padding: 8px 12px;
  margin-bottom: 12px;
  background: var(--error-color-suppl, #fff1f0);
  border-radius: 4px;
}
.ticket-reject-reason {
  margin: 0;
  font-size: 13px;
}
.ticket-reject-hint {
  margin: 4px 0 0;
  font-size: 12px;
  color: var(--color-text-secondary, #888);
}

/* 需求描述：保留换行。 */
.ticket-detail-desc {
  margin: 12px 0;
  font-size: 13px;
  line-height: 1.6;
  white-space: pre-wrap;
}

/* 对话流：本人气泡靠右、对方靠左。 */
.ticket-comments {
  margin-top: 16px;
}
.ticket-comment {
  display: flex;
  margin: 8px 0;
}
.ticket-comment.mine {
  justify-content: flex-end;
}
.ticket-comment.theirs {
  justify-content: flex-start;
}
.ticket-comment-bubble {
  max-width: 80%;
  padding: 8px 12px;
  font-size: 13px;
  line-height: 1.5;
  border-radius: 8px;
  white-space: pre-wrap;
  word-break: break-word;
}
.ticket-comment.mine .ticket-comment-bubble {
  background: var(--primary-color-suppl, #e6f4ff);
}
.ticket-comment.theirs .ticket-comment-bubble {
  background: var(--code-color, #f4f4f5);
}

/* 附件区。 */
.ticket-attachments {
  margin-top: 16px;
}
.ticket-attachment-list {
  list-style: none;
  padding: 0;
  margin: 8px 0;
}
.ticket-attachment-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 4px 0;
  font-size: 13px;
}
.ticket-attachment-name {
  word-break: break-all;
}
/* 原生 file input 隐藏，仅通过「上传附件」按钮触发。 */
.ticket-file-input {
  display: none;
}

/* 回复框：输入 + 发送按钮纵向排列。 */
.ticket-reply {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 16px;
}
.ticket-reply .n-button {
  align-self: flex-end;
}

/* delivered 抽屉「去安装」按钮区。 */
.ticket-detail-actions {
  margin-top: 16px;
}
</style>
