package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubDBTX 是受控的 sqlc.DBTX 实现，用于驱动 loggingDBTX 的各个分支：
// 通过 execResult/execErr 控制返回值，通过 delay 制造慢查询。
type stubDBTX struct {
	execResult sql.Result    // ExecContext 返回的结果（含影响行数）
	execErr    error         // Exec/Query 返回的错误
	delay      time.Duration // 模拟查询耗时，用于触发慢查询分支
}

func (s stubDBTX) ExecContext(ctx context.Context, q string, args ...interface{}) (sql.Result, error) {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	return s.execResult, s.execErr
}
func (s stubDBTX) PrepareContext(ctx context.Context, q string) (*sql.Stmt, error) { return nil, nil }
func (s stubDBTX) QueryContext(ctx context.Context, q string, args ...interface{}) (*sql.Rows, error) {
	return nil, s.execErr
}
func (s stubDBTX) QueryRowContext(ctx context.Context, q string, args ...interface{}) *sql.Row {
	return nil
}

// fakeResult 返回固定 RowsAffected，用于断言写操作记录的行数。
type fakeResult struct{ rows int64 }

func (f fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (f fakeResult) RowsAffected() (int64, error) { return f.rows, nil }

// parseLast 取捕获缓冲区最后一行 JSON 日志并反序列化为 map，便于按字段断言。
func parseLast(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	require.NotEmpty(t, lines)
	var m map[string]any
	require.NoError(t, json.Unmarshal(lines[len(lines)-1], &m))
	return m
}

// TestLoggingDBTX_正常Exec记debug带行数 覆盖正常写操作路径：
// 记 Debug 级别、带 rows 行数、log_type=sql，且日志中不出现参数值（PII 防护边界）。
func TestLoggingDBTX_正常Exec记debug带行数(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)

	w := newLoggingDBTX(stubDBTX{execResult: fakeResult{rows: 3}}, 200*time.Millisecond)
	_, err := w.ExecContext(context.Background(), "UPDATE users SET name=? WHERE id=?", "secret-name", "u1")
	require.NoError(t, err)

	m := parseLast(t, &buf)
	assert.Equal(t, "DEBUG", m["level"])            // 正常路径为 Debug
	assert.Equal(t, "sql", m["log_type"])           // log_type 固定 sql
	assert.Equal(t, float64(3), m["rows"])          // 写操作记录影响行数
	assert.NotContains(t, buf.String(), "secret-name") // 不记参数值，避免 PII 入日志
}

// TestLoggingDBTX_慢查询记warn 覆盖慢查询边界：
// 实际耗时（20ms）超过阈值（5ms）时，级别从 Debug 抬到 Warn。
func TestLoggingDBTX_慢查询记warn(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)

	w := newLoggingDBTX(stubDBTX{execResult: fakeResult{rows: 1}, delay: 20 * time.Millisecond}, 5*time.Millisecond)
	_, _ = w.ExecContext(context.Background(), "UPDATE t SET x=1", nil)

	m := parseLast(t, &buf)
	assert.Equal(t, "WARN", m["level"]) // 超阈值即慢查询
}

// TestLoggingDBTX_执行错误记error 覆盖执行失败路径：
// 底层返回 error 时记 Error 级别并带 error 字段，且错误原样透传给调用方。
func TestLoggingDBTX_执行错误记error(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)

	w := newLoggingDBTX(stubDBTX{execErr: errors.New("deadlock")}, 200*time.Millisecond)
	_, err := w.ExecContext(context.Background(), "UPDATE t SET x=1", nil)
	require.Error(t, err) // 错误必须原样透传

	m := parseLast(t, &buf)
	assert.Equal(t, "ERROR", m["level"])    // 执行错误为 Error
	assert.Equal(t, "deadlock", m["error"]) // 带 error 字段
}

// TestLoggingDBTX_Query不带行数 覆盖查询类路径：
// QueryContext 不消费 *sql.Rows、不统计行数，故日志中不应出现 rows 字段。
func TestLoggingDBTX_Query不带行数(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)

	w := newLoggingDBTX(stubDBTX{}, 200*time.Millisecond)
	_, _ = w.QueryContext(context.Background(), "SELECT 1", nil)

	m := parseLast(t, &buf)
	_, hasRows := m["rows"]
	assert.False(t, hasRows) // 查询类不记 rows
}
