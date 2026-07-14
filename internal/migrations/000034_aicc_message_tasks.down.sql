-- 子表先于 aicc_messages 与 aicc_sessions 回滚，避免外键依赖阻塞历史 AICC 迁移回退。
DROP TABLE IF EXISTS aicc_message_tasks;
