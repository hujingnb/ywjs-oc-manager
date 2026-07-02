<template>
  <!-- ModelPickerPopover：语音识别模型选择弹层。选择下载源 + 档位后点确认，
       下载中展示进度条并禁用确认。通过 v-model:show 控制显隐。 -->
  <n-modal
    :show="show"
    preset="card"
    :title="t('apps.conversations.voice.pickTitle')"
    style="width: 420px; max-width: calc(100vw - 32px)"
    :mask-closable="!downloading"
    @update:show="(v: boolean) => emit('update:show', v)"
  >
    <n-space vertical size="large">
      <!-- 下载源选择 -->
      <div>
        <div class="picker-label">{{ t('apps.conversations.voice.sourceLabel') }}</div>
        <n-radio-group v-model:value="source" :disabled="downloading">
          <n-radio-button value="domestic">{{ t('apps.conversations.voice.sourceDomestic') }}</n-radio-button>
          <n-radio-button value="official">{{ t('apps.conversations.voice.sourceOfficial') }}</n-radio-button>
        </n-radio-group>
      </div>
      <!-- 档位选择 -->
      <div>
        <div class="picker-label">{{ t('apps.conversations.voice.tierLabel') }}</div>
        <n-radio-group v-model:value="tier" :disabled="downloading">
          <n-space vertical>
            <n-radio v-for="opt in MODEL_OPTIONS" :key="opt.tier" :value="opt.tier">
              {{ tierHint(opt.tier) }}（{{ opt.sizeLabel }}）
              <!-- 该档位在当前所选源下已完整缓存到本地时标记，提示无需再次下载 -->
              <n-tag v-if="cached[opt.tier]" size="tiny" type="success" :bordered="false" class="downloaded-tag">
                {{ t('apps.conversations.voice.downloaded') }}
              </n-tag>
            </n-radio>
          </n-space>
        </n-radio-group>
      </div>
      <!-- 下载进度条 -->
      <n-progress
        v-if="downloading"
        type="line"
        :percentage="Math.round(progress * 100)"
        :indicator-placement="'inside'"
      />
    </n-space>
    <template #footer>
      <n-space justify="end">
        <n-button
          type="primary"
          :loading="downloading"
          :disabled="downloading"
          @click="onConfirm"
        >
          {{ downloading
            ? t('apps.conversations.voice.downloading', { percent: Math.round(progress * 100) })
            : t('apps.conversations.voice.confirm') }}
        </n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
// 模型选择弹层：受控组件，选择结果通过 confirm 事件回传父组件驱动 voiceController。
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NModal, NSpace, NRadioGroup, NRadioButton, NRadio, NProgress, NButton, NTag } from 'naive-ui'
import { MODEL_OPTIONS, DEFAULT_TIER, DEFAULT_SOURCE, type ModelTier, type SourceId } from './voiceSettings'
import { cachedTiers } from './modelCache'

const props = defineProps<{
  show: boolean
  // downloading 为 true 时锁定选择并显示进度(由父组件依据 voiceController.state 传入)。
  downloading: boolean
  // progress 下载进度 0..1。
  progress: number
  // 初始预选(记住上次选择)。
  initialTier: ModelTier | null
  initialSource: SourceId
}>()
const emit = defineEmits<{
  'update:show': [boolean]
  confirm: [tier: ModelTier, source: SourceId]
}>()

const { t } = useI18n()
// 本地选择态，用初始值(或默认)预填，便于用户直接确认。
const tier = ref<ModelTier>(props.initialTier ?? DEFAULT_TIER)
const source = ref<SourceId>(props.initialSource ?? DEFAULT_SOURCE)
// cached 记录各档位在「当前所选源」下是否已下载到本地缓存（缓存按源区分），用于打「已下载」标记。
const cached = ref<Record<string, boolean>>({})

// refreshCached 重新查询当前源下各档位的本地缓存状态。
async function refreshCached() {
  cached.value = await cachedTiers(
    MODEL_OPTIONS.map((o) => o.tier),
    source.value,
  )
}

// 弹层每次打开时把单选回填为最新持久化值，避免沿用上次未提交/已取消的临时选择，并刷新缓存标记。
watch(
  () => props.show,
  (visible) => {
    if (visible) {
      tier.value = props.initialTier ?? DEFAULT_TIER
      source.value = props.initialSource ?? DEFAULT_SOURCE
      void refreshCached()
    }
  },
)

// 切换下载源时重算缓存标记（同一档位不同源是不同缓存条目）。
watch(source, () => void refreshCached())

// 一次下载结束（downloading 由 true 变 false）后刷新，让刚下好的档位立即显示「已下载」。
watch(
  () => props.downloading,
  (now, prev) => {
    if (prev && !now) void refreshCached()
  },
)

// tierHint 按档位取对应说明文案。
function tierHint(tv: ModelTier): string {
  const map: Record<ModelTier, string> = {
    tiny: t('apps.conversations.voice.tierTiny'),
    base: t('apps.conversations.voice.tierBase'),
    small: t('apps.conversations.voice.tierSmall'),
  }
  return map[tv]
}

// onConfirm 把当前选择回传父组件，由父组件调用 voiceController.chooseModel 触发下载。
function onConfirm() {
  emit('confirm', tier.value, source.value)
}
</script>

<style scoped>
.picker-label {
  margin-bottom: 8px;
  font-weight: 600;
}
.downloaded-tag {
  margin-left: 6px;
}
</style>
