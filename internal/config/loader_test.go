// Package config 的测试覆盖 YAML 解析、必需项校验和安全密钥格式。
package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validBase64MasterKey 提供测试用的 32 字节 base64 master_key。
const validBase64MasterKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

// fullValidYAML 返回一份带新字段的合法配置文本，便于多个用例共用。
// 任何 security/hermes 校验路径都应基于此文本派生最小修改。
func fullValidYAML() string {
	return `
app:
  env: dev
  http_addr: ":8080"
  public_base_url: "http://localhost:8080"
  data_root: "./data/manager"
database:
  url: "postgres://ocm:ocm@manager-postgres:5432/ocm?sslmode=disable"
redis:
  addr: "redis:6379"
  password: ""
  db: 0
  key_prefix: "ocm:"
auth:
  cookie_domain: "localhost"
  access_token_ttl: "15m"
  refresh_token_ttl: "720h"
  jwt_access_secret: "access-secret"
  jwt_refresh_secret: "refresh-secret"
  csrf_secret: "csrf-secret"
security:
  master_key: "` + validBase64MasterKey + `"
hermes:
  workspace:
    archive_retention_days: 14
aicc:
  runtime_image: "registry.example.com/app/oc-manager-aigowork-aicc:v1.0.0-test"
`
}

// TestLoad_DoesNotExpandEnvPlaceholders 验证加载Does未ExpandEnvPlaceholders的预期行为场景。
func TestLoad_DoesNotExpandEnvPlaceholders(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://should-not-be-substituted/db")

	yaml := strings.Replace(fullValidYAML(),
		`url: "postgres://ocm:ocm@manager-postgres:5432/ocm?sslmode=disable"`,
		`url: "${DATABASE_URL}"`, 1)
	path := writeTempConfig(t, yaml)

	cfg, err := LoadFile(path)
	require.NoError(t, err)
	require.Equal(t, "${DATABASE_URL}", cfg.Database.URL)
	require.Equal(t, "15m0s", cfg.Auth.AccessTokenTTL.Duration.String())
	require.Equal(t, validBase64MasterKey, cfg.Security.MasterKey)
	require.Equal(t, 14, cfg.Hermes.Workspace.ArchiveRetentionDays)
}

// TestLoad_RejectsUnknownFields 校验 yaml 字段拼写错误会 fail-fast，避免可选配置因 typo 被静默忽略。
func TestLoad_RejectsUnknownFields(t *testing.T) {
	yaml := strings.Replace(fullValidYAML(),
		`key_prefix: "ocm:"`,
		`key_prefx: "ocm:"`, 1)
	path := writeTempConfig(t, yaml)
	_, err := LoadFile(path)
	require.Error(t, err)
}

// TestValidateReportsRequiredFields 验证校验配置必填项必填字段的预期行为场景。
func TestValidateReportsRequiredFields(t *testing.T) {
	err := (Config{}).Validate()
	require.Error(t, err)
	required := []string{
		"app.http_addr", "app.data_root", "database.url", "redis.addr",
		"auth.access_token_ttl", "auth.refresh_token_ttl",
		"auth.jwt_access_secret", "auth.jwt_refresh_secret", "auth.csrf_secret",
		"security.master_key",
	}
	for _, field := range required {
		require.True(t, strings.Contains(err.Error(), field))
	}
}

// TestLoadFileFailsWhenDurationInvalid 验证加载文件失败当时长非法的异常或拒绝路径场景。
func TestLoadFileFailsWhenDurationInvalid(t *testing.T) {
	path := writeTempConfig(t, `
app:
  http_addr: ":8080"
  data_root: "./data/manager"
database:
  url: "postgres://ocm:ocm@manager-postgres:5432/ocm?sslmode=disable"
redis:
  addr: "redis:6379"
auth:
  access_token_ttl: "not-a-duration"
  refresh_token_ttl: "720h"
  jwt_access_secret: "access-secret"
  jwt_refresh_secret: "refresh-secret"
  csrf_secret: "csrf-secret"
security:
  master_key: "`+validBase64MasterKey+`"
`)

	_, err := LoadFile(path)
	require.Error(t, err)
}

