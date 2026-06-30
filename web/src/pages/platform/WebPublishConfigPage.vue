<template>
  <div style="display: grid; gap: 18px">
    <!-- 企业选择器 -->
    <n-card :bordered="true">
      <template #header>
        <div>
          <p class="eyebrow">Platform · Web Publish</p>
          <h2 style="margin: 0">{{ t('platform.webPublishConfig.title') }}</h2>
        </div>
      </template>
      <n-form-item :label="t('platform.webPublishConfig.labelOrg')" label-placement="top">
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

            <!-- DNS provider：必填；值与后端白名单对齐 alidns/huaweicloud/tencentcloud/cmcccloud -->
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

      <!-- 开通 / 停用操作卡片 -->
      <n-card :bordered="true">
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

      <!-- 证书状态面板：复用 WebPublishCertPanel，canRetry=true 允许平台管理员重试 -->
      <WebPublishCertPanel :org-id="selectedOrgId" :can-retry="true" />
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
import { computed, reactive, ref } from 'vue'
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

// WebPublishConfigPage 是平台管理员专属页，用于开通和配置企业 web-publish 能力。
// 企业选择使用下拉选择器（in-page selector），与 RechargePage 使用路由参数 :orgId 不同，
// 此页需跨企业快速切换，使用在页面选择器更符合操作习惯。
const { t } = useI18n()

// 企业列表：平台管理员视角，仅平台管理员有权访问。
const { data: organizations, isLoading: orgsLoading } = useOrganizationsQuery()

// orgOptions 将企业列表转为 NSelect 选项；含企业名和 code 便于快速识别。
const orgOptions = computed(() =>
  (organizations.value ?? []).map(org => ({
    label: `${org.name} (${org.code})`,
    value: org.id,
  }))
)

// selectedOrgId 保存当前选中的企业 ID，是页面后续所有查询和操作的驱动源。
const selectedOrgId = ref<string | undefined>(undefined)

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
const dnsProviderOptions = [
  { label: '阿里云 DNS (alidns)', value: 'alidns' },
  { label: '华为云 DNS (huaweicloud)', value: 'huaweicloud' },
  { label: '腾讯云 DNS (tencentcloud)', value: 'tencentcloud' },
  { label: '移动云 DNS (cmcccloud)', value: 'cmcccloud' },
]

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
