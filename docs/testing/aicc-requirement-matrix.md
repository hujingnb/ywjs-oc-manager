# AICC 生产就绪需求覆盖矩阵

- 基线提交：`fc7c0bac`（运行时限流恢复与浏览器回归稳定性修正；报告结论为 NO-GO）
- 最终镜像：`aicc-readiness-08fc4401735b`（本地 k3d）
- 结果定义：`PASS` / `FAIL` / `BLOCKED` / `N/A`

最终 Chromium 14/14、故障恢复和升级回退均已在同一最终镜像完成；其中覆盖 new-api 短暂 429 下运行时初始化恢复、行业知识库撤销授权清理和提示词注入拒绝边界。容量门禁仍失败，整体不可上线。

| ID | 需求 | 自动化证据 | 浏览器/环境证据 | 结果 |
|---|---|---|---|---|
| AICC-ENTRY-01 | 企业管理员从概览进入独立工作台 | `aicc-access-i18n.spec.ts` | Chromium 已从概览卡片进入 | BLOCKED |
| AICC-ENTRY-02 | 平台管理员从企业列表进入指定企业工作台 | `aicc-access-i18n.spec.ts` | Chromium 已验证 `org_id` 范围和只读界面 | BLOCKED |
| AICC-ENTRY-03 | 企业普通成员无入口且直接访问被拒绝 | `aicc-access-i18n.spec.ts` | Chromium 已验证无入口并被路由守卫拒绝 | BLOCKED |
| AICC-ORG-01 | 平台可开通或关闭企业 AICC | `aicc.spec.ts` 已覆盖开通 | Chromium 已验证开通；关闭待测 | BLOCKED |
| AICC-ORG-02 | 智能体数量上限生效 | 待执行 | 待执行 | BLOCKED |
| AICC-AGENT-01 | 企业管理员可新建、编辑、启停和删除智能体 | `aicc.spec.ts` 已覆盖新建、启动 | Chromium 已验证新建、启动；编辑、停用、删除待测 | BLOCKED |
| AICC-AGENT-02 | 顶部切换智能体后所有模块使用同一智能体 | 待执行 | 待执行 | BLOCKED |
| AICC-DELIVERY-01 | 独立链接和二维码指向正确公开页 | 待执行 | 待执行 | BLOCKED |
| AICC-DELIVERY-02 | 网页挂件可打开并记录来源页 | `aicc.spec.ts` 已覆盖挂件加载 | Chromium 已验证挂件打开；来源记录待测 | BLOCKED |
| AICC-DELIVERY-03 | 域名白名单阻止未授权站点 | 待执行 | 待执行 | BLOCKED |
| AICC-SESSION-01 | 打开公开页或挂件不创建空 session | `aicc.spec.ts` 监听首条消息前请求 | Chromium 未观察到提前创建请求 | BLOCKED |
| AICC-SESSION-02 | 首次发送消息只创建一个 session | `aicc.spec.ts` 校验首次消息建会话 | Chromium 已验证单次建会话并成功发消息 | BLOCKED |
| AICC-SESSION-03 | 刷新恢复原 session 和消息 | `aicc.spec.ts` 校验 localStorage token 和恢复接口 | Chromium 刷新后原消息可见 | BLOCKED |
| AICC-SESSION-04 | 新建对话建立新 session 边界 | `aicc.spec.ts` 校验新 token | Chromium 已验证点击不立即建会话、发送后建立新边界 | BLOCKED |
| AICC-SESSION-05 | 零消息会话不进入后台列表 | `aicc.spec.ts` 校验打开和新建动作均不建会话 | Chromium 后台计数仅包含非空会话 | BLOCKED |
| AICC-SESSION-06 | 会话列表筛选和分页正确 | `aicc.spec.ts` 覆盖筛选及 21 条会话翻页 | Chromium 已验证筛选参数和第二页单条数据 | BLOCKED |
| AICC-SESSION-07 | 来源、地域、消息数量和详情一致 | 待执行 | 待执行 | BLOCKED |
| AICC-STATUS-01 | 新会话默认显示跟进中 | `aicc.spec.ts` 与后台列表断言 | Chromium 已验证默认跟进中 | BLOCKED |
| AICC-STATUS-02 | 会话级已解决和未解决正确流转 | `aicc.spec.ts` 连续提交两种状态 | Chromium 已验证未解决到已解决流转 | BLOCKED |
| AICC-LEAD-01 | 自定义留资字段和必填规则生效 | 待执行 | 待执行 | BLOCKED |
| AICC-LEAD-02 | 已留资恢复会话不重复强制留资 | `aicc.spec.ts` 刷新恢复断言 | Chromium 刷新后原消息可见且留资表单不再出现 | BLOCKED |
| AICC-LEAD-03 | 线索列表、已读、关联会话和 CSV 正确 | `aicc.spec.ts` 覆盖完整闭环 | Chromium 已验证列表、自动已读、关联对话和 CSV | BLOCKED |
| AICC-KB-01 | 当前客服知识库始终参与检索 | `aicc-knowledge.spec.ts` 上传、解析、公开问答 | Chromium 已验证唯一口令命中；关闭企业库后仍可命中 | BLOCKED |
| AICC-KB-02 | 企业知识库可启用和停用 | `aicc-knowledge.spec.ts` 启用、停用和公开问答 | Chromium 已验证启用命中、停用后不再返回企业口令 | BLOCKED |
| AICC-KB-03 | 只能选择平台授权的行业知识库 | `organization_service_test.go`、`aicc_service_test.go`、`OrganizationsPage.spec.ts` | Chrome DevTools 真实浏览器验证平台创建行业库、授权企业、企业管理员选择并保存；撤销授权后旧关联被清理且候选为空 | BLOCKED |
| AICC-KB-04 | 当前客服知识库可上传、解析、下载和删除 | `aicc-knowledge.spec.ts` | Chromium 已验证上传、RAGFlow 完成解析、下载和删除 | BLOCKED |
| AICC-KB-05 | RAGFlow 故障和无匹配知识行为稳定 | 待执行 | 待执行 | BLOCKED |
| AICC-CHAT-01 | 知识问答实际调用 oc-kb 并返回命中内容 | `aicc-knowledge.spec.ts` | Chromium 经真实 Hermes/new-api/RAGFlow 返回当前客服和企业唯一口令 | BLOCKED |
| AICC-CHAT-02 | 访客图片上传、恢复和限制正确 | 待执行 | 待执行 | BLOCKED |
| AICC-CHAT-03 | 提示词注入不能越过知识范围或执行操作 | 待执行 | 待执行 | BLOCKED |
| AICC-SAFETY-01 | 消息数量和频率限制生效 | 待执行 | 待执行 | BLOCKED |
| AICC-SAFETY-02 | 访客封禁、敏感词和余额不足路径稳定 | 待执行 | 待执行 | BLOCKED |
| AICC-AUTH-01 | 未登录、伪造 token 和跨企业访问被拒绝 | `aicc_service_test.go` 覆盖跨企业视图拒绝 | Chrome DevTools 真实浏览器验证未登录 401、伪造令牌 401、企业管理员跨企业读取 403 | BLOCKED |
| AICC-AUTH-02 | 平台管理员只读边界生效 | `aicc-access-i18n.spec.ts` 已验证无新建入口；`aicc_service_test.go` 覆盖管理权限 | Chrome DevTools 真实浏览器验证平台管理员创建智能体返回 403 | BLOCKED |
| AICC-I18N-01 | 工作台和公开页中英文用户文案完整 | `aicc-access-i18n.spec.ts`、`aicc.spec.ts` 已覆盖核心界面 | Chromium 已验证工作台六个子页和公开页核心文案；全量可见文案清扫待测 | BLOCKED |
| AICC-MOBILE-01 | 移动视口无溢出、遮挡或不可操作控件 | `aicc.spec.ts` 390x844 视口断言 | Chromium 已验证公开页头部、输入区及横向溢出；工作台移动视口待测 | BLOCKED |
| AICC-ANALYTICS-01 | 趋势、地域、来源、问题和线索统计正确 | `aicc.spec.ts` 已覆盖时间、智能体筛选和未读线索变化 | Chromium 已验证筛选及已读后计数归零；其余统计口径待测 | BLOCKED |
| AICC-ANALYTICS-02 | 未解决率排除跟进中会话 | 待执行 | 待执行 | BLOCKED |
| AICC-GEOIP-01 | 镜像内置 IPv4/IPv6 XDB 可解析地域 | 待执行 | 待执行 | BLOCKED |
| AICC-GEOIP-02 | 国内更新源可定期安装有效 XDB | 待执行 | 待执行 | BLOCKED |
| AICC-RETENTION-01 | 过期会话、线索关联和图片按策略清理 | 待执行 | 待执行 | BLOCKED |
| AICC-FAULT-01 | Hermes 故障恢复后可续聊且不重复消息 | Hermes 全量测试通过 | 重启后先受控 503，api_server 就绪后续聊成功且仅写入一组问答 | PASS |
| AICC-FAULT-02 | RAGFlow/new-api/Redis/MySQL/API 故障可恢复 | 故障脚本门禁通过 | 所有依赖故障安全降级并在恢复后续聊成功 | PASS |
| AICC-LOAD-01 | 100 并发 30 分钟达到成功率和延迟门禁 | 负载工具单测通过 | 25,584 请求 100% 成功、零串写，但 P95 24.502 秒超过 15 秒阈值 | FAIL |
| AICC-UPGRADE-01 | master 数据可升级到最终 AICC 版本 | 升级脚本测试通过 | 基线 27 升级至最终 migration 32，历史数据计数保持 | PASS |
| AICC-ROLLBACK-01 | 应用回滚边界和数据库恢复经过验证 | 升级脚本测试通过 | 旧应用受控失败，重建数据库后恢复基线 27，再恢复最终 32 | PASS |