// TestLoad_RejectsMissingMasterKey 校验 security.master_key 缺失时启动 fail-fast。
func TestLoad_RejectsMissingMasterKey(t *testing.T) {
	yaml := strings.Replace(fullValidYAML(),
		"security:\n  master_key: \""+validBase64MasterKey+"\"",
		"security:\n  master_key: \"\"", 1)
	path := writeTempConfig(t, yaml)
	_, err := LoadFile(path)
	if err == nil || !strings.Contains(err.Error(), "security.master_key") {
		t.Fatalf("LoadFile() err = %v, want security.master_key 错误", err)
	}
}

// TestLoad_RejectsShortMasterKey 校验非 32 字节解码后的 master_key 被拒绝。
func TestLoad_RejectsShortMasterKey(t *testing.T) {
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	yaml := strings.Replace(fullValidYAML(), validBase64MasterKey, short, 1)
	path := writeTempConfig(t, yaml)
	_, err := LoadFile(path)
	if err == nil || !strings.Contains(err.Error(), "32 字节") {
		t.Fatalf("LoadFile() err = %v, want 长度错误", err)
	}
}

// TestLoad_RejectsBadBase64MasterKey 校验非法 base64 的 master_key 被拒绝。
func TestLoad_RejectsBadBase64MasterKey(t *testing.T) {
	yaml := strings.Replace(fullValidYAML(), validBase64MasterKey, "!!!not-base64!!!", 1)
	path := writeTempConfig(t, yaml)
	_, err := LoadFile(path)
	if err == nil || !strings.Contains(err.Error(), "base64") {
		t.Fatalf("LoadFile() err = %v, want base64 错误", err)
	}
}

// TestLoad_AcceptsValidConfig 校验完整合法配置可被加载。
func TestLoad_AcceptsValidConfig(t *testing.T) {
	path := writeTempConfig(t, fullValidYAML())
	cfg, err := LoadFile(path)
	require.NoError(t, err)
	require.Equal(t, validBase64MasterKey, cfg.Security.MasterKey)
}

// TestLoad_RejectsMissingAICCRuntimeImage 验证客服运行时镜像缺失会在加载阶段失败，
// 防止服务启动后创建 AICC 隐藏应用时才发现运行时没有独立镜像。
func TestLoad_RejectsMissingAICCRuntimeImage(t *testing.T) {
	yaml := strings.Replace(fullValidYAML(), "aicc:\n  runtime_image: \"registry.example.com/app/oc-manager-aigowork-aicc:v1.0.0-test\"\n", "", 1)
	_, err := loadConfigFromStringErr(t, yaml)
	require.Error(t, err)
	assert.ErrorContains(t, err, "aicc.runtime_image")
}

// TestLoad_AllowsMissingRAGFlowConfig 验证本地未启用 RAGFlow 时 manager 仍可启动，知识库请求由 service 层返回未配置。
func TestLoad_AllowsMissingRAGFlowConfig(t *testing.T) {
	cfg := loadConfigFromString(t, fullValidYAML())
	assert.Empty(t, cfg.RAGFlow.BaseURL)
	assert.Empty(t, cfg.RAGFlow.APIKey)
}

// TestLoad_RejectsInvalidRAGFlowBaseURL 验证 RAGFlow 地址配置错误时启动阶段直接失败。
func TestLoad_RejectsInvalidRAGFlowBaseURL(t *testing.T) {
	yaml := fullValidYAML() + "\nragflow:\n  base_url: \"://bad\"\n  api_key: \"secret\"\n"
	_, err := loadConfigFromStringErr(t, yaml)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ragflow.base_url")
}

// TestLoad_RejectsUnsupportedRAGFlowBaseURLScheme 验证 RAGFlow 仅允许 http/https 地址，避免误把 ftp 等非 HTTP 协议交给运行期客户端。
func TestLoad_RejectsUnsupportedRAGFlowBaseURLScheme(t *testing.T) {
	yaml := fullValidYAML() + "\nragflow:\n  base_url: \"ftp://ragflow:9380\"\n  api_key: \"secret\"\n"
	_, err := loadConfigFromStringErr(t, yaml)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ragflow.base_url")
}

// TestLoad_RejectsIncompleteRAGFlowConfig 验证 RAGFlow 地址和 API key 必须成对配置。
func TestLoad_RejectsIncompleteRAGFlowConfig(t *testing.T) {
	for name, yaml := range map[string]string{
		// 只配置 base_url 会让运行期鉴权失败，启动阶段应直接拒绝。
		"missing_api_key": fullValidYAML() + "\nragflow:\n  base_url: \"http://ragflow:9380\"\n",
		// 只配置 api_key 无法确定上游地址，启动阶段应直接拒绝。
		"missing_base_url": fullValidYAML() + "\nragflow:\n  api_key: \"secret\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadConfigFromStringErr(t, yaml)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "ragflow.")
		})
	}
}

