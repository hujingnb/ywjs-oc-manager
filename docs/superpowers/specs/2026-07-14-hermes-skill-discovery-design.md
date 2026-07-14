# Hermes 与 AICC 提示词隔离设计

## 目标

普通 Hermes 实例和 AICC 智能客服分别使用独立的平台层提示词，并在处理任务前主动调用 `skills_list` 查找适用技能。

## 方案

manager 定义 `DefaultInstanceSystemPromptTemplate` 与 `DefaultAICCSystemPromptTemplate`。普通实例定位为可交付文件的通用工作助手，保留 workspace 约束；AICC 定位为面向外部访客的智能客服，要求专业、如实、不承诺无法保证的结果且不暴露内部实现。两者都保留完整的技能发现规则。

Bootstrap 依据 `app.AiccHidden` 选择 prompt，写入 `platform-rules.md`。`PlatformPromptHash` 也依据同一标识计算，概览和 bootstrap stamp 因而始终对应实例实际使用的文本。

## 验证

配置测试覆盖两份常量的定位、技能规则、无花括号约束、选择函数与类型 hash。Bootstrap 测试覆盖 AICC 选取与 stamp；应用概览测试覆盖两类 pending-restart 判断。
