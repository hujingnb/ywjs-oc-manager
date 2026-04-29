package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// runtimeNodeStore 在 sqlc.Queries 之外补齐 service 需要的扩展能力，例如轮换 bootstrap token。
// 这样 service 层只依赖 RuntimeNodeStore 接口，不直接访问连接池或写裸 SQL。
type runtimeNodeStore struct {
	*sqlc.Queries
	pool *pgxpool.Pool
}

// NewRuntimeNodeStore 用现有 Store 构造满足 service.RuntimeNodeStore 与 BootstrapRotator 的实现。
func NewRuntimeNodeStore(s *Store) *runtimeNodeStore {
	return &runtimeNodeStore{Queries: s.Queries, pool: s.pool}
}

const rotateRuntimeNodeBootstrap = `
UPDATE runtime_nodes
SET bootstrap_token_hash = $2,
    bootstrap_token_expires_at = $3,
    updated_at = now()
WHERE id = $1
RETURNING id, name, status, agent_docker_endpoint, agent_file_endpoint, agent_tls_ca_cert,
    agent_token_hash, bootstrap_token_hash, bootstrap_token_expires_at, agent_version,
    heartbeat_interval_seconds, last_heartbeat_at, resource_snapshot_json, metadata_json,
    node_data_root, registered_at, created_at, updated_at
`

// RotateBootstrapToken 直接通过连接池执行 rotate；不在 sqlc 中生成是为了避免因单一查询触发重新生成全量代码。
func (s *runtimeNodeStore) RotateBootstrapToken(ctx context.Context, arg service.RotateBootstrapTokenParams) (sqlc.RuntimeNode, error) {
	row := s.pool.QueryRow(ctx, rotateRuntimeNodeBootstrap, arg.ID,
		pgtype.Text{String: arg.BootstrapTokenHash, Valid: true},
		pgtype.Timestamptz{Time: arg.BootstrapTokenExpiresAt, Valid: true},
	)
	var node sqlc.RuntimeNode
	err := row.Scan(
		&node.ID,
		&node.Name,
		&node.Status,
		&node.AgentDockerEndpoint,
		&node.AgentFileEndpoint,
		&node.AgentTlsCaCert,
		&node.AgentTokenHash,
		&node.BootstrapTokenHash,
		&node.BootstrapTokenExpiresAt,
		&node.AgentVersion,
		&node.HeartbeatIntervalSeconds,
		&node.LastHeartbeatAt,
		&node.ResourceSnapshotJson,
		&node.MetadataJson,
		&node.NodeDataRoot,
		&node.RegisteredAt,
		&node.CreatedAt,
		&node.UpdatedAt,
	)
	return node, err
}
