<template>
  <!-- OrgSkillsPage：企业用户左侧「技能」入口页，通过 useOwnApp 取当前用户自己的实例后复用 SkillManager。 -->
  <div>
    <!-- 加载态：等待用户实例信息返回时展示加载提示。 -->
    <div v-if="isLoading" class="state-text">{{ t('common.status.loading') }}</div>
    <!-- 无实例态：不再整页空态，而是仍渲染定制技能工单提交/跟踪（SkillTicketPanel 自包含、per-user、不需要 appId），
         顶部加提示 banner 说明无实例可提交需求、但需有实例才能安装。 -->
    <template v-else-if="!hasApp">
      <n-alert type="info" :bordered="false" class="no-app-banner">
        {{ t('skills.state.noApp') }}
      </n-alert>
      <!-- 无实例场景下「去安装」无可用实例，提示用户先创建实例再安装。 -->
      <SkillTicketPanel @go-install="onGoInstallWithoutApp" />
    </template>
    <!-- 有实例态：实例就绪时将 appId 传给 SkillManager；SkillManager 内部 inject app 做权限。
         开启「定制技能」工单 tab（show-tickets），可在市场内安装定制技能。 -->
    <SkillManager v-else :app-id="appId!" :show-tickets="true" />
  </div>
</template>

<script setup lang="ts">
import { provide } from 'vue'
import { NAlert, useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'

import SkillManager from '@/components/SkillManager.vue'
import SkillTicketPanel from '@/components/SkillTicketPanel.vue'
import { useOwnApp } from '@/composables/useOwnApp'

defineOptions({ name: 'OrgSkillsPage' })

// OrgSkillsPage 现同时服务 org_member 与 org_admin 两类用户：各自通过 useOwnApp 取自己的实例。
// 有实例时把 appId 传给 SkillManager 复用技能列表+市场逻辑；无实例时仅渲染工单面板（仍可提交定制技能需求）。
// 此页面无 allowedRoles 限制，org_member 与 org_admin 均可直接访问。
const { appId, hasApp, isLoading, app } = useOwnApp()

const message = useMessage()
const { t } = useI18n()

// provide('app')：把用户实例对象注入给 SkillManager，使其 canManageAppSkill 能判定本人归属，
// 从而在市场展示「安装」按钮（用户可安装包括定制技能在内的 skill 到自己实例）。
// 管理员 per-app 入口由 AppDetailPage 另行 provide('app')，两条路径一致。
provide('app', app)

// onGoInstallWithoutApp：无实例时 delivered 工单的「去安装」无可落地实例，提示用户先创建实例再安装。
function onGoInstallWithoutApp() {
  message.info(t('skills.messages.createAppFirst'))
}
</script>

<style scoped>
/* 无实例提示 banner：与下方工单面板留出间距。 */
.no-app-banner {
  margin-bottom: 16px;
}
</style>
