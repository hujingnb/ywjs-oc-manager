package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// STSCredentialIssuer 用标准 STS AssumeRole 签发限定到 app prefix 的临时写凭证。
// 依赖标准 STS 协议（MinIO 与 AWS/云 OSS 均兼容 AssumeRole + 内联 policy）。
type STSCredentialIssuer struct {
	client  *sts.Client
	bucket  string
	roleARN string
}

// NewSTSCredentialIssuer 用 manager 长期凭证构造 STS 客户端（endpoint 同 S3 端点）。
func NewSTSCredentialIssuer(cfg S3Config) *STSCredentialIssuer {
	client := sts.New(sts.Options{
		BaseEndpoint: aws.String(cfg.Endpoint),
		Region:       cfg.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID, cfg.SecretAccessKey, ""),
	})
	return &STSCredentialIssuer{client: client, bucket: cfg.Bucket, roleARN: cfg.STSRoleARN}
}

// 编译时断言：STSCredentialIssuer 实现 STSIssuer。
var _ STSIssuer = (*STSCredentialIssuer)(nil)

// buildPrefixPolicy 生成限定到 appPrefix 的内联 IAM policy（标准 S3 资源 ARN 语法）。
// 允许：对 bucket/appPrefix* 的对象读写；列举 bucket 但限定 prefix 条件。
func (i *STSCredentialIssuer) buildPrefixPolicy(appPrefix string) (string, error) {
	policy := map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{
				"Effect": "Allow",
				"Action": []string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject"},
				"Resource": []string{
					fmt.Sprintf("arn:aws:s3:::%s/%s*", i.bucket, appPrefix),
				},
			},
			{
				"Effect":   "Allow",
				"Action":   []string{"s3:ListBucket"},
				"Resource": []string{fmt.Sprintf("arn:aws:s3:::%s", i.bucket)},
				"Condition": map[string]any{
					"StringLike": map[string]any{"s3:prefix": []string{appPrefix + "*"}},
				},
			},
		},
	}
	b, err := json.Marshal(policy)
	if err != nil {
		return "", fmt.Errorf("storage: 序列化 STS policy 失败: %w", err)
	}
	return string(b), nil
}

// AssumeAppRole 调标准 STS AssumeRole，内联 policy 限定到 appPrefix，签发 ttl 时长的临时凭证。
func (i *STSCredentialIssuer) AssumeAppRole(ctx context.Context, appPrefix string, ttl time.Duration) (TempCredentials, error) {
	policy, err := i.buildPrefixPolicy(appPrefix)
	if err != nil {
		return TempCredentials{}, err
	}
	out, err := i.client.AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         aws.String(i.roleARN),
		RoleSessionName: aws.String("ocapp"),
		Policy:          aws.String(policy),
		DurationSeconds: aws.Int32(int32(ttl.Seconds())),
	})
	if err != nil {
		return TempCredentials{}, fmt.Errorf("storage: AssumeRole 失败: %w", err)
	}
	if out.Credentials == nil {
		return TempCredentials{}, fmt.Errorf("storage: AssumeRole 返回空凭证")
	}
	c := out.Credentials
	return TempCredentials{
		AccessKeyID:     aws.ToString(c.AccessKeyId),
		SecretAccessKey: aws.ToString(c.SecretAccessKey),
		SessionToken:    aws.ToString(c.SessionToken),
		ExpiresAt:       aws.ToTime(c.Expiration),
	}, nil
}