// TestLoad_DefaultsHermesRuntimeBaseURL 验证 Hermes 容器访问 manager 的内部地址有稳定默认值。
func TestLoad_DefaultsHermesRuntimeBaseURL(t *testing.T) {
	cfg := loadConfigFromString(t, fullValidYAML())
	assert.Equal(t, "http://manager-api:8080", cfg.Hermes.ManagerRuntimeBaseURL)
}

// TestLoad_DefaultsLogging 验证未配置 logging 段时回填与历史默认一致的值（info/json/200ms），
// 保证旧部署在引入 logging 段后行为不变。
func TestLoad_DefaultsLogging(t *testing.T) {
	cfg := loadConfigFromString(t, fullValidYAML())
	assert.Equal(t, "info", cfg.Logging.Level)    // 默认级别 info
	assert.Equal(t, "json", cfg.Logging.Format)   // 默认格式 json
	assert.Equal(t, 200, cfg.Logging.SlowQueryMS) // 默认慢查询阈值 200ms
}

// TestLoad_LoggingExplicitValues 验证显式配置的 logging 字段被原样保留，不被默认覆盖。
func TestLoad_LoggingExplicitValues(t *testing.T) {
	cfg := loadConfigFromString(t, fullValidYAML()+`
logging:
  level: "debug"
  format: "text"
  slow_query_ms: 50
`)
	assert.Equal(t, "debug", cfg.Logging.Level)  // 显式 debug 保留
	assert.Equal(t, "text", cfg.Logging.Format)  // 显式 text 保留
	assert.Equal(t, 50, cfg.Logging.SlowQueryMS) // 显式阈值 50ms 保留
}

// TestLoad_DefaultsRAGFlowOptions 验证 RAGFlow 启用后未显式配置的请求选项会使用保守默认值。
func TestLoad_DefaultsRAGFlowOptions(t *testing.T) {
	cfg := loadConfigFromString(t, fullValidYAML()+`
ragflow:
  base_url: "http://ragflow:9380"
  api_key: "secret"
`)
	assert.Equal(t, "30s", cfg.RAGFlow.RequestTimeout.Duration.String())
	assert.Equal(t, "naive", cfg.RAGFlow.ChunkMethod)
	// self_heal 整段缺省时,loader 应填入内置默认:间隔 10m、卡死阈值 30m、上限 3 次、每轮 100。
	assert.Equal(t, "10m0s", cfg.RAGFlow.SelfHeal.Interval.Duration.String())
	assert.Equal(t, "30m0s", cfg.RAGFlow.SelfHeal.StuckThreshold.Duration.String())
	assert.Equal(t, 3, cfg.RAGFlow.SelfHeal.MaxAttempts)
	assert.Equal(t, 100, cfg.RAGFlow.SelfHeal.BatchLimit)
}

// TestLoad_RAGFlowEmbeddingModelsAcceptHumanNames 验证 embedding 模型配置只需要填写 RAGFlow 控制台可见的人类模型名。
func TestLoad_RAGFlowEmbeddingModelsAcceptHumanNames(t *testing.T) {
	yaml := fullValidYAML() + `
ragflow:
  base_url: "http://ragflow:9380"
  api_key: "secret"
  default_embedding_model: "BAAI/bge-m3"
  embedding_models:
    - name: "BAAI/bge-m3"
      label: "BAAI/bge-m3"
      provider: "OpenAI-API-Compatible"
    - name: "netease-youdao/bce-embedding-base_v1"
      provider: "OpenAI-API-Compatible"
`
	cfg := loadConfigFromString(t, yaml)
	require.Len(t, cfg.RAGFlow.EmbeddingModels, 2)
	assert.Equal(t, "BAAI/bge-m3", cfg.RAGFlow.DefaultEmbeddingModel)
	assert.Equal(t, "BAAI/bge-m3", cfg.RAGFlow.EmbeddingModels[0].Name)
	assert.Equal(t, "BAAI/bge-m3", cfg.RAGFlow.EmbeddingModels[0].Label)
	assert.Equal(t, "OpenAI-API-Compatible", cfg.RAGFlow.EmbeddingModels[0].Provider)
}

