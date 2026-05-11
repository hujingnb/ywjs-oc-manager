// useFormModal.ts 封装常见表单弹窗的打开、提交、错误和 loading 状态。
// 调用方负责传入可 JSON clone 的初始值，以及与表单类型匹配的 mutation。
import { reactive, ref, type Ref } from 'vue'
import type { UseMutationReturnType } from '@tanstack/vue-query'

// UseFormModalOptions 定义表单弹窗组合式函数的输入契约。
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

// UseFormModalReturn 是页面组件直接绑定的弹窗状态和动作。
export interface UseFormModalReturn<TPayload> {
  // 控制弹窗显隐。
  formVisible: Ref<boolean>
  // 响应式表单对象，openForm 会重置为 initial 的深拷贝。
  form: TPayload
  // 提交中的 loading 状态。
  creating: Ref<boolean>
  // 最近一次提交错误，成功或重新打开时清空。
  submitError: Ref<string | null>
  // 打开弹窗并重置表单。
  openForm: () => void
  // 仅关闭弹窗，不清空表单。
  closeForm: () => void
  // 提交表单并处理 mutation 成功/失败状态。
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
