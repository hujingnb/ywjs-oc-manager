<template>
  <!-- AuthLayout 承载登录相关页面：背景层铺满视口，内容层负责 hero 与登录卡片整体居中。 -->
  <main class="auth-stage">
    <!-- 背景层：神经网络粒子画布 + 极光 + 网格 + 扫描光带，全部为纯装饰，不参与交互。 -->
    <canvas ref="neural" class="auth-neural" aria-hidden="true"></canvas>
    <div class="auth-aurora" aria-hidden="true"></div>
    <div class="auth-grid" aria-hidden="true"></div>
    <div class="auth-scan" aria-hidden="true"></div>

    <!-- 内容层：把平台介绍和登录卡片作为一个整体居中，避免大屏下左右分散。 -->
    <div class="auth-content">
      <section class="auth-hero" :aria-label="t('layout.auth.heroLabel')">
        <div class="auth-hero-copy">
          <div class="auth-eyebrow">ENTERPRISE AI AGENT PLATFORM</div>
          <h1 class="auth-title">{{ t('layout.auth.titlePrefix') }}<span class="auth-title-hot">{{ t('layout.auth.titleHot') }}</span>{{ t('layout.auth.titleSuffix') }}</h1>
          <p class="auth-lead">
            {{ t('layout.auth.lead') }}
          </p>
        </div>
        <div class="auth-metrics" :aria-label="t('layout.auth.metricsLabel')">
          <div class="auth-metric">
            <strong>{{ t('layout.auth.metrics.agent.title') }}</strong>
            <span>{{ t('layout.auth.metrics.agent.desc') }}</span>
          </div>
          <div class="auth-metric">
            <strong>{{ t('layout.auth.metrics.unified.title') }}</strong>
            <span>{{ t('layout.auth.metrics.unified.desc') }}</span>
          </div>
          <div class="auth-metric">
            <strong>{{ t('layout.auth.metrics.custom.title') }}</strong>
            <span>{{ t('layout.auth.metrics.custom.desc') }}</span>
          </div>
        </div>
      </section>

      <section class="auth-login-shell" :aria-label="t('layout.auth.loginLabel')">
        <LoginForm />
      </section>
    </div>
  </main>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import LoginForm from './LoginForm.vue'

const { t } = useI18n()

// neural 指向背景画布元素，登录页挂载后在其上绘制连线粒子动效。
const neural = ref<HTMLCanvasElement | null>(null)

// 动效相关的运行时句柄，组件卸载时需逐一释放，避免离开登录页后仍占用 rAF。
let rafId = 0
let stopResize: (() => void) | null = null