// TestLoad_RAGFlowEmbeddingModelRejectsBlankName 验证启用 RAGFlow 后兜底模型名不能为空，避免创建 dataset 时提交空模型。
func TestLoad_RAGFlowEmbeddingModelRejectsBlankName(t *testing.T) {
	yaml := fullValidYAML() + `
ragflow:
  base_url: "http://ragflow:9380"
  api_key: "secret"
  default_embedding_model: "BAAI/bge-m3"
  embedding_models:
    - name: "   "
      provider: "OpenAI-API-Compatible"
`
	_, err := loadConfigFromStringErr(t, yaml)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ragflow.embedding_models[0].name")
}

// TestLoad_RAGFlowDefaultEmbeddingModelMustExistInFallbackList 验证默认模型必须出现在兜底列表中，避免 RAGFlow 模型列表不可用时无法解析默认值。
func TestLoad_RAGFlowDefaultEmbeddingModelMustExistInFallbackList(t *testing.T) {
	yaml := fullValidYAML() + `
ragflow:
  base_url: "http://ragflow:9380"
  api_key: "secret"
  default_embedding_model: "missing-model"
  embedding_models:
    - name: "BAAI/bge-m3"
      provider: "OpenAI-API-Compatible"
`
	_, err := loadConfigFromStringErr(t, yaml)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ragflow.default_embedding_model")
}

// TestLoad_RAGFlowEmbeddingModelsRejectDuplicateNameProvider 验证兜底模型按模型名和 provider 去重，避免同一远端模型被重复展示或解析。
func TestLoad_RAGFlowEmbeddingModelsRejectDuplicateNameProvider(t *testing.T) {
	yaml := fullValidYAML() + `
ragflow:
  base_url: "http://ragflow:9380"
  api_key: "secret"
  default_embedding_model: "BAAI/bge-m3"
  embedding_models:
    - name: " BAAI/bge-m3 "
      provider: " OpenAI-API-Compatible "
    - name: "BAAI/bge-m3"
      provider: "OpenAI-API-Compatible"
`
	_, err := loadConfigFromStringErr(t, yaml)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ragflow.embedding_models[1]")
}

// TestLoad_RAGFlowEmbeddingModelsAllowSameNameDifferentProviderWithoutDefault 验证同名不同 provider 的兜底模型在未指定默认模型时允许共存。
func TestLoad_RAGFlowEmbeddingModelsAllowSameNameDifferentProviderWithoutDefault(t *testing.T) {
	yaml := fullValidYAML() + `
ragflow:
  base_url: "http://ragflow:9380"
  api_key: "secret"
  embedding_models:
    - name: "BAAI/bge-m3"
      provider: "OpenAI-API-Compatible"
    - name: "BAAI/bge-m3"
      provider: "Local-Provider"
`
	cfg := loadConfigFromString(t, yaml)
	require.Len(t, cfg.RAGFlow.EmbeddingModels, 2)
	assert.Empty(t, cfg.RAGFlow.DefaultEmbeddingModel)
	assert.Equal(t, "OpenAI-API-Compatible", cfg.RAGFlow.EmbeddingModels[0].Provider)
	assert.Equal(t, "Local-Provider", cfg.RAGFlow.EmbeddingModels[1].Provider)
}

// TestLoad_RAGFlowDefaultEmbeddingModelRejectsAmbiguousName 验证默认模型名命中多个 provider 时必须拒绝，避免只按人类模型名无法唯一解析。
func TestLoad_RAGFlowDefaultEmbeddingModelRejectsAmbiguousName(t *testing.T) {
	yaml := fullValidYAML() + `
ragflow:
  base_url: "http://ragflow:9380"
  api_key: "secret"
  default_embedding_model: "BAAI/bge-m3"
  embedding_models:
    - name: "BAAI/bge-m3"
      provider: "OpenAI-API-Compatible"
    - name: " BAAI/bge-m3 "
      provider: "Local-Provider"
`
	_, err := loadConfigFromStringErr(t, yaml)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ragflow.default_embedding_model")
}

