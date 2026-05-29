package storage

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildPrefixPolicy 验证内联 policy 把资源限定到 app prefix（标准 S3 ARN 语法）。
func TestBuildPrefixPolicy(t *testing.T) {
	issuer := &STSCredentialIssuer{bucket: "oc-apps"}
	// appPrefix 形如 apps/<id>/，policy 资源应通配该前缀下所有对象
	raw, err := issuer.buildPrefixPolicy("apps/a1/")
	require.NoError(t, err)

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &doc))
	// 版本号固定为 IAM 标准日期
	assert.Equal(t, "2012-10-17", doc["Version"])
	// 序列化结果必须包含限定到 apps/a1/* 的对象资源 ARN
	assert.Contains(t, raw, "arn:aws:s3:::oc-apps/apps/a1/*")
	// ListBucket 受 s3:prefix 条件约束，防止越权列举其它 app
	assert.Contains(t, raw, "apps/a1/*")
}
