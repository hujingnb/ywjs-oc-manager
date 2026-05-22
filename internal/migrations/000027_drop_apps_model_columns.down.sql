-- 000027_drop_apps_model_columns.down.sql
-- 回滚：恢复 apps.model_id 与 apps.model_synced 两列。
--
-- model_id：原由 000015 引入（text NOT NULL + 非空 check 约束），
-- 但 000024 已删除该 check 约束，故回滚仅恢复 text NOT NULL 列，
-- 不再重建 apps_model_id_not_blank_check。
-- model_synced：原由 000022 引入（boolean NOT NULL DEFAULT true）。
ALTER TABLE apps ADD COLUMN model_id text NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN model_synced boolean NOT NULL DEFAULT true;
