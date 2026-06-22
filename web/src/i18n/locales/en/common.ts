// common 模块：跨页面复用的通用文案（通用动作、状态词、表格通用列、通用提示）。
// 各业务模块的专有文案放各自模块文件；这里只放真正全局复用的词条，避免重复。
export default {
  // languageName 是该语言的自报名，供语言选择器展示（保证母语者能认出自己的语言）。
  languageName: 'English',
  // actions 是通用操作按钮文案。
  actions: {
    confirm: 'Confirm',
    cancel: 'Cancel',
    save: 'Save',
    delete: 'Delete',
    edit: 'Edit',
    create: 'Create',
    close: 'Close',
    back: 'Back',
    search: 'Search',
    reset: 'Reset',
    refresh: 'Refresh',
    submit: 'Submit',
    retry: 'Retry',
    copy: 'Copy',
    copied: 'Copied',
    view: 'View',
    download: 'Download',
    upload: 'Upload',
    more: 'More',
  },
  // status 是通用状态/结果词。
  status: {
    loading: 'Loading…',
    empty: 'No data',
    success: 'Success',
    failed: 'Failed',
    enabled: 'Enabled',
    disabled: 'Disabled',
    yes: 'Yes',
    no: 'No',
    all: 'All',
    unknown: 'Unknown',
  },
  // table 是数据表通用列名与分页文案。
  table: {
    actions: 'Actions',
    createdAt: 'Created at',
    updatedAt: 'Updated at',
    name: 'Name',
    remark: 'Remark',
    total: 'Total {n}',
  },
} as const
