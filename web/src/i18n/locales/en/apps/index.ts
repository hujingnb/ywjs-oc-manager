// apps 命名空间聚合：root 为应用详情/列表等顶层文案，cron/kanban/conversations 为子命名空间。
// 各子文件独立维护，迁移时互不冲突。
import root from './root'
import cron from './cron'
import kanban from './kanban'
import conversations from './conversations'

export default { ...root, cron, kanban, conversations }
