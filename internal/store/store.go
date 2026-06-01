package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"oc-manager/internal/store/sqlc"
)

// Store 封装数据库连接和 sqlc 查询入口。
// service 层通过 Store 获取查询对象和事务能力，不直接管理连接生命周期。
type Store struct {
	// db 是全局共享的 MySQL 连接池（database/sql 内部维护连接复用），由 Store 统一关闭。
	db *sql.DB
	// Queries 暴露 sqlc 生成的类型安全查询方法，供 service 层组合使用。
	Queries *sqlc.Queries
}

// Open 用 MySQL DSN 创建连接池并返回可复用的 Store。
// databaseURL 形如 "mysql://user:pass@tcp(host:3306)/ocm?parseTime=true&loc=UTC"，
// 与 cmd/migrate 共用同一配置项。go-sql-driver/mysql 的 DSN 不接受 "mysql://" scheme
// 前缀，故在此剥离后再交给 sql.Open；golang-migrate 那侧才需要保留该前缀。
// ctx 暂未被惰性的 sql.Open 使用，但保留入参以兼容调用方与未来探活逻辑。
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	dsn := strings.TrimPrefix(databaseURL, "mysql://")
	// sql.Open 是惰性的、不校验 DSN；启动阶段先用 mysql.ParseDSN 显式校验，
	// 让非法连接配置在启动时即失败，而非延迟到首次查询（与原 pgxpool.ParseConfig 行为一致）。
	// 同时把时区相关参数强制规整为 UTC，避免「Go 时间 vs SQL now()」跨时区比较出错。
	normalized, err := normalizeDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("解析数据库连接配置失败: %w", err)
	}
	db, err := sql.Open("mysql", normalized)
	if err != nil {
		return nil, fmt.Errorf("打开数据库连接失败: %w", err)
	}
	// New 只负责组合 Store，不额外 Ping；调用方按启动流程决定是否探活。
	return New(db), nil
}

// normalizeDSN 把 MySQL DSN 的时区相关参数强制规整为 UTC，是「时区错位」类 bug 的源头根治：
//
// 背景：移动云等托管 MySQL 服务器 time_zone 常为 +08:00，而本项目 DSN 用 loc=UTC。
// 列类型是裸 datetime（无时区转换），于是：
//   - now() 按服务器会话时区写入「北京墙钟」裸值（如 14:46），驱动按 loc=UTC 读回 → 凭空 +8h；
//   - reaper/scheduler 等用 Go 侧 time.Now() 算阈值（真实 UTC），驱动按 UTC 发给 MySQL。
//
// 两者跨时区边界比较必然错位（曾导致 reaper 的 ListStaleInits 恒返回 0、init 子状态孤儿永不回收）。
// 这里在打开连接时强制：
//   - parseTime=true、loc=UTC：Go 侧按 UTC 解析/发送时间；
//   - 会话变量 time_zone='+00:00'：让 now()/CURRENT_TIMESTAMP 返回 UTC，与 loc=UTC 对齐。
//
// 如此 now() 写入与 Go 时间在全代码范围内同处 UTC，根除整类比较错位，且不依赖每份部署配置手填。
// 注意：本次切换前已写入的旧行仍是服务器本地墙钟裸值，会在下一次被改写时自然纠正；
// 历史数据的一次性纠偏不在本函数职责内。
func normalizeDSN(dsn string) (string, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return "", err
	}
	// 解析时间列为 time.Time 而非 []byte，并固定按 UTC 解释裸 datetime。
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	// 会话级 time_zone 设为 UTC，使 now() 与 loc=UTC 一致；值需带引号，驱动据此下发 SET time_zone。
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["time_zone"] = "'+00:00'"
	return cfg.FormatDSN(), nil
}

// New 用已有连接创建 Store，主要用于 server 启动组装和测试注入。
func New(db *sql.DB) *Store {
	return &Store{db: db, Queries: sqlc.New(db)}
}

// Ping 强制建立一次真实连接以校验数据库可达；sql.Open 是惰性的，本身不会立即连接。
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Close 关闭数据库连接池。
func (s *Store) Close() {
	if s == nil || s.db == nil {
		return
	}
	_ = s.db.Close()
}

// WithTx 在单个数据库事务中执行 fn。
// fn 返回错误时回滚；提交失败时返回提交错误。业务层不得在 fn 内部自行 Commit 或 Rollback。
func (s *Store) WithTx(ctx context.Context, fn func(*sqlc.Queries) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启数据库事务失败: %w", err)
	}

	if err := fn(s.Queries.WithTx(tx)); err != nil {
		// 业务错误优先返回；回滚失败通常说明连接已失效，此处不覆盖原始失败原因。
		_ = tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交数据库事务失败: %w", err)
	}
	return nil
}
