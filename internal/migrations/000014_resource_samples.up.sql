-- resource sample tables store raw 30-second runtime metrics for node and instance trend views.
CREATE TABLE node_resource_samples (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    runtime_node_id uuid NOT NULL REFERENCES runtime_nodes(id) ON DELETE CASCADE,
    sampled_at timestamptz NOT NULL,
    cpu_percent double precision NULL,
    memory_used_bytes bigint NULL,
    memory_total_bytes bigint NULL,
    disk_used_bytes bigint NULL,
    disk_total_bytes bigint NULL,
    network_rx_bytes bigint NULL,
    network_tx_bytes bigint NULL,
    instance_count integer NULL,
    last_error text NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE instance_resource_samples (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    runtime_node_id uuid NOT NULL REFERENCES runtime_nodes(id) ON DELETE CASCADE,
    container_id text NOT NULL,
    sampled_at timestamptz NOT NULL,
    container_status text NULL,
    cpu_percent double precision NULL,
    memory_used_bytes bigint NULL,
    memory_limit_bytes bigint NULL,
    disk_read_bytes bigint NULL,
    disk_write_bytes bigint NULL,
    network_rx_bytes bigint NULL,
    network_tx_bytes bigint NULL,
    last_error text NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX node_resource_samples_node_time_idx
    ON node_resource_samples (runtime_node_id, sampled_at DESC);

CREATE INDEX node_resource_samples_sampled_at_idx
    ON node_resource_samples (sampled_at);

CREATE INDEX instance_resource_samples_app_time_idx
    ON instance_resource_samples (app_id, sampled_at DESC);

CREATE INDEX instance_resource_samples_node_time_idx
    ON instance_resource_samples (runtime_node_id, sampled_at DESC);

CREATE INDEX instance_resource_samples_node_app_time_idx
    ON instance_resource_samples (runtime_node_id, app_id, sampled_at DESC);

CREATE INDEX instance_resource_samples_sampled_at_idx
    ON instance_resource_samples (sampled_at);

COMMENT ON TABLE node_resource_samples IS '运行节点资源原始采样，保留 30 天供趋势图查询。';
COMMENT ON TABLE instance_resource_samples IS '实例容器资源原始采样，保留 30 天供节点抽屉和实例运行页查询。';
