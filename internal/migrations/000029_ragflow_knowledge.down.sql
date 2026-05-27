DROP INDEX IF EXISTS apps_runtime_token_hash_active_unique;
ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_runtime_token_pair_check;
ALTER TABLE apps DROP COLUMN IF EXISTS runtime_token_ciphertext;
ALTER TABLE apps DROP COLUMN IF EXISTS runtime_token_hash;
DROP TABLE IF EXISTS ragflow_documents;
DROP TABLE IF EXISTS ragflow_datasets;
