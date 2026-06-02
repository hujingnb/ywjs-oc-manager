<template>
  <!-- 右侧帮助抽屉：不打断当前页面，按当前登录角色展示对应的使用手册 -->
  <n-drawer
    :show="show"
    :width="drawerWidth"
    placement="right"
    @update:show="(value: boolean) => emit('update:show', value)"
  >
    <n-drawer-content :title="`使用手册 · ${manual.roleLabel}`" closable :native-scrollbar="false">
      <p class="help-summary">{{ manual.summary }}</p>

      <!-- 功能介绍：逐个菜单分区说明用途、范围与边界 -->
      <h2 class="help-group-title">功能介绍</h2>
      <section
        v-for="section in manual.sections"
        :key="section.title"
        class="help-section"
      >
        <h3 class="help-section-title">{{ section.title }}</h3>
        <ul class="help-list">
          <li v-for="(item, index) in section.items" :key="index">{{ item }}</li>
        </ul>
      </section>

      <!-- 操作指引：面向常见任务的分步教程，回答「怎么用」 -->
      <h2 class="help-group-title">操作指引</h2>
      <section
        v-for="guide in manual.guides"
        :key="guide.title"
        class="help-section"
      >
        <h3 class="help-section-title">{{ guide.title }}</h3>
        <ol class="help-steps">
          <li v-for="(step, index) in guide.steps" :key="index">{{ step }}</li>
        </ol>
      </section>
    </n-drawer-content>
  </n-drawer>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NDrawer, NDrawerContent } from 'naive-ui'

import { getHelpManual } from '@/domain/helpContent'

// HelpDrawer 是无业务状态的纯展示组件：show 控制显隐，role 决定展示哪一套手册。
// 抽屉开关状态由父级（DashboardLayout）持有，组件本身只负责按角色渲染静态文案。
const props = defineProps<{
  // show 控制抽屉显隐，配合 v-model:show 使用。
  show: boolean
  // role 为当前登录用户角色；未知角色由 getHelpManual 降级到成员手册。
  role: string | undefined | null
}>()

const emit = defineEmits<{
  // update:show 透传抽屉的显隐变化，支持父级 v-model:show 双向绑定。
  (event: 'update:show', value: boolean): void
}>()

// manual 根据角色解析对应手册，role 变化时自动切换内容。
const manual = computed(() => getHelpManual(props.role))

// drawerWidth 取较宽的固定值以容纳详细文案与操作步骤；窄屏按视口宽度的 92% 自适应收窄，避免溢出。
// 后台为桌面端使用，这里取一次即可，不监听 resize。
const drawerWidth = computed(() => {
  const viewport = typeof window !== 'undefined' ? window.innerWidth : 1280
  return Math.min(680, Math.round(viewport * 0.92))
})
</script>

<style scoped>
.help-summary {
  margin: 0 0 24px;
  color: var(--color-text-secondary, #4b5563);
  line-height: 1.8;
}

.help-group-title {
  margin: 0 0 16px;
  padding-bottom: 8px;
  font-size: 16px;
  font-weight: 700;
  color: var(--color-text-primary, #1f2329);
  border-bottom: 2px solid var(--color-brand, #ff6a00);
}

/* 两大块之间留出更明显的间距 */
.help-group-title + .help-section {
  margin-top: 0;
}

.help-section + .help-group-title {
  margin-top: 32px;
}

.help-section {
  margin-bottom: 22px;
}

.help-section-title {
  margin: 0 0 8px;
  font-size: 14px;
  font-weight: 600;
  color: var(--color-text-primary, #1f2329);
}

.help-list,
.help-steps {
  margin: 0;
  padding-left: 20px;
}

.help-list li,
.help-steps li {
  margin-bottom: 7px;
  color: var(--color-text-secondary, #4b5563);
  line-height: 1.8;
}
</style>
