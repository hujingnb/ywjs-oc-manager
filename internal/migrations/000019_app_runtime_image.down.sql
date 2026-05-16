ALTER TABLE apps DROP CONSTRAINT apps_status_check;

ALTER TABLE apps ADD CONSTRAINT apps_status_check CHECK (
    status IN (
        'draft',
        'pulling_image', 'syncing_image', 'preparing_runtime',
        'creating_container', 'starting',
        'binding_waiting', 'binding_failed',
        'running', 'stopped', 'error', 'deleted'
    )
);

ALTER TABLE apps
    DROP COLUMN runtime_image_ref,
    DROP COLUMN runtime_image_sha256;