// TestIndustryKnowledgeUploadTokenAllowsEmptyConfig 验证外部行业库上传 token 可为空，空配置表示禁用外部上传接口。
func TestIndustryKnowledgeUploadTokenAllowsEmptyConfig(t *testing.T) {
	yaml := fullValidYAML() + `
industry_knowledge:
  upload_token: ""
`
	cfg, err := LoadFile(writeTempConfig(t, yaml))

	require.NoError(t, err)
	assert.Empty(t, cfg.IndustryKnowledge.UploadToken)
}

// TestIndustryKnowledgeUploadTokenRejectsWhitespace 验证只包含空白字符的固定鉴权字符串会被拒绝，避免启动后所有请求都无法通过。
func TestIndustryKnowledgeUploadTokenRejectsWhitespace(t *testing.T) {
	yaml := fullValidYAML() + `
industry_knowledge:
  upload_token: "   "
`
	_, err := LoadFile(writeTempConfig(t, yaml))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "industry_knowledge.upload_token")
}

// TestTransferLimitDefaultsToUnlimited 验证未配置 transfer_limit 时保持历史行为，不启用上传或下载限速。
func TestTransferLimitDefaultsToUnlimited(t *testing.T) {
	cfg := loadConfigFromString(t, fullValidYAML())

	assert.Equal(t, int64(0), cfg.TransferLimit.UploadBytesPerSec)
	assert.Equal(t, int64(0), cfg.TransferLimit.DownloadBytesPerSec)
}

// TestTransferLimitLoadsExplicitBytesPerSecond 验证配置的字节每秒速率会原样加载，供 handler 层执行单请求限速。
func TestTransferLimitLoadsExplicitBytesPerSecond(t *testing.T) {
	yaml := fullValidYAML() + `
transfer_limit:
  upload_bytes_per_sec: 524288
  download_bytes_per_sec: 524288
`
	cfg := loadConfigFromString(t, yaml)

	assert.Equal(t, int64(524288), cfg.TransferLimit.UploadBytesPerSec)
	assert.Equal(t, int64(524288), cfg.TransferLimit.DownloadBytesPerSec)
}

