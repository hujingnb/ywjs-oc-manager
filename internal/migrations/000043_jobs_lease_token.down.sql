ALTER TABLE jobs
    DROP KEY idx_jobs_running_locked_at,
    DROP COLUMN lease_token;
