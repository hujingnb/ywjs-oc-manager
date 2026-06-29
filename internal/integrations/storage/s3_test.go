package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestErrObjectNotFoundExists 覆盖：哨兵错误存在且语义稳定，site-server 据此把缺失对象映射为 404。
func TestErrObjectNotFoundExists(t *testing.T) {
	assert.EqualError(t, ErrObjectNotFound, "storage: 对象不存在")
}
