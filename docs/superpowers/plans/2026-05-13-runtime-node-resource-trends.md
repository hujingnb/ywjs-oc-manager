# Runtime Node Resource Trends Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add 30-day resource trend storage and UI for runtime nodes and their associated instances.

**Architecture:** Store resource samples as append-only time-series rows. Runtime-agent reports node-level samples through heartbeat, while manager keeps pulling instance-level samples through Docker proxy in `runtime_refresh_status`. Frontend reads current node summaries and trend APIs, opens a wide drawer from `/runtime-nodes`, and reuses the same app resource API in the instance runtime tab.

**Tech Stack:** Go 1.22+, Gin, pgx/sqlc, PostgreSQL migrations, runtime-agent Go binary, Vue 3, TanStack Query, Naive UI, SVG charts, OpenAPI generation.

---

## File Structure

- Create `internal/migrations/000014_resource_samples.up.sql` and `.down.sql`: add node and instance sample tables with indexes.
- Create `internal/store/queries/resource_samples.sql`: sqlc queries for inserting, latest lookup, range listing, aggregation, and cleanup.
- Modify generated sqlc files by running `make sqlc-generate`.
- Modify `internal/api/handlers/dto.go`: add heartbeat node resource request DTO.
- Modify `internal/api/handlers/agent.go`: parse `sampled_at` and `node_resource` from heartbeat/enroll payloads.
- Modify `internal/service/runtime_node_service.go`: write node samples during heartbeat and include `current_resource` in node list/detail results.
- Create `internal/service/resource_metrics_service.go` and `internal/service/resource_metrics_service_test.go`: resource range queries, app permissions, node instance list, bucket validation.
- Create `internal/api/handlers/resource_metrics.go` and tests: new trend endpoints.
- Modify `internal/api/router.go` and `cmd/server/main.go`: wire `ResourceMetricsService`.
- Modify `internal/worker/handlers/runtime_refresh_status.go` and tests: write instance samples instead of resource display snapshots.
- Modify `internal/integrations/runtime/adapter.go`, `internal/integrations/runtime/agent_backed.go`, and tests: add disk read/write stats to `ContainerStats`.
- Create `runtime/agent/node_resource.go` and tests: node CPU, memory, disk, network, instance count collector.
- Modify `runtime/agent/heartbeat.go`, `enroll.go`, and tests: include node resource samples in heartbeat/enroll.
- Modify `cmd/server/wiring.go`: keep `runtime_refresh_status` dispatch, update comments and add cleanup dispatcher.
- Create `internal/service/resource_sample_cleanup.go` and tests: delete samples older than 30 days in batches.
- Modify `openapi/openapi.yaml` and `web/src/api/generated.ts` via generators after handler annotations change.
- Modify `web/src/api/index.ts`: add resource sample aliases if generated schemas need required fields.
- Modify `web/src/api/hooks/useRuntimeNodes.ts`: add current resource types, node trend, node instances, node instance trend hooks.
- Modify `web/src/api/hooks/useApps.ts`: add app resource trend hook and remove runtime snapshot display dependency from page code.
- Create `web/src/components/ResourceTrendChart.vue` and `web/src/components/__tests__/ResourceTrendChart.spec.ts`: reusable SVG trend chart and tests.
- Modify `web/src/pages/runtime-nodes/RuntimeNodesPage.vue` and spec: add current resource column and wide drawer.
- Modify `web/src/pages/apps/AppRuntimeTab.vue` and spec: replace snapshot cards with trend charts while keeping operations.

---

### Task 1: Resource Sample Schema And Queries

**Files:**
- Create: `internal/migrations/000014_resource_samples.up.sql`
- Create: `internal/migrations/000014_resource_samples.down.sql`
- Create: `internal/store/queries/resource_samples.sql`
- Generated: `internal/store/sqlc/*.go`

- [ ] **Step 1: Add migration tests by running current migrations before editing**

Run:

```bash
rtk make migrate-up
```

Expected: current migrations apply successfully. If local Docker services are not running, start them with `rtk make dev-up` first.

- [ ] **Step 2: Create resource sample migration**

Write `internal/migrations/000014_resource_samples.up.sql`:

```sql
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

CREATE INDEX instance_resource_samples_app_time_idx
    ON instance_resource_samples (app_id, sampled_at DESC);

CREATE INDEX instance_resource_samples_node_time_idx
    ON instance_resource_samples (runtime_node_id, sampled_at DESC);

CREATE INDEX instance_resource_samples_node_app_time_idx
    ON instance_resource_samples (runtime_node_id, app_id, sampled_at DESC);

COMMENT ON TABLE node_resource_samples IS '运行节点资源原始采样，保留 30 天供趋势图查询。';
COMMENT ON TABLE instance_resource_samples IS '实例容器资源原始采样，保留 30 天供节点抽屉和实例运行页查询。';
```

Write `internal/migrations/000014_resource_samples.down.sql`:

```sql
DROP TABLE IF EXISTS instance_resource_samples;
DROP TABLE IF EXISTS node_resource_samples;
```

- [ ] **Step 3: Add sqlc queries**

Write `internal/store/queries/resource_samples.sql`:

