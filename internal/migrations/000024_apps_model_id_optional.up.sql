-- 000024_apps_model_id_optional.up.sql
-- 助手版本特性下实例模型由绑定版本（assistant_versions.main_model）提供，
-- apps.model_id 不再是实例自身的必填属性。组织级模型配置已在 Phase 3 移除，
-- 新建组织 org.model_id 为空，onboarding 写入 apps.model_id 时即为空串，
-- 触发 000015 引入的 apps_model_id_not_blank_check 约束导致建实例失败。
-- 这里移除该非空校验；apps.model_id 列本身保留，留待后续阶段整体清理。
ALTER TABLE apps
DROP CONSTRAINT IF EXISTS apps_model_id_not_blank_check;
