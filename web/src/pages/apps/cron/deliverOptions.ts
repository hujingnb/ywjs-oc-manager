// deliverOptions.ts —— deliver 投递渠道下拉选项构建。
// 规则：「不投递」常驻置顶；只列出已绑定渠道；编辑态保留当前值（即使未绑定）避免回填丢值。
// buildDeliverOptions 接受 t 参数以支持 i18n，调用方在 computed 中传入保证响应性。
import { translateDeliver } from './cronDisplay'

// TFunc 是 vue-i18n t 函数的精简签名，与 cronDisplay 保持一致。
type TFunc = (key: string, params?: Record<string, string | number>) => string

// DeliverOption 是 n-select 选项结构。
export interface DeliverOption {
  label: string
  value: string
}

// buildDeliverOptions 组装下拉项。boundTypes 是已绑定渠道类型集合；currentValue 是编辑态原值。
export function buildDeliverOptions(boundTypes: string[], currentValue: string, t: TFunc): DeliverOption[] {
  // 「不投递」常驻置顶，使用独立 key 避免与渠道 deliver.none 混用。
  const options: DeliverOption[] = [{ label: t('apps.cron.display.deliverNone'), value: '' }]
  for (const type of boundTypes) {
    options.push({ label: translateDeliver(type, t), value: type })
  }
  // 编辑态当前值非空且不在已绑定列表里时，单独保留，避免下拉无对应项导致显示空白。
  if (currentValue && !boundTypes.includes(currentValue)) {
    options.push({ label: translateDeliver(currentValue, t), value: currentValue })
  }
  return options
}
