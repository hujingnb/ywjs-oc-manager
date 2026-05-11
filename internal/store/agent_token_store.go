package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AgentTokenStore 维护 runtime_nodes.agent_token_ciphertext 列。
//
// Phase A 已知妥协修复（B6）：agent_token 现在加密入库，进程重启不需要 rotate-bootstrap。
// 字段在迁移 000003 中创建为 nullable，便于历史节点平滑过渡。
type AgentTokenStore struct {
	// pool 直接执行少量手写 SQL，避免修改 sqlc 生成目录中的文件。
	pool *pgxpool.Pool
}

// NewAgentTokenStore 创建 token 持久化 store。
func NewAgentTokenStore(s *Store) *AgentTokenStore {
	return &AgentTokenStore{pool: s.pool}
}

const setAgentTokenCiphertextSQL = `
UPDATE runtime_nodes SET agent_token_ciphertext = $2, updated_at = now() WHERE id = $1
`

// Set 把加密后的 agent token 持久化到指定节点。
// ciphertext 为空时写入 SQL NULL，表示该节点尚未完成自动注册或仍在兼容迁移期。
func (s *AgentTokenStore) Set(ctx context.Context, nodeID pgtype.UUID, ciphertext string) error {
	_, err := s.pool.Exec(ctx, setAgentTokenCiphertextSQL, nodeID, pgtype.Text{String: ciphertext, Valid: ciphertext != ""})
	return err
}

const getAgentTokenCiphertextSQL = `
SELECT agent_token_ciphertext FROM runtime_nodes WHERE id = $1
`

// Get 取节点的加密 agent token；列为空时返回空字符串和 nil error。
// 调用方负责区分“节点不存在”的查询错误与“节点存在但 token 未写入”的空字符串。
func (s *AgentTokenStore) Get(ctx context.Context, nodeID pgtype.UUID) (string, error) {
	var ciphertext pgtype.Text
	row := s.pool.QueryRow(ctx, getAgentTokenCiphertextSQL, nodeID)
	if err := row.Scan(&ciphertext); err != nil {
		return "", err
	}
	if !ciphertext.Valid {
		return "", nil
	}
	return ciphertext.String, nil
}
