<template>
  <div style="display: grid; gap: 18px">
    <!-- 工单队列：顶部状态/关键字筛选 + n-data-table 列表。 -->
    <n-card :bordered="true">
      <template #header>
        <div>
          <p class="eyebrow">Platform</p>
          <h2 style="margin: 0">定制技能工单</h2>
        </div>
      </template>

      <!-- 筛选区：状态下拉 + 关键字输入，均为前端 computed 过滤（数据量小，不发额外请求）。 -->
      <div class="ticket-filters">
        <n-select
          v-model:value="filterStatus"
          :options="statusFilterOptions"
          size="small"
          style="width: 160px"
        />
        <n-input
          v-model:value="filterKeyword"
          size="small"
          clearable
          placeholder="按标题/描述关键字过滤"
          style="width: 240px"
        />
      </div>

      <!-- 加载态 / 错误态 / 数据表。 -->
      <div v-if="ticketsQuery.isLoading.value" class="state-text">加载中…</div>
      <p v-else-if="ticketsQuery.error.value" class="state-text danger">
        工单查询失败：{{ ticketsQuery.error.value?.message }}
      </p>
      <n-data-table
        v-else
        :columns="columns"
        :data="filteredTickets"
        size="small"
        :bordered="false"
        :row-key="(row: SkillTicket) => row.id"
      />
    </n-card>

    <!-- 宽抽屉：左=需求+对话+回复，右=操作区（状态/报价/拒绝/交付）。 -->
    <n-drawer :show="detailOpen" :width="640" placement="right" @update:show="onDrawerToggle">
      <n-drawer-content :title="detail?.title ?? '工单详情'" closable>
        <div v-if="detailQuery.isLoading.value" class="state-text">加载中…</div>
        <p v-else-if="detailQuery.error.value" class="state-text danger">详情查询失败</p>
        <div v-else-if="detail" class="ticket-detail-layout">
          <!-- ===== 左列：需求 / 附件 / 对话流 / 回复 ===== -->
          <div class="ticket-detail-left">
            <!-- 头部：状态徽章 + 提交者 + 报价。 -->
            <div class="ticket-detail-head">
              <n-tag :type="statusTag(detail.status).type" size="small" :bordered="false">
                {{ statusTag(detail.status).label }}
              </n-tag>
              <span class="ticket-detail-meta">提交者：{{ roleLabel(detail.requester_role) }}</span>
              <span class="ticket-detail-meta">报价 {{ yuan(detail.quote_amount_cents) }}</span>
            </div>

            <!-- rejected 工单回显已填的拒绝原因。 -->
            <div v-if="detail.status === 'rejected' && detail.reject_reason" class="ticket-reject">
              拒绝原因：{{ detail.reject_reason }}
            </div>

            <!-- 需求描述。 -->
            <p v-if="detail.description" class="ticket-detail-desc">{{ detail.description }}</p>

            <!-- 附件下载列表。 -->
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
            </div>

            <!-- 对话流：按 author_user_id 是否当前管理员区分左右气泡。 -->
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

            <!-- 回复框：管理员发评论，任何状态可发。 -->
            <div class="ticket-reply">
              <n-input
                v-model:value="replyText"
                type="textarea"
                :autosize="{ minRows: 2, maxRows: 5 }"
                placeholder="回复申请人…"
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
          </div>

          <!-- ===== 右列：操作区（状态 / 报价 / 拒绝 / 交付） ===== -->
          <div class="ticket-detail-right">
            <strong>处理操作</strong>

            <!-- 状态：pending / processing 互转。 -->
            <div class="op-field">
              <label class="op-label">状态</label>
              <n-select
                v-model:value="statusEdit"
                :options="statusEditOptions"
                size="small"
              />
              <n-button
                size="small"
                :loading="statusMut.isPending.value"
                :disabled="!statusEdit || statusEdit === detail.status"
                @click="onSaveStatus"
              >
                保存状态
              </n-button>
            </div>

            <!-- 报价：元为输入单位，提交时 *100 取整成分。 -->
            <div class="op-field">
              <label class="op-label">报价（元）</label>
              <n-input-number
                v-model:value="quoteYuan"
                size="small"
                :min="0"
                :precision="2"
                placeholder="如 99.00"
                style="width: 100%"
              />
              <n-button
                size="small"
                :loading="quoteMut.isPending.value"
                :disabled="quoteYuan == null"
                @click="onSaveQuote"
              >
                保存报价
              </n-button>
            </div>

            <!-- 拒绝 / 交付：打开各自的弹窗。 -->
            <div class="op-actions">
              <n-button size="small" type="error" @click="rejectOpen = true">拒绝</n-button>
              <n-button size="small" type="primary" @click="openDeliver">交付</n-button>
            </div>
          </div>
        </div>
      </n-drawer-content>
    </n-drawer>

    <!-- 拒绝弹窗：填写原因后调用 useRejectSkillTicket。 -->
    <n-modal
      v-model:show="rejectOpen"
      preset="card"
      title="拒绝工单"
      class="reject-modal"
      :style="{ width: '460px' }"
    >
      <n-form>
        <n-form-item label="拒绝原因">
          <n-input
            v-model:value="rejectReason"
            type="textarea"
            :autosize="{ minRows: 3, maxRows: 6 }"
            placeholder="说明拒绝原因，申请人可在补充说明后重新提交"
          />
        </n-form-item>
      </n-form>
      <template #footer>
        <div class="modal-footer">
          <n-button @click="rejectOpen = false">取消</n-button>
          <n-button
            type="error"
            :loading="rejectMut.isPending.value"
            :disabled="!rejectReason.trim()"
            @click="onReject"
          >
            确认拒绝
          </n-button>
        </div>
      </template>
    </n-modal>

    <!-- 交付弹窗：复用 PlatformSkillsPage 的「粘贴 Markdown / 上传文件夹」打包交互；无版本输入。 -->
    <n-modal
      v-model:show="deliverOpen"
      preset="card"
      title="交付定制技能"
      class="deliver-modal"
      :style="{ width: '640px', maxWidth: 'calc(100vw - 32px)' }"
    >
      <n-form label-placement="top">
        <!-- 上传方式切换：粘贴 Markdown / 上传 skill 文件夹（整段交互照 PlatformSkillsPage）。 -->
        <n-form-item label="上传方式">
          <n-radio-group v-model:value="mode">
            <n-radio-button value="markdown">粘贴 Markdown</n-radio-button>
            <n-radio-button value="folder">上传文件夹</n-radio-button>
          </n-radio-group>
        </n-form-item>

        <!-- 粘贴 Markdown：内容即单个 SKILL.md，需含 frontmatter。 -->
        <n-form-item v-if="mode === 'markdown'" label="SKILL.md 内容 *">
          <n-input
            v-model:value="mdText"
            type="textarea"
            :rows="10"
            placeholder="粘贴 SKILL.md 全文，需含 --- 包裹的 frontmatter（至少含 name 字段）"
          />
        </n-form-item>

        <!-- 上传文件夹：选择 skill 文件夹（其中直接包含 SKILL.md）。 -->
        <n-form-item v-if="mode === 'folder'" label="Skill 文件夹 *">
          <input
            ref="folderInputRef"
            type="file"
            multiple
            style="display: none"
            @change="onFolderChange"
          />
          <div style="display: flex; align-items: center; gap: 12px">
            <n-button @click="triggerFolderInput">选择文件夹</n-button>
            <span v-if="folderName" class="state-text" style="margin: 0">{{ folderName }}（{{ folderFiles.length }} 个文件）</span>
            <span v-else class="state-text" style="margin: 0">未选择文件夹</span>
          </div>
          <p class="deliver-hint" style="margin: 8px 0 0">
            文件夹需包含 <code>SKILL.md</code> 文件；该 <code>.md</code> 文件需包含 YAML 格式的技能名称（<code>name</code>）和描述（<code>description</code>）。
          </p>
        </n-form-item>

        <!-- 解析预览：成功展示识别到的技能 name/description，失败展示红色错误提示。 -->
        <p v-if="parsed.error" class="state-text danger" style="margin: 4px 0">{{ parsed.error }}</p>
        <p v-else-if="parsed.meta" class="state-text" style="margin: 4px 0">
          识别到技能：<strong>{{ parsed.meta.name }}</strong>
          <template v-if="parsed.meta.description"> — {{ parsed.meta.description }}</template>
        </p>

        <!-- 描述：默认取自解析 description，可改；无版本输入（后端按上传时间自动生成）。 -->
        <n-form-item label="描述">
          <n-input
            v-model:value="deliverDescription"
            type="textarea"
            :rows="2"
            placeholder="技能描述（默认取自 SKILL.md，可修改）"
          />
        </n-form-item>

        <!-- 目标范围编辑器：默认据工单 requester_role + org_id 预填一条，可改受众、可加组织。 -->
        <n-form-item label="目标范围">
          <div class="deliver-targets">
            <div v-for="(t, idx) in targets" :key="idx" class="deliver-target-row">
              <span class="deliver-target-org">{{ orgLabel(t.org_id) }}</span>
              <n-select
                v-model:value="t.audience"
                :options="audienceOptions"
                size="small"
                style="width: 160px"
              />
              <n-button
                v-if="targets.length > 1"
                size="tiny"
                type="error"
                @click="removeTarget(idx)"
              >
                移除
              </n-button>
            </div>
            <!-- 加组织：有组织下拉时选 org → 追加一条 {org_id, audience:all_org}。 -->
            <div v-if="addableOrgOptions.length" class="deliver-add-org">
              <n-select
                v-model:value="addOrgId"
                :options="addableOrgOptions"
                size="small"
                clearable
                placeholder="选择组织以追加目标"
                style="width: 240px"
              />
              <n-button size="small" :disabled="!addOrgId" @click="addTarget">+ 加组织</n-button>
            </div>
          </div>
        </n-form-item>
      </n-form>

      <template #footer>
        <div class="modal-footer">
          <n-button @click="deliverOpen = false">取消</n-button>
          <n-button
            type="primary"
            :loading="deliverMut.isPending.value"
            :disabled="parsed.meta === null"
            @click="onDeliver"
          >
            确认交付
          </n-button>
        </div>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { computed, h, ref, watch } from 'vue'