```sql
-- name: InsertNodeResourceSample :one
INSERT INTO node_resource_samples (
    runtime_node_id, sampled_at, cpu_percent,
    memory_used_bytes, memory_total_bytes,
    disk_used_bytes, disk_total_bytes,
    network_rx_bytes, network_tx_bytes,
    instance_count, last_error
) VALUES (
    $1, $2, $3,
    $4, $5,
    $6, $7,
    $8, $9,
    $10, $11
)
RETURNING *;

-- name: InsertInstanceResourceSample :one
INSERT INTO instance_resource_samples (
    app_id, runtime_node_id, container_id, sampled_at, container_status,
    cpu_percent, memory_used_bytes, memory_limit_bytes,
    disk_read_bytes, disk_write_bytes,
    network_rx_bytes, network_tx_bytes, last_error
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8,
    $9, $10,
    $11, $12, $13
)
RETURNING *;

-- name: GetLatestNodeResourceSample :one
SELECT *
FROM node_resource_samples
WHERE runtime_node_id = $1
ORDER BY sampled_at DESC, id DESC
LIMIT 1;

-- name: ListLatestNodeResourceSamples :many
SELECT DISTINCT ON (runtime_node_id) *
FROM node_resource_samples
WHERE runtime_node_id = ANY($1::uuid[])
ORDER BY runtime_node_id, sampled_at DESC, id DESC;

-- name: ListNodeResourceSamples :many
SELECT *
FROM node_resource_samples
WHERE runtime_node_id = $1
  AND sampled_at >= $2
  AND sampled_at <= $3
ORDER BY sampled_at ASC, id ASC;

-- name: ListNodeResourceBuckets :many
SELECT
    to_timestamp(floor(extract(epoch FROM sampled_at) / $4)::bigint * $4)::timestamptz AS sampled_at,
    avg(cpu_percent)::double precision AS cpu_percent,
    avg(memory_used_bytes)::bigint AS memory_used_bytes,
    max(memory_total_bytes)::bigint AS memory_total_bytes,
    avg(disk_used_bytes)::bigint AS disk_used_bytes,
    max(disk_total_bytes)::bigint AS disk_total_bytes,
    min(network_rx_bytes)::bigint AS network_rx_bytes,
    min(network_tx_bytes)::bigint AS network_tx_bytes,
    avg(instance_count)::integer AS instance_count,
    (array_remove(array_agg(last_error ORDER BY sampled_at DESC), NULL))[1] AS last_error
FROM node_resource_samples
WHERE runtime_node_id = $1
  AND sampled_at >= $2
  AND sampled_at <= $3
GROUP BY 1
ORDER BY 1 ASC;

-- name: GetLatestInstanceResourceSample :one
SELECT *
FROM instance_resource_samples
WHERE app_id = $1
ORDER BY sampled_at DESC, id DESC
LIMIT 1;

-- name: ListLatestInstanceResourceSamplesByNode :many
SELECT DISTINCT ON (app_id) *
FROM instance_resource_samples
WHERE runtime_node_id = $1
ORDER BY app_id, sampled_at DESC, id DESC;

-- name: ListInstanceResourceSamples :many
SELECT *
FROM instance_resource_samples
WHERE app_id = $1
  AND sampled_at >= $2
  AND sampled_at <= $3
ORDER BY sampled_at ASC, id ASC;

-- name: ListNodeInstanceResourceSamples :many
SELECT *
FROM instance_resource_samples
WHERE runtime_node_id = $1
  AND app_id = $2
  AND sampled_at >= $3
  AND sampled_at <= $4
ORDER BY sampled_at ASC, id ASC;

-- name: ListInstanceResourceBuckets :many
SELECT
    to_timestamp(floor(extract(epoch FROM sampled_at) / $4)::bigint * $4)::timestamptz AS sampled_at,
    (array_remove(array_agg(container_status ORDER BY sampled_at DESC), NULL))[1] AS container_status,
    avg(cpu_percent)::double precision AS cpu_percent,
    avg(memory_used_bytes)::bigint AS memory_used_bytes,
    max(memory_limit_bytes)::bigint AS memory_limit_bytes,
    min(disk_read_bytes)::bigint AS disk_read_bytes,
    min(disk_write_bytes)::bigint AS disk_write_bytes,
    min(network_rx_bytes)::bigint AS network_rx_bytes,
    min(network_tx_bytes)::bigint AS network_tx_bytes,
    (array_remove(array_agg(last_error ORDER BY sampled_at DESC), NULL))[1] AS last_error
FROM instance_resource_samples
WHERE app_id = $1
  AND sampled_at >= $2
  AND sampled_at <= $3
GROUP BY 1
ORDER BY 1 ASC;

-- name: DeleteOldNodeResourceSamples :execrows
DELETE FROM node_resource_samples
WHERE id IN (
    SELECT id
    FROM node_resource_samples
    WHERE sampled_at < $1
    ORDER BY sampled_at ASC
    LIMIT $2
);

-- name: DeleteOldInstanceResourceSamples :execrows
DELETE FROM instance_resource_samples
WHERE id IN (
    SELECT id
    FROM instance_resource_samples
    WHERE sampled_at < $1
    ORDER BY sampled_at ASC
    LIMIT $2
);
```

- [ ] **Step 4: Generate sqlc code**

Run:

```bash
rtk make sqlc-generate
```

Expected: sqlc generation succeeds and `internal/store/sqlc/resource_samples.sql.go` exists.

- [ ] **Step 5: Verify migrations**

Run:

```bash
rtk make migrate-up
```

Expected: migration `000014_resource_samples` applies successfully.

- [ ] **Step 6: Commit schema work**

```bash
rtk git add internal/migrations/000014_resource_samples.up.sql internal/migrations/000014_resource_samples.down.sql internal/store/queries/resource_samples.sql internal/store/sqlc
rtk git commit -m "feat(runtime): 增加资源采样时序表" -m "新增节点和实例资源采样表，并补充 sqlc 查询。\n\n采样表保存 30 天原始资源数据，供运行节点抽屉和实例运行页查询趋势。"
```

---

### Task 2: Resource Service Types And Query Logic

