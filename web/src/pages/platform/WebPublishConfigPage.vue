<template>
  <div style="display: grid; gap: 18px">
    <!-- 企业选择器 -->
    <n-card :bordered="true">
      <template #header>
        <div>
          <p class="eyebrow">{{ isPlatformAdmin ? 'Platform · Web Publish' : 'Web Publish' }}</p>
          <h2 style="margin: 0">{{ t('platform.webPublishConfig.title') }}</h2>
        </div>
      </template>
      <!-- 平台管理员：企业选择器，可跨企业切换 -->
      <n-form-item v-if="isPlatformAdmin" :label="t('platform.webPublishConfig.labelOrg')" label-placement="top">
        <n-select
          v-model:value="selectedOrgId"
          :loading="orgsLoading"
          :options="orgOptions"
          :placeholder="t('platform.webPublishConfig.placeholderOrg')"
          clearable
          filterable
          style="max-width: 400px"
        />
      </n-form-item>
      <!-- 企业管理员：锁定本企业，无选择器；展示当前通配域名作为作用范围提示 -->
      <p v-else class="state-text">
        {{ t('platform.webPublishConfig.labelOrg') }}：本企业<span v-if="webPublishConfig?.wildcard_domain"> · {{ webPublishConfig.wildcard_domain }}</span>
      </p>
    </n-card>

    <!-- 以下区块仅在选中企业后展示 -->
    <template v-if="selectedOrgId">
      <!-- 配置表单卡片 -->
      <n-card :bordered="true">
        <template #header>
          <h3 style="margin: 0">{{ t('platform.webPublishConfig.formTitle') }}</h3>
        </template>

        <n-form label-placement="top" @submit.prevent="submitConfig">
          <n-grid :cols="2" :x-gap="14">
            <!-- 根域名：必填，企业所有站点子域名均基于此域名生成 -->
            <n-grid-item>
              <n-form-item :label="t('platform.webPublishConfig.labelBaseDomain')">
                <n-input
                  v-model:value="form.base_domain"
                  :placeholder="t('platform.webPublishConfig.placeholderBaseDomain')"
                />
              </n-form-item>
            </n-grid-item>

            <!-- DNS provider：必填；当前仅开放阿里云 alidns（dev 模式另含 local），其余三家待实现 -->
            <n-grid-item>
              <n-form-item :label="t('platform.webPublishConfig.labelDnsProvider')">
                <n-select
                  v-model:value="form.dns_provider"
                  :options="dnsProviderOptions"
                  :placeholder="t('platform.webPublishConfig.placeholderDnsProvider')"
                  style="width: 100%"
                />
              </n-form-item>
            </n-grid-item>

            <!-- 站点存活天数：<=0 时后端默认 7 -->
            <n-grid-item>
              <n-form-item :label="t('platform.webPublishConfig.labelSiteTtlDays')">
                <n-input-number
                  v-model:value="form.site_ttl_days"
                  :min="1"
                  :precision="0"
                  style="width: 100%"
                  :placeholder="t('platform.webPublishConfig.placeholderSiteTtlDays')"
                />
              </n-form-item>
            </n-grid-item>

            <!-- 最大站点数：<=0 时后端默认 20 -->
            <n-grid-item>
              <n-form-item :label="t('platform.webPublishConfig.labelMaxSites')">
                <n-input-number
                  v-model:value="form.max_sites"
                  :min="1"
                  :precision="0"
                  style="width: 100%"
                  :placeholder="t('platform.webPublishConfig.placeholderMaxSites')"
                />
              </n-form-item>
            </n-grid-item>

            <!-- 凭证区：依据所选 DNS provider 显示对应凭证字段
                 凭证为写入-only；GET 响应已脱敏，不回填，避免明文泄露 -->
            <n-grid-item :span="2">
              <div v-if="form.dns_provider" style="display: grid; gap: 0">
                <p class="state-text" style="margin-bottom: 8px">
                  {{ t('platform.webPublishConfig.credentialHint') }}
                </p>
                <n-grid :cols="2" :x-gap="14">
                  <n-grid-item>
                    <n-form-item :label="t('platform.webPublishConfig.labelAccessKeyId')">
                      <n-input
                        v-model:value="credentials.access_key_id"
                        type="password"
                        show-password-on="click"
                        :placeholder="t('platform.webPublishConfig.placeholderAccessKeyId')"
                        autocomplete="new-password"
                      />
                    </n-form-item>
                  </n-grid-item>
                  <n-grid-item>
                    <n-form-item :label="t('platform.webPublishConfig.labelAccessKeySecret')">
                      <n-input
                        v-model:value="credentials.access_key_secret"
                        type="password"
                        show-password-on="click"
                        :placeholder="t('platform.webPublishConfig.placeholderAccessKeySecret')"
                        autocomplete="new-password"
                      />
                    </n-form-item>
                  </n-grid-item>
                  <!-- 华为云额外需要 region 字段 -->
                  <n-grid-item v-if="form.dns_provider === 'huaweicloud'">
                    <n-form-item :label="t('platform.webPublishConfig.labelRegion')">
                      <n-input
                        v-model:value="credentials.region"
                        type="password"
                        show-password-on="click"
                        :placeholder="t('platform.webPublishConfig.placeholderRegion')"
                        autocomplete="new-password"
                      />
                    </n-form-item>
                  </n-grid-item>
                </n-grid>
              </div>
            </n-grid-item>

            <!-- 提交按钮 -->
            <n-grid-item :span="2">
              <n-space justify="end">
                <n-button
                  type="primary"
                  attr-type="submit"
                  :loading="configureMutation.isPending.value"
                  :disabled="!canSubmitConfig || configureMutation.isPending.value"
                >
                  {{ t('platform.webPublishConfig.saveButton') }}
                </n-button>
              </n-space>
              <p v-if="configFeedback" class="state-text" :class="{ danger: configError }">
                {{ configFeedback }}
              </p>
            </n-grid-item>
          </n-grid>
        </n-form>
      </n-card>

      <!-- 开通 / 停用操作卡片：基础设施层闸门，仅平台管理员可见可操作 -->
      <n-card v-if="isPlatformAdmin" :bordered="true">
        <template #header>
          <h3 style="margin: 0">{{ t('platform.webPublishConfig.enableDisableTitle') }}</h3>
        </template>

        <!-- 当前开通状态展示 -->
        <div v-if="configQuery.isLoading.value" class="state-text">{{ t('platform.webPublishConfig.statusLoading') }}</div>
        <template v-else-if="webPublishConfig">
          <p class="state-text" style="margin-bottom: 12px">
            {{ t('platform.webPublishConfig.currentStatus') }}
            <!-- enabled/provisioning_status 显示在此 -->
            <strong>
              {{ webPublishConfig.enabled
                ? t('platform.webPublishConfig.statusEnabled')
                : t('platform.webPublishConfig.statusDisabled') }}
            </strong>
            <span v-if="webPublishConfig.provisioning_status" style="margin-left: 8px; color: var(--color-text-secondary, #6b7280)">
              ({{ webPublishConfig.provisioning_status }})
            </span>
          </p>
          <!-- provisioning_message：失败时显示错误摘要 -->
          <p v-if="webPublishConfig.provisioning_message" class="state-text danger" style="margin-bottom: 12px">
            {{ webPublishConfig.provisioning_message }}
          </p>
        </template>

        <n-space>
          <!-- 开通按钮：对应 POST /enable；已开通时仍可重新触发 provisioning -->
          <n-button
            type="primary"
            :loading="enableMutation.isPending.value"
            :disabled="enableMutation.isPending.value || disableMutation.isPending.value"
            @click="onEnable"
          >
            {{ t('platform.webPublishConfig.enableButton') }}
          </n-button>
          <!-- 停用按钮：需二次确认，避免误操作 -->
          <n-button
            type="warning"
            :loading="disableMutation.isPending.value"
            :disabled="enableMutation.isPending.value || disableMutation.isPending.value"
            @click="disableConfirmVisible = true"
          >
            {{ t('platform.webPublishConfig.disableButton') }}
          </n-button>
        </n-space>

        <p v-if="enableDisableFeedback" class="state-text" :class="{ danger: enableDisableError }" style="margin-top: 8px">
          {{ enableDisableFeedback }}
        </p>
      </n-card>

      <!-- 证书状态面板：复用 WebPublishCertPanel；仅平台管理员可重试签发，企业管理员只读 -->
      <WebPublishCertPanel :org-id="selectedOrgId" :can-retry="isPlatformAdmin" />
    </template>

    <!-- 停用二次确认弹框 -->
    <n-modal
      v-model:show="disableConfirmVisible"
      preset="dialog"
      type="warning"
      :title="t('platform.webPublishConfig.disableConfirmTitle')"
      :content="t('platform.webPublishConfig.disableConfirmMessage')"
      :positive-text="t('platform.webPublishConfig.disableConfirmOk')"
      :negative-text="t('common.actions.cancel')"
      @positive-click="onDisable"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NCard, NForm, NFormItem, NGrid, NGridItem, NInput, NInputNumber,
  NModal, NSelect, NSpace,
} from 'naive-ui'

