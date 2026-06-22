// deliverOptions.ts —— deliver 投递渠道下拉选项构建。
// 规则：「不投递」常驻置顶；只列出已绑定渠道；编辑态保留当前值（即使未绑定）避免回填丢值。
import { translateDeliver } from './cronDisplay'

// DeliverOption 是 n-select 选项结构。
export interface DeliverOption {
  label: string
  value: string
}

// buildDeliverOptions 组装下拉项。boundTypes 是已绑定渠道类型集合；currentValue 是编辑态原值。
export function buildDeliverOptions(boundTypes: string[], currentValue: string): DeliverOption[] {
  const options: DeliverOption[] = [{ label: '不投递', value: '' }]
  for (const type of boundTypes) {
    options.push({ label: translateDeliver(type), value: type })
  }
  // 编辑态当前值非空且不在已绑定列表里时，单独保留，避免下拉无对应项导致显示空白。
  if (currentValue && !boundTypes.includes(currentValue)) {
    options.push({ label: translateDeliver(currentValue), value: currentValue })
  }
  return options
}