**Files:**
- Create: `internal/service/resource_metrics_service.go`
- Create: `internal/service/resource_metrics_service_test.go`
- Modify: `internal/service/runtime_node_service.go`
- Modify: `internal/service/runtime_node_service_test.go`

- [ ] **Step 1: Add failing service tests**

Create tests covering:

Create these exact test functions and cover the named assertions:

- `TestResourceMetricsServiceListAppResourcesRequiresViewPermission`: arrange an app owned by another org member, call `ListAppResources` as an unrelated org member, assert `service.ErrForbidden`.
- `TestResourceMetricsServiceRejectsInvalidBucket`: call `NormalizeResourceRange("", "", "2m", fixedNow)`, assert an invalid input error.
- `TestRuntimeNodeServiceListNodesIncludesCurrentResource`: arrange one runtime node and one latest node sample, call `ListNodes` as `platform_admin`, assert `CurrentResource.CPUPercent` equals the sample value.

Use `require.NoError`, `require.ErrorIs`, and `assert.Equal`. Each test and table row must include adjacent Chinese comments per project rules.

- [ ] **Step 2: Run failing tests**

Run:

```bash
rtk go test ./internal/service -run 'TestResourceMetrics|TestRuntimeNodeServiceListNodesIncludesCurrentResource' -count=1
```

Expected: FAIL because service/types do not exist yet.

- [ ] **Step 3: Implement shared resource DTOs and range parsing**

Create `internal/service/resource_metrics_service.go` with these exported result types:

```go
type ResourceTimeRange struct {
	From   time.Time
	To     time.Time
	Bucket int32
}

type NodeResourceSampleResult struct {
	SampledAt        string   `json:"sampled_at"`
	CPUPercent      *float64 `json:"cpu_percent,omitempty"`
	MemoryUsedBytes *int64   `json:"memory_used_bytes,omitempty"`
	MemoryTotalBytes *int64  `json:"memory_total_bytes,omitempty"`
	DiskUsedBytes   *int64   `json:"disk_used_bytes,omitempty"`
	DiskTotalBytes  *int64   `json:"disk_total_bytes,omitempty"`
	NetworkRxBytes  *int64   `json:"network_rx_bytes,omitempty"`
	NetworkTxBytes  *int64   `json:"network_tx_bytes,omitempty"`
	InstanceCount   *int32   `json:"instance_count,omitempty"`
	LastError        string   `json:"last_error,omitempty"`
}

type InstanceResourceSampleResult struct {
	SampledAt       string   `json:"sampled_at"`
	ContainerStatus string  `json:"container_status,omitempty"`
	CPUPercent     *float64 `json:"cpu_percent,omitempty"`
	MemoryUsedBytes *int64  `json:"memory_used_bytes,omitempty"`
	MemoryLimitBytes *int64 `json:"memory_limit_bytes,omitempty"`
	DiskReadBytes  *int64   `json:"disk_read_bytes,omitempty"`
	DiskWriteBytes *int64   `json:"disk_write_bytes,omitempty"`
	NetworkRxBytes *int64   `json:"network_rx_bytes,omitempty"`
	NetworkTxBytes *int64   `json:"network_tx_bytes,omitempty"`
	LastError       string   `json:"last_error,omitempty"`
}
```

Implement `NormalizeResourceRange(fromRaw, toRaw, bucketRaw string, now time.Time) (ResourceTimeRange, error)`:

- Empty range defaults to `now - 7*24h` through `now`.
- `bucket=""` means raw samples.
- `bucket="5m"` maps to `300`.
- `bucket="1h"` maps to `3600`.
- invalid `from/to/bucket` returns a service error that handlers map to 400.

- [ ] **Step 4: Implement ResourceMetricsService**

Define:

```go
type ResourceMetricsStore interface {
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	GetRuntimeNode(ctx context.Context, id pgtype.UUID) (sqlc.RuntimeNode, error)
	ListAppsByRuntimeNode(ctx context.Context, arg sqlc.ListAppsByRuntimeNodeParams) ([]sqlc.App, error)
	ListNodeResourceSamples(ctx context.Context, arg sqlc.ListNodeResourceSamplesParams) ([]sqlc.NodeResourceSample, error)
	ListNodeResourceBuckets(ctx context.Context, arg sqlc.ListNodeResourceBucketsParams) ([]sqlc.ListNodeResourceBucketsRow, error)
	ListInstanceResourceSamples(ctx context.Context, arg sqlc.ListInstanceResourceSamplesParams) ([]sqlc.InstanceResourceSample, error)
	ListNodeInstanceResourceSamples(ctx context.Context, arg sqlc.ListNodeInstanceResourceSamplesParams) ([]sqlc.InstanceResourceSample, error)
	ListInstanceResourceBuckets(ctx context.Context, arg sqlc.ListInstanceResourceBucketsParams) ([]sqlc.ListInstanceResourceBucketsRow, error)
	ListLatestInstanceResourceSamplesByNode(ctx context.Context, runtimeNodeID pgtype.UUID) ([]sqlc.InstanceResourceSample, error)
}
```

Methods:

- `ListNodeResources(ctx, principal, nodeID string, r ResourceTimeRange) ([]NodeResourceSampleResult, error)`
- `ListNodeInstances(ctx, principal, nodeID string, limit, offset int32) ([]NodeInstanceResult, error)`
- `ListNodeInstanceResources(ctx, principal, nodeID, appID string, r ResourceTimeRange) ([]InstanceResourceSampleResult, error)`
- `ListAppResources(ctx, principal, appID string, r ResourceTimeRange) ([]InstanceResourceSampleResult, error)`

All node methods require `principal.Role == domain.UserRolePlatformAdmin`. `ListAppResources` uses `auth.CanViewApp`.

- [ ] **Step 5: Extend RuntimeNodeResult with current_resource**