import { useOrganizationsQuery } from '@/api/hooks/useOrganizations'
import {
  useWebPublishConfigQuery,
  useConfigureWebPublish,
  useEnableWebPublish,
  useDisableWebPublish,
} from '@/api/hooks/useWebPublish'
import WebPublishCertPanel from '@/components/WebPublishCertPanel.vue'
import type { ConfigureWebPublishRequest } from '@/api/hooks/useWebPublish'
import { useAuthStore } from '@/stores/auth'
import { useQuery } from '@tanstack/vue-query'
import { apiRequest } from '@/api/client'

// WebPublishConfigPage：web-publish 配置页，按角色自适应。
// - 平台管理员：可跨企业选择，配置 + 开通/停用 + 重试签发；
// - 企业管理员：仅管理「自己企业且平台已开通」的配置（无企业选择器、无开通/停用、证书只读）。
const { t } = useI18n()

const auth = useAuthStore()
// isPlatformAdmin 决定页面形态：选择器/开通停用/证书重试均仅平台管理员可见。
const isPlatformAdmin = computed(() => auth.isPlatformAdmin)

// 企业列表：仅平台管理员有权访问，企业管理员不拉取（避免 403）。
const { data: organizations, isLoading: orgsLoading } = useOrganizationsQuery(() => isPlatformAdmin.value)