import {
  NButton,
  NCard,
  NDataTable,
  NDrawer,
  NDrawerContent,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NModal,
  NRadioButton,
  NRadioGroup,
  NSelect,
  NTag,
  useMessage,
  type DataTableColumns,
  type SelectOption,
} from 'naive-ui'

import type { SkillTicket, SkillTicketAttachment, SkillTicketDetail } from '@/api'
import {
  downloadSkillTicketAttachment,
  useAddSkillTicketComment,
  useAdminSkillTicketsQuery,
  useDeliverCustomSkill,
  useRejectSkillTicket,
  useSetSkillTicketQuote,
  useSkillTicketAttachmentsQuery,
  useSkillTicketDetailQuery,
  useUpdateSkillTicketStatus,
  type DeliverTarget,
} from '@/api/hooks/useSkillTickets'
import { useOrganizationsQuery } from '@/api/hooks/useOrganizations'
import {
  packFromFolder,
  packFromMarkdown,
  parseSkillFrontmatter,
  type SkillMeta,
  type UploadedFile,
} from '@/domain/skillPackaging'
import { useAuthStore } from '@/stores/auth'

// CustomSkillTicketsPage 是平台管理员的「定制技能」工单处理页：
// 队列（筛选 + 表格）→ 宽抽屉（左需求/对话/回复，右改状态/报价/拒绝/交付）→ 交付弹窗（复用打包逻辑）。
const auth = useAuthStore()
const message = useMessage()