In `runtime_node_service.go`, add:

```go
type NodeCurrentResourceResult struct {
	SampledAt        string   `json:"sampled_at,omitempty"`
	CPUPercent      *float64 `json:"cpu_percent,omitempty"`
	MemoryUsedBytes *int64   `json:"memory_used_bytes,omitempty"`
	MemoryTotalBytes *int64  `json:"memory_total_bytes,omitempty"`
	DiskUsedBytes   *int64   `json:"disk_used_bytes,omitempty"`
	DiskTotalBytes  *int64   `json:"disk_total_bytes,omitempty"`
	InstanceCount   *int32   `json:"instance_count,omitempty"`
	LastError        string   `json:"last_error,omitempty"`
}
```

Add `CurrentResource *NodeCurrentResourceResult `json:"current_resource,omitempty"` to `RuntimeNodeResult`.

Extend `RuntimeNodeStore` with `ListLatestNodeResourceSamples(ctx, []pgtype.UUID) ([]sqlc.NodeResourceSample, error)` and attach latest samples in `ListNodes`. Leave `GetNode` unchanged; the drawer trend API supplies detailed resource data.

- [ ] **Step 6: Run service tests**

```bash
rtk go test ./internal/service -run 'TestResourceMetrics|TestRuntimeNodeService' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit service work**

```bash
rtk git add internal/service/resource_metrics_service.go internal/service/resource_metrics_service_test.go internal/service/runtime_node_service.go internal/service/runtime_node_service_test.go
rtk git commit -m "feat(runtime): 增加资源趋势查询服务" -m "新增资源指标查询服务，封装时间范围、bucket 校验和权限边界。\n\n运行节点列表同步带出最近节点资源采样，供前端展示当前资源摘要。"
```

---

### Task 3: Agent Node Resource Collection

**Files:**
- Create: `runtime/agent/node_resource.go`
- Create: `runtime/agent/node_resource_test.go`
- Modify: `runtime/agent/heartbeat.go`
- Modify: `runtime/agent/enroll.go`
- Modify: `runtime/agent/heartbeat_test.go`

- [ ] **Step 1: Add failing agent tests**

Add tests:

Create these exact test functions and cover the named assertions:

- `TestNodeResourceCollectorParsesLinuxProcFiles`: feed representative `/proc/stat`, `/proc/meminfo`, and `/proc/net/dev` text into parser helpers; assert total CPU ticks, used memory, total memory, RX, and TX.
- `TestHeartbeatPayloadIncludesNodeResource`: run one heartbeat tick against an `httptest.Server`, decode the JSON body, and assert `sampled_at` and `node_resource.cpu_percent` are present.

Use temp files for `/proc/stat`, `/proc/meminfo`, `/proc/net/dev` parsing helpers. Do not require real Docker in unit tests.

- [ ] **Step 2: Run failing tests**

```bash
rtk go test ./runtime/agent -run 'TestNodeResource|TestHeartbeatPayloadIncludesNodeResource' -count=1
```

Expected: FAIL because collector and payload fields do not exist.

- [ ] **Step 3: Implement node resource collector**

Create `runtime/agent/node_resource.go`:

```go
type NodeResourceSnapshot struct {
	CPUPercent      *float64 `json:"cpu_percent,omitempty"`
	MemoryUsedBytes *int64   `json:"memory_used_bytes,omitempty"`
	MemoryTotalBytes *int64  `json:"memory_total_bytes,omitempty"`
	DiskUsedBytes   *int64   `json:"disk_used_bytes,omitempty"`
	DiskTotalBytes  *int64   `json:"disk_total_bytes,omitempty"`
	NetworkRxBytes  *int64   `json:"network_rx_bytes,omitempty"`
	NetworkTxBytes  *int64   `json:"network_tx_bytes,omitempty"`
	InstanceCount   *int32   `json:"instance_count,omitempty"`
	LastError        string   `json:"last_error,omitempty"`
}
```

Implement helpers:

- `parseProcStatCPU(line string) (idle, total uint64, error)`
- `parseMemInfo(raw string) (used, total int64, error)`
- `parseNetDev(raw string) (rx, tx int64, error)`
- `statDiskUsage(path string) (used, total int64, error)`
- `collectNodeResource(dataRoot string, docker DockerClient, prev *nodeResourceCounters) (NodeResourceSnapshot, nodeResourceCounters)`

For instance count, extend `DockerClient` with:

```go
ListContainers(ctx context.Context, namePrefix string) (int32, error)
```

Count containers whose names match `ocm-`, matching `app_initialize.go`.

- [ ] **Step 4: Include node resource in heartbeat and enroll**

Modify `heartbeat.tick` body:

```go
sampledAt := time.Now().UTC()
body := map[string]any{
	"agent_token":   h.tokenGetter(),
	"agent_version": agentVersion,
	"sampled_at":    sampledAt.Format(time.RFC3339),
	"node_resource": h.collectNodeResource(sampledAt),
}
```

Stop using old `resource_snapshot` for resource trend display; new manager logic reads `node_resource`.

- [ ] **Step 5: Run agent tests**

```bash
rtk go test ./runtime/agent -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit agent work**

```bash
rtk git add runtime/agent/node_resource.go runtime/agent/node_resource_test.go runtime/agent/heartbeat.go runtime/agent/enroll.go runtime/agent/heartbeat_test.go runtime/agent/docker_client.go
rtk git commit -m "feat(agent): 上报节点资源采样" -m "runtime-agent 采集节点 CPU、内存、磁盘、网络和实例数，并在心跳中上报。\n\n采集失败时保留可用字段并写入 last_error，避免单项异常中断心跳。"
```

---

### Task 4: Persist Node And Instance Samples

**Files:**
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/api/handlers/agent.go`
- Modify: `internal/api/handlers/agent_test.go`
- Modify: `internal/service/runtime_node_service.go`
- Modify: `internal/service/runtime_node_service_test.go`
- Modify: `internal/worker/handlers/runtime_refresh_status.go`
- Modify: `internal/worker/handlers/runtime_refresh_status_test.go`
- Modify: `internal/integrations/runtime/adapter.go`
- Modify: `internal/integrations/runtime/agent_backed.go`
- Modify: `internal/integrations/runtime/agent_backed_test.go`

