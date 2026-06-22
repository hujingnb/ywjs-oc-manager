import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick, ref } from 'vue'

import RAGFlowDatasetInfoDialog from './RAGFlowDatasetInfoDialog.vue'
import type { KnowledgeEmbeddingModelList, KnowledgeRAGFlowDatasetInfo } from '@/api/hooks/useKnowledge'
import { i18n } from '@/i18n'

const info = ref<KnowledgeRAGFlowDatasetInfo>({
  scope: 'org',
  target_id: 'org-1',
  target_name: '测试企业',
  status: 'ok',
  ragflow_dataset_id: 'remote-ds-1',
  ragflow_dataset_name: 'ocm-org-test',
  embedding_model: { name: 'BAAI/bge-m3', label: 'BAAI/bge-m3', provider: 'OpenAI-API-Compatible', available: true },
  doc_num: 2,
  chunk_num: 8,
})

const models = ref<KnowledgeEmbeddingModelList>({
  items: [
    { name: 'BAAI/bge-m3', label: 'BAAI/bge-m3', provider: 'OpenAI-API-Compatible', available: true },
    { name: 'netease-youdao/bce-embedding-base_v1', label: 'netease-youdao/bce-embedding-base_v1', provider: 'OpenAI-API-Compatible', available: true },
  ],
})

const mutateAsync = vi.fn()
const refetchInfo = vi.fn()
const refetchModels = vi.fn()

vi.mock('@/api/hooks/useKnowledge', () => ({
  useRAGFlowDatasetInfoQuery: () => ({
    data: info,
    isLoading: ref(false),
    error: ref(null),
    refetch: refetchInfo,
  }),
  useKnowledgeEmbeddingModelsQuery: () => ({
    data: models,
    isLoading: ref(false),
    error: ref(null),
    refetch: refetchModels,
  }),
  useUpdateRAGFlowDatasetEmbeddingModel: () => ({
    mutateAsync,
    isPending: ref(false),
  }),
}))

function passthroughStub(name: string) {
  return defineComponent({
    name,
    setup(_, { slots }) {
      return () => h('div', slots.default?.())
    },
  })
}

const selectStub = defineComponent({
  name: 'NSelect',
  props: {
    value: String,
    options: Array,
    disabled: Boolean,
  },
  emits: ['update:value'],
  setup(props, { emit }) {
    return () => h('select', {
      value: props.value,
      disabled: props.disabled,
      onChange: (event: Event) => emit('update:value', (event.target as HTMLSelectElement).value),
    }, (props.options as Array<{ label: string; value: string }> | undefined)?.map(option =>
      h('option', { value: option.value }, option.label),
    ))
  },
})

const buttonStub = defineComponent({
  name: 'NButton',
  props: {
    disabled: Boolean,
    type: String,
    attrType: String,
  },
  emits: ['click'],
  setup(props, { emit, slots }) {
    return () => h('button', {
      disabled: props.disabled,
      type: props.attrType === 'submit' ? 'submit' : 'button',
      onClick: () => emit('click'),
    }, slots.default?.())
  },
})

const confirmStub = defineComponent({
  name: 'ConfirmActionModal',
  props: {
    visible: Boolean,
  },
  emits: ['confirm', 'cancel'],
  setup(props, { emit }) {
    return () => props.visible
      ? h('button', { class: 'confirm-submit', onClick: () => emit('confirm') }, '确认修改')
      : null
  },
})

function mountDialog() {
  // i18n 插件注入确保 useI18n() 在组件内可用；locale 设为 zh 使文案断言沿用中文词条。
  i18n.global.locale.value = 'zh'
  return mount(RAGFlowDatasetInfoDialog, {
    attachTo: document.body,
    props: { visible: true, scope: 'org', targetId: 'org-1', targetName: '测试企业' },
    global: {
      plugins: [i18n],
      stubs: {
        'n-modal': passthroughStub('NModal'),
        'n-spin': passthroughStub('NSpin'),
        'n-alert': passthroughStub('NAlert'),
        'n-descriptions': passthroughStub('NDescriptions'),
        'n-descriptions-item': passthroughStub('NDescriptionsItem'),
        'n-form': defineComponent({
          name: 'NForm',
          setup(_, { slots }) {
            return () => h('form', slots.default?.())
          },
        }),
        'n-form-item': passthroughStub('NFormItem'),
        'n-space': passthroughStub('NSpace'),
        'n-select': selectStub,
        'n-button': buttonStub,
        NModal: passthroughStub('NModal'),
        NSpin: passthroughStub('NSpin'),
        NAlert: passthroughStub('NAlert'),
        NDescriptions: passthroughStub('NDescriptions'),
        NDescriptionsItem: passthroughStub('NDescriptionsItem'),
        NForm: defineComponent({
          name: 'NForm',
          setup(_, { slots }) {
            return () => h('form', slots.default?.())
          },
        }),
        NFormItem: passthroughStub('NFormItem'),
        NSpace: passthroughStub('NSpace'),
        NSelect: selectStub,
        NButton: buttonStub,
        ConfirmActionModal: confirmStub,
      },
    },
  })
}

describe('RAGFlowDatasetInfoDialog', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    mutateAsync.mockReset()
    refetchInfo.mockReset()
    refetchModels.mockReset()
    info.value = {
      scope: 'org',
      target_id: 'org-1',
      target_name: '测试企业',
      status: 'ok',
      ragflow_dataset_id: 'remote-ds-1',
      ragflow_dataset_name: 'ocm-org-test',
      embedding_model: { name: 'BAAI/bge-m3', label: 'BAAI/bge-m3', provider: 'OpenAI-API-Compatible', available: true },
      doc_num: 2,
      chunk_num: 8,
    }
  })

  it('展示 RAGFlow dataset 名称和当前 embedding 模型', async () => {
    // 弹框打开后应展示远端 dataset 信息，便于平台管理员核对 RAGFlow 侧名称。
    const wrapper = mountDialog()
    await nextTick()
    expect(document.body.textContent).toContain('ocm-org-test')
    expect(document.body.textContent).toContain('BAAI/bge-m3')
    wrapper.unmount()
  })

  it('not_created 状态禁用保存按钮', async () => {
    // 尚未创建远端 dataset 时不能修改模型，也不能触发懒创建。
    info.value = { scope: 'org', target_id: 'org-1', target_name: '测试企业', status: 'not_created' }
    const wrapper = mountDialog()
    await nextTick()
    expect(document.body.textContent).toContain('尚未创建 RAGFlow dataset')
    expect((wrapper.vm as unknown as { canSubmit: boolean }).canSubmit).toBe(false)
    wrapper.unmount()
  })

  it('提交时使用模型 name 和 provider 而不是内部 ID', async () => {
    // 前端只提交用户可识别的模型名，内部 RAGFlow ID 由后端解析。
    mutateAsync.mockResolvedValue(info.value)
    const wrapper = mountDialog()
    await nextTick()
    const vm = wrapper.vm as unknown as {
      selectedModelKey: string
      openConfirm: () => void
      submit: () => Promise<void>
    }
    vm.selectedModelKey = 'netease-youdao/bce-embedding-base_v1|OpenAI-API-Compatible'
    await nextTick()
    vm.openConfirm()
    await nextTick()
    await vm.submit()
    expect(mutateAsync).toHaveBeenCalledWith({
      name: 'netease-youdao/bce-embedding-base_v1',
      provider: 'OpenAI-API-Compatible',
    })
    expect(wrapper.emitted('updated')).toHaveLength(1)
    wrapper.unmount()
  })

})
