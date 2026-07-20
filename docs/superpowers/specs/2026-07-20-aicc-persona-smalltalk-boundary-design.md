# AICC 人设闲聊边界设计

## 背景

企业管理员可以为智能客服设置人设，运行时会将人设写入 SOUL.md。现有平台提示词要求所有企业相关问题在回复前检索知识库，模型将“介绍自己”等非事实性表达也归入该规则，导致人设问候无法稳定生效。

## 决策

在 AICC 平台提示词中增加一个最小、显式的例外：非企业事实的寒暄、身份介绍、礼貌回应和人设表达可以直接答复，并应遵守当前智能体人设。

企业事实、产品、价格、政策、售后、行业资料及任何需要确认的信息仍必须先调用 `aicc_knowledge_search`；没有充分依据时仍按现有规则说明无法确认并建议人工客服。

## 实现边界

- 只调整 `DefaultAICCSystemPromptTemplate` 的文本，不新增接口、数据表或模型配置。
- 平台提示词哈希自然变化，现有“需重启”与 AICC rollout 机制负责使运行中实例静默应用新规则。
- 不放宽内部实现保密、工具白名单、最终 JSON 回复契约或写入操作禁令。

## 存量客服生效

平台提示词哈希变化后，存量运行中的 AICC 也必须静默应用新规则。新增独立的
`aicc_platform_prompt_rollout` job：它按稳定顺序逐台重启 active AICC 智能体，并在每台
智能体的 `applied_platform_prompt_hash` 与当前哈希一致、运行时 ready 后继续下一台。

该任务不复用 `aicc_model_rollout`，因为企业模型 revision 与平台提示词版本没有同一生命周期；
两个任务的 marker 和互斥判断也必须隔离，避免模型切换与提示词更新互相覆盖。暂停中的客服
不被唤醒，下一次启动时由 bootstrap 自动使用最新提示词。

两类 rollout 共享一张以 `app_id` 为主键的持久 ownership guard 表。任务在写自身 payload marker
前必须原子领取 guard；同一 job 已持有时允许恢复，另一 job 持有时延迟重试而非重启。只有 owner
完成自身 hash/revision 核验、runtime ready 和 marker 清理后才释放 guard。运行时 ready 写入还必须
确认不存在任何活跃 rollout owner，防止另一类任务的旧 Pod 被提前解闸。

若启动时发现已有活跃提示词 rollout 但其 payload hash 落后当前 hash，协调器不创建并行任务；
旧任务完成时由 handler 重新检查 stale 客服并在同一全局 guard 下续建最新 hash 的后继任务。

平台启动时检查是否存在 platform prompt hash 落后的 active AICC 智能体；只有不存在同类
活跃任务时才创建一个全局 rollout job。任务失败保留给既有 job 重试机制，避免 manager 重启时
重复创建并发重启。

## 验证

- 提示词单元测试断言闲聊/人设例外与企业事实检索规则同时存在。
- 浏览器定向场景验证带有人设的客服可完成公开寒暄；模型切换、公开接待及普通助手回归保持覆盖。
- worker 单元测试验证提示词 rollout 严格逐台、跳过暂停客服、与模型 rollout 互不抢占；本地浏览器
  验证存量客服在平台提示词变更后静默重启并应用新规则。
