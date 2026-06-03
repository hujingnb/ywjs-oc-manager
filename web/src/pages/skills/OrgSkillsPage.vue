<template>
  <!-- OrgSkillsPage：企业成员左侧「技能」入口页，通过 useMemberApp 取当前用户的实例后复用 SkillManager。 -->
  <div>
    <!-- 加载态：等待成员实例信息返回时展示加载提示。 -->
    <div v-if="isLoading" class="state-text">加载中…</div>
    <!-- 空态：当前账号尚未关联实例时展示占位提示。 -->
    <p v-else-if="!hasApp" class="state-text">当前账号暂无关联实例，请联系管理员创建实例后再访问。</p>
    <!-- 正常态：实例就绪时将 appId 传给 SkillManager；SkillManager 内部 inject app 做权限。 -->
    <SkillManager v-else :app-id="appId!" />
  </div>
</template>

<script setup lang="ts">
import SkillManager from '@/components/SkillManager.vue'
import { useMemberApp } from '@/composables/useMemberApp'

// OrgSkillsPage 是企业成员左侧菜单「技能」的顶级页面。
// 通过 useMemberApp 获取成员唯一实例 ID，再将 appId 传给 SkillManager 复用技能列表+市场逻辑。
// 此页面无 allowedRoles 限制，org_member 可直接访问。
const { appId, hasApp, isLoading } = useMemberApp()
</script>