// orgOptions 将企业列表转为 NSelect 选项；含企业名和 code 便于快速识别。
const orgOptions = computed(() =>
  (organizations.value ?? []).map(org => ({
    label: `${org.name} (${org.code})`,
    value: org.id,
  }))
)

// selectedOrgId 保存当前作用企业 ID。平台管理员通过选择器选择；企业管理员锁定为自己所属企业。
const selectedOrgId = ref<string | undefined>(
  auth.isPlatformAdmin ? undefined : (auth.user?.org_id ?? undefined),
)

// selectedOrgIdRef 包装为满足 hooks 参数签名的响应式引用。
const selectedOrgIdRef = computed(() => selectedOrgId.value)

// 查询选中企业的 web-publish 配置及证书状态。
const configQuery = useWebPublishConfigQuery(selectedOrgIdRef)
const webPublishConfig = computed(() => configQuery.data.value ?? null)

// 三个写操作 mutation hook，均在调用时需 selectedOrgId 有值。
const configureMutation = useConfigureWebPublish(selectedOrgIdRef)
const enableMutation = useEnableWebPublish(selectedOrgIdRef)
const disableMutation = useDisableWebPublish(selectedOrgIdRef)

// DNS provider 枚举选项：与后端白名单 alidns/huaweicloud/tencentcloud/cmcccloud 对齐。
// webPublishDevMode 来自公开配置端点（platform config.WebPublish.DevSelfSignedCert）：
// 仅当平台开启本地/dev 自签模式时，下方 provider 下拉才追加「本地调试(local)」选项。
const publicConfigQuery = useQuery({
  queryKey: ['public-config'],
  queryFn: () => apiRequest<{ web_publish_dev_mode?: boolean }>('/api/v1/config', { withAuth: false }),
})
const webPublishDevMode = computed(() => Boolean(publicConfigQuery.data.value?.web_publish_dev_mode))

// DNS provider 枚举选项：当前仅开放阿里云 DNS（alidns）——huaweicloud/tencentcloud/cmcccloud
// 的通配 A 记录尚未实现，后端 Configure 也会拒绝，故先不在下拉暴露；待实现后再放开。
// dev 模式额外提供「本地调试(local)」占位 provider，配合自签证书在本地一键开通（生产不出现）。
const dnsProviderOptions = computed(() => {
  const base = [
    { label: '阿里云 DNS (alidns)', value: 'alidns' },
  ]
  if (webPublishDevMode.value) {
    base.push({ label: '本地调试 (local)', value: 'local' })
  }
  return base
})

// form 是配置表单的响应式状态，提交后不回填凭证（凭证为 write-only）。
const form = reactive({
  // base_domain：企业 web-publish 根域名（如 apps.example.com），必填。
  base_domain: '',
  // dns_provider：DNS provider 枚举值，必填。
  dns_provider: '',
  // site_ttl_days：站点存活天数；undefined 时后端默认 7。
  site_ttl_days: 7 as number | undefined,
  // max_sites：最大站点数；undefined 时后端默认 20。
  max_sites: 20 as number | undefined,
})

