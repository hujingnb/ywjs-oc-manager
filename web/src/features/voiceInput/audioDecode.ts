// audioDecode：把 MediaRecorder 产出的录音 Blob 解码并重采样为 whisper 要求的
// 16kHz 单声道 Float32 PCM。使用 OfflineAudioContext 一步完成解码+下混+重采样，
// 兼容各浏览器录音编码(webm/opus、mp4/aac 等)。仅在浏览器运行。
const TARGET_SAMPLE_RATE = 16000

// decodeToPcm16k 解码录音 Blob → 单声道 16kHz Float32Array。
// 空录音(0 采样)返回空数组，交由上层判定为「未识别到语音」。
export async function decodeToPcm16k(blob: Blob): Promise<Float32Array> {
  const arrayBuffer = await blob.arrayBuffer()
  if (arrayBuffer.byteLength === 0) return new Float32Array(0)

  // 先用临时 AudioContext 解码为 AudioBuffer(拿到原始采样率与声道)。
  const AudioCtx = window.AudioContext || (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext
  const tmpCtx = new AudioCtx()
  let decoded: AudioBuffer
  try {
    decoded = await tmpCtx.decodeAudioData(arrayBuffer.slice(0))
  } finally {
    void tmpCtx.close()
  }

  // 用 OfflineAudioContext 以目标采样率渲染，得到重采样后的单声道数据。
  const frameCount = Math.ceil((decoded.duration || 0) * TARGET_SAMPLE_RATE)
  if (frameCount === 0) return new Float32Array(0)
  const offline = new OfflineAudioContext(1, frameCount, TARGET_SAMPLE_RATE)
  const src = offline.createBufferSource()
  src.buffer = decoded
  src.connect(offline.destination)
  src.start()
  const rendered = await offline.startRendering()
  return rendered.getChannelData(0).slice()
}