- [ ] **Step 1: Write failing handler/service tests**

Add tests:

Create these exact test functions and cover the named assertions:

- `TestAgentHeartbeatPersistsNodeResourceSample`: send a heartbeat request with `node_resource`, call handler/service, and assert the store stub received `InsertNodeResourceSample`.
- `TestRuntimeRefreshStatusWritesInstanceSample`: run `RuntimeRefreshStatusHandler.Handle` with fake inspect/stats data and assert the store stub received `InsertInstanceResourceSample`.

The runtime refresh test should assert `SetAppRuntimeSnapshot` is not required for display behavior. Resource display must use `InsertInstanceResourceSample`.

- [ ] **Step 2: Run failing tests**

```bash
rtk go test ./internal/api/handlers ./internal/service ./internal/worker/handlers -run 'TestAgentHeartbeatPersistsNodeResourceSample|TestRuntimeRefreshStatusWritesInstanceSample' -count=1
```

Expected: FAIL.

- [ ] **Step 3: Extend heartbeat DTO and service input**

In `dto.go`, add:

```go
type AgentNodeResourceRequest struct {
	CPUPercent      *float64 `json:"cpu_percent"`
	MemoryUsedBytes *int64   `json:"memory_used_bytes"`
	MemoryTotalBytes *int64  `json:"memory_total_bytes"`
	DiskUsedBytes   *int64   `json:"disk_used_bytes"`
	DiskTotalBytes  *int64   `json:"disk_total_bytes"`
	NetworkRxBytes  *int64   `json:"network_rx_bytes"`
	NetworkTxBytes  *int64   `json:"network_tx_bytes"`
	InstanceCount   *int32   `json:"instance_count"`
	LastError        string   `json:"last_error"`
}
```

Add `SampledAt string` and `NodeResource *AgentNodeResourceRequest` to enroll/heartbeat request types. Handler parses `sampled_at`; if absent, use `time.Now().UTC()` to avoid rejecting old agents during rollout.

- [ ] **Step 4: Persist node samples**

Extend `AgentHeartbeatInput` and `AgentEnrollInput` with:

```go
SampledAt    time.Time
NodeResource *NodeResourceInput
```

Extend `RuntimeNodeStore` with `InsertNodeResourceSample`. In `HandleHeartbeat`, after `UpdateRuntimeNodeHeartbeat`, insert a node resource sample when `NodeResource != nil`. Use the updated node ID to avoid trusting request body node IDs.

- [ ] **Step 5: Extend ContainerStats with disk IO**

In `internal/integrations/runtime/adapter.go`:

```go
type ContainerStats struct {
	CPUPercent     float64 `json:"cpu_percent"`
	MemoryUsage    uint64  `json:"memory_usage_bytes"`
	MemoryLimit    uint64  `json:"memory_limit_bytes"`
	DiskReadBytes  uint64  `json:"disk_read_bytes"`
	DiskWriteBytes uint64  `json:"disk_write_bytes"`
	NetworkRxBytes uint64  `json:"network_rx_bytes"`
	NetworkTxBytes uint64  `json:"network_tx_bytes"`
}
```

Update `agent_backed.go` Docker stats parsing to sum block IO read/write entries. If Docker response lacks block IO, leave zero and let `last_error` remain empty; only transport/parse failures should set `last_error`.

- [ ] **Step 6: Write instance samples in runtime refresh handler**

Replace the display snapshot write with `InsertInstanceResourceSample`. Remove `SetAppRuntimeSnapshot` from the resource display path; update old tests to assert instance samples instead of app snapshot JSON.

`AppRuntimeSnapshot` can become an internal sample struct or be replaced with `InstanceResourceSampleInput` containing:

- app ID
- runtime node ID
- container ID
- sampled_at
- container status
- CPU, memory, disk, network
- last_error

- [ ] **Step 7: Run backend tests**

