// useVoiceRecorder：封装 getUserMedia + MediaRecorder，实现 voiceController 的 Recorder 接口。
// start 申请麦克风并开始录音，stop 结束并把所有分片合成一个 Blob，同时释放音轨。
import type { Recorder } from './voiceController'

// createRecorder 返回一个 Recorder 实例；录音数据累积在闭包内。
export function createRecorder(): Recorder {
  let media: MediaRecorder | null = null
  let stream: MediaStream | null = null
  let chunks: Blob[] = []

  return {
    async start() {
      // getUserMedia 在非安全上下文或被拒时抛错，交由 voiceController 映射为 errorKey。
      stream = await navigator.mediaDevices.getUserMedia({ audio: true })
      chunks = []
      media = new MediaRecorder(stream)
      media.ondataavailable = (e) => {
        if (e.data.size > 0) chunks.push(e.data)
      }
      media.start()
    },

    stop() {
      return new Promise<Blob>((resolve) => {
        const mr = media
        if (!mr) {
          resolve(new Blob())
          return
        }
        mr.onstop = () => {
          const blob = new Blob(chunks, { type: mr.mimeType || 'audio/webm' })
          // 释放麦克风占用(否则浏览器标签页一直显示录音中)。
          stream?.getTracks().forEach((t) => t.stop())
          stream = null
          media = null
          resolve(blob)
        }
        mr.stop()
      })
    },
  }
}
