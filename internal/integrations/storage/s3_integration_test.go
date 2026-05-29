package storage_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/integrations/storage"
)

// minioCfgFromEnv 从环境变量读 MinIO 接入参数；缺失则跳过整组集成测。
// 需设置：OC_S3_TEST_ENDPOINT / OC_S3_TEST_BUCKET / OC_S3_TEST_AK / OC_S3_TEST_SK
// 可选：OC_S3_TEST_STS_ROLE（默认 arn:aws:iam:::role/dev）
func minioCfgFromEnv(t *testing.T) storage.S3Config {
	t.Helper()
	ep := os.Getenv("OC_S3_TEST_ENDPOINT")
	if ep == "" {
		t.Skip("未设置 OC_S3_TEST_ENDPOINT，跳过 MinIO 集成测")
	}
	role := os.Getenv("OC_S3_TEST_STS_ROLE")
	if role == "" {
		role = "arn:aws:iam:::role/dev"
	}
	return storage.S3Config{
		Endpoint:        ep,
		Region:          "us-east-1",
		Bucket:          os.Getenv("OC_S3_TEST_BUCKET"),
		AccessKeyID:     os.Getenv("OC_S3_TEST_AK"),
		SecretAccessKey: os.Getenv("OC_S3_TEST_SK"),
		UsePathStyle:    true,
		STSRoleARN:      role,
	}
}

// TestS3RoundTrip 验证上传对象后预签名 GET 能下载到一致内容（真实 MinIO，标准 S3 协议）。
func TestS3RoundTrip(t *testing.T) {
	cfg := minioCfgFromEnv(t)
	store := storage.NewS3ObjectStore(cfg)
	ctx := context.Background()

	// 固定 appPrefix：key 与清理都基于同一时间戳，避免两次 time.Now() 导致前缀不一致清理失败
	appPrefix := fmt.Sprintf("apps/it-%d/", time.Now().UnixNano())
	key := appPrefix + "probe.txt"
	payload := []byte("hello-s3-roundtrip")

	// 注册清理钩子：即使后续断言失败也能删除测试对象，防止垃圾数据残留
	t.Cleanup(func() {
		// 用 manager 凭证的 store 清理整个测试前缀，断言失败也执行
		_ = store.DeletePrefix(context.Background(), appPrefix)
	})

	// 上传 → 存在性为真
	require.NoError(t, store.PutObject(ctx, key, bytes.NewReader(payload), int64(len(payload))))
	exists, err := store.ObjectExists(ctx, key)
	require.NoError(t, err)
	assert.True(t, exists)

	// 预签名 GET 下载内容一致
	url, err := store.PresignGet(ctx, key, 1*time.Minute)
	require.NoError(t, err)
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	// 预签名签名问题会返回 403+XML，先断言状态码再读 body，避免误导性断言失败信息
	require.Equal(t, http.StatusOK, resp.StatusCode)
	got, _ := io.ReadAll(resp.Body)
	assert.Equal(t, payload, got)
}

// TestSTSPrefixIsolation 验证 STS 临时凭证只能写自己 app 前缀，越权写其它 app 被拒。
func TestSTSPrefixIsolation(t *testing.T) {
	cfg := minioCfgFromEnv(t)
	issuer := storage.NewSTSCredentialIssuer(cfg)
	ctx := context.Background()

	prefix := fmt.Sprintf("apps/it-sts-%d/", time.Now().UnixNano())

	// 注册清理钩子：用 manager 凭证的 store 清理测试写入的对象，防止垃圾数据残留
	cleanup := storage.NewS3ObjectStore(cfg)
	t.Cleanup(func() { _ = cleanup.DeletePrefix(context.Background(), prefix) })

	creds, err := issuer.AssumeAppRole(ctx, prefix, 15*time.Minute)
	require.NoError(t, err)
	require.NotEmpty(t, creds.SessionToken) // 标准临时凭证必带 session token

	// 用临时凭证构造受限 S3 客户端
	scoped := s3.New(s3.Options{
		BaseEndpoint: aws.String(cfg.Endpoint),
		Region:       cfg.Region,
		UsePathStyle: true,
		Credentials: credentials.NewStaticCredentialsProvider(
			creds.AccessKeyID, creds.SecretAccessKey, creds.SessionToken),
	})

	// 写自己前缀：允许
	_, err = scoped.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(cfg.Bucket),
		Key:    aws.String(prefix + "ok.txt"),
		Body:   bytes.NewReader([]byte("ok")),
	})
	assert.NoError(t, err, "写自身 app 前缀应被允许")

	// 写其它 app 前缀：应被拒（AccessDenied）
	_, err = scoped.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(cfg.Bucket),
		Key:    aws.String("apps/other-victim/evil.txt"),
		Body:   bytes.NewReader([]byte("evil")),
	})
	assert.Error(t, err, "越权写其它 app 前缀必须被拒绝")
}
