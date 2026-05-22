-- 000027_drop_apps_model_columns.up.sql
-- apps.model_id 与 apps.model_synced 已成死列：
-- 实例模型由绑定的助手版本 main_model 提供，model_synced 已被 Phase 4
-- 引入的 version_synced 机制取代。两列连同其 check 约束均已在 000024 中
-- 解除强约束，此处直接物理删除。
ALTER TABLE apps DROP COLUMN model_id;
ALTER TABLE apps DROP COLUMN model_synced;