// TestTransferLimitRejectsNegativeValues 验证负数限速会在启动阶段 fail-fast，避免运行期出现无意义的 limiter 参数。
func TestTransferLimitRejectsNegativeValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		// 上传限速为负数没有业务含义，应拒绝启动。
		{name: "negative_upload", body: "upload_bytes_per_sec: -1\n  download_bytes_per_sec: 0", want: "transfer_limit.upload_bytes_per_sec"},
		// 下载限速为负数没有业务含义，应拒绝启动。
		{name: "negative_download", body: "upload_bytes_per_sec: 0\n  download_bytes_per_sec: -1", want: "transfer_limit.download_bytes_per_sec"},
	} {
		// 子测试：逐条确认负数限速字段会返回可定位到具体 YAML key 的错误。
		t.Run(tc.name, func(t *testing.T) {
			yaml := fullValidYAML() + `
transfer_limit:
  ` + tc.body + `
`
			_, err := loadConfigFromStringErr(t, yaml)
			require.Error(t, err)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// TestAICCGovernanceDefaults 验证未配置治理段时仍会得到有限的生产安全默认值。
func TestAICCGovernanceDefaults(t *testing.T) {
	cfg := loadConfigFromString(t, fullValidYAML())

	assert.Equal(t, int64(64), cfg.AICC.Governance.GlobalQueueCapacity)
	assert.Equal(t, int64(32), cfg.AICC.Governance.UpstreamConcurrency)
	assert.Equal(t, 30*time.Second, cfg.AICC.Governance.CircuitCooldown.Duration)
}

// TestAICCGovernanceRejectsNegativeCapacity 验证队列容量不能被配置成负数。
func TestAICCGovernanceRejectsNegativeCapacity(t *testing.T) {
	yaml := strings.Replace(fullValidYAML(), "  runtime_image: \"registry.example.com/app/oc-manager-aigowork-aicc:v1.0.0-test\"", "  runtime_image: \"registry.example.com/app/oc-manager-aigowork-aicc:v1.0.0-test\"\n  governance:\n    global_queue_capacity: -1", 1)
	_, err := loadConfigFromStringErr(t, yaml)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "aicc.governance.global_queue_capacity")
}

// TestStorageS3ValidationRequiresFields 验证启用 S3 但字段不全时加载报错（fail-fast）。
func TestStorageS3ValidationRequiresFields(t *testing.T) {
	// 启用 S3 却缺 endpoint/bucket/access_key_id/secret_access_key 等，Validate 必须 fail-fast。
	// 使用完整合法的基础配置拼接 storage.s3.enabled=true 触发 S3 专属校验路径。
	yaml := fullValidYAML() + "\nstorage:\n  s3:\n    enabled: true\n"
	_, err := loadConfigFromStringErr(t, yaml)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage.s3")
}

// TestStorageS3DefaultsApplied 验证 S3 启用且未配置 region/presign_ttl 时默认值被正确填充。
func TestStorageS3DefaultsApplied(t *testing.T) {
	// S3 启用且字段完整但未指定 region/presign_ttl，applyDefaults 应填充稳定默认值。
	yaml := fullValidYAML() + `
storage:
  s3:
    enabled: true
    endpoint: "http://minio:9000"
    bucket: "oc-manager"
    access_key_id: "minioadmin"
    secret_access_key: "minioadmin"
    use_path_style: true
`
	cfg := loadConfigFromString(t, yaml)
	// region 未配置时应填入默认值 us-east-1。
	assert.Equal(t, "us-east-1", cfg.Storage.S3.Region)
	// presign_ttl 未配置时应填入默认值 15m。
	assert.Equal(t, "15m0s", cfg.Storage.S3.PresignTTL.Duration.String())
}

// TestStorageS3RejectsEndpointWithoutScheme 验证 S3 endpoint 缺 http(s):// scheme 时启动期报错。
func TestStorageS3RejectsEndpointWithoutScheme(t *testing.T) {
	// endpoint 写成无 scheme 的裸主机名（如云 OSS 域名漏写 https://）：字段非空能过上一道
	// 检查，但 S3 client 运行期才会报 unsupported protocol scheme，进而让 bootstrap 在用户
	// 建应用时返回 500。本用例确保这类错误在 Validate 阶段就 fail-fast，而非拖到运行期。
	yaml := fullValidYAML() + `
storage:
  s3:
    enabled: true
    endpoint: "eos-beijing-2-internal.cmecloud.cn"
    bucket: "oc-manager"
    access_key_id: "ak"
    secret_access_key: "sk"
`
	_, err := loadConfigFromStringErr(t, yaml)
	require.Error(t, err)
	// 错误须明确指向 endpoint，便于部署者快速定位是缺了 scheme。
	assert.Contains(t, err.Error(), "storage.s3.endpoint")
}

// TestStorageS3DisabledByDefaultAllowsMissingFields 验证 S3 未启用时缺少字段不会报错。
func TestStorageS3DisabledByDefaultAllowsMissingFields(t *testing.T) {
	// storage 段完全不配置时，Validate 不应因 S3 字段缺失而失败。
	cfg := loadConfigFromString(t, fullValidYAML())
	assert.False(t, cfg.Storage.S3.Enabled)
}

// TestKubernetesValidationRequiresFields 验证启用 k8s 但缺关键字段时 Validate 报错。
func TestKubernetesValidationRequiresFields(t *testing.T) {
	// 启用 k8s 却缺 ops_image/bootstrap_base_url，Validate 必须 fail-fast。
	// 使用完整合法的基础配置拼接 k8s.enabled=true 触发 k8s 专属校验路径，
	// 确保错误来自 k8s 分支而非其他必填项缺失。
	yaml := fullValidYAML() + "\nk8s:\n  enabled: true\n"
	_, err := loadConfigFromStringErr(t, yaml)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "k8s")
}

// TestKubernetesDefaultsAICCNamespace 验证启用 k8s 且未配置客服命名空间时使用隔离默认值。
func TestKubernetesDefaultsAICCNamespace(t *testing.T) {
	cfg := loadConfigFromString(t, fullValidYAML()+"\nk8s:\n  enabled: true\n  ops_image: registry/ops:v1\n  bootstrap_base_url: http://manager-api:8080\n")
	assert.Equal(t, "oc-apps", cfg.Kubernetes.Namespace)
	assert.Equal(t, "oc-aicc", cfg.Kubernetes.AICCNamespace)
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(path, []byte(content), 0o600)
	require.NoError(t, err)
	return path
}

func loadConfigFromString(t *testing.T, content string) Config {
	t.Helper()
	cfg, err := loadConfigFromStringErr(t, content)
	require.NoError(t, err)
	return cfg
}

func loadConfigFromStringErr(t *testing.T, content string) (Config, error) {
	t.Helper()
	return LoadFile(writeTempConfig(t, content))
}

// validBaseConfig 返回一份通过 Validate 的最小配置，供验证码用例在其上改字段。
func validBaseConfig() Config {
	c := Config{}
	c.App.HTTPAddr = ":8080"
	c.App.DataRoot = "/data"
	c.Database.URL = "mysql://u:p@tcp(127.0.0.1:3306)/ocm"
	c.Redis.Addr = "127.0.0.1:6379"
	c.Auth.AccessTokenTTL = Duration{Duration: time.Hour}
	c.Auth.RefreshTokenTTL = Duration{Duration: 24 * time.Hour}
	c.Auth.JWTAccessSecret = "a"
	c.Auth.JWTRefreshSecret = "b"
	c.Auth.CSRFSecret = "c"
	c.Security.MasterKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAEE=" // 32 字节 base64
	c.AICC.RuntimeImage = "registry.example.com/app/oc-manager-aigowork-aicc:v1.0.0-test"
	return c
}

// 启用验证码但缺 hmac_secret 应校验失败。
func TestValidateCaptchaEnabledRequiresSecret(t *testing.T) {
	c := validBaseConfig()
	c.Captcha.Enabled = true // 开启但不给 hmac_secret
	err := c.Validate()
	require.Error(t, err)
	require.ErrorContains(t, err, "captcha.hmac_secret")
}

// 启用验证码且给了 hmac_secret 应通过，且 applyDefaults 填好难度与 TTL 默认值。
func TestCaptchaEnabledAppliesDefaults(t *testing.T) {
	c := validBaseConfig()
	c.Captcha.Enabled = true
	c.Captcha.HMACSecret = "secret" // 满足必填
	c.applyDefaults()
	require.NoError(t, c.Validate())
	assert.Equal(t, int64(50000), c.Captcha.Difficulty)    // 默认难度
	assert.Equal(t, 5*time.Minute, c.Captcha.TTL.Duration) // 默认有效期
}

// TestLoad_DefaultsI18nLocale 覆盖：未配置 i18n.default_locale 时回退 en，
// 显式配置 zh 时保留，保证平台默认语言可由配置文件控制。
func TestLoad_DefaultsI18nLocale(t *testing.T) {
	var c Config // 未配置
	c.applyDefaults()
	assert.Equal(t, "en", c.I18n.DefaultLocale) // 缺省回退 en

	c2 := Config{I18n: I18nConfig{DefaultLocale: "zh"}} // 显式 zh
	c2.applyDefaults()
	assert.Equal(t, "zh", c2.I18n.DefaultLocale)
}

// 关闭验证码时缺省全部字段也应通过（向后兼容）。
func TestCaptchaDisabledNeedsNothing(t *testing.T) {
	c := validBaseConfig()
	c.applyDefaults()
	require.NoError(t, c.Validate())
}

// TestWebPublishDefaults 覆盖：web_publish 段缺省时填默认（site-server:80 + ACME staging 目录），
// 避免最小配置启动失败。
func TestWebPublishDefaults(t *testing.T) {
	var c Config
	c.applyDefaults()
	assert.Equal(t, "site-server", c.WebPublish.SiteServerService) // 默认 backend 名
	assert.Equal(t, int32(80), c.WebPublish.SiteServerPort)        // 默认 backend 端口
	assert.NotEmpty(t, c.WebPublish.ACMEDirectoryURL)              // 默认 staging 目录
}

// 启用验证码时，负数难度或负 TTL 会让出题不可用，启动阶段应 fail-fast。
func TestLoadRejectsCaptchaInvalidPositiveFields(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		// difficulty 为负数时 maxNumber 无法形成可解的 PoW 空间，必须拒绝启动。
		{name: "negative_difficulty", body: "difficulty: -1\n  ttl: \"5m\"", want: "captcha.difficulty"},
		// ttl 为负数会让题目一生成就过期，必须拒绝启动。
		{name: "negative_ttl", body: "difficulty: 50000\n  ttl: \"-1m\"", want: "captcha.ttl"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			yaml := fullValidYAML() + `
captcha:
  enabled: true
  hmac_secret: "secret"
  ` + tc.body + `
`
			_, err := loadConfigFromStringErr(t, yaml)
			require.Error(t, err)
			require.ErrorContains(t, err, tc.want)
		})
	}
}
