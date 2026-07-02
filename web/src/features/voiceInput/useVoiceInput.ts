// useVoiceInput：把真实的录音器、Worker 识别器、音频解码接到 voiceController，
// 并把「识别出的文本」通过回调交给调用方(对话页把它追加进 draft)。
// language 依据当前 UI 语言选择 whisper 语种以提升准确率。
import { useI18n } from 'vue-i18n'
import { createVoiceController } from './voiceController'
import { createRecorder } from './useVoiceRecorder'
import { createRecognizer } from './speechRecognizerClient'
import { decodeToPcm16k } from './audioDecode'
import { loadVoiceSettings, saveVoiceSettings } from './voiceSettings'

// useVoiceInput 接收 onText 回调(落文本目标)，返回 voiceController 的响应式接口。
export function useVoiceInput(onText: (text: string) => void) {
  const { locale } = useI18n()
  // whisper 语种名：中文界面→chinese，其余→english。
  const language = () => (String(locale.value).startsWith('zh') ? 'chinese' : 'english')

  return createVoiceController({
    recorder: createRecorder(),
    recognizer: createRecognizer(),
    decode: decodeToPcm16k,
    language,
    onText,
    loadSettings: () => loadVoiceSettings(),
    saveSettings: (s) => saveVoiceSettings(s),
  })
}
