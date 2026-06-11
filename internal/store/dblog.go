package store

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"strconv"
	"time"

	mlog "oc-manager/internal/log"
	"oc-manager/internal/store/sqlc"
)

// defaultSlowQueryThreshold 是慢查询阈值默认值；可由环境变量 LOG_SLOW_QUERY_MS 覆盖。
// 选 200ms 是经验阈值：超过该耗时的查询通常值得关注（缺索引 / 锁等待 / 大结果集）。
const defaultSlowQueryThreshold = 200 * time.Millisecond

// slowQueryThreshold 在包加载时从环境变量读一次；非法或空值回退默认值。
// 包级变量而非每次解析，避免热路径反复读 env。
var slowQueryThreshold = parseSlowQueryThreshold(os.Getenv("LOG_SLOW_QUERY_MS"))

// parseSlowQueryThreshold 把毫秒字符串解析为 time.Duration。
// 边界：空串、非数字、负数都视为非法配置并回退默认值，避免误配把所有查询都标记为慢查询。
func parseSlowQueryThreshold(s string) time.Duration {
	if s == "" {
		return defaultSlowQueryThreshold
	}
	ms, err := strconv.Atoi(s)
	if err != nil || ms < 0 {
		return defaultSlowQueryThreshold
	}
	return time.Duration(ms) * time.Millisecond
}

// loggingDBTX 包装 sqlc.DBTX，在每次 Exec/Query 后记录 SQL 语句文本、耗时、写操作影响行数与错误。
// 设计意图：把 SQL 可观测性下沉到接口层，业务代码无感；trace_id 经 ctx 串联到每条 SQL 日志。
// 边界约束：不记录参数值（args），因为参数可能含密码 hash / token / PII，脱敏 writer 只是兜底。
// log_type 固定为 sql，便于按类型过滤。
type loggingDBTX struct {
	// inner 是被包装的真实执行体（*sql.DB 或 *sql.Tx，均满足 sqlc.DBTX）。
	inner sqlc.DBTX
	// threshold 为慢查询阈值；耗时超过它的查询抬到 Warn 级别。
	threshold time.Duration
}

// newLoggingDBTX 构造 SQL 日志包装器。
// inner 为被包装的执行体，threshold 为慢查询阈值。返回 sqlc.DBTX 接口以便直接喂给 sqlc.New。
func newLoggingDBTX(inner sqlc.DBTX, threshold time.Duration) sqlc.DBTX {
	return &loggingDBTX{inner: inner, threshold: threshold}
}

// logQuery 按耗时 / 错误分级记录一条 SQL 日志。
// rows<0 表示不记行数（查询类无零成本行数，避免消费业务方 *sql.Rows）。
// 级别规则：执行出错 Error；否则耗时超阈值为慢查询 Warn；正常 Debug（生产默认不输出）。
func (l *loggingDBTX) logQuery(ctx context.Context, query string, start time.Time, rows int64, err error) {
	latency := time.Since(start)
	attrs := []slog.Attr{
		slog.String(mlog.KeyLogType, mlog.LogTypeSQL),
		slog.String(mlog.KeySQL, query), // sqlc 占位符已参数化，语句文本不含真实参数值
		slog.Int64(mlog.KeyLatencyMS, latency.Milliseconds()),
	}
	if rows >= 0 {
		attrs = append(attrs, slog.Int64(mlog.KeyRows, rows))
	}
	level := slog.LevelDebug
	switch {
	case err != nil:
		// 执行失败需排查（死锁 / 约束冲突 / 连接断），抬到 Error 并带错误信息。
		attrs = append(attrs, mlog.Err(err))
		level = slog.LevelError
	case latency > l.threshold:
		// 慢查询不算失败但值得关注，抬到 Warn。
		level = slog.LevelWarn
	}
	slog.LogAttrs(ctx, level, "sql_query", attrs...)
}

// ExecContext 执行写操作并记录日志。写操作可零成本拿到影响行数，故记录 rows。
func (l *loggingDBTX) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	start := time.Now()
	res, err := l.inner.ExecContext(ctx, query, args...)
	var rows int64 = -1
	if err == nil && res != nil {
		// RowsAffected 取行数；驱动若不支持则忽略（rows 保持 -1 即不记录）。
		if n, e := res.RowsAffected(); e == nil {
			rows = n
		}
	}
	l.logQuery(ctx, query, start, rows, err)
	return res, err // 错误与结果原样透传，包装层不改变语义
}

// QueryContext 执行查询并记录日志。
// 不数行数：消费 *sql.Rows 会破坏业务方的游标，故传 rows=-1 不记录行数。
func (l *loggingDBTX) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	start := time.Now()
	rows, err := l.inner.QueryContext(ctx, query, args...)
	l.logQuery(ctx, query, start, -1, err)
	return rows, err
}

// QueryRowContext 执行单行查询并记录日志。
// 边界：QueryRow 的 error 延迟到调用方 Scan 时才暴露，包装层此处无法取得，故按正常路径（rows 不适用、无 err）记录。
func (l *loggingDBTX) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	start := time.Now()
	row := l.inner.QueryRowContext(ctx, query, args...)
	l.logQuery(ctx, query, start, -1, nil)
	return row
}

// PrepareContext 透传：预编译语句本身不产生查询日志，真正执行由返回的 *sql.Stmt 触发（不经本包装），故不在此记录。
func (l *loggingDBTX) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return l.inner.PrepareContext(ctx, query)
}
