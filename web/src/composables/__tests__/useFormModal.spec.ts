import { describe, expect, it, vi } from 'vitest'
import { useFormModal } from '../useFormModal'

// fakeMutation 模拟 TanStack useMutation 的最小契约：仅含 mutateAsync。
function fakeMutation<TResult>(behavior: 'success' | 'fail', result?: TResult, error?: Error) {
  return {
    mutateAsync: vi.fn().mockImplementation(async (payload: unknown) => {
      void payload
      if (behavior === 'fail') throw error ?? new Error('mock fail')
      return result
    }),
  } as any
}

type Payload = { name: string; age?: number }
const initial: Payload = { name: '', age: undefined }

describe('useFormModal', () => {
  it('initial state: form 与 initial 等价，modal 隐藏', () => {
    const m = useFormModal({ initial, mutation: fakeMutation('success') })
    expect(m.form.name).toBe('')
    expect(m.form.age).toBeUndefined()
    expect(m.formVisible.value).toBe(false)
    expect(m.creating.value).toBe(false)
    expect(m.submitError.value).toBeNull()
  })

  it('openForm 显示 modal 并清空错误', () => {
    const m = useFormModal({ initial, mutation: fakeMutation('success') })
    m.submitError.value = '旧错误'
    m.openForm()
    expect(m.formVisible.value).toBe(true)
    expect(m.submitError.value).toBeNull()
  })

  it('openForm 重置 form 字段（即使被改过）', () => {
    const m = useFormModal({ initial, mutation: fakeMutation('success') })
    m.form.name = '修改过'
    m.form.age = 30
    m.openForm()
    expect(m.form.name).toBe('')
    expect(m.form.age).toBeUndefined()
  })

  it('submit 成功路径：mutateAsync 调用，formVisible=false，onSuccess 回调', async () => {
    const onSuccess = vi.fn()
    const mutation = fakeMutation<{ id: string }>('success', { id: 'created-1' })
    const m = useFormModal({ initial, mutation, onSuccess })
    m.openForm()
    m.form.name = 'Alice'
    await m.submit()
    expect(mutation.mutateAsync).toHaveBeenCalledOnce()
    expect(m.formVisible.value).toBe(false)
    expect(m.creating.value).toBe(false)
    expect(m.submitError.value).toBeNull()
    expect(onSuccess).toHaveBeenCalledWith({ id: 'created-1' })
  })

  it('submit 失败路径：submitError 写入，formVisible 不变，onSuccess 不调用', async () => {
    const onSuccess = vi.fn()
    const mutation = fakeMutation('fail', undefined, new Error('网络错误'))
    const m = useFormModal({ initial, mutation, onSuccess })
    m.openForm()
    await m.submit()
    expect(m.formVisible.value).toBe(true)
    expect(m.submitError.value).toBe('网络错误')
    expect(m.creating.value).toBe(false)
    expect(onSuccess).not.toHaveBeenCalled()
  })

  it('errorMessage 选项覆盖默认错误转换', async () => {
    const mutation = fakeMutation('fail', undefined, new Error('原始'))
    const m = useFormModal({
      initial,
      mutation,
      errorMessage: () => '自定义错误',
    })
    m.openForm()
    await m.submit()
    expect(m.submitError.value).toBe('自定义错误')
  })

  it('toPayload 转换 form 后传给 mutation', async () => {
    const mutation = fakeMutation('success')
    const m = useFormModal<{ a: string; b: string }>({
      initial: { a: '', b: '' },
      mutation,
      toPayload: (f) => ({ a: f.a.toUpperCase(), b: f.b }),
    })
    m.openForm()
    m.form.a = 'hello'
    m.form.b = 'world'
    await m.submit()
    expect(mutation.mutateAsync).toHaveBeenCalledWith({ a: 'HELLO', b: 'world' })
  })

  it('closeForm 设置 formVisible=false', () => {
    const m = useFormModal({ initial, mutation: fakeMutation('success') })
    m.openForm()
    m.closeForm()
    expect(m.formVisible.value).toBe(false)
  })
})