onMounted(() => {
  const canvas = neural.value
  const ctx = canvas?.getContext('2d')
  if (!canvas || !ctx) return

  // dots 保存所有粒子的位置、速度、半径与颜色；colors 为品牌科技色板。
  const dots: Array<{ x: number; y: number; vx: number; vy: number; r: number; c: string }> = []
  const colors = ['#20d7ff', '#72ffb6', '#8a5cff']

  // resize 让画布跟随视口尺寸并按设备像素比放大，保证高分屏下线条清晰。
  function resize() {
    const dpr = Math.min(window.devicePixelRatio || 1, 2)
    canvas!.width = Math.floor(window.innerWidth * dpr)
    canvas!.height = Math.floor(window.innerHeight * dpr)
    canvas!.style.width = `${window.innerWidth}px`
    canvas!.style.height = `${window.innerHeight}px`
    ctx!.setTransform(dpr, 0, 0, dpr, 0, 0)
  }

  // seed 按视口宽度推算粒子数量并随机初始化，窗口缩放时重新播种。
  function seed() {
    dots.length = 0
    const count = Math.min(78, Math.max(42, Math.floor(window.innerWidth / 22)))
    for (let i = 0; i < count; i++) {
      dots.push({
        x: Math.random() * window.innerWidth,
        y: Math.random() * window.innerHeight,
        vx: (Math.random() - 0.5) * 0.34,
        vy: (Math.random() - 0.5) * 0.34,
        r: Math.random() * 1.8 + 0.8,
        c: colors[i % colors.length],
      })
    }
  }

  // draw 每帧推进粒子位置、绘制邻近粒子连线与粒子本体，并递归请求下一帧。
  function draw() {
    ctx!.clearRect(0, 0, window.innerWidth, window.innerHeight)
    for (const dot of dots) {
      dot.x += dot.vx
      dot.y += dot.vy
      // 粒子飘出视口边界后从对侧回绕，保持整体密度恒定。
      if (dot.x < -20) dot.x = window.innerWidth + 20
      if (dot.x > window.innerWidth + 20) dot.x = -20
      if (dot.y < -20) dot.y = window.innerHeight + 20
      if (dot.y > window.innerHeight + 20) dot.y = -20
    }

    // 两两计算距离，距离小于阈值时按远近渐隐绘制连线，形成神经网络观感。
    for (let i = 0; i < dots.length; i++) {
      for (let j = i + 1; j < dots.length; j++) {
        const a = dots[i]
        const b = dots[j]
        const dist = Math.hypot(a.x - b.x, a.y - b.y)
        if (dist < 155) {
          ctx!.globalAlpha = (1 - dist / 155) * 0.34
          ctx!.strokeStyle = '#20d7ff'
          ctx!.lineWidth = 1
          ctx!.beginPath()
          ctx!.moveTo(a.x, a.y)
          ctx!.lineTo(b.x, b.y)
          ctx!.stroke()
        }
      }
    }

    for (const dot of dots) {
      ctx!.globalAlpha = 0.85
      ctx!.fillStyle = dot.c
      ctx!.beginPath()
      ctx!.arc(dot.x, dot.y, dot.r, 0, Math.PI * 2)
      ctx!.fill()
    }
    ctx!.globalAlpha = 1
    rafId = window.requestAnimationFrame(draw)
  }

  // onResize 在视口变化时重建画布与粒子，避免拉伸变形。
  const onResize = () => {
    resize()
    seed()
  }
  window.addEventListener('resize', onResize)
  stopResize = () => window.removeEventListener('resize', onResize)

  resize()
  seed()
  draw()
})

onBeforeUnmount(() => {
  // 离开登录页时停止动画循环并解绑监听，防止后台持续占用资源。
  if (rafId) window.cancelAnimationFrame(rafId)
  stopResize?.()
})
</script>

<style scoped>
.auth-stage {
  /* 局部科技色板，通过 CSS 自定义属性向登录卡片（子组件）继承。 */
  --auth-cyan: #20d7ff;
  --auth-violet: #8a5cff;
  --auth-lime: #72ffb6;
  --auth-orange: #ff7a1a;

  position: relative;
  min-height: 100vh;
  min-height: 100dvh;
  display: grid;
  align-items: center;
  justify-items: center;
  padding: clamp(38px, 6vh, 56px) clamp(22px, 6vw, 92px);
  isolation: isolate;
  overflow-x: hidden;
  overflow-y: auto;
  color: #f8fbff;
}

.auth-content {
  width: min(100%, 1280px);
  display: grid;
  grid-template-columns: minmax(0, 1.1fr) minmax(428px, 488px);
  align-items: center;
  gap: clamp(28px, 4vw, 56px);
}

