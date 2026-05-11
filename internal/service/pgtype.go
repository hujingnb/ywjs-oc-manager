// Package service 的 pgtype.go 集中 service 层与 pgx/pgtype 之间的 UUID 转换 helper。
package service

import (
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
)

// uuidToString 将有效 pgtype.UUID 转成标准 UUID 字符串。
// 调用方必须保证 value.Valid=true；无效值请使用 uuidToOptionalString，避免把零值误当真实 ID。
func uuidToString(value pgtype.UUID) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", value.Bytes[0:4], value.Bytes[4:6], value.Bytes[6:8], value.Bytes[8:10], value.Bytes[10:16])
}

// uuidToOptionalString 将可空 UUID 转成 API 层可接受的字符串。
// 数据库 NULL 会映射为空串，便于响应结构用 omitempty 或前端空值语义处理。
func uuidToOptionalString(value pgtype.UUID) string {
	if !value.Valid {
		return ""
	}
	return uuidToString(value)
}

// parseUUID 使用 pgtype 的扫描逻辑解析外部传入的 UUID 字符串。
// service 层统一通过该函数把 handler 参数转成 sqlc 查询所需类型。
func parseUUID(value string) (pgtype.UUID, error) {
	var result pgtype.UUID
	err := result.Scan(value)
	return result, err
}
