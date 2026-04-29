package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"oc-manager/internal/store/sqlc"
)

// Store 封装数据库连接池和 sqlc 查询入口。
// service 层通过 Store 获取查询对象和事务能力，不直接管理连接生命周期。
type Store struct {
	pool    *pgxpool.Pool
	Queries *sqlc.Queries
}

// Open 创建 PostgreSQL 连接池，并返回可复用的 Store。
// 连接字符串错误会在启动时直接返回，避免服务运行后才暴露数据库配置问题。
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("解析数据库连接配置失败: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("创建数据库连接池失败: %w", err)
	}
	return New(pool), nil
}

// New 用已有连接池创建 Store，主要用于 server 启动组装和测试注入。
func New(pool *pgxpool.Pool) *Store {
	return &Store{
		pool:    pool,
		Queries: sqlc.New(pool),
	}
}

// Pool 返回底层连接池，供少量需要 pgx 原语的基础设施代码使用。
func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

// Close 关闭数据库连接池。
func (s *Store) Close() {
	if s == nil || s.pool == nil {
		return
	}
	s.pool.Close()
}

// WithTx 在单个数据库事务中执行 fn。
// fn 返回错误时回滚；提交失败时返回提交错误。业务层不得在 fn 内部自行 Commit 或 Rollback。
func (s *Store) WithTx(ctx context.Context, fn func(*sqlc.Queries) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("开启数据库事务失败: %w", err)
	}

	if err := fn(s.Queries.WithTx(tx)); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交数据库事务失败: %w", err)
	}
	return nil
}
