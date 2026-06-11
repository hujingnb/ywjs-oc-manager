<template>
  <div class="ticket-targets-editor">
    <div v-if="!modelValue.length" class="target-empty">暂无可见范围</div>
    <div v-for="target in modelValue" :key="target.org_id" class="target-row">
      <span class="target-org">{{ orgLabel(target.org_id) }}</span>
      <n-select
        :value="target.audience"
        :options="audienceOptions"
        size="small"
        class="target-audience"
        @update:value="(value) => updateAudience(target.org_id, String(value))"
      />
      <n-button quaternary size="small" @click="removeTarget(target.org_id)">移除</n-button>
    </div>
    <div class="target-add">
      <n-select
        v-model:value="pendingOrgID"
        :options="addableOrgOptions"
        size="small"
        placeholder="选择组织"
        class="target-add-select"
      />
      <n-button size="small" :disabled="!pendingOrgID" @click="addTarget">加组织</n-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { NButton, NSelect } from 'naive-ui'

import type { DeliverTarget } from '@/api/hooks/useSkillTickets'

interface OrgOption {
  id: string
  name?: string
  code?: string
}

const props = defineProps<{
  modelValue: DeliverTarget[]
  orgs: OrgOption[]
}>()

const emit = defineEmits<{ 'update:modelValue': [DeliverTarget[]] }>()

const pendingOrgID = ref<string | null>(null)

const audienceOptions = [
  { label: '整企业', value: 'all_org' },
  { label: '仅管理员', value: 'org_admins' },
  { label: '仅申请人', value: 'requester_only' },
]

const selectedOrgIDs = computed(() => new Set(props.modelValue.map((target) => target.org_id)))

const addableOrgOptions = computed(() =>
  props.orgs
    .filter((org) => !selectedOrgIDs.value.has(org.id))
    .map((org) => ({ label: orgLabel(org.id), value: org.id })),
)

// orgLabel 优先展示组织名称,其次登录标识,最后回退 id,保证数据缺字段时仍可识别。
function orgLabel(orgID: string) {
  const org = props.orgs.find((item) => item.id === orgID)
  return org?.name || org?.code || orgID
}

function updateAudience(orgID: string, audience: string) {
  emit(
    'update:modelValue',
    props.modelValue.map((target) =>
      target.org_id === orgID ? { ...target, audience } : target,
    ),
  )
}

function removeTarget(orgID: string) {
  emit(
    'update:modelValue',
    props.modelValue.filter((target) => target.org_id !== orgID),
  )
}

function addTarget() {
  if (!pendingOrgID.value) return
  emit('update:modelValue', [...props.modelValue, { org_id: pendingOrgID.value, audience: 'all_org' }])
  pendingOrgID.value = null
}
</script>

<style scoped>
.ticket-targets-editor {
  display: grid;
  gap: 10px;
}

.target-empty {
  color: #64748b;
  font-size: 13px;
}

.target-row,
.target-add {
  display: grid;
  grid-template-columns: minmax(120px, 1fr) minmax(140px, 180px) auto;
  gap: 8px;
  align-items: center;
}

.target-add {
  grid-template-columns: minmax(160px, 1fr) auto;
}

.target-org {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: #334155;
}

.target-audience,
.target-add-select {
  width: 100%;
}
</style>
