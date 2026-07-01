<template>
  <!-- VoiceInputButton：对话输入区的麦克风按钮 + 模型选择弹层。
       点击麦克风开始/结束录音；未选过档位则先弹选择框；识别结果经 onText 追加到草稿。
       录音中按钮高亮并显示状态提示；识别/下载中禁用点击。 -->
  <div class="voice-input">
    <n-tooltip trigger="hover">
      <template #trigger>
        <n-button
          circle
          :type="ctrl.state.value === 'recording' ? 'error' : 'default'"
          :disabled="disabled || busy"
          :loading="ctrl.state.value === 'transcribing' || ctrl.state.value === 'downloading'"
          :class="{ 'voice-input__rec': ctrl.state.value === 'recording' }"
          data-test="voice-toggle"
          @click="ctrl.toggle()"
        >
          🎤
        </n-button>
      </template>
      {{ hint }}
    </n-tooltip>
    <!-- 切换模型入口：小号文字按钮 -->
    <n-button text size="tiny" :disabled="disabled || busy" @click="openPicker">
      {{ t('apps.conversations.voice.switch') }}
    </n-button>

    <ModelPickerPopover
      :show="pickerShow"
      :downloading="ctrl.state.value === 'downloading'"
      :progress="ctrl.downloadProgress.value"
      :initial-tier="settings.tier"
      :initial-source="settings.source"
      @update:show="pickerShow = $event"
      @confirm="onPickerConfirm"
    />
  </div>
</template>

<script setup lang="ts">
// 麦克风按钮：驱动 useVoiceInput 状态机，串联模型选择弹层与错误提示。
import { computed, ref, watch, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NTooltip, useMessage } from 'naive-ui'
import ModelPickerPopover from './ModelPickerPopover.vue'
import { useVoiceInput } from './useVoiceInput'
import { loadVoiceSettings, type ModelTier, type SourceId } from './voiceSettings'

const props = defineProps<{
  // disabled 由对话页传入(未选中会话时禁用)。
  disabled: boolean
}>()
const emit = defineEmits<{ text: [string] }>()

const { t } = useI18n()
const message = useMessage()
// 识别文本经事件抛给对话页追加到 draft。
const ctrl = useVoiceInput((text) => emit('text', text))
// settings 用于弹层预选(展示时读一次最新值)。
const settings = ref(loadVoiceSettings())
const pickerShow = ref(false)

// busy 识别/下载/申请权限期间禁用交互。
const busy = computed(() => ['transcribing', 'downloading', 'requesting'].includes(ctrl.state.value))

// hint 按状态给 tooltip 文案。
const hint = computed(() => {
  switch (ctrl.state.value) {
    case 'recording': return t('apps.conversations.voice.recording')
    case 'transcribing': return t('apps.conversations.voice.transcribing')
    // 从缓存(重)载模型也走 downloading 态，给出进度提示避免按钮看起来卡死。
    case 'downloading': return t('apps.conversations.voice.downloading', { percent: Math.round(ctrl.downloadProgress.value * 100) })
    default: return t('apps.conversations.voice.start')
  }
})

// needModelPick 被状态机置真(首次未选档位点麦克风)时，打开弹层。
watch(ctrl.needModelPick, (v) => {
  if (v) {
    settings.value = loadVoiceSettings()
    pickerShow.value = true
  }
})

// errorKey 变化时弹 toast 提示对应错误。
watch(ctrl.errorKey, (k) => {
  if (k) message.error(t(`apps.conversations.voice.errors.${k}`))
})

// openPicker 手动打开「切换模型」弹层。
function openPicker() {
  settings.value = loadVoiceSettings()
  pickerShow.value = true
}

// onPickerConfirm 用户在弹层确认：触发下载并(成功后)自动开始录音；下载完成后关闭弹层。
async function onPickerConfirm(tier: ModelTier, source: SourceId) {
  await ctrl.chooseModel(tier, source)
  settings.value = loadVoiceSettings()
  // 仅在未报错时关闭(下载失败保留弹层供切源重试)。
  if (!ctrl.errorKey.value) pickerShow.value = false
}

// 组件卸载(切换 tab/实例)时释放 Worker 与麦克风，避免遗留孤儿 Worker 与录音指示灯常亮。
onUnmounted(() => ctrl.dispose())
</script>

<style scoped>
.voice-input {
  display: flex;
  align-items: center;
  gap: 4px;
}
/* 录音中脉冲动效，提示正在录制 */
.voice-input__rec {
  animation: voice-pulse 1.2s ease-in-out infinite;
}
@keyframes voice-pulse {
  0%, 100% { box-shadow: 0 0 0 0 rgba(208, 48, 80, 0.5); }
  50% { box-shadow: 0 0 0 6px rgba(208, 48, 80, 0); }
}
</style>