// ===== 队列查询与筛选 =====
const ticketsQuery = useAdminSkillTicketsQuery()
// 组织列表用于交付弹窗「加组织」下拉与目标范围 org 名称展示；仅平台管理员有权访问。
const orgsQuery = useOrganizationsQuery()

// filterStatus：状态筛选，'all' 表示不限；filterKeyword：标题/描述关键字。
const filterStatus = ref<string>('all')
const filterKeyword = ref<string>('')

// statusFilterOptions 是顶部状态筛选下拉项（含「全部」）。
const statusFilterOptions: SelectOption[] = [
  { label: '全部状态', value: 'all' },
  { label: '待处理', value: 'pending' },
  { label: '制作中', value: 'processing' },
  { label: '已交付', value: 'delivered' },
  { label: '已拒绝', value: 'rejected' },
]

// filteredTickets 在前端按状态 + 关键字过滤队列（数据量小，无需服务端分页过滤）。
const filteredTickets = computed<SkillTicket[]>(() => {
  const list = ticketsQuery.data.value ?? []
  const kw = filterKeyword.value.trim().toLowerCase()
  return list.filter((t) => {
    if (filterStatus.value !== 'all' && t.status !== filterStatus.value) return false
    if (kw) {
      const hay = `${t.title ?? ''} ${t.description ?? ''}`.toLowerCase()
      if (!hay.includes(kw)) return false
    }
    return true
  })
})

