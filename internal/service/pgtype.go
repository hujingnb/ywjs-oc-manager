package service

import (
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
)

func uuidToString(value pgtype.UUID) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", value.Bytes[0:4], value.Bytes[4:6], value.Bytes[6:8], value.Bytes[8:10], value.Bytes[10:16])
}

func uuidToOptionalString(value pgtype.UUID) string {
	if !value.Valid {
		return ""
	}
	return uuidToString(value)
}

func parseUUID(value string) (pgtype.UUID, error) {
	var result pgtype.UUID
	err := result.Scan(value)
	return result, err
}
