-- 000024_apps_model_id_optional.down.sql
-- 回滚：恢复 apps.model_id 非空校验。
-- 注意：若回滚时已存在 model_id 为空串的实例行，本约束会校验失败；
-- 这是 Phase 3 之后版本接管模型的预期不兼容点，回滚前需自行清理空 model_id 行。
ALTER TABLE apps
ADD CONSTRAINT apps_model_id_not_blank_check
CHECK (btrim(model_id) <> '');
