-- 000023_assistant_versions.down.sql
DROP INDEX IF EXISTS apps_version_id_idx;
ALTER TABLE apps
    DROP COLUMN IF EXISTS applied_image_ref,
    DROP COLUMN IF EXISTS applied_version_revision,
    DROP COLUMN IF EXISTS version_id;
ALTER TABLE organizations DROP COLUMN IF EXISTS assistant_version_ids;
DROP TABLE IF EXISTS assistant_versions;
