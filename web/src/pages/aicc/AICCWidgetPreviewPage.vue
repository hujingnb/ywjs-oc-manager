<template>
  <main class="aicc-widget-preview-page">
    <header class="preview-header">
      <div>
        <p>{{ t('aicc.manager.widgetPreview.eyebrow') }}</p>
        <h1>{{ t('aicc.manager.widgetPreview.title') }}</h1>
      </div>
      <n-button secondary @click="goBack">
        {{ t('aicc.manager.widgetPreview.back') }}
      </n-button>
    </header>

    <section class="preview-hero">
      <p>{{ t('aicc.manager.widgetPreview.heroEyebrow') }}</p>
      <h2>{{ t('aicc.manager.widgetPreview.heroTitle') }}</h2>
      <span>{{ t('aicc.manager.widgetPreview.heroDescription') }}</span>
    </section>

    <section class="preview-grid" :aria-label="t('aicc.manager.widgetPreview.cardsLabel')">
      <article class="preview-card">
        <h3>{{ t('aicc.manager.widgetPreview.cardPricingTitle') }}</h3>
        <p>{{ t('aicc.manager.widgetPreview.cardPricingText') }}</p>
      </article>
      <article class="preview-card">
        <h3>{{ t('aicc.manager.widgetPreview.cardProductTitle') }}</h3>
        <p>{{ t('aicc.manager.widgetPreview.cardProductText') }}</p>
      </article>
      <article class="preview-card">
        <h3>{{ t('aicc.manager.widgetPreview.cardFaqTitle') }}</h3>
        <p>{{ t('aicc.manager.widgetPreview.cardFaqText') }}</p>
      </article>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NButton } from 'naive-ui'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const widgetToken = computed(() => String(route.params.widgetToken || ''))
let scriptEl: HTMLScriptElement | undefined

// 挂件脚本按企业官网集成方式真实加载；预览页只负责提供模拟官网内容和 token。
onMounted(() => {
  if (!widgetToken.value || typeof document === 'undefined') return
  scriptEl = document.createElement('script')
  scriptEl.src = `${window.location.origin}/aicc-widget.js`
  scriptEl.dataset.aiccWidgetToken = widgetToken.value
  scriptEl.async = true
  document.body.appendChild(scriptEl)
})

onBeforeUnmount(() => {
  scriptEl?.remove()
  document.querySelectorAll('[data-aicc-widget-launcher], [data-aicc-widget-frame]').forEach(element => element.remove())
})

function goBack() {
  router.push('/aicc-console')
}
</script>

<style scoped>
.aicc-widget-preview-page {
  min-height: 100vh;
  padding: 28px;
  color: var(--color-text-primary);
  background: var(--color-bg);
}

.preview-header {
  display: flex;
  gap: 16px;
  align-items: center;
  justify-content: space-between;
  max-width: 1080px;
  margin: 0 auto 22px;
}

.preview-header p,
.preview-hero p {
  margin: 0;
  color: var(--color-text-secondary);
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.preview-header h1 {
  margin: 4px 0 0;
  font-size: 22px;
}

.preview-hero {
  display: grid;
  gap: 14px;
  max-width: 1080px;
  margin: 0 auto;
  padding: 34px;
  border: 1px solid var(--color-divider);
  border-radius: 8px;
  background: var(--color-surface);
}

.preview-hero h2 {
  margin: 0;
  font-size: 34px;
  line-height: 1.2;
}

.preview-hero span,
.preview-card p {
  color: var(--color-text-secondary);
  line-height: 1.7;
}

.preview-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
  max-width: 1080px;
  margin: 18px auto 96px;
}

.preview-card {
  min-height: 154px;
  padding: 18px;
  border: 1px solid var(--color-divider);
  border-radius: 8px;
  background: var(--color-surface);
}

.preview-card h3 {
  margin: 0 0 10px;
  font-size: 16px;
}

.preview-card p {
  margin: 0;
}

@media (max-width: 760px) {
  .aicc-widget-preview-page {
    padding: 16px;
  }

  .preview-header {
    align-items: stretch;
    flex-direction: column;
  }

  .preview-hero {
    padding: 22px;
  }

  .preview-hero h2 {
    font-size: 26px;
  }

  .preview-grid {
    grid-template-columns: 1fr;
  }
}
</style>