// ===== 抽屉：详情 / 附件 / 评论 =====
// selectedId：当前打开详情抽屉的工单 ID；undefined 表示未打开（详情/附件 query 据此 enabled）。
const selectedId = ref<string | undefined>()
const detailOpen = ref(false)
const detailQuery = useSkillTicketDetailQuery(selectedId)
const attachmentsQuery = useSkillTicketAttachmentsQuery(selectedId)
const commentMut = useAddSkillTicketComment(selectedId)

const detail = computed<SkillTicketDetail | null>(() => detailQuery.data.value ?? null)
const comments = computed(() => detail.value?.comments ?? [])
const attachments = computed<SkillTicketAttachment[]>(() => attachmentsQuery.data.value ?? [])

// 回复框文本。
const replyText = ref('')

// ===== 操作区编辑态（状态 / 报价） =====
const statusMut = useUpdateSkillTicketStatus()
const quoteMut = useSetSkillTicketQuote()

// statusEdit：状态下拉编辑值；quoteYuan：报价输入（元，保存时 *100 取整成分）。
const statusEdit = ref<string>('pending')
const quoteYuan = ref<number | null>(null)

// statusEditOptions：管理员可主动设置的状态（仅 pending/processing 互转；delivered/rejected 由交付/拒绝动作驱动）。
const statusEditOptions: SelectOption[] = [
  { label: '待处理', value: 'pending' },
  { label: '制作中', value: 'processing' },
]

// 打开新工单详情后，把右侧操作区编辑态初始化为该工单当前的状态与报价。
watch(detail, (d) => {
  if (!d) return
  statusEdit.value = d.status === 'processing' ? 'processing' : 'pending'
  quoteYuan.value = d.quote_amount_cents == null ? null : d.quote_amount_cents / 100
})

// ===== 拒绝弹窗 =====
const rejectMut = useRejectSkillTicket()
const rejectOpen = ref(false)
const rejectReason = ref('')

// ===== 交付弹窗（复用 PlatformSkillsPage 的打包交互） =====
const deliverMut = useDeliverCustomSkill()
const deliverOpen = ref(false)
// 上传方式：markdown=粘贴 SKILL.md 全文；folder=上传 skill 文件夹。
const mode = ref<'markdown' | 'folder'>('markdown')
const mdText = ref('')
const folderFiles = ref<UploadedFile[]>([])
const folderName = ref('')
const folderInputRef = ref<HTMLInputElement | null>(null)
// 交付描述：默认取自解析 description，可改。
const deliverDescription = ref('')
// targets：目标范围列表；addOrgId：「加组织」下拉的待追加组织 ID。
const targets = ref<DeliverTarget[]>([])
const addOrgId = ref<string | null>(null)

// audienceOptions：受众范围下拉项。
const audienceOptions: SelectOption[] = [
  { label: '整企业可见', value: 'all_org' },
  { label: '仅管理员可见', value: 'org_admins' },
  { label: '仅申请人可见', value: 'requester_only' },
]

