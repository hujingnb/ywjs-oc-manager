package store

import (
	"context"
	"database/sql"
)

// AgentTokenStore 维护 runtime_nodes.agent_token_ciphertext 列。
//
// Phase A 已知妥协修复（B6）：agent_token 现在加密入库，进程重启不需要 rotate-bootstrap。
// 字段在迁移 000003 中创建为 nullable，便于历史节点平滑过渡。
type AgentTokenStore struct {
	// db 直接执行少量手写 SQL，避免修改 sqlc 生成目录中的文件。
	db *sql.DB
}

// NewAgentTokenStore 创建 token 持久化 store。
func NewAgentTokenStore(s *Store) *AgentTokenStore {
	return &AgentTokenStore{db: s.db}
}

const setAgentTokenCiphertextSQL = `
UPDATE runtime_nodes SET agent_token_ciphertext = ?, updated_at = now() WHERE id = ?
`

// Set 把加密后的 agent token 持久化到指定节点。
// ciphertext 为空时写入 SQL NULL，表示该节点尚未完成自动注册或仍在兼容迁移期。
func (s *AgentTokenStore) Set(ctx context.Context, nodeID string, ciphertext string) error {
	// ciphertext 为空时传 nil，让驱动写入 SQL NULL。
	var val interface{}
	if ciphertext != "" {
		val = ciphertext
	}
	_, err := s.db.ExecContext(ctx, setAgentTokenCiphertextSQL, val, nodeID)
	return err
}

const getAgentTokenCiphertextSQL = `
SELECT agent_token_ciphertext FROM runtime_nodes WHERE id = ?
`

// Get 取节点的加密 agent token；列为空时返回空字符串和 nil error。
// 调用方负责区分"节点不存在"的查询错误与"节点存在但 token 未写入"的空字符串。
func (s *AgentTokenStore) Get(ctx context.Context, nodeID string) (string, error) {
	var ciphertext sql.NullString
	row := s.db.QueryRowContext(ctx, getAgentTokenCiphertextSQL, nodeID)
	if err := row.Scan(&ciphertext); err != nil {
		return "", err
	}
	if !ciphertext.Valid {
		return "", nil
	}
	return ciphertext.String, nil
}
