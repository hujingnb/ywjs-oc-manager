import { describe, expect, it } from 'vitest'

import { buildIndustryKnowledgeFileListQuery } from './useIndustryKnowledge'

describe('行业知识库文件列表查询参数', () => {
  // 覆盖默认分页：行业库文件列表未传筛选时仍请求第一页和 50 条，避免一次拉取全量文件。
  it('默认请求第一页和 50 条行业库文件', () => {
    expect(buildIndustryKnowledgeFileListQuery({})).toEqual({
      page: 1,
      page_size: 50,
    })
  })

  // 覆盖组合筛选：文件名、解析状态和创建日期会裁剪空白后进入后端查询参数。
  it('构造文件名、状态和创建日期筛选参数', () => {
    expect(buildIndustryKnowledgeFileListQuery({
      page: 2,
      pageSize: 20,
      keyword: ' policy ',
      status: ' completed ',
      createdFrom: ' 2026-06-01 ',
      createdTo: ' 2026-06-05 ',
    })).toEqual({
      page: 2,
      page_size: 20,
      keyword: 'policy',
      status: 'completed',
      created_from: '2026-06-01',
      created_to: '2026-06-05',
    })
  })

  // 覆盖空筛选和异常分页：空白筛选不进入 URL，非法分页回退到安全默认值。
  it('归一化空筛选和无效分页参数', () => {
    expect(buildIndustryKnowledgeFileListQuery({
      page: 0,
      pageSize: -1,
      keyword: '   ',
      createdFrom: '',
      createdTo: '   ',
    })).toEqual({
      page: 1,
      page_size: 50,
    })
  })
})
