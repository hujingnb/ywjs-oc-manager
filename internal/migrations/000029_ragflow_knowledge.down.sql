ALTER TABLE apps DROP COLUMN IF EXISTS runtime_token_ciphertext;
ALTER TABLE apps DROP COLUMN IF EXISTS runtime_token_hash;
DROP TABLE IF EXISTS ragflow_documents;
DROP TABLE IF EXISTS ragflow_datasets;
