// 语言目录聚合器：把各业务模块文案合并为单一 messages 对象。
// 新增模块时在此 import 并加入 default 导出；各模块文件内部独立维护，避免单文件膨胀与并发冲突。
import common from './common'
import locale from './locale'
import login from './login'
import layout from './layout'
import dashboard from './dashboard'
import apps from './apps'
import org from './org'
import audit from './audit'
import knowledge from './knowledge'
import usage from './usage'
import platform from './platform'
import skills from './skills'
import tickets from './tickets'
import components from './components'
import domain from './domain'
import aicc from './aicc'

export default {
  common,
  locale,
  login,
  layout,
  dashboard,
  apps,
  org,
  audit,
  knowledge,
  usage,
  platform,
  skills,
  tickets,
  components,
  domain,
  aicc,
}
