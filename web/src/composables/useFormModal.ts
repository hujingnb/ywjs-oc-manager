import { reactive, ref, type Ref } from 'vue'
import type { UseMutationReturnType } from '@tanstack/vue-query'

export interface UseFormModalOptions<TPayload, TResult> {
  // 表单初始值；openForm 每次都会 deep clone 此对象重置 form。
  // 必须是 JSON-serializable 对象（不含 Date / Map / Set / 函数）。
  initial: TPayload
  // TanStack Query mutation；submit 调用其 mutateAsync。
  mutation: UseMutationReturnType<TResult, Error, TPayload, unknown>
  // 提交成功后业务后置（如展示 token、跳转）；不在此关 modal，关闭已自动处理。
  onSuccess?: (result: TResult) => void
  // 自定义错误消息生成；默认用 err.message 或 fallback。
  errorMessage?: (err: unknown) => string
  // 提交前对 form 做适配（如 || undefined 过滤）；返回值替代 form 作为 payload。
  toPayload?: (form: TPayload) => TPayload
}

export interface UseFormModalReturn<TPayload> {
  formVisible: Ref<boolean>
  form: TPayload
  creating: Ref<boolean>
  submitError: Ref<string | null>
  openForm: () => void
  closeForm: () => void
  submit: () => Promise<void>
}

// useFormModal 把页面里 formVisible / creating / submitError + openForm + onSubmit
// 三件套统一到一个组合式函数。submit 只做：清错 → mutateAsync → 关闭 modal → onSuccess。
export function useFormModal<TPayload extends object, TResult = unknown>(
  opts: UseFormModalOptions<TPayload, TResult>,
): UseFormModalReturn<TPayload> {
  const formVisible = ref(false)
  const creating = ref(false)
  const submitError = ref<string | null>(null)
  const form = reactive(structuredClone(opts.initial)) as TPayload

  function openForm() {
    Object.assign(form as object, structuredClone(opts.initial))
    submitError.value = null
    formVisible.value = true
  }

  function closeForm() {
    formVisible.value = false
  }

  async function submit() {
    submitError.value = null
    creating.value = true
    try {
      const payload = opts.toPayload ? opts.toPayload(form) : form
      const result = await opts.mutation.mutateAsync(payload as TPayload)
      formVisible.value = false
      opts.onSuccess?.(result)
    } catch (err) {
      const fallback = err instanceof Error ? err.message : '操作失败'
      submitError.value = opts.errorMessage?.(err) ?? fallback
    } finally {
      creating.value = false
    }
  }

  return { formVisible, form, creating, submitError, openForm, closeForm, submit }
}