```bash
rtk go test ./internal/api/handlers ./internal/service ./internal/worker/handlers ./internal/integrations/runtime -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit persistence work**

```bash
rtk git add internal/api/handlers/dto.go internal/api/handlers/agent.go internal/api/handlers/agent_test.go internal/service/runtime_node_service.go internal/service/runtime_node_service_test.go internal/worker/handlers/runtime_refresh_status.go internal/worker/handlers/runtime_refresh_status_test.go internal/integrations/runtime/adapter.go internal/integrations/runtime/agent_backed.go internal/integrations/runtime/agent_backed_test.go
rtk git commit -m "feat(runtime): 持久化资源采样数据" -m "心跳写入节点资源采样，runtime_refresh_status 写入实例资源采样。\n\n实例指标继续由 manager 通过 Docker proxy 主动拉取，避免实例容器自身上报。"
```

---

### Task 5: Resource Metrics HTTP API And OpenAPI

**Files:**
- Create: `internal/api/handlers/resource_metrics.go`
- Create: `internal/api/handlers/resource_metrics_test.go`
- Modify: `internal/api/router.go`
- Modify: `cmd/server/main.go`
- Generated: `openapi/openapi.yaml`
- Generated: `web/src/api/generated.ts`
- Modify: `web/src/api/index.ts` if generated required fields need aliases

- [ ] **Step 1: Add failing handler tests**

Cover:

Create these exact test functions and cover the named assertions:

- `TestResourceMetricsHandlerRejectsOrgMemberForNodeResources`: call `GET /api/v1/runtime-nodes/:nodeId/resources` with an org member token and assert HTTP 403.
- `TestResourceMetricsHandlerReturnsAppResources`: call `GET /api/v1/apps/:appId/resources` with an allowed principal and assert HTTP 200 plus a `samples` array.

- [ ] **Step 2: Run failing handler tests**

```bash
rtk go test ./internal/api/handlers -run 'TestResourceMetricsHandler' -count=1
```

Expected: FAIL.

- [ ] **Step 3: Implement resource handler**

Create `resource_metrics.go` with routes:

```go
func RegisterResourceMetricsRoutes(router gin.IRouter, handler *ResourceMetricsHandler) {
	router.GET("/api/v1/runtime-nodes/:nodeId/resources", handler.NodeResources)
	router.GET("/api/v1/runtime-nodes/:nodeId/instances", handler.NodeInstances)
	router.GET("/api/v1/runtime-nodes/:nodeId/instances/:appId/resources", handler.NodeInstanceResources)
	router.GET("/api/v1/apps/:appId/resources", handler.AppResources)
}
```

Use existing `bearerToken` helper and token verification style. Query params: `from`, `to`, `bucket`, `limit`, `offset`.

Response shapes:

```json
{ "samples": [] }
{ "instances": [] }
```

- [ ] **Step 4: Wire dependencies**

Add `ResourceMetricsService *service.ResourceMetricsService` to `api.Dependencies` and register routes when non-nil. In `cmd/server/main.go`, instantiate:

```go
resourceMetricsService := service.NewResourceMetricsService(dbStore.Queries)
```

Pass it into router dependencies.

- [ ] **Step 5: Update OpenAPI**

Add swag comments to new handler methods. Run:

```bash
rtk make openapi-gen
rtk make web-types-gen
rtk make openapi-check
```

Expected: generation succeeds and `openapi/openapi.yaml` remains clean after `openapi-check`.

- [ ] **Step 6: Run handler tests**

```bash
rtk go test ./internal/api/handlers -run 'TestResourceMetricsHandler|TestRuntimeNodes' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit API work**

```bash
rtk git add internal/api/handlers/resource_metrics.go internal/api/handlers/resource_metrics_test.go internal/api/router.go cmd/server/main.go openapi/openapi.yaml web/src/api/generated.ts web/src/api/index.ts
rtk git commit -m "feat(runtime): 增加资源趋势查询接口" -m "新增节点资源、节点关联实例、实例资源趋势接口。\n\n接口按平台和应用权限校验，并同步 OpenAPI 与前端生成类型。"
```

---

### Task 6: Cleanup Old Resource Samples

**Files:**
- Create: `internal/service/resource_sample_cleanup.go`
- Create: `internal/service/resource_sample_cleanup_test.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Add cleanup tests**

Create `TestResourceSampleCleanupDeletesOldSamplesInBatches`: arrange a fixed clock, run `RunOnce`, and assert both delete methods receive cutoff `fixedNow - 30*24h` and limit `1000`.

- [ ] **Step 2: Implement cleanup reconciler**

Create service:

```go
type ResourceSampleCleanupStore interface {
	DeleteOldNodeResourceSamples(ctx context.Context, cutoff pgtype.Timestamptz, limit int32) (int64, error)
	DeleteOldInstanceResourceSamples(ctx context.Context, cutoff pgtype.Timestamptz, limit int32) (int64, error)
}

type ResourceSampleCleanup struct {
	store ResourceSampleCleanupStore
	now   func() time.Time
}
```

`RunOnce(ctx)` deletes samples older than `now()-30*24h` with limit `1000` for each table and returns counts.

- [ ] **Step 3: Wire periodic cleanup**

In `cmd/server/main.go`, add a periodic reconciler:

```go
resourceCleanup := service.NewResourceSampleCleanup(dbStore.Queries)
resourceCleanupTask := service.NewPeriodicReconciler("resource_sample_cleanup", time.Hour, func(ctx context.Context) error {
	_, _, err := resourceCleanup.RunOnce(ctx)
	return err
})
eg.Go(func() error { return resourceCleanupTask.Run(gctx, logger) })
```

- [ ] **Step 4: Run tests**

```bash
rtk go test ./internal/service ./cmd/server -run 'TestResourceSampleCleanup|TestRunManager' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit cleanup**

```bash
rtk git add internal/service/resource_sample_cleanup.go internal/service/resource_sample_cleanup_test.go cmd/server/main.go
rtk git commit -m "feat(runtime): 清理过期资源采样" -m "新增资源采样清理任务，按批删除 30 天前的节点和实例资源数据。\n\n清理任务随 manager 周期运行，避免资源采样表无限增长。"
```

---

### Task 7: Frontend Resource Hooks And Chart Component

**Files:**
- Modify: `web/src/api/hooks/useRuntimeNodes.ts`
- Modify: `web/src/api/hooks/useApps.ts`
- Create: `web/src/components/ResourceTrendChart.vue`
- Create: `web/src/components/__tests__/ResourceTrendChart.spec.ts`

- [ ] **Step 1: Add chart component tests**

Test cases:

Create these exact Vitest cases:

- `renders empty state when samples are empty`: mount the component with `samples=[]`, assert empty text is visible and no `polyline` exists.
- `renders a polyline when samples contain values`: mount with two samples containing numeric values, assert one `polyline` exists.
- `formats byte values in tooltip labels`: mount with `unit="bytes"`, assert formatted `KB` or `MB` text is rendered.