// credentials 是凭证表单，单独维护以便提交时按 provider 组装 map。
// 凭证明文不持久化，不回填——每次保存必须完整重新输入（与后端约定：credentials 为空时不更新已有密文）。
const credentials = reactive({
  // access_key_id：阿里云/华为云/腾讯云/移动云通用凭证 key 名。
  access_key_id: '',
  // access_key_secret：对应 secret key。
  access_key_secret: '',
  // region：华为云 DNS 额外必需字段，其他 provider 不传。
  region: '',
})

// 配置加载后回填可编辑字段，让管理员看到并续编当前配置（凭证为 write-only，绝不回填）。
// immediate + watch 兼顾切换企业（平台管理员）与首次加载（企业管理员锁定本企业）。
watch(webPublishConfig, (cfg) => {
  if (!cfg) return
  form.base_domain = cfg.base_domain ?? ''
  form.dns_provider = cfg.dns_provider ?? ''
  if (cfg.site_ttl_days) form.site_ttl_days = cfg.site_ttl_days
  if (cfg.max_sites) form.max_sites = cfg.max_sites
}, { immediate: true })

// canSubmitConfig 要求根域名和 provider 非空，才允许提交。
const canSubmitConfig = computed(() =>
  form.base_domain.trim().length > 0 && form.dns_provider.length > 0
)

// buildCredentialsMap 按当前 provider 构造凭证 map；空字段不纳入（让后端保留旧密文）。
// 任意凭证字段有值时才提交 credentials，否则传 undefined 让后端保留已有密文。
function buildCredentialsMap(): Record<string, string> | undefined {
  const creds: Record<string, string> = {}
  if (credentials.access_key_id.trim()) creds.access_key_id = credentials.access_key_id.trim()
  if (credentials.access_key_secret.trim()) creds.access_key_secret = credentials.access_key_secret.trim()
  if (form.dns_provider === 'huaweicloud' && credentials.region.trim()) {
    creds.region = credentials.region.trim()
  }
  return Object.keys(creds).length > 0 ? creds : undefined
}

// configFeedback/configError 保存配置表单提交结果的内联反馈。
const configFeedback = ref('')
const configError = ref(false)

// submitConfig 提交 web-publish 配置；凭证为空时 map 不传，后端保留已有密文。
async function submitConfig() {
  if (!selectedOrgId.value || !canSubmitConfig.value) return
  configFeedback.value = ''
  configError.value = false
  const body: ConfigureWebPublishRequest = {
    base_domain: form.base_domain.trim(),
    dns_provider: form.dns_provider,
    site_ttl_days: form.site_ttl_days,
    max_sites: form.max_sites,
    credentials: buildCredentialsMap(),
  }
  try {
    await configureMutation.mutateAsync(body)
    configFeedback.value = t('platform.webPublishConfig.saveSuccess')
    // 保存成功后清空凭证字段（明文不持久化）。
    credentials.access_key_id = ''
    credentials.access_key_secret = ''
    credentials.region = ''
  } catch (err) {
    configError.value = true
    configFeedback.value = err instanceof Error ? err.message : t('platform.webPublishConfig.saveFail')
  }
}

// enableDisableFeedback/enableDisableError 保存开通/停用操作结果的内联反馈。
const enableDisableFeedback = ref('')
const enableDisableError = ref(false)

// disableConfirmVisible 控制停用二次确认弹框显隐。
const disableConfirmVisible = ref(false)

// onEnable 调用开通接口，触发后台 provisioning job。
async function onEnable() {
  enableDisableFeedback.value = ''
  enableDisableError.value = false
  try {
    await enableMutation.mutateAsync()
    enableDisableFeedback.value = t('platform.webPublishConfig.enableSuccess')
  } catch (err) {
    enableDisableError.value = true
    enableDisableFeedback.value = err instanceof Error ? err.message : t('platform.webPublishConfig.enableFail')
  }
}

// onDisable 在二次确认后调用停用接口，写状态机为 disabled，不删除配置数据。
async function onDisable() {
  disableConfirmVisible.value = false
  enableDisableFeedback.value = ''
  enableDisableError.value = false
  try {
    await disableMutation.mutateAsync()
    enableDisableFeedback.value = t('platform.webPublishConfig.disableSuccess')
  } catch (err) {
    enableDisableError.value = true
    enableDisableFeedback.value = err instanceof Error ? err.message : t('platform.webPublishConfig.disableFail')
  }
}
</script>

<style scoped>
/* state-text：操作结果内联反馈，danger 变体表示失败。 */
.state-text {
  margin: 8px 0 0;
  font-size: 13px;
  color: var(--color-text-secondary, #6b7280);
}
.state-text.danger {
  color: var(--n-color-danger, #d03050);
}
</style>
