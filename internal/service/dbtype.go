// Package service 的 dbtype.go 集中 service 层与 sqlc(MySQL) 生成类型之间的转换 helper。
// UUID 列在 DB 是 CHAR(36)、Go 侧是 string（应用层用 uuid.NewString() 生成）；
// 可空列由 sqlc 映射为 guregu/null 类型，这里提供与原 pgtype helper 等价的读写转换。
package service

import (
	"time"

	"github.com/guregu/null/v5"
)

// strOrEmpty 把可空字符串列读成 API 友好的普通 string（NULL → ""）。
func strOrEmpty(v null.String) string {
	if !v.Valid {
		return ""
	}
	return v.String
}

// nullStr 把普通 string 转成可空列写入值；空串视为 NULL。
// 适用于"空即未设置"的列；若空串本身有业务含义需写入，请用 nullStrFromPtr。
func nullStr(s string) null.String {
	if s == "" {
		return null.String{}
	}
	return null.StringFrom(s)
}

// nullStrFromPtr 在"空串也要写入"的场景下，用指针区分 NULL（nil）与空串（非 nil）。
func nullStrFromPtr(s *string) null.String {
	if s == nil {
		return null.String{}
	}
	return null.StringFrom(*s)
}

// nullTime 把 time.Time 转成可空时间列写入值；零值时间视为 NULL。
func nullTime(t time.Time) null.Time {
	if t.IsZero() {
		return null.Time{}
	}
	return null.TimeFrom(t)
}

// timeOrZero 把可空时间列读成普通 time.Time（NULL → 零值）。
func timeOrZero(v null.Time) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return v.Time
}

// nullInt 把 int64 转成可空整数列写入值（总是有效；如需 NULL 直接用 null.Int{}）。
func nullInt(i int64) null.Int { return null.IntFrom(i) }

// intOrZero 把可空整数列读成普通 int64（NULL → 0）。
func intOrZero(v null.Int) int64 {
	if !v.Valid {
		return 0
	}
	return v.Int64
}