- [ ] **Step 2: Add API hook types**

In `useRuntimeNodes.ts`, add:

```ts
export type ResourceRange = '1h' | '24h' | '7d' | '30d'
export interface NodeResourceSample {
  sampled_at: string
  cpu_percent?: number
  memory_used_bytes?: number
  memory_total_bytes?: number
  disk_used_bytes?: number
  disk_total_bytes?: number
  network_rx_bytes?: number
  network_tx_bytes?: number
  instance_count?: number
  last_error?: string
}
export interface InstanceResourceSample {
  sampled_at: string
  container_status?: string
  cpu_percent?: number
  memory_used_bytes?: number
  memory_limit_bytes?: number
  disk_read_bytes?: number
  disk_write_bytes?: number
  network_rx_bytes?: number
  network_tx_bytes?: number
  last_error?: string
}
export interface NodeInstanceResourceRow {
  id: string
  name: string
  org_id: string
  org_name?: string
  status: string
  container_id?: string
  current_resource?: InstanceResourceSample
}
```

Add helpers:

```ts
export function rangeQuery(range: ResourceRange): { from: string; to: string; bucket?: string } {
  const to = new Date()
  const from = new Date(to)
  if (range === '1h') from.setHours(from.getHours() - 1)
  if (range === '24h') from.setDate(from.getDate() - 1)
  if (range === '7d') from.setDate(from.getDate() - 7)
  if (range === '30d') from.setDate(from.getDate() - 30)
  const query: { from: string; to: string; bucket?: string } = { from: from.toISOString(), to: to.toISOString() }
  if (range === '24h' || range === '7d') query.bucket = '5m'
  if (range === '30d') query.bucket = '1h'
  return query
}
```

Rules:

- `1h`: no bucket.
- `24h`: `bucket=5m`.
- `7d`: `bucket=5m`.
- `30d`: `bucket=1h`.

Hooks:

- `useRuntimeNodeResourcesQuery(nodeId, range)`
- `useRuntimeNodeInstancesQuery(nodeId)`
- `useRuntimeNodeInstanceResourcesQuery(nodeId, appId, range, enabled)`

In `useApps.ts`, add `useAppResourcesQuery(appId, range)`.

- [ ] **Step 3: Implement ResourceTrendChart**

Use SVG, similar to `web/src/pages/usage/UsageSummary.vue`, but make it reusable:

Props:

```ts
defineProps<{
  title: string
  samples: Array<{ sampled_at: string; value?: number | null; secondary?: number | null }>
  unit: 'percent' | 'bytes' | 'rate' | 'count'
  emptyText?: string
}>()
```

Render empty state when there is no numeric value. Keep fixed SVG dimensions and responsive width.

- [ ] **Step 4: Run component tests**

```bash
rtk sh -c 'cd web && npm test -- --run ResourceTrendChart'
```

Expected: PASS.

- [ ] **Step 5: Commit frontend foundation**

```bash
rtk git add web/src/api/hooks/useRuntimeNodes.ts web/src/api/hooks/useApps.ts web/src/components/ResourceTrendChart.vue web/src/components/__tests__/ResourceTrendChart.spec.ts
rtk git commit -m "feat(web): 增加资源趋势查询组件" -m "新增资源趋势 API hooks 和通用 SVG 趋势图组件。\n\n时间范围统一映射 bucket 参数，供节点抽屉和实例运行页复用。"
```

---

### Task 8: Runtime Nodes Drawer UI

**Files:**
- Modify: `web/src/pages/runtime-nodes/RuntimeNodesPage.vue`
- Modify: `web/src/pages/runtime-nodes/RuntimeNodesPage.spec.ts`

- [ ] **Step 1: Add failing page tests**

Cover:

Create these exact Vitest cases:

- `shows current resource column for runtime nodes`: mock `/runtime-nodes` with `current_resource`, mount page, assert `CPU` and `内存` text appears.
- `opens drawer without changing route when view is clicked`: click `查看`, assert drawer title contains node name and router path remains `/runtime-nodes`.
- `loads instance resources when an instance row is expanded`: mock node instance resources endpoint, expand a row, assert instance chart title appears.

- [ ] **Step 2: Implement current resource column**

Add render helper:

```ts
function formatCurrentResource(node: RuntimeNode): string {
  const r = node.current_resource
  if (!r) return '未采集'
  return `CPU ${formatPercent(r.cpu_percent)} · 内存 ${formatRatio(r.memory_used_bytes, r.memory_total_bytes)} · 磁盘 ${formatRatio(r.disk_used_bytes, r.disk_total_bytes)}`
}
```

Add “当前资源” and “最近采样” columns. Keep enable/disable actions.

- [ ] **Step 3: Implement wide drawer**

Use Naive UI `NDrawer`, `NDrawerContent`, `NSegmented`, `NDataTable`, `NCollapse` or expandable row rendering.

State:

```ts
const selectedNode = ref<RuntimeNode | null>(null)
const drawerVisible = computed({
  get: () => selectedNode.value != null,
  set: (value) => { if (!value) selectedNode.value = null },
})
const range = ref<ResourceRange>('7d')
```

Do not call `router.push` for drawer open.

- [ ] **Step 4: Implement node trend cards and instance expandable rows**

Use `ResourceTrendChart` for:

- CPU percent.
- memory percent.
- disk percent.
- network RX/TX rate.
- instance count.

Instance table expandable row loads `useRuntimeNodeInstanceResourcesQuery(selectedNodeId, row.id, range, expanded)`.

- [ ] **Step 5: Run page tests**

```bash
rtk sh -c 'cd web && npm test -- --run RuntimeNodesPage'
```

Expected: PASS.