// parsed 实时解析当前输入，得到 frontmatter 的 name/description 或校验错误，供预览与提交按钮使用。
// markdown 模式只解析 frontmatter（不打包）；folder 模式调用 packFromFolder 以同时校验扁平布局。
const parsed = computed<{ meta: SkillMeta | null; error: string }>(() => {
  try {
    if (mode.value === 'markdown') {
      if (!mdText.value.trim()) return { meta: null, error: '' }
      return { meta: parseSkillFrontmatter(mdText.value), error: '' }
    }
    if (folderFiles.value.length === 0) return { meta: null, error: '' }
    const r = packFromFolder(folderFiles.value)
    return { meta: { name: r.name, description: r.description }, error: '' }
  } catch (e) {
    return { meta: null, error: e instanceof Error ? e.message : String(e) }
  }
})

// 解析出 frontmatter 的 description 后自动预填到描述框（仅当用户尚未手填时），保持「自动带出但可编辑」。
watch(
  () => parsed.value.meta?.description,
  (desc) => {
    if (desc && !deliverDescription.value.trim()) {
      deliverDescription.value = desc
    }
  },
)

// ===== 展示辅助 =====

// statusTag 把工单状态映射为徽章颜色 + 中文文案。
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

// roleLabel 把申请人角色映射为中文：org_admin→管理员，其余（org_member）→成员。
function roleLabel(role?: string): string {
  return role === 'org_admin' ? '管理员' : '成员'
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

// isMine 判断评论作者是否当前登录管理员，用于气泡左右区分。
function isMine(authorUserId?: string): boolean {
  return Boolean(authorUserId && authorUserId === auth.user?.id)
}

// orgLabel 把目标 org_id 渲染为组织名称（取自组织列表）；查不到时回退到「本工单组织」或裸 ID。
function orgLabel(orgId: string): string {
  const org = (orgsQuery.data.value ?? []).find((o) => o.id === orgId)
  if (org) return org.name
  if (orgId && detail.value && orgId === detail.value.org_id) return '本工单组织'
  return orgId || '（未知组织）'
}

// addableOrgOptions：交付弹窗「加组织」下拉项（排除已在 targets 中的组织）。
const addableOrgOptions = computed<SelectOption[]>(() => {
  const used = new Set(targets.value.map((t) => t.org_id))
  return (orgsQuery.data.value ?? [])
    .filter((o) => o.id && !used.has(o.id))
    .map((o) => ({ label: o.name, value: o.id! }))
})

// ===== 默认目标范围推导 =====
// defaultTargets：据工单 requester_role + org_id 预填一条；管理员申请→仅管理员可见，成员申请→整企业可见。
function defaultTargets(ticket: SkillTicketDetail): DeliverTarget[] {
  const audience = ticket.requester_role === 'org_admin' ? 'org_admins' : 'all_org'
  return [{ org_id: ticket.org_id ?? '', audience }]
}

// ===== 抽屉开关 =====

// openDetail 由「处理」按钮触发：记录工单 ID 并打开抽屉（详情/附件 query 随 selectedId 自动拉取）。
function openDetail(row: SkillTicket) {
  selectedId.value = row.id
  detailOpen.value = true
  replyText.value = ''
}

// onDrawerToggle 同步抽屉显隐：关闭时清空 selectedId 让详情/附件 query 失效，避免下次打开闪现旧数据。
function onDrawerToggle(show: boolean) {
  detailOpen.value = show
  if (!show) selectedId.value = undefined
}

// ===== 操作动作 =====

// onSaveStatus 提交状态变更（pending/processing）。
async function onSaveStatus() {
  const id = selectedId.value
  if (!id || !statusEdit.value || statusEdit.value === detail.value?.status) return
  try {
    await statusMut.mutateAsync({ id, status: statusEdit.value })
    message.success('状态已更新')
  } catch (err) {
    message.error(err instanceof Error ? err.message : '状态更新失败')
  }
}

// onSaveQuote 提交报价：输入为元，*100 取整成分后传入。
async function onSaveQuote() {
  const id = selectedId.value
  if (!id || quoteYuan.value == null) return
  try {
    await quoteMut.mutateAsync({ id, quote_amount_cents: Math.round(quoteYuan.value * 100) })
    message.success('报价已更新')
  } catch (err) {
    message.error(err instanceof Error ? err.message : '报价更新失败')
  }
}

// onReject 提交拒绝原因；成功后关闭弹窗（hook 自动 invalidate 列表/角标）。
async function onReject() {
  const id = selectedId.value
  const reason = rejectReason.value.trim()
  if (!id || !reason) return
  try {
    await rejectMut.mutateAsync({ id, reason })
    message.success('工单已拒绝')
    rejectOpen.value = false
    rejectReason.value = ''
  } catch (err) {
    message.error(err instanceof Error ? err.message : '拒绝失败')
  }
}

// onSendReply 发送评论：管理员回复申请人；hook 自动 invalidate 详情。
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

// onDownloadAttachment 带鉴权下载附件并触发浏览器保存；失败 toast。
async function onDownloadAttachment(att: SkillTicketAttachment) {
  if (!selectedId.value) return
  try {
    await downloadSkillTicketAttachment(selectedId.value, att)
  } catch (err) {
    message.error(err instanceof Error ? err.message : '下载失败')
  }
}

// ===== 交付弹窗开关与提交 =====

// openDeliver 打开交付弹窗并据当前工单初始化默认目标范围、清空上一次的打包输入。
function openDeliver() {
  if (!detail.value) return
  mode.value = 'markdown'
  mdText.value = ''
  folderFiles.value = []
  folderName.value = ''
  deliverDescription.value = ''
  addOrgId.value = null
  targets.value = defaultTargets(detail.value)
  deliverOpen.value = true
}

// triggerFolderInput 在点击前动态设置 webkitdirectory，触发浏览器目录选择框。
// （以属性方式设置而非写在模板里，避免 webkitdirectory 这一非标准属性触发 vue-tsc 类型告警。）
function triggerFolderInput() {
  const el = folderInputRef.value
  if (!el) return
  el.setAttribute('webkitdirectory', '')
  el.setAttribute('directory', '')
  el.click()
}

// onFolderChange 读入所选文件夹下全部文件（含 webkitRelativePath 与字节），供后续打包。
async function onFolderChange(event: Event) {
  const input = event.target as HTMLInputElement
  const list = input.files
  if (!list || list.length === 0) {
    folderFiles.value = []
    folderName.value = ''
    return
  }
  const arr: UploadedFile[] = []
  for (const f of Array.from(list)) {
    const buf = new Uint8Array(await f.arrayBuffer())
    arr.push({ relativePath: f.webkitRelativePath || f.name, data: buf })
  }
  folderFiles.value = arr
  // 顶层目录名（webkitRelativePath 首段）即所选文件夹名，仅用于展示。
  folderName.value = arr[0]?.relativePath.split('/')[0] ?? ''
  // 重置后允许再次选择同一文件夹。
  input.value = ''
}

// addTarget 把「加组织」下拉选中的组织追加为新目标（默认整企业可见）；避免重复。
function addTarget() {
  const id = addOrgId.value
  if (!id || targets.value.some((t) => t.org_id === id)) return
  targets.value.push({ org_id: id, audience: 'all_org' })
  addOrgId.value = null
}

// removeTarget 从目标列表移除一条（至少保留一条，由模板的 length>1 守卫）。
function removeTarget(idx: number) {
  targets.value.splice(idx, 1)
}

// onDeliver 在浏览器内把输入打包成扁平 tar，再走 multipart 交付。
// 成功后提示并关闭弹窗（hook 已 invalidate 详情/队列/角标/市场）。
// 改名冲突（后端 409）→ message.error 展示「迭代必须沿用同一技能名」。
async function onDeliver() {
  const id = selectedId.value
  if (!id || parsed.value.meta === null) return
  try {
    const result = mode.value === 'markdown' ? packFromMarkdown(mdText.value) : packFromFolder(folderFiles.value)
    // result.tar 是 Uint8Array，作为 BlobPart 传入 File；显式标注规避新版 TS lib 对
    // ArrayBufferLike vs ArrayBuffer 的泛型差异告警。
    const file = new File([result.tar as BlobPart], `${result.name}.tar`, { type: 'application/x-tar' })
    await deliverMut.mutateAsync({
      ticketId: id,
      // 描述以用户手填为准，未填则回退到 frontmatter 的 description。
      description: deliverDescription.value.trim() || result.description,
      targets: targets.value,
      file,
    })
    message.success(`已交付技能 ${result.name}`)
    deliverOpen.value = false
  } catch (err) {
    // 后端 409：迭代交付改了技能名（必须沿用同一技能名）。
    if (isConflictError(err)) {
      message.error('迭代必须沿用同一技能名')
      return
    }
    message.error(err instanceof Error ? err.message : '交付失败')
  }
}

// isConflictError 判定错误是否为 HTTP 409 冲突；apiRequest/xhrUpload 抛出的错误带 status 字段。
function isConflictError(err: unknown): boolean {
  if (typeof err === 'object' && err !== null && 'status' in err) {
    return (err as { status?: number }).status === 409
  }
  return false
}

// ===== 队列表格列 =====
const columns: DataTableColumns<SkillTicket> = [
  // 标题列。
  { title: '标题', key: 'title', render: (row) => row.title },
  // 提交者列：requester_role → 成员/管理员（org_member 显示「成员」，org_admin 显示「管理员」）。
  {
    title: '提交者',
    key: 'requester',
    render: (row) => roleLabel(row.requester_role),
  },
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
  // 操作列：「处理」打开详情抽屉。
  {
    title: '操作',
    key: 'actions',
    render: (row) =>
      h(
        NButton,
        { size: 'small', type: 'primary', onClick: () => openDetail(row) },
        { default: () => '处理' },
      ),
  },
]
</script>

<style scoped>
/* 筛选区：状态下拉 + 关键字输入同行。 */
.ticket-filters {
  display: flex;
  gap: 12px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}

/* 抽屉双列布局：左需求/对话占主，右操作固定宽。 */
.ticket-detail-layout {
  display: flex;
  gap: 20px;
  align-items: flex-start;
}
.ticket-detail-left {
  flex: 1;
  min-width: 0;
}
.ticket-detail-right {
  width: 220px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding-left: 16px;
  border-left: 1px solid var(--color-divider, #eee);
}

/* 详情头部：状态徽章 + 提交者 + 报价同行。 */
.ticket-detail-head {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.ticket-detail-meta {
  font-size: 13px;
  color: var(--color-text-secondary, #888);
}

/* 拒绝原因块。 */
.ticket-reject {
  padding: 8px 12px;
  margin-bottom: 12px;
  font-size: 13px;
  background: var(--error-color-suppl, #fff1f0);
  border-radius: 4px;
}

/* 需求描述：保留换行。 */
.ticket-detail-desc {
  margin: 12px 0;
  font-size: 13px;
  line-height: 1.6;
  white-space: pre-wrap;
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

/* 对话流：本人（管理员）气泡靠右、对方靠左。 */
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

/* 右侧操作字段：标签 + 控件 + 保存按钮纵向排列。 */
.op-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.op-label {
  font-size: 12px;
  color: var(--color-text-secondary, #888);
}
.op-actions {
  display: flex;
  gap: 8px;
}

/* 弹窗底部按钮区。 */
.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

/* 交付弹窗目标范围编辑器。 */
.deliver-targets {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
}
.deliver-target-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.deliver-target-org {
  flex: 1;
  font-size: 13px;
  word-break: break-all;
}
.deliver-add-org {
  display: flex;
  gap: 8px;
  margin-top: 4px;
}

/* 交付弹窗格式提示文案。 */
.deliver-hint {
  font-size: 12px;
  line-height: 1.6;
  color: #8a8f99;
}
.deliver-hint code {
  padding: 1px 5px;
  background: rgba(0, 0, 0, 0.06);
  border-radius: 4px;
  font-family: ui-monospace, monospace;
  font-size: 11px;
}
</style>