.auth-neural {
  position: fixed;
  inset: 0;
  width: 100vw;
  height: 100vh;
  z-index: -4;
  background:
    radial-gradient(circle at 70% 20%, rgba(52, 120, 255, 0.55), transparent 34%),
    radial-gradient(circle at 25% 80%, rgba(32, 215, 255, 0.36), transparent 34%),
    linear-gradient(135deg, #041025 0%, #071b46 54%, #0b1230 100%);
}

.auth-aurora {
  position: fixed;
  inset: -18%;
  z-index: -3;
  background:
    conic-gradient(from 130deg at 55% 45%, transparent, rgba(32, 215, 255, 0.23), transparent 26%),
    conic-gradient(from 310deg at 28% 70%, transparent, rgba(138, 92, 255, 0.22), transparent 28%),
    linear-gradient(110deg, transparent 28%, rgba(114, 255, 182, 0.1), transparent 52%);
  filter: blur(34px) saturate(130%);
  animation: auth-drift 16s ease-in-out infinite alternate;
}

.auth-grid {
  position: fixed;
  inset: 0;
  z-index: -2;
  background-image:
    linear-gradient(rgba(255, 255, 255, 0.032) 1px, transparent 1px),
    linear-gradient(90deg, rgba(255, 255, 255, 0.032) 1px, transparent 1px);
  background-size: 72px 72px;
  mask-image: linear-gradient(90deg, transparent, #000 18%, #000 86%, transparent);
}

.auth-scan {
  position: fixed;
  inset: 0;
  z-index: -1;
  background: linear-gradient(100deg, transparent 0%, rgba(32, 215, 255, 0.12) 46%, transparent 58%);
  transform: translateX(-110%);
  animation: auth-scan 7s cubic-bezier(0.32, 0, 0.2, 1) infinite;
}

.auth-hero {
  width: 100%;
  max-width: 760px;
  min-height: 576px;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
}

.auth-hero-copy {
  max-width: 760px;
}

.auth-eyebrow {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  height: 34px;
  padding: 0 14px;
  border: 1px solid rgba(32, 215, 255, 0.38);
  background: rgba(4, 18, 42, 0.5);
  color: #9eeeff;
  font-size: 13px;
  backdrop-filter: blur(14px);
}

.auth-eyebrow::before {
  content: '';
  width: 8px;
  height: 8px;
  background: var(--auth-lime);
  box-shadow: 0 0 18px var(--auth-lime);
}

.auth-title {
  margin: 24px 0 18px;
  max-width: 720px;
  color: #ffffff;
  font-size: clamp(46px, 6vw, 84px);
  line-height: 1.16;
  font-weight: 760;
}

.auth-title-hot {
  background: linear-gradient(90deg, #ff8a22, #ff6b16 52%, #ffb13d);
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
}

.auth-lead {
  margin: 14px 0 0;
  max-width: 650px;
  color: rgba(236, 247, 255, 0.78);
  font-size: 20px;
  line-height: 1.7;
}

.auth-metrics {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 14px;
  max-width: 650px;
}

.auth-metric {
  min-height: 116px;
  padding: 18px;
  border: 1px solid rgba(255, 255, 255, 0.14);
  background: linear-gradient(180deg, rgba(255, 255, 255, 0.14), rgba(255, 255, 255, 0.06));
  backdrop-filter: blur(18px);
}

.auth-metric strong {
  display: block;
  color: #ffffff;
  font-size: 26px;
  line-height: 1.15;
}

.auth-metric span {
  display: block;
  margin-top: 10px;
  color: rgba(219, 237, 255, 0.72);
  font-size: 13px;
  line-height: 1.45;
}

.auth-login-shell {
  position: relative;
  width: min(100%, 428px);
  justify-self: center;
}

.auth-login-shell::before {
  content: '';
  position: absolute;
  inset: -18px;
  border: 1px solid rgba(32, 215, 255, 0.28);
  background:
    linear-gradient(90deg, var(--auth-cyan), transparent 22%) top left / 52% 1px no-repeat,
    linear-gradient(180deg, var(--auth-cyan), transparent 22%) top left / 1px 52% no-repeat,
    linear-gradient(270deg, var(--auth-violet), transparent 22%) bottom right / 52% 1px no-repeat,
    linear-gradient(0deg, var(--auth-violet), transparent 22%) bottom right / 1px 52% no-repeat;
  filter: drop-shadow(0 0 28px rgba(32, 215, 255, 0.22));
  pointer-events: none;
}

@keyframes auth-drift {
  from {
    transform: translate3d(-2%, -1%, 0) rotate(-1deg);
  }
  to {
    transform: translate3d(2%, 1%, 0) rotate(1deg);
  }
}

@keyframes auth-scan {
  0%,
  56% {
    transform: translateX(-110%);
  }
  100% {
    transform: translateX(110%);
  }
}

@media (max-width: 980px) {
  .auth-stage {
    align-items: start;
    padding: 38px 22px;
  }

  .auth-content {
    grid-template-columns: 1fr;
    justify-items: center;
    gap: 32px;
  }

  .auth-hero {
    max-width: none;
    min-height: 0;
    display: block;
  }

  .auth-login-shell {
    width: 100%;
    max-width: 520px;
  }

  .auth-metrics {
    grid-template-columns: 1fr;
    margin-top: 32px;
  }
}

@media (max-width: 560px) {
  .auth-stage {
    padding: 32px 20px;
  }

  .auth-content {
    gap: 32px;
  }

  .auth-login-shell::before {
    inset: -12px;
  }

  .auth-lead {
    font-size: 16px;
  }
}

@media (max-height: 720px) and (min-width: 981px) {
  .auth-stage {
    align-items: start;
  }
}
</style>