- [ ] **Step 6: Commit node UI**

```bash
rtk git add web/src/pages/runtime-nodes/RuntimeNodesPage.vue web/src/pages/runtime-nodes/RuntimeNodesPage.spec.ts
rtk git commit -m "feat(web): 增加节点资源抽屉" -m "运行节点列表展示当前资源摘要，并通过宽抽屉查看节点趋势和关联实例趋势。\n\n抽屉不修改 URL，关闭后保留列表上下文。"
```

---

### Task 9: App Runtime Tab Trend UI

**Files:**
- Modify: `web/src/pages/apps/AppRuntimeTab.vue`
- Modify: `web/src/pages/apps/AppDetailPage.spec.ts` or create/update `AppRuntimeTab.spec.ts`

- [ ] **Step 1: Add failing tests**

Cover:

Create these exact Vitest cases:

- `renders resource trend charts in runtime tab`: mock app resources with samples, mount tab, assert CPU and network chart titles appear.
- `keeps runtime operation buttons visible when no samples exist`: mock empty samples, assert start/stop/restart/delete controls remain visible according to app status.
- `changes resource query when range changes`: click `30d`, assert resource API was called with `bucket=1h`.

- [ ] **Step 2: Replace snapshot cards with resource charts**

Keep existing operation buttons and job panel. Replace the `runtime?.snapshot` grid with:

- time range segmented control.
- CPU chart.
- memory chart.
- disk read/write chart.
- network RX/TX chart.
- latest sample status line.

`useAppRuntimeQuery` may remain for container inspect/status, but chart data comes from `useAppResourcesQuery`.

- [ ] **Step 3: Preserve stop/delete confirmations**

Verify `ConfirmActionModal` logic stays unchanged. Operation mutations should continue invalidating app/runtime queries and additionally invalidate app resource query on success.

- [ ] **Step 4: Run app page tests**

```bash
rtk sh -c 'cd web && npm test -- --run AppRuntimeTab AppDetailPage'
```

Expected: PASS.

- [ ] **Step 5: Commit runtime tab UI**

```bash
rtk git add web/src/pages/apps/AppRuntimeTab.vue web/src/pages/apps/AppDetailPage.spec.ts web/src/pages/apps/AppRuntimeTab.spec.ts web/src/api/hooks/useApps.ts
rtk git commit -m "feat(web): 将实例运行页改为趋势视图" -m "实例运行 tab 使用资源采样接口展示 CPU、内存、磁盘和网络趋势。\n\n保留启动、停止、重启、删除操作和 job 进度展示。"
```

---

### Task 10: End-To-End Verification And Browser Acceptance

**Files:**
- Generated/modified only if earlier verification reveals fixes.

- [ ] **Step 1: Run backend tests**

```bash
rtk go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 2: Run frontend typecheck and tests**

```bash
rtk sh -c 'cd web && npm run typecheck'
rtk sh -c 'cd web && npm test -- --run'
```

Expected: both PASS.

- [ ] **Step 3: Run OpenAPI synchronization**

```bash
rtk make openapi-gen
rtk make web-types-gen
rtk make openapi-check
```

Expected: PASS. If `openapi-check` reports a diff, commit generated `openapi/openapi.yaml` and `web/src/api/generated.ts` with the related API changes.

- [ ] **Step 4: Start local stack for browser validation**

```bash
rtk make dev-up
```

Then start the web dev server if compose does not already serve it:

```bash
rtk sh -c 'cd web && npm run dev -- --host 0.0.0.0'
```

Use an available URL shown by Vite or the compose frontend URL.

- [ ] **Step 5: Browser validate `/runtime-nodes`**

Use Chrome DevTools MCP or Playwright:

1. Log in as platform admin with `admin / admin123`.
2. Open `/runtime-nodes`.
3. Confirm the current resource column renders values or “未采集”.
4. Click “查看” on a node.
5. Confirm drawer opens and URL remains `/runtime-nodes`.
6. Switch 1h, 24h, 7d, 30d.
7. Confirm no console errors.
8. Expand an associated instance row and confirm charts or empty states render.

- [ ] **Step 6: Browser validate app runtime tab**

1. Open an app detail runtime tab.
2. Confirm operation buttons still render.
3. Confirm resource charts or empty states render.
4. If app is stopped, confirm historical trend message says data ends at latest sample.
5. Confirm no console errors.

- [ ] **Step 7: Fix browser issues before final commit**

When a browser check fails:

1. Fix the issue.
2. Rerun the targeted frontend test.
3. Repeat the browser step that failed.

Do not leave a known browser logic issue in the handoff.

- [ ] **Step 8: Final status commit for verification fixes**

When verification fixes changed files, commit them:

```bash
rtk git status --short
rtk git add internal web openapi
rtk git commit -m "fix(runtime): 修复资源趋势验收问题" -m "根据浏览器验收结果修复节点抽屉或实例运行页问题。\n\n重新运行相关单元测试和浏览器验收。"
```

---

## Self-Review Checklist

- Spec coverage:
  - Node current resource column: Task 2 and Task 8.
  - Wide drawer without URL query: Task 8.
  - Node trends and associated instances: Task 5, Task 7, Task 8.
  - Instance runtime trend tab: Task 5, Task 7, Task 9.
  - Node resources from agent heartbeat: Task 3 and Task 4.
  - Instance resources from manager Docker proxy: Task 4.
  - 30-second raw samples and 30-day retention: Task 1, Task 4, Task 6.
  - Browser validation and self-fix requirement: Task 10.
- Placeholder scan: no unresolved placeholder steps; each task has concrete files, commands, and expected outcomes.
- Type consistency: service DTO names, API response keys, and frontend hook names are defined before use.
