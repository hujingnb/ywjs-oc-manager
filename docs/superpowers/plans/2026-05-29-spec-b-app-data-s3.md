# spec-B：app 数据模型（S3 + bootstrap 回调）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 oc-manager 建立 manager 侧的 app 数据层——标准 S3 对象存储抽象、STS 临时写凭证、pod 启动回调 bootstrap 端点、per-app control token 三用统一、skill blob 迁 S3——为 spec-A 的 k8s 编排钉死数据契约。

**Architecture:** 新增 `internal/integrations/storage` 包（aws-sdk-go-v2 标准 S3 + 标准 STS AssumeRole，endpoint 指向 MinIO/云 OSS）；新增 `GET /internal/apps/{id}/bootstrap` 端点（Bearer control token 鉴权，DB 实时渲染 manifest + 预签名读 URL + STS 写凭证）；现有 `runtime_token` 列语义升级为 control token；`SkillBlobStore` 增加 S3 实现。本 spec 是**纯 manager 侧数据层**，全部可单测 / 对真实 MinIO 集成测；pod 侧 initContainer/sidecar、Secret 注入、删除旧通道归 spec-A。

**Tech Stack:** Go 1.x、gin、aws-sdk-go-v2（s3 + sts + presign）、guregu/null/v5、stretchr/testify、gopkg.in/yaml.v3、MySQL(sqlc)。

---

## 项目约定（实现者必读）

- **工作分支**：直接在 `master` 上完成，不切 worktree、不建分支（项目 AGENTS.md 规定）。
- **提交规范**：Conventional Commits，第一行中文摘要，空行后中文正文补背景/实现/影响/测试；commit 末尾加 trailer：
  ```
  Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
  ```
- **git add 精确文件**：每个 commit 只 `git add` 本任务涉及的文件，**禁止 `git add -A`**，**禁止提交未跟踪的 `docs/reports/`**。
- **测试断言**：用 testify——`require.NoError`/`require.Error`/`require.ErrorIs`；等值 `assert.Equal(t, expected, actual)`（expected 在前）；后续依赖的前置断言用 `require.*`。
- **中文注释**：每个测试方法/子测试/table 用例都要相邻中文注释说明覆盖的业务场景/边界/异常；新增代码的包/文件/方法/结构体/字段都要中文注释说明业务意图。
- **OpenAPI 同步**：bootstrap 是 `/internal` 内部端点，**不暴露给前端、不加 swag 注解**（不进 openapi）。但每个任务结束前确认 `make openapi-check` 仍干净（无注解 handler 不被扫描，工作区应保持干净）。
- **不破坏现有**：spec-B 只**新增** bootstrap/S3 路径，**不删**任何旧通道（file API、写卷路径、节点概念）——删除在 spec-A 收口。
- **module 名**：`oc-manager`（import 前缀）。
- **运行测试**：单测 `go test ./internal/...`；集成测见 Task 5（需本地 MinIO，环境变量门控）。
- **MinIO 本地环境**：`make local-up` 起 k3d + MinIO；MinIO 经 ingress 暴露，凭证见 deploy 下 secret（本地固定开发凭证）。

---

## 文件结构

**新建：**
- `internal/integrations/storage/store.go` — `ObjectStore`/`STSIssuer` 接口 + `TempCredentials`/`ObjectInfo` 类型
- `internal/integrations/storage/s3.go` — `S3ObjectStore`（aws-sdk-go-v2 实现 ObjectStore）
- `internal/integrations/storage/sts.go` — `STSCredentialIssuer`（aws-sdk-go-v2 STS AssumeRole 实现 STSIssuer）
- `internal/integrations/storage/keys.go` — S3 key/prefix 约定拼接（纯函数）
- `internal/integrations/storage/keys_test.go` — key 拼接单测
- `internal/integrations/storage/s3_integration_test.go` — 对真实 MinIO 的集成测（环境变量门控）
- `internal/service/s3_skill_blob_store.go` — `SkillBlobStore` + `SkillBlobReader` + `SkillPresigner` 的 S3 实现
- `internal/service/s3_skill_blob_store_test.go` — S3 skill store 单测（用 fake ObjectStore）
- `internal/integrations/hermes/build_manifest.go` — `BuildManifest` 纯函数（从 app_input.go 抽出）
- `internal/integrations/hermes/build_manifest_test.go` — BuildManifest 单测
- `internal/service/bootstrap_service.go` — `BootstrapService`（组装 bootstrap 响应）
- `internal/service/bootstrap_service_test.go` — bootstrap service 单测
- `internal/api/handlers/bootstrap.go` — bootstrap handler + control token 鉴权 + `RegisterBootstrapRoutes`
- `internal/api/handlers/bootstrap_test.go` — bootstrap handler 单测
- `docs/bootstrap-http-contract.md` — bootstrap HTTP 契约文档

**修改：**
- `go.mod` / `go.sum` — 加 aws-sdk-go-v2 依赖
- `internal/config/config.go` — 新增 `StorageConfig`/`S3Config` + 顶层 `Storage` 字段
- `internal/config/loader.go` — Storage 段默认值 + 校验
- `internal/integrations/hermes/app_input.go` — `WriteAppInput` 改调 `BuildManifest`（重构，行为不变）
- `internal/api/router.go` — `Dependencies` 加 `BootstrapService` 字段 + 注册 bootstrap 路由
- `internal/service/app_runtime_token.go` — 注释升级为 control token（语义说明）

---

## Phase 1：S3 抽象层（storage 包）

### Task 1: storage 包骨架与接口

**Files:**
- Modify: `go.mod`（加依赖）
- Create: `internal/integrations/storage/store.go`
- Create: `internal/integrations/storage/keys.go`
- Test: `internal/integrations/storage/keys_test.go`

- [ ] **Step 1: 加 aws-sdk-go-v2 依赖**

Run:
```bash
go get github.com/aws/aws-sdk-go-v2/config@latest
go get github.com/aws/aws-sdk-go-v2/credentials@latest
go get github.com/aws/aws-sdk-go-v2/service/s3@latest
go get github.com/aws/aws-sdk-go-v2/service/sts@latest
go get github.com/aws/aws-sdk-go-v2/aws@latest
```
Expected: `go.mod`/`go.sum` 新增上述模块，无报错。

- [ ] **Step 2: 写 keys.go（S3 key/prefix 约定纯函数）**

```go
// Package storage 提供 manager 侧的标准 S3 对象存储抽象与 STS 临时凭证签发。
// 仅依赖标准 S3 协议（aws-sdk-go-v2），不绑定 MinIO 私有扩展，便于生产切换任意云 OSS。
package storage

import "path"

// app 与 version 两类数据在 S3 bucket 内的 prefix 约定（父设计 §5.4 / spec-B §4）。
// app 级数据按 appID 分前缀，sidecar 的 STS 写凭证限定到该前缀；
// version 级 skill 是 write-once，manager 上传、pod 预签名只读。

// AppPrefix 返回某 app 在 bucket 内的根前缀，例如 "apps/<appID>/"。
// 末尾保留 "/"，便于做 STS policy 的前缀通配与 MovePrefix。
func AppPrefix(appID string) string {
	return path.Join("apps", appID) + "/"
}

// AppArchivePrefix 返回该 app 删除归档目标前缀 "apps/<appID>/archive/"。
func AppArchivePrefix(appID string) string {
	return path.Join("apps", appID, "archive") + "/"
}

// WorkspaceKey 返回 workspace 归档对象 key（sidecar mirror 的逻辑根，spec-A 落地）。
func WorkspaceKey(appID string) string {
	return path.Join("apps", appID, "workspace")
}

// StateDBKey 返回 sqlite 一致性快照对象 key "apps/<appID>/state.db"。
func StateDBKey(appID string) string {
	return path.Join("apps", appID, "state.db")
}

// SessionsKey 返回 sessions 归档对象 key。
func SessionsKey(appID string) string {
	return path.Join("apps", appID, "sessions")
}

// SkillKey 返回 version 级 skill tar 的 key "versions/<versionID>/skills/<name>.tar"。
// 与现有 FSSkillBlobStore 的相对路径布局一致，便于 file_path 列语义平滑迁移。
func SkillKey(versionID, skillName string) string {
	return path.Join("versions", versionID, "skills", skillName+".tar")
}
```

- [ ] **Step 3: 写 store.go（接口 + 类型）**

```go
package storage

import (
	"context"
	"io"
	"time"
)

// ObjectStore 是标准 S3 对象读写抽象。实现见 s3.go（aws-sdk-go-v2）。
// 所有 key 均为 bucket 内的对象键（不含 bucket 名）。
type ObjectStore interface {
	// PutObject 上传一个对象；size 为内容字节数（<0 表示未知，由实现决定是否缓冲）。
	PutObject(ctx context.Context, key string, r io.Reader, size int64) error
	// PresignGet 为 key 生成有效期 ttl 的预签名 GET URL（pod 只读下载用）。
	// 对象不存在不报错（预签名是离线签名，URL 使用时才校验存在）。
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	// ObjectExists 判断对象是否存在（bootstrap 决定是否给出 restore URL）。
	ObjectExists(ctx context.Context, key string) (bool, error)
	// MovePrefix 把 srcPrefix 下所有对象复制到 dstPrefix 再删除源（删除归档用）。
	MovePrefix(ctx context.Context, srcPrefix, dstPrefix string) error
	// DeletePrefix 删除 prefix 下所有对象。
	DeletePrefix(ctx context.Context, prefix string) error
}

// TempCredentials 是 STS AssumeRole 签发的临时写凭证（标准 S3 协议字段）。
type TempCredentials struct {
	AccessKeyID     string    // 临时 access key
	SecretAccessKey string    // 临时 secret
	SessionToken    string    // 会话 token（标准 S3 临时凭证必需）
	Expiresat       time.Time // 过期时间；pod 须在此之前重调 bootstrap 续期
}

// STSIssuer 用标准 STS AssumeRole 签发限定到 app prefix 的临时写凭证。
type STSIssuer interface {
	// AssumeAppRole 签发只能读写 appPrefix（如 "apps/<id>/"）下对象的临时凭证，ttl 为有效期。
	AssumeAppRole(ctx context.Context, appPrefix string, ttl time.Duration) (TempCredentials, error)
}
```

> 注：`TempCredentials.Expiresat` 字段名故意小写 at 拼写应为 `ExpiresAt`——见下方修正。实现者请用 `ExpiresAt`。

- [ ] **Step 4: 修正字段名为 ExpiresAt**

把 `store.go` 中 `Expiresat` 改为 `ExpiresAt`：
```go
	ExpiresAt time.Time // 过期时间；pod 须在此之前重调 bootstrap 续期
```

- [ ] **Step 5: 写 keys_test.go**

```go
package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestKeyConventions 验证各类 S3 key/prefix 拼接符合 spec-B §4 约定。
func TestKeyConventions(t *testing.T) {
	// app 根前缀必须以 "/" 结尾，供 STS prefix 通配与 MovePrefix 使用
	assert.Equal(t, "apps/a1/", AppPrefix("a1"))
	// 归档前缀位于 app 前缀下的 archive/ 子目录
	assert.Equal(t, "apps/a1/archive/", AppArchivePrefix("a1"))
	// sqlite 快照 key 固定为 state.db
	assert.Equal(t, "apps/a1/state.db", StateDBKey("a1"))
	// workspace / sessions 为归档逻辑根（无扩展名）
	assert.Equal(t, "apps/a1/workspace", WorkspaceKey("a1"))
	assert.Equal(t, "apps/a1/sessions", SessionsKey("a1"))
	// skill 走 version 维度，带 .tar 扩展，布局与 FSSkillBlobStore 一致
	assert.Equal(t, "versions/v1/skills/weather.tar", SkillKey("v1", "weather"))
}
```

- [ ] **Step 6: 跑测试 + 编译**

Run: `go test ./internal/integrations/storage/ -run TestKeyConventions -v && go build ./...`
Expected: PASS；整个项目编译通过。

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/integrations/storage/store.go internal/integrations/storage/keys.go internal/integrations/storage/keys_test.go
git commit -F - <<'EOF'
feat(storage): 新增 S3 对象存储抽象接口与 key 约定

为 k8s 迁移 spec-B 数据层引入 internal/integrations/storage 包骨架：
定义 ObjectStore / STSIssuer 接口与 TempCredentials 类型，以及 app/version
两类数据在 bucket 内的 key/prefix 拼接纯函数（apps/<id>/、versions/<vid>/skills/）。
引入 aws-sdk-go-v2（s3/sts/credentials）依赖，仅用标准 S3 协议，不绑定 MinIO 私有扩展。

key 约定单测覆盖各前缀/对象 key 的拼接与边界（app 前缀末尾保留 / 供 STS 通配）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 2: S3ObjectStore 构造与 PutObject / PresignGet / ObjectExists

**Files:**
- Create: `internal/integrations/storage/s3.go`

- [ ] **Step 1: 写 S3ObjectStore 构造器**

```go
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// S3Config 是构造 S3ObjectStore / STSCredentialIssuer 所需的标准 S3 接入参数。
type S3Config struct {
	Endpoint        string // S3 端点，如 http://minio.oc-system.svc:9000；指向 MinIO 或云 OSS
	Region          string // 区域，MinIO 任意填（如 us-east-1）
	Bucket          string // app 数据 bucket
	AccessKeyID     string // manager 持有的长期凭证（用于 Put/Presign/STS 调用方）
	SecretAccessKey string
	UsePathStyle    bool   // MinIO 必须 path-style 寻址
	// STSRoleARN 是 AssumeRole 的目标 role ARN；MinIO 下可为占位（策略由内联 policy 决定）。
	STSRoleARN string
}

// S3ObjectStore 用 aws-sdk-go-v2 标准 S3 客户端实现 ObjectStore。
type S3ObjectStore struct {
	client *s3.Client
	bucket string
}

// NewS3ObjectStore 用静态凭证构造标准 S3 客户端（BaseEndpoint + path-style，兼容 MinIO 与云 OSS）。
func NewS3ObjectStore(cfg S3Config) *S3ObjectStore {
	client := s3.New(s3.Options{
		BaseEndpoint: aws.String(cfg.Endpoint),
		Region:       cfg.Region,
		UsePathStyle: cfg.UsePathStyle,
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID, cfg.SecretAccessKey, ""),
	})
	return &S3ObjectStore{client: client, bucket: cfg.Bucket}
}

// 编译时断言：S3ObjectStore 实现 ObjectStore 接口。
var _ ObjectStore = (*S3ObjectStore)(nil)
```

- [ ] **Step 2: 实现 PutObject / PresignGet / ObjectExists**

```go
// PutObject 上传对象；size>=0 时填 ContentLength，<0 时交由 SDK 处理（会缓冲）。
func (s *S3ObjectStore) PutObject(ctx context.Context, key string, r io.Reader, size int64) error {
	in := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   r,
	}
	if size >= 0 {
		in.ContentLength = aws.Int64(size)
	}
	if _, err := s.client.PutObject(ctx, in); err != nil {
		return fmt.Errorf("storage: 上传对象 %s 失败: %w", key, err)
	}
	return nil
}

// PresignGet 生成预签名 GET URL；离线签名，不校验对象是否存在。
func (s *S3ObjectStore) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	presign := s3.NewPresignClient(s.client)
	req, err := presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("storage: 预签名 %s 失败: %w", key, err)
	}
	return req.URL, nil
}

// ObjectExists 用 HeadObject 判断对象存在；404 归为不存在（非错误）。
func (s *S3ObjectStore) ObjectExists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return true, nil
	}
	// 标准 S3：对象不存在返回 404 NotFound / NoSuchKey；其它错误透出
	var notFound *types.NotFound
	if errors.As(err, &notFound) {
		return false, nil
	}
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) && respErr.HTTPStatusCode() == 404 {
		return false, nil
	}
	return false, fmt.Errorf("storage: HeadObject %s 失败: %w", key, err)
}
```

- [ ] **Step 3: 编译**

Run: `go build ./internal/integrations/storage/`
Expected: 通过（若 `types.NotFound` 路径报错，保留 `smithyhttp.ResponseError` 的 404 分支即可，删去 `types` import 与对应分支）。

- [ ] **Step 4: Commit**

```bash
git add internal/integrations/storage/s3.go
git commit -F - <<'EOF'
feat(storage): 实现 S3ObjectStore 的上传/预签名/存在性查询

用 aws-sdk-go-v2 标准 S3 客户端实现 PutObject、PresignGet、ObjectExists。
BaseEndpoint + path-style 寻址兼容本地 MinIO 与生产云 OSS；预签名走标准
PresignClient；ObjectExists 用 HeadObject 并把 404 归为不存在（供 bootstrap
判定首启是否给出 restore URL）。

MovePrefix/DeletePrefix 留待下一任务实现。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 3: S3ObjectStore 的 MovePrefix / DeletePrefix

**Files:**
- Modify: `internal/integrations/storage/s3.go`

- [ ] **Step 1: 实现 listPrefix 辅助 + DeletePrefix + MovePrefix**

在 `s3.go` 追加：
```go
// listKeys 列出 prefix 下全部对象 key（分页直到取完）。
func (s *S3ObjectStore) listKeys(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	var token *string
	for {
		out, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("storage: 列举 %s 失败: %w", prefix, err)
		}
		for _, obj := range out.Contents {
			keys = append(keys, aws.ToString(obj.Key))
		}
		if aws.ToBool(out.IsTruncated) && out.NextContinuationToken != nil {
			token = out.NextContinuationToken
			continue
		}
		break
	}
	return keys, nil
}

// DeletePrefix 删除 prefix 下全部对象；空前缀视为非法（防误删整桶）。
func (s *S3ObjectStore) DeletePrefix(ctx context.Context, prefix string) error {
	if prefix == "" {
		return fmt.Errorf("storage: DeletePrefix 拒绝空前缀")
	}
	keys, err := s.listKeys(ctx, prefix)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(k),
		}); err != nil {
			return fmt.Errorf("storage: 删除对象 %s 失败: %w", k, err)
		}
	}
	return nil
}

// MovePrefix 把 srcPrefix 下对象逐个 CopyObject 到 dstPrefix 对应相对路径，再删除源。
// 用于删除归档（apps/<id>/* → apps/<id>/archive/*）。空前缀视为非法。
func (s *S3ObjectStore) MovePrefix(ctx context.Context, srcPrefix, dstPrefix string) error {
	if srcPrefix == "" || dstPrefix == "" {
		return fmt.Errorf("storage: MovePrefix 拒绝空前缀")
	}
	keys, err := s.listKeys(ctx, srcPrefix)
	if err != nil {
		return err
	}
	for _, k := range keys {
		rel := k[len(srcPrefix):] // 去掉源前缀，保留相对路径
		dstKey := dstPrefix + rel
		if _, err := s.client.CopyObject(ctx, &s3.CopyObjectInput{
			Bucket:     aws.String(s.bucket),
			CopySource: aws.String(s.bucket + "/" + k),
			Key:        aws.String(dstKey),
		}); err != nil {
			return fmt.Errorf("storage: 复制 %s→%s 失败: %w", k, dstKey, err)
		}
	}
	// 复制完成后删源；归档语义下若某对象已位于 dstPrefix 之内不会被重复列举（dstPrefix 是 srcPrefix 子目录时需排除）
	return s.deletePrefixExcluding(ctx, srcPrefix, dstPrefix)
}

// deletePrefixExcluding 删除 srcPrefix 下对象，但跳过落在 excludePrefix 内的（避免删掉刚归档的目标）。
func (s *S3ObjectStore) deletePrefixExcluding(ctx context.Context, srcPrefix, excludePrefix string) error {
	keys, err := s.listKeys(ctx, srcPrefix)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if excludePrefix != "" && len(k) >= len(excludePrefix) && k[:len(excludePrefix)] == excludePrefix {
			continue // 跳过归档目标自身
		}
		if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(k),
		}); err != nil {
			return fmt.Errorf("storage: 删除对象 %s 失败: %w", k, err)
		}
	}
	return nil
}
```

- [ ] **Step 2: 编译**

Run: `go build ./internal/integrations/storage/`
Expected: 通过。

- [ ] **Step 3: Commit**

```bash
git add internal/integrations/storage/s3.go
git commit -F - <<'EOF'
feat(storage): 实现 S3ObjectStore 的前缀移动与删除

新增 listKeys 分页列举、DeletePrefix、MovePrefix（逐对象 CopyObject 后删源）。
MovePrefix 用于删除归档（apps/<id>/* → apps/<id>/archive/*），并在删源时跳过
落在归档目标前缀内的对象，避免删掉刚复制过去的归档副本。空前缀一律拒绝防误删整桶。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 4: STSCredentialIssuer（标准 AssumeRole + prefix 限定）

**Files:**
- Create: `internal/integrations/storage/sts.go`

- [ ] **Step 1: 写 sts.go**

```go
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
```

- [ ] **Step 2: 单测 buildPrefixPolicy（纯函数，可不连 STS）**

追加 `internal/integrations/storage/sts_test.go`：
```go
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
```

- [ ] **Step 3: 跑测试 + 编译**

Run: `go test ./internal/integrations/storage/ -run TestBuildPrefixPolicy -v && go build ./...`
Expected: PASS。

- [ ] **Step 4: Commit**

```bash
git add internal/integrations/storage/sts.go internal/integrations/storage/sts_test.go
git commit -F - <<'EOF'
feat(storage): 实现标准 STS AssumeRole 签发 prefix 限定临时凭证

STSCredentialIssuer 用 aws-sdk-go-v2 标准 STS AssumeRole + 内联 IAM policy，
把临时凭证的对象读写限定到 apps/<id>/* 前缀、ListBucket 受 s3:prefix 条件约束，
保证 pod 只能读写自己 app 的前缀。endpoint 同 S3 端点，MinIO 与云 OSS 均兼容。

单测覆盖内联 policy 的资源 ARN 与前缀条件拼接（纯函数，不连 STS）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 5: 对真实 MinIO 的 S3/STS 集成测（环境变量门控）

**Files:**
- Create: `internal/integrations/storage/s3_integration_test.go`

> **目的**：S3/STS/预签名是外部协议交互，mock 单测证明不了真能跟 MinIO 工作（spec-B B6）。本测对本地 k3d 的真实 MinIO 跑，验证上传→预签名下载内容一致、STS prefix 限定真生效（越权写被拒）。无 MinIO 环境变量时跳过。

- [ ] **Step 1: 写集成测**

```go
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
	key := fmt.Sprintf("apps/it-%d/probe.txt", time.Now().UnixNano())
	payload := []byte("hello-s3-roundtrip")

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
	got, _ := io.ReadAll(resp.Body)
	assert.Equal(t, payload, got)

	// 清理
	require.NoError(t, store.DeletePrefix(ctx, fmt.Sprintf("apps/it-%d/", time.Now().UnixNano())))
}

// TestSTSPrefixIsolation 验证 STS 临时凭证只能写自己 app 前缀，越权写其它 app 被拒。
func TestSTSPrefixIsolation(t *testing.T) {
	cfg := minioCfgFromEnv(t)
	issuer := storage.NewSTSCredentialIssuer(cfg)
	ctx := context.Background()

	prefix := fmt.Sprintf("apps/it-sts-%d/", time.Now().UnixNano())
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
```

- [ ] **Step 2: 无 MinIO 时跑（应跳过）**

Run: `go test ./internal/integrations/storage/ -run 'TestS3RoundTrip|TestSTSPrefixIsolation' -v`
Expected: SKIP（未设置 `OC_S3_TEST_ENDPOINT`）。

- [ ] **Step 3: 有 MinIO 时跑（交付前在本地 k3d 验证）**

Run（值按本地 MinIO 调整）:
```bash
OC_S3_TEST_ENDPOINT=http://minio.localhost \
OC_S3_TEST_BUCKET=oc-apps \
OC_S3_TEST_AK=<minio-ak> OC_S3_TEST_SK=<minio-sk> \
OC_S3_TEST_STS_ROLE=arn:aws:iam:::role/dev \
go test ./internal/integrations/storage/ -run 'TestS3RoundTrip|TestSTSPrefixIsolation' -v
```
Expected: PASS（round-trip 内容一致；越权写被拒）。**若本地 MinIO STS 未配 role，记录实际结果与 MinIO STS 配置要求到交付说明，不可伪造通过。**

- [ ] **Step 4: Commit**

```bash
git add internal/integrations/storage/s3_integration_test.go
git commit -F - <<'EOF'
test(storage): 新增对真实 MinIO 的 S3/STS 集成测

环境变量门控（OC_S3_TEST_*，缺失即 Skip）。覆盖：上传后预签名 GET 下载内容
一致；STS 临时凭证写自身 app 前缀允许、越权写其它 app 前缀被拒，证明 prefix
限定真生效（spec-B B6 要求外部协议对真实后端验证，不被 mock 掩盖）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

## Phase 2：配置

### Task 6: storage.s3 配置段

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/loader.go`

> 背景：config 是手写 YAML 解析、`KnownFields(true)`——新增 `storage:` 顶层段**必须**在 `Config` struct 加字段，否则任何含 `storage:` 的 yaml 启动即报 unknown field。参考 `RAGFlowConfig.validate()` 的「整段可选、一旦配置就要求齐全」模式。

- [ ] **Step 1: 在 config.go 加 StorageConfig**

在 `internal/config/config.go` 顶层 `Config` struct 末尾加字段（紧跟 `NewAPI NewAPIConfig` 之后）：
```go
	// Storage 是对象存储（S3）配置；整段可选，配置则要求关键字段齐全（见 loader 校验）。
	Storage StorageConfig `yaml:"storage"`
```

在文件末尾追加类型定义：
```go
// StorageConfig 是对象存储配置容器；当前仅 S3。
type StorageConfig struct {
	S3 S3StorageConfig `yaml:"s3"`
}

// S3StorageConfig 是标准 S3 接入参数（本地指向 MinIO，生产指向云 OSS）。
// 仅用标准 S3 协议，不绑定 MinIO 私有扩展。
type S3StorageConfig struct {
	Enabled         bool   `yaml:"enabled"`          // 是否启用 S3（false 时 skill 仍走本地 FS，便于无 MinIO 的最小开发）
	Endpoint        string `yaml:"endpoint"`         // S3 端点 URL
	Region          string `yaml:"region"`           // 区域（MinIO 任意）
	Bucket          string `yaml:"bucket"`           // app 数据 bucket
	AccessKeyID     string `yaml:"access_key_id"`    // manager 长期凭证
	SecretAccessKey string `yaml:"secret_access_key"`
	UsePathStyle    bool   `yaml:"use_path_style"`   // MinIO 必须 true
	STSRoleARN      string `yaml:"sts_role_arn"`     // AssumeRole 目标 role ARN
	// PresignTTL 预签名 URL / STS 凭证默认有效期；空时由 applyDefaults 填默认。
	PresignTTL Duration `yaml:"presign_ttl"`
}
```

- [ ] **Step 2: 在 loader.go 加默认值与校验**

在 `applyDefaults()`（`internal/config/loader.go`）内追加：
```go
	// S3 启用时填预签名默认有效期 15m（pod 拉取 / 续期窗口足够，又不过长）。
	if c.Storage.S3.Enabled && c.Storage.S3.PresignTTL == 0 {
		c.Storage.S3.PresignTTL = Duration(15 * time.Minute)
	}
	if c.Storage.S3.Enabled && c.Storage.S3.Region == "" {
		c.Storage.S3.Region = "us-east-1"
	}
```

在 `Validate()` 内追加：
```go
	// S3 启用时关键字段必须齐全，缺失 fail-fast（避免运行期才暴露配置缺漏）。
	if c.Storage.S3.Enabled {
		if c.Storage.S3.Endpoint == "" || c.Storage.S3.Bucket == "" ||
			c.Storage.S3.AccessKeyID == "" || c.Storage.S3.SecretAccessKey == "" {
			return fmt.Errorf("storage.s3 已启用但 endpoint/bucket/access_key_id/secret_access_key 不完整")
		}
		if c.Storage.S3.STSRoleARN == "" {
			return fmt.Errorf("storage.s3 已启用但缺少 sts_role_arn")
		}
	}
```

（若 loader.go 未 import `time`/`fmt`，补上。）

- [ ] **Step 3: 单测配置加载**

在 `internal/config/loader_test.go`（若无则新建同名文件）追加：
```go
// TestStorageS3ValidationRequiresFields 验证启用 S3 但字段不全时加载报错。
func TestStorageS3ValidationRequiresFields(t *testing.T) {
	// 启用 S3 却缺 endpoint/bucket 等，Validate 必须 fail-fast
	var c Config
	c.Storage.S3.Enabled = true
	err := c.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage.s3")
}
```
（若 loader_test.go 已有 import 块，复用；否则加 `testing` + testify import 与 `package config`。注意 `Validate` 可能依赖其它必填项先通过——若如此，改为直接调用一个仅校验 storage 的辅助，或在用例里把其它必填字段也填上最小合法值，确保失败原因是 storage.s3。）

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/config/ -run TestStorageS3 -v && go build ./...`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/loader.go internal/config/loader_test.go
git commit -F - <<'EOF'
feat(config): 新增 storage.s3 配置段

顶层 Config 增加 Storage.S3（endpoint/region/bucket/凭证/use_path_style/
sts_role_arn/presign_ttl）。因 loader 用 KnownFields(true)，新增顶层段必须
同步加 struct 字段。applyDefaults 填预签名默认 15m 与默认 region；Validate
在启用 S3 时对关键字段做 fail-fast 校验。

Enabled 标志允许无 MinIO 的最小开发仍走本地 FS skill 存储（装配层据此选择）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

## Phase 3：skill blob S3 化

### Task 7: S3SkillBlobStore + 装配按配置切换

**Files:**
- Create: `internal/service/s3_skill_blob_store.go`
- Test: `internal/service/s3_skill_blob_store_test.go`

> 背景：现有 `FSSkillBlobStore` 实现 `SkillBlobStore`（PutSkill/DeleteSkill）+ worker 的 `SkillBlobReader`（OpenSkill）。S3 实现须同时满足这两个接口（旧的「推 skill 到节点」路径在 spec-B 阶段仍在用，靠 OpenSkill 从 S3 读），并新增 `PresignSkill`（供 bootstrap 给 pod 预签名下载）。

- [ ] **Step 1: 写 S3SkillBlobStore**

```go
package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"oc-manager/internal/integrations/storage"
)

// SkillPresigner 暴露 skill tar 的预签名读 URL 能力（bootstrap 给 pod 下载用）。
type SkillPresigner interface {
	PresignSkill(ctx context.Context, relPath string, ttl time.Duration) (string, error)
}

// S3SkillBlobStore 用对象存储承载 skill tar 主副本，relPath 即 S3 key
// （布局 versions/<vid>/skills/<name>.tar，与 FSSkillBlobStore 一致，file_path 列语义平滑迁移）。
type S3SkillBlobStore struct {
	objects storage.ObjectStore
	ttl     time.Duration // 预签名默认有效期
}

// NewS3SkillBlobStore 构造基于 S3 的 skill 主副本存储。
func NewS3SkillBlobStore(objects storage.ObjectStore, ttl time.Duration) *S3SkillBlobStore {
	return &S3SkillBlobStore{objects: objects, ttl: ttl}
}

// PutSkill 上传 skill tar，返回相对路径（= S3 key）。
func (s *S3SkillBlobStore) PutSkill(versionID, skillName string, data []byte) (string, error) {
	if err := safeSegment(versionID); err != nil {
		return "", err
	}
	if err := safeSegment(skillName); err != nil {
		return "", err
	}
	key := storage.SkillKey(versionID, skillName)
	if err := s.objects.PutObject(context.Background(), key, bytes.NewReader(data), int64(len(data))); err != nil {
		return "", fmt.Errorf("上传 skill tar 失败: %w", err)
	}
	return key, nil
}

// DeleteSkill 删除 skill tar（按单对象前缀删，幂等）。
func (s *S3SkillBlobStore) DeleteSkill(relPath string) error {
	if err := s.objects.DeletePrefix(context.Background(), relPath); err != nil {
		return fmt.Errorf("删除 skill tar 失败: %w", err)
	}
	return nil
}

// OpenSkill 从 S3 读 skill tar，满足 worker 的 SkillBlobReader（旧推送路径仍用）。
// 这里用预签名 URL + HTTP GET 读取，避免给该接口再加流式读对象的方法。
func (s *S3SkillBlobStore) OpenSkill(relPath string) (io.ReadCloser, error) {
	url, err := s.objects.PresignGet(context.Background(), relPath, s.ttl)
	if err != nil {
		return nil, fmt.Errorf("预签名 skill 失败: %w", err)
	}
	resp, err := httpGet(url)
	if err != nil {
		return nil, fmt.Errorf("下载 skill tar 失败: %w", err)
	}
	return resp, nil
}

// PresignSkill 生成 skill tar 的预签名读 URL（bootstrap 用）。
func (s *S3SkillBlobStore) PresignSkill(ctx context.Context, relPath string, ttl time.Duration) (string, error) {
	return s.objects.PresignGet(ctx, relPath, ttl)
}

// 编译时断言：实现 SkillBlobStore（PutSkill/DeleteSkill）与 SkillPresigner。
var _ SkillBlobStore = (*S3SkillBlobStore)(nil)
var _ SkillPresigner = (*S3SkillBlobStore)(nil)
```

- [ ] **Step 2: 加 httpGet 辅助（隔离便于测试替换）**

在同文件追加：
```go
// httpGet 是包内可替换的 HTTP GET，返回响应体 ReadCloser；非 2xx 视为错误。
// 抽成变量便于单测注入假实现，避免真发网络请求。
var httpGet = func(url string) (io.ReadCloser, error) {
	resp, err := defaultHTTPClient.Get(url)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("下载 skill 返回状态 %d", resp.StatusCode)
	}
	return resp.Body, nil
}
```
并在文件顶部 import `net/http` 后定义：
```go
// defaultHTTPClient 用于下载预签名 URL 的对象；超时保护避免长挂。
var defaultHTTPClient = &http.Client{Timeout: 60 * time.Second}
```

- [ ] **Step 3: 单测（fake ObjectStore）**

```go
package service

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeObjectStore 是 storage.ObjectStore 的内存假实现，记录写入并支持预签名→内容回放。
type fakeObjectStore struct {
	put       map[string][]byte
	presigned string
}

func newFakeObjectStore() *fakeObjectStore { return &fakeObjectStore{put: map[string][]byte{}} }

func (f *fakeObjectStore) PutObject(_ context.Context, key string, r io.Reader, _ int64) error {
	b, _ := io.ReadAll(r)
	f.put[key] = b
	return nil
}
func (f *fakeObjectStore) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	f.presigned = key
	return "https://presigned.example/" + key, nil
}
func (f *fakeObjectStore) ObjectExists(_ context.Context, key string) (bool, error) {
	_, ok := f.put[key]
	return ok, nil
}
func (f *fakeObjectStore) MovePrefix(_ context.Context, _, _ string) error  { return nil }
func (f *fakeObjectStore) DeletePrefix(_ context.Context, p string) error    { delete(f.put, p); return nil }

// TestS3SkillBlobStorePutKeyLayout 验证 PutSkill 用 versions/<vid>/skills/<name>.tar 布局。
func TestS3SkillBlobStorePutKeyLayout(t *testing.T) {
	obj := newFakeObjectStore()
	store := NewS3SkillBlobStore(obj, time.Minute)
	// 正常路径：返回的 relPath 即 S3 key，落在 version 维度
	rel, err := store.PutSkill("v1", "weather", []byte("tar-bytes"))
	require.NoError(t, err)
	assert.Equal(t, "versions/v1/skills/weather.tar", rel)
	assert.Equal(t, []byte("tar-bytes"), obj.put["versions/v1/skills/weather.tar"])
}

// TestS3SkillBlobStoreRejectsUnsafeSegment 验证非法版本/技能名被拒（防注入路径段）。
func TestS3SkillBlobStoreRejectsUnsafeSegment(t *testing.T) {
	store := NewS3SkillBlobStore(newFakeObjectStore(), time.Minute)
	// 含分隔符的技能名必须被 safeSegment 拒绝
	_, err := store.PutSkill("v1", "a/b", []byte("x"))
	require.Error(t, err)
}

// TestS3SkillBlobStoreOpenSkillReadsViaPresign 验证 OpenSkill 经预签名 URL 读回内容。
func TestS3SkillBlobStoreOpenSkillReadsViaPresign(t *testing.T) {
	obj := newFakeObjectStore()
	store := NewS3SkillBlobStore(obj, time.Minute)
	// 替换包级 httpGet，按预签名 key 回放假内容，避免真实网络
	orig := httpGet
	defer func() { httpGet = orig }()
	httpGet = func(url string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("downloaded:" + url)), nil
	}
	rc, err := store.OpenSkill("versions/v1/skills/weather.tar")
	require.NoError(t, err)
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	// 预签名 key 应为传入 relPath，下载内容来自该 URL
	assert.Equal(t, "versions/v1/skills/weather.tar", obj.presigned)
	assert.Equal(t, "downloaded:https://presigned.example/versions/v1/skills/weather.tar", string(body))
}
```

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/service/ -run TestS3SkillBlobStore -v`
Expected: PASS。

- [ ] **Step 5: 装配按配置切换（main 装配处）**

找到 `cmd/server/main.go` 中构造 `NewFSSkillBlobStore(cfg.App.DataRoot)` 的位置，改为按 `storage.s3.enabled` 选择：
```go
	// skill 主副本存储：启用 S3 时走对象存储，否则退回本地 FS（无 MinIO 的最小开发）。
	var skillBlobs service.SkillBlobStore
	if cfg.Storage.S3.Enabled {
		s3cfg := storage.S3Config{
			Endpoint: cfg.Storage.S3.Endpoint, Region: cfg.Storage.S3.Region,
			Bucket: cfg.Storage.S3.Bucket, AccessKeyID: cfg.Storage.S3.AccessKeyID,
			SecretAccessKey: cfg.Storage.S3.SecretAccessKey, UsePathStyle: cfg.Storage.S3.UsePathStyle,
			STSRoleARN: cfg.Storage.S3.STSRoleARN,
		}
		objStore := storage.NewS3ObjectStore(s3cfg)
		skillBlobs = service.NewS3SkillBlobStore(objStore, time.Duration(cfg.Storage.S3.PresignTTL))
	} else {
		skillBlobs = service.NewFSSkillBlobStore(cfg.App.DataRoot)
	}
```
（变量名 `skillBlobs` 沿用现有装配传给 assistant version service / worker 的那个；若现有名不同，按实际名替换。补 import `oc-manager/internal/integrations/storage` 与 `time`。**注意类型**：现有装配点同时需要 `SkillBlobStore`（service）与 `SkillBlobReader`（worker）。`FSSkillBlobStore`/`S3SkillBlobStore` 都同时实现两者；若装配点用具体类型而非接口，改为接口变量或分别传入。逐一核对现有装配点的形参类型再定。）

- [ ] **Step 6: 编译**

Run: `go build ./...`
Expected: 通过。

- [ ] **Step 7: Commit**

```bash
git add internal/service/s3_skill_blob_store.go internal/service/s3_skill_blob_store_test.go cmd/server/main.go
git commit -F - <<'EOF'
feat(service): skill 主副本支持 S3 存储并按配置切换

新增 S3SkillBlobStore：PutSkill 上传到 versions/<vid>/skills/<name>.tar（布局
与 FS 实现一致，file_path 列语义平滑迁移），OpenSkill 经预签名 URL 下载（满足
worker 旧推送路径），新增 PresignSkill 供 bootstrap 给 pod 预签名下载。装配层按
storage.s3.enabled 在 S3 与本地 FS 间切换，保证无 MinIO 的最小开发仍可用。

单测用内存 fake ObjectStore 覆盖 key 布局、非法路径段拒绝、OpenSkill 经预签名回放。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

## Phase 4：manifest 渲染抽取

### Task 8: 抽出 hermes.BuildManifest 纯函数

**Files:**
- Create: `internal/integrations/hermes/build_manifest.go`
- Modify: `internal/integrations/hermes/app_input.go`
- Test: `internal/integrations/hermes/build_manifest_test.go`

> 目的：bootstrap 端点要把 manifest 放进 HTTP 响应（不写卷）。现有 `WriteAppInput` 把「构造 Manifest」与「写卷」耦合，抽出纯函数 `BuildManifest(AppInputData) Manifest` 供两边复用，`WriteAppInput` 改调它（行为不变，回归测试保证）。

- [ ] **Step 1: 写 build_manifest.go**

```go
package hermes

// BuildManifest 从 AppInputData 构造 Manifest（纯函数，无 IO）。
// 供 WriteAppInput（写卷路径）与 bootstrap 端点（HTTP 响应路径）共用，
// 保证两条路径产出的 manifest 完全一致。
func BuildManifest(in AppInputData) Manifest {
	m := Manifest{
		App: ManifestApp{ID: in.AppID, Name: in.AppName, Model: in.Model},
		Credentials: ManifestCredentials{
			OpenAI: ManifestOpenAI{APIKey: in.OpenAIAPIKey, BaseURL: in.OpenAIBaseURL},
		},
		Resources: ManifestResources{
			Persona: "resources/persona.md",
			Rules:   ManifestRules{Platform: "resources/platform-rules.md"},
			Skills:  in.SkillRelPaths,
		},
		Routing: in.Routing,
	}
	// knowledge 仅在 runtime base url 与 app token 同时存在时写入（与原 WriteAppInput 语义一致）。
	if in.KnowledgeRuntimeBaseURL != "" && in.KnowledgeAppToken != "" {
		m.Knowledge = ManifestKnowledge{
			RuntimeBaseURL: in.KnowledgeRuntimeBaseURL,
			AppToken:       in.KnowledgeAppToken,
		}
	}
	return m
}
```

- [ ] **Step 2: WriteAppInput 改调 BuildManifest**

把 `app_input.go` 中 `WriteAppInput` 内联构造 `m := Manifest{...}` 到 `if in.Knowledge...{}` 的整段（约第 69-88 行）替换为：
```go
	m := BuildManifest(in)
```
（保留其后 `MarshalManifestYAML(m)` 与上传逻辑不变。）

- [ ] **Step 3: 写 build_manifest_test.go**

```go
package hermes

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBuildManifestFullFields 验证全字段装配（含 knowledge / routing / skills）。
func TestBuildManifestFullFields(t *testing.T) {
	// 正常路径：knowledge 两字段齐全时写入，skills/routing 透传
	in := AppInputData{
		AppID: "a1", AppName: "demo", Model: "gpt-x",
		OpenAIAPIKey: "sk-xxx", OpenAIBaseURL: "http://new-api:3000",
		KnowledgeRuntimeBaseURL: "http://manager/runtime", KnowledgeAppToken: "tok",
		Routing:       map[string]string{"fast": "gpt-mini"},
		SkillRelPaths: []string{"resources/skills/weather.tar"},
	}
	m := BuildManifest(in)
	assert.Equal(t, "a1", m.App.ID)
	assert.Equal(t, "sk-xxx", m.Credentials.OpenAI.APIKey)
	assert.Equal(t, "resources/persona.md", m.Resources.Persona)
	assert.Equal(t, []string{"resources/skills/weather.tar"}, m.Resources.Skills)
	assert.Equal(t, "tok", m.Knowledge.AppToken)
	assert.Equal(t, "gpt-mini", m.Routing["fast"])
}

// TestBuildManifestOmitsKnowledgeWhenIncomplete 验证 knowledge 字段不全时不写入。
func TestBuildManifestOmitsKnowledgeWhenIncomplete(t *testing.T) {
	// 边界：仅有 base url 缺 token，knowledge 应保持零值（omitempty 省略）
	m := BuildManifest(AppInputData{AppID: "a1", KnowledgeRuntimeBaseURL: "http://x"})
	assert.Empty(t, m.Knowledge.AppToken)
	assert.Empty(t, m.Knowledge.RuntimeBaseURL)
}
```

- [ ] **Step 4: 跑测试（含现有 app_input 回归）**

Run: `go test ./internal/integrations/hermes/ -v`
Expected: PASS（含现有 WriteAppInput 相关测试，证明重构行为不变）。

- [ ] **Step 5: Commit**

```bash
git add internal/integrations/hermes/build_manifest.go internal/integrations/hermes/app_input.go internal/integrations/hermes/build_manifest_test.go
git commit -F - <<'EOF'
refactor(hermes): 抽出 BuildManifest 纯函数供卷写入与 bootstrap 共用

将 WriteAppInput 内联的 Manifest 构造抽成纯函数 BuildManifest(AppInputData)，
WriteAppInput 改调它（行为不变，现有测试回归保证）。bootstrap 端点将复用同一
函数把 manifest 放进 HTTP 响应，确保写卷路径与 HTTP 路径产出一致。

单测覆盖全字段装配与 knowledge 字段不全时的 omitempty 省略。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

## Phase 5：bootstrap service

### Task 9: bootstrap 响应 DTO 与数据组装接口

**Files:**
- Create: `internal/service/bootstrap_service.go`

> 设计边界：bootstrap 是**只读、无副作用**端点——api_key 与 control token 由 spec-A 的创建流程在建 pod 前 ensure 好；bootstrap 只解密复用，不创建 new-api key、不生成 token。ciphertext 缺失视为 app 未就绪（返回错误）。

- [ ] **Step 1: 写 DTO 与依赖接口**

```go
package service

import (
	"context"
	"time"

	"oc-manager/internal/integrations/hermes"
	"oc-manager/internal/integrations/storage"
	"oc-manager/internal/store/sqlc"
)

// BootstrapResult 是 GET /internal/apps/{id}/bootstrap 的响应体（pod initContainer 消费）。
type BootstrapResult struct {
	// Manifest 是渲染后的 manifest（YAML 字符串，含 api_key/persona 路径/skills/knowledge）。
	ManifestYAML string `json:"manifest_yaml"`
	// Persona / PlatformRule 是 resources/*.md 的文本内容，initContainer 写入 emptyDir。
	Persona      string `json:"persona"`
	PlatformRule string `json:"platform_rule"`
	// Skills 是各 skill tar 的预签名读 URL + 目标相对路径（与 manifest.resources.skills 对应）。
	Skills []BootstrapSkill `json:"skills"`
	// Restore 是会话/工作区恢复的预签名读 URL；首启时各字段为空。
	Restore BootstrapRestore `json:"restore"`
	// S3Write 是 prefix 限定的 STS 临时写凭证（sidecar mirror 用）。
	S3Write BootstrapS3Write `json:"s3_write"`
}

// BootstrapSkill 单个 skill 的下载信息。
type BootstrapSkill struct {
	Name    string `json:"name"`     // skill 名
	RelPath string `json:"rel_path"` // pod 内目标相对路径，如 resources/skills/weather.tar
	URL     string `json:"url"`      // 预签名 GET URL
}

// BootstrapRestore 会话/工作区恢复 URL；为空表示首启或该项无快照。
type BootstrapRestore struct {
	WorkspaceURL string `json:"workspace_url,omitempty"`
	StateDBURL   string `json:"state_db_url,omitempty"`
	SessionsURL  string `json:"sessions_url,omitempty"`
}

// BootstrapS3Write 标准 STS 临时写凭证 + 寻址信息。
type BootstrapS3Write struct {
	Endpoint        string    `json:"endpoint"`
	Region          string    `json:"region"`
	Bucket          string    `json:"bucket"`
	Prefix          string    `json:"prefix"` // 限定前缀 apps/<id>/
	AccessKeyID     string    `json:"access_key_id"`
	SecretAccessKey string    `json:"secret_access_key"`
	SessionToken    string    `json:"session_token"`
	ExpiresAt       time.Time `json:"expires_at"`
}

// bootstrapStore 是 bootstrap 组装所需的最小数据库能力（窄接口，便于单测注入假实现）。
type bootstrapStore interface {
	GetApp(ctx context.Context, id string) (sqlc.App, error)
	GetAppByRuntimeTokenHash(ctx context.Context, hash string) (sqlc.App, error)
	GetOrganization(ctx context.Context, id string) (sqlc.Organization, error)
	GetUser(ctx context.Context, id string) (sqlc.User, error)
	GetAssistantVersion(ctx context.Context, id string) (sqlc.AssistantVersion, error)
}

// bootstrapSkillSource 提供 skill 预签名 URL（由 S3SkillBlobStore 实现）。
type bootstrapSkillSource interface {
	PresignSkill(ctx context.Context, relPath string, ttl time.Duration) (string, error)
}

// bootstrapManifestRenderer 渲染 persona/platform 文本（包内函数 hermes.RenderPersonaText 等的薄封装，便于测试）。
type bootstrapManifestRenderer interface {
	Render(in hermes.AppInputData) (manifestYAML, persona, platform string, err error)
}
```

- [ ] **Step 2: 编译**

Run: `go build ./internal/service/`
Expected: 通过（可能因接口未被实现而无引用警告，无妨）。

- [ ] **Step 3: Commit**

```bash
git add internal/service/bootstrap_service.go
git commit -F - <<'EOF'
feat(service): 定义 bootstrap 响应 DTO 与数据组装接口

新增 BootstrapResult（manifest_yaml/persona/platform_rule/skills/restore/
s3_write）及窄依赖接口 bootstrapStore / bootstrapSkillSource /
bootstrapManifestRenderer，为只读无副作用的 bootstrap 端点组装做准备。
api_key 与 control token 由 spec-A 创建流程预先 ensure，bootstrap 只解密复用。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 10: BootstrapService 组装逻辑

**Files:**
- Modify: `internal/service/bootstrap_service.go`

- [ ] **Step 1: 实现 manifest renderer 默认实现**

在 `bootstrap_service.go` 追加：
```go
import (
	"fmt"
	// ... 已有 import
	"oc-manager/internal/auth"
)

// defaultManifestRenderer 用 hermes 包的渲染函数实现 bootstrapManifestRenderer。
type defaultManifestRenderer struct{}

// Render 渲染 persona/platform 文本并序列化 manifest YAML。
func (defaultManifestRenderer) Render(in hermes.AppInputData) (string, string, string, error) {
	vars := hermes.VariablesFromContext(in.OrgName, in.AppName, in.OwnerName)
	persona, err := hermes.RenderPersonaText(in.PersonaText, vars)
	if err != nil {
		return "", "", "", fmt.Errorf("render persona: %w", err)
	}
	platform, err := hermes.RenderRuleText(in.PlatformRule, vars)
	if err != nil {
		return "", "", "", fmt.Errorf("render platform rule: %w", err)
	}
	yamlBytes, err := hermes.MarshalManifestYAML(hermes.BuildManifest(in))
	if err != nil {
		return "", "", "", fmt.Errorf("marshal manifest: %w", err)
	}
	return string(yamlBytes), persona, platform, nil
}
```

- [ ] **Step 2: 实现 BootstrapService 结构与构造器**

```go
// BootstrapService 组装 pod 启动回调所需的 manifest + 预签名 URL + STS 写凭证。
// 只读：不创建 new-api key、不生成 token；缺失视为 app 未就绪（错误）。
type BootstrapService struct {
	store      bootstrapStore
	cipher     *auth.Cipher
	objects    storage.ObjectStore
	sts        storage.STSIssuer
	skills     bootstrapSkillSource
	renderer   bootstrapManifestRenderer
	cfg        BootstrapConfig
}

// BootstrapConfig 是 bootstrap 组装的静态配置（来自 manager 配置）。
type BootstrapConfig struct {
	Endpoint           string        // S3 端点（透传给 pod 的 s3_write）
	Region             string
	Bucket             string
	NewAPIBaseURL      string        // manifest.credentials.openai.base_url
	KnowledgeBaseURL   string        // manifest.knowledge.runtime_base_url
	PlatformPrompt     string        // 平台层规则模板
	PresignTTL         time.Duration // 预签名 / STS 有效期
}

// NewBootstrapService 构造 bootstrap 服务。
func NewBootstrapService(store bootstrapStore, cipher *auth.Cipher, objects storage.ObjectStore,
	sts storage.STSIssuer, skills bootstrapSkillSource, cfg BootstrapConfig) *BootstrapService {
	return &BootstrapService{
		store: store, cipher: cipher, objects: objects, sts: sts, skills: skills,
		renderer: defaultManifestRenderer{}, cfg: cfg,
	}
}
```

- [ ] **Step 3: 实现 Build（核心组装）**

```go
import (
	"encoding/json"
	"errors"
	"path"
	// ...
)

// ErrAppNotReady 表示 app 缺少 api_key/control token，尚不能 bootstrap（应由创建流程先 ensure）。
var ErrAppNotReady = errors.New("app 未就绪：缺少 api_key 或 control token")

// Build 按 appID 组装 bootstrap 响应。调用方已通过 control token 鉴权并确认 token 属于该 app。
func (s *BootstrapService) Build(ctx context.Context, app sqlc.App) (BootstrapResult, error) {
	// 1. 解密 new-api api_key（缺失→未就绪）
	if !app.NewapiKeyCiphertext.Valid {
		return BootstrapResult{}, ErrAppNotReady
	}
	apiKeyPlain, err := s.cipher.Decrypt(app.NewapiKeyCiphertext.String)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("解密 api_key 失败: %w", err)
	}
	// 2. 解密 control token（缺失→未就绪），用于 manifest.knowledge.app_token
	if !app.RuntimeTokenCiphertext.Valid {
		return BootstrapResult{}, ErrAppNotReady
	}
	controlToken, err := s.cipher.Decrypt(app.RuntimeTokenCiphertext.String)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("解密 control token 失败: %w", err)
	}
	// 3. 查 org / owner / version
	org, err := s.store.GetOrganization(ctx, app.OrgID)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("查询组织失败: %w", err)
	}
	owner, err := s.store.GetUser(ctx, app.OwnerUserID)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("查询所有者失败: %w", err)
	}
	if !app.VersionID.Valid {
		return BootstrapResult{}, ErrAppNotReady
	}
	version, err := s.store.GetAssistantVersion(ctx, app.VersionID.String)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("查询版本失败: %w", err)
	}
	// 4. 解析 routing 与 skills（version.RoutingJson / version.SkillsJson）
	routing := map[string]string{}
	if len(version.RoutingJson) > 0 {
		_ = json.Unmarshal(version.RoutingJson, &routing) // 容错：非法 routing 退化为空映射
	}
	skills, skillRelPaths, err := s.presignSkills(ctx, version)
	if err != nil {
		return BootstrapResult{}, err
	}
	// 5. 组装 AppInputData 并渲染 manifest / persona / platform
	in := hermes.AppInputData{
		AppID: app.ID, AppName: app.Name, Model: version.MainModel,
		OpenAIAPIKey: string(apiKeyPlain), OpenAIBaseURL: s.cfg.NewAPIBaseURL,
		KnowledgeRuntimeBaseURL: s.cfg.KnowledgeBaseURL, KnowledgeAppToken: string(controlToken),
		PersonaText: version.SystemPrompt, PlatformRule: s.cfg.PlatformPrompt,
		Routing: routing, SkillRelPaths: skillRelPaths,
		OrgName: org.Name, OwnerName: owner.DisplayName,
	}
	manifestYAML, persona, platform, err := s.renderer.Render(in)
	if err != nil {
		return BootstrapResult{}, err
	}
	// 6. restore 预签名（对象存在才给 URL）
	restore, err := s.presignRestore(ctx, app.ID)
	if err != nil {
		return BootstrapResult{}, err
	}
	// 7. STS 写凭证（限定到 apps/<id>/）
	prefix := storage.AppPrefix(app.ID)
	creds, err := s.sts.AssumeAppRole(ctx, prefix, s.cfg.PresignTTL)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("签发 STS 凭证失败: %w", err)
	}
	return BootstrapResult{
		ManifestYAML: manifestYAML, Persona: persona, PlatformRule: platform,
		Skills: skills, Restore: restore,
		S3Write: BootstrapS3Write{
			Endpoint: s.cfg.Endpoint, Region: s.cfg.Region, Bucket: s.cfg.Bucket, Prefix: prefix,
			AccessKeyID: creds.AccessKeyID, SecretAccessKey: creds.SecretAccessKey,
			SessionToken: creds.SessionToken, ExpiresAt: creds.ExpiresAt,
		},
	}, nil
}
```

- [ ] **Step 4: 实现 presignSkills / presignRestore 辅助**

```go
// skillEntry 是 version.SkillsJson 中单条 skill 的最小视图。
type skillEntry struct {
	Name     string `json:"name"`
	FilePath string `json:"file_path"` // S3 key（迁移后语义）
}

// presignSkills 解析 version.SkillsJson，为每个 skill 生成预签名 URL 与 manifest 相对路径。
func (s *BootstrapService) presignSkills(ctx context.Context, version sqlc.AssistantVersion) ([]BootstrapSkill, []string, error) {
	if len(version.SkillsJson) == 0 {
		return nil, nil, nil
	}
	var entries []skillEntry
	if err := json.Unmarshal(version.SkillsJson, &entries); err != nil {
		return nil, nil, fmt.Errorf("解析 skills_json 失败: %w", err)
	}
	var out []BootstrapSkill
	var relPaths []string
	for _, e := range entries {
		url, err := s.skills.PresignSkill(ctx, e.FilePath, s.cfg.PresignTTL)
		if err != nil {
			return nil, nil, fmt.Errorf("预签名 skill %s 失败: %w", e.Name, err)
		}
		rel := path.Join("resources", "skills", e.Name+".tar")
		out = append(out, BootstrapSkill{Name: e.Name, RelPath: rel, URL: url})
		relPaths = append(relPaths, rel)
	}
	return out, relPaths, nil
}

// presignRestore 对存在的 workspace/state.db/sessions 对象给出预签名 URL；不存在则留空（首启）。
func (s *BootstrapService) presignRestore(ctx context.Context, appID string) (BootstrapRestore, error) {
	var r BootstrapRestore
	type item struct {
		key string
		dst *string
	}
	items := []item{
		{storage.WorkspaceKey(appID), &r.WorkspaceURL},
		{storage.StateDBKey(appID), &r.StateDBURL},
		{storage.SessionsKey(appID), &r.SessionsURL},
	}
	for _, it := range items {
		exists, err := s.objects.ObjectExists(ctx, it.key)
		if err != nil {
			return BootstrapRestore{}, fmt.Errorf("查询 restore 对象 %s 失败: %w", it.key, err)
		}
		if !exists {
			continue
		}
		url, err := s.objects.PresignGet(ctx, it.key, s.cfg.PresignTTL)
		if err != nil {
			return BootstrapRestore{}, fmt.Errorf("预签名 restore %s 失败: %w", it.key, err)
		}
		*it.dst = url
	}
	return r, nil
}
```

> **实现者注意（字段类型已核实）**：`sqlc.AssistantVersion.RoutingJson` 与 `.SkillsJson` 均为 `json.RawMessage`（故 `len(x)>0` 判空 + `json.Unmarshal(x, &dst)` 写法正确）；`.SystemPrompt`/`.MainModel` 为 `string`；`sqlc.Organization.Name` 为 `string`；`sqlc.User.DisplayName` 为 `string`。`app.VersionID`/`app.NewapiKeyCiphertext`/`app.RuntimeTokenCiphertext` 为 `null.String`（用 `.Valid` 判空、`.String` 取值）。

- [ ] **Step 5: 编译**

Run: `go build ./internal/service/`
Expected: 通过（按上一条注意修正字段名/类型）。

- [ ] **Step 6: Commit**

```bash
git add internal/service/bootstrap_service.go
git commit -F - <<'EOF'
feat(service): 实现 BootstrapService 组装逻辑

Build 按 appID 只读组装：解密 api_key 与 control token（缺失→ErrAppNotReady）、
查 org/owner/version、解析 routing/skills_json、复用 hermes.BuildManifest 渲染
manifest 与 persona/platform 文本、对存在的 workspace/state.db/sessions 给出
预签名 restore URL（首启留空）、签发限定到 apps/<id>/ 的 STS 临时写凭证。
defaultManifestRenderer 封装 hermes 渲染函数便于单测替换。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 11: BootstrapService 单测

**Files:**
- Create: `internal/service/bootstrap_service_test.go`

- [ ] **Step 1: 写单测（fake store / objects / sts / skills）**

```go
package service

import (
	"context"
	"testing"
	"time"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/storage"
	"oc-manager/internal/store/sqlc"
)

// --- fakes ---

// fakeBootstrapStore 实现 bootstrapStore，返回预置的 app/org/owner/version。
type fakeBootstrapStore struct {
	app     sqlc.App
	org     sqlc.Organization
	owner   sqlc.User
	version sqlc.AssistantVersion
}

func (f *fakeBootstrapStore) GetApp(_ context.Context, _ string) (sqlc.App, error) { return f.app, nil }
func (f *fakeBootstrapStore) GetAppByRuntimeTokenHash(_ context.Context, _ string) (sqlc.App, error) {
	return f.app, nil
}
func (f *fakeBootstrapStore) GetOrganization(_ context.Context, _ string) (sqlc.Organization, error) {
	return f.org, nil
}
func (f *fakeBootstrapStore) GetUser(_ context.Context, _ string) (sqlc.User, error) {
	return f.owner, nil
}
func (f *fakeBootstrapStore) GetAssistantVersion(_ context.Context, _ string) (sqlc.AssistantVersion, error) {
	return f.version, nil
}

// fakeSTS 返回固定临时凭证，并记录被限定的 prefix。
type fakeSTS struct{ gotPrefix string }

func (f *fakeSTS) AssumeAppRole(_ context.Context, prefix string, _ time.Duration) (storage.TempCredentials, error) {
	f.gotPrefix = prefix
	return storage.TempCredentials{AccessKeyID: "AK", SecretAccessKey: "SK", SessionToken: "ST",
		ExpiresAt: time.Unix(1000, 0)}, nil
}

// fakeSkills 返回固定预签名 URL。
type fakeSkills struct{}

func (fakeSkills) PresignSkill(_ context.Context, relPath string, _ time.Duration) (string, error) {
	return "https://presigned/" + relPath, nil
}

// 复用 Task 7 的 fakeObjectStore（同 package），这里默认对象不存在（首启）。

// helper：构造带 api_key/control token 密文的 app。
func newBootstrapApp(t *testing.T, cipher *auth.Cipher) sqlc.App {
	t.Helper()
	keyCt, err := cipher.Encrypt([]byte("sk-test"))
	require.NoError(t, err)
	tokCt, err := cipher.Encrypt([]byte("control-tok"))
	require.NoError(t, err)
	return sqlc.App{
		ID: "a1", OrgID: "o1", OwnerUserID: "u1", Name: "demo",
		NewapiKeyCiphertext:    null.StringFrom(keyCt),
		RuntimeTokenCiphertext: null.StringFrom(tokCt),
		RuntimeTokenHash:       null.StringFrom(HashAppRuntimeToken("control-tok")),
		VersionID:              null.StringFrom("v1"),
	}
}

// TestBootstrapBuildHappyPath 验证正常组装：manifest 含 api_key、STS prefix 限定到本 app、首启 restore 为空。
func TestBootstrapBuildHappyPath(t *testing.T) {
	cipher := newRuntimeTokenTestCipher(t) // 复用 app_runtime_token_test.go 的 helper
	app := newBootstrapApp(t, cipher)
	store := &fakeBootstrapStore{
		app: app,
		org: sqlc.Organization{ID: "o1", Name: "Org"},
		// owner.DisplayName 字段名以实际 models.go 为准
		version: sqlc.AssistantVersion{ID: "v1", MainModel: "gpt-x", SystemPrompt: "you are bot"},
	}
	sts := &fakeSTS{}
	svc := NewBootstrapService(store, cipher, newFakeObjectStore(), sts, fakeSkills{}, BootstrapConfig{
		Endpoint: "http://minio:9000", Region: "us-east-1", Bucket: "oc-apps",
		NewAPIBaseURL: "http://new-api:3000", KnowledgeBaseURL: "http://manager/runtime",
		PlatformPrompt: "platform rule", PresignTTL: time.Minute,
	})

	res, err := svc.Build(context.Background(), app)
	require.NoError(t, err)
	// manifest 含解密后的 api_key 明文
	assert.Contains(t, res.ManifestYAML, "sk-test")
	// STS 凭证限定到本 app 前缀
	assert.Equal(t, "apps/a1/", sts.gotPrefix)
	assert.Equal(t, "apps/a1/", res.S3Write.Prefix)
	assert.Equal(t, "AK", res.S3Write.AccessKeyID)
	// 首启：fakeObjectStore 无对象，restore 全空
	assert.Empty(t, res.Restore.WorkspaceURL)
	assert.Empty(t, res.Restore.StateDBURL)
}

// TestBootstrapBuildAppNotReady 验证缺 api_key 密文时返回 ErrAppNotReady。
func TestBootstrapBuildAppNotReady(t *testing.T) {
	cipher := newRuntimeTokenTestCipher(t)
	app := sqlc.App{ID: "a1"} // 无任何密文
	svc := NewBootstrapService(&fakeBootstrapStore{app: app}, cipher,
		newFakeObjectStore(), &fakeSTS{}, fakeSkills{}, BootstrapConfig{PresignTTL: time.Minute})
	_, err := svc.Build(context.Background(), app)
	require.ErrorIs(t, err, ErrAppNotReady)
}
```

> **实现者注意**：字段名已核实（`Organization.Name`/`User.DisplayName`/`AssistantVersion.{MainModel,SystemPrompt,RoutingJson,SkillsJson}` 均存在，类型见 Task 10 注意）。`newRuntimeTokenTestCipher`（`app_runtime_token_test.go`）与 `fakeObjectStore`（Task 7 的 `s3_skill_blob_store_test.go`）已在同 package（`service`）测试文件中定义，跨 test 文件可直接复用。

- [ ] **Step 2: 跑测试**

Run: `go test ./internal/service/ -run TestBootstrapBuild -v`
Expected: PASS。

- [ ] **Step 3: Commit**

```bash
git add internal/service/bootstrap_service_test.go
git commit -F - <<'EOF'
test(service): 覆盖 BootstrapService 组装的正常路径与未就绪边界

happy path 断言 manifest 含解密 api_key、STS 凭证限定到 apps/<id>/、首启 restore
为空；未就绪用例断言缺 api_key 密文时返回 ErrAppNotReady。用内存 fake store/
objects/sts/skills 注入，不依赖真实 S3 与 DB。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

## Phase 6：bootstrap handler + 路由 + 鉴权

### Task 12: bootstrap handler 与 control token 鉴权

**Files:**
- Create: `internal/api/handlers/bootstrap.go`

> 参考 `agent.go`：handler 内联鉴权（不走 middleware）、`apierror.New(code,msg)` 返回错误、`RegisterXxxRoutes` 同文件。control token 鉴权 = 取 Bearer → `service.HashAppRuntimeToken` → `GetAppByRuntimeTokenHash` 反查 → 校验 path `{id}` 与 token 所属 app 一致。

- [ ] **Step 1: 写 handler**

```go
package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// BootstrapAppService 是 bootstrap handler 所需的服务能力（窄接口，便于测试注入）。
type BootstrapAppService interface {
	// ResolveByControlToken 用 control token hash 反查 app；用于鉴权即定位。
	ResolveByControlToken(ctx context.Context, tokenHash string) (sqlc.App, error)
	// Build 组装 bootstrap 响应。
	Build(ctx context.Context, app sqlc.App) (service.BootstrapResult, error)
}

// BootstrapHandler 处理 pod 启动回调 GET /internal/apps/{id}/bootstrap。
type BootstrapHandler struct {
	service BootstrapAppService
}

// NewBootstrapHandler 构造 bootstrap handler。
func NewBootstrapHandler(svc BootstrapAppService) *BootstrapHandler {
	return &BootstrapHandler{service: svc}
}

// RegisterBootstrapRoutes 注册内部路由 /internal/apps/:id/bootstrap。
// 该组不挂用户鉴权中间件，由 handler 内联校验 control token。
func RegisterBootstrapRoutes(router gin.IRouter, handler *BootstrapHandler) {
	group := router.Group("/internal")
	group.GET("/apps/:id/bootstrap", handler.Bootstrap)
}

// Bootstrap 校验 control token 并返回组装结果。
func (h *BootstrapHandler) Bootstrap(c *gin.Context) {
	token, ok := bootstrapBearer(c.GetHeader("Authorization"))
	if !ok {
		c.JSON(http.StatusUnauthorized, apierror.New("UNAUTHORIZED", "缺少 control token"))
		return
	}
	app, err := h.service.ResolveByControlToken(c.Request.Context(), service.HashAppRuntimeToken(token))
	if err != nil {
		// token 无效 / 查无此 app 一律按未授权处理，不泄露 app 是否存在
		c.JSON(http.StatusUnauthorized, apierror.New("UNAUTHORIZED", "control token 无效"))
		return
	}
	// 校验 path id 与 token 所属 app 一致，防止持 A 的 token 拉 B 的配置
	if app.ID != c.Param("id") {
		c.JSON(http.StatusUnauthorized, apierror.New("UNAUTHORIZED", "control token 与目标 app 不匹配"))
		return
	}
	res, err := h.service.Build(c.Request.Context(), app)
	if err != nil {
		if errors.Is(err, service.ErrAppNotReady) {
			c.JSON(http.StatusConflict, apierror.New("APP_NOT_READY", "app 未就绪"))
			return
		}
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "bootstrap 组装失败"))
		return
	}
	c.JSON(http.StatusOK, res)
}

// bootstrapBearer 从 Authorization header 提取 Bearer token（scheme 大小写不敏感）。
func bootstrapBearer(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	return token, ok && strings.EqualFold(scheme, "Bearer") && token != ""
}
```

> **实现者注意**：`HashAppRuntimeToken` 在 `service` 包已导出（首字母大写）。确认 `apierror.New` 的 import 路径（agent.go 用的同一个）。`ResolveByControlToken` 在 service 侧实现（Task 13 Step 1）。

- [ ] **Step 2: 编译**

Run: `go build ./internal/api/handlers/`
Expected: 通过。

- [ ] **Step 3: Commit**

```bash
git add internal/api/handlers/bootstrap.go
git commit -F - <<'EOF'
feat(api): 新增 bootstrap handler 与 control token 内联鉴权

GET /internal/apps/:id/bootstrap：从 Bearer 取 control token、按 hash 反查 app
（复用 service.HashAppRuntimeToken + ResolveByControlToken）、校验 path id 与
token 所属 app 一致（防跨 app 拉配置），再调 BootstrapService.Build。错误映射：
缺/无效 token→401、app 未就绪→409、组装失败→500。鉴权内联（参考 agent handler），
/internal 组不挂用户鉴权中间件。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 13: ResolveByControlToken + 路由装配

**Files:**
- Modify: `internal/service/bootstrap_service.go`
- Modify: `internal/api/router.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: BootstrapService 实现 ResolveByControlToken**

在 `bootstrap_service.go` 追加方法：
```go
// ResolveByControlToken 用 control token hash 反查 app（鉴权即定位）。
func (s *BootstrapService) ResolveByControlToken(ctx context.Context, tokenHash string) (sqlc.App, error) {
	return s.store.GetAppByRuntimeTokenHash(ctx, tokenHash)
}
```

- [ ] **Step 2: router.go 加 Dependencies 字段 + 注册**

在 `Dependencies` struct 末尾加：
```go
	// BootstrapService 提供 pod 启动回调（/internal/apps/:id/bootstrap）；nil 时不注册。
	BootstrapService handlers.BootstrapAppService
```

在 `NewRouter` 的 agent 组附近（`/internal` 与 agent 一样不挂用户鉴权）追加注册——放在 `if dep.KnowledgeService != nil {...}` 之后、`user := router.Group("")` 之前：
```go
	if dep.BootstrapService != nil {
		handlers.RegisterBootstrapRoutes(router, handlers.NewBootstrapHandler(dep.BootstrapService))
	}
```

- [ ] **Step 3: main.go 装配 BootstrapService**

在 `cmd/server/main.go` 装配区（S3 store 构造之后，需 `storage.S3Config` 已构造）追加：
```go
	// bootstrap 服务仅在启用 S3 时装配（依赖对象存储与 STS）。
	if cfg.Storage.S3.Enabled {
		s3cfg := storage.S3Config{ /* 同 Task 7 装配的 s3cfg；可提取为局部变量复用 */ }
		objStore := storage.NewS3ObjectStore(s3cfg)
		stsIssuer := storage.NewSTSCredentialIssuer(s3cfg)
		// skillPresigner：S3SkillBlobStore 同时实现 SkillBlobStore 与 SkillPresigner
		bootstrapSvc := service.NewBootstrapService(
			dbStore.Queries,           // 实现 bootstrapStore（GetApp/GetAppByRuntimeTokenHash/GetOrganization/GetUser/GetAssistantVersion）
			cipher,                    // *auth.Cipher
			objStore, stsIssuer,
			skillBlobs.(service.SkillPresigner), // S3SkillBlobStore 实现 SkillPresigner
			service.BootstrapConfig{
				Endpoint: cfg.Storage.S3.Endpoint, Region: cfg.Storage.S3.Region, Bucket: cfg.Storage.S3.Bucket,
				NewAPIBaseURL: cfg.NewAPI.BaseURL, KnowledgeBaseURL: cfg.Hermes.ManagerRuntimeBaseURL,
				PlatformPrompt: cfg.Hermes.SystemPromptTemplate, PresignTTL: time.Duration(cfg.Storage.S3.PresignTTL),
			},
		)
		deps.BootstrapService = bootstrapSvc
	}
```

> **实现者注意**：
> - `dbStore.Queries`（sqlc.Queries）须满足 `bootstrapStore` 接口——已核实 `GetApp`（apps.sql:16）/`GetUser`（users.sql:14）/`GetAssistantVersion`（assistant_versions.sql:9）/`GetOrganization`（organizations.sql:25）/`GetAppByRuntimeTokenHash`（apps.sql:71）查询**均已存在**，sqlc.Queries 直接满足接口，无需新增查询。
> - `cipher`、`dbStore`、`skillBlobs` 用现有装配变量名；`NewAPIBaseURL`/`KnowledgeBaseURL`/`PlatformPrompt` 的 config 路径以实际 `config.go` 字段为准（调研：Hermes.ManagerRuntimeBaseURL、Hermes.SystemPromptTemplate 存在）。
> - 把 Task 7 与本任务的 `s3cfg`/`objStore` 合并构造，避免重复。

- [ ] **Step 4: 编译**

Run: `go build ./...`
Expected: 通过（按上面注意补齐缺失查询/字段）。

- [ ] **Step 5: Commit**

```bash
git add internal/service/bootstrap_service.go internal/api/router.go cmd/server/main.go
git commit -F - <<'EOF'
feat(api): 装配 bootstrap 路由与服务

BootstrapService 实现 ResolveByControlToken（按 control token hash 反查 app）。
router 的 Dependencies 增加 BootstrapService，注册 /internal 组（不挂用户鉴权）。
main 在启用 S3 时构造 ObjectStore/STSIssuer 并装配 BootstrapService，复用
S3SkillBlobStore 作为 skill 预签名源。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 14: bootstrap handler 单测

**Files:**
- Create: `internal/api/handlers/bootstrap_test.go`

- [ ] **Step 1: 写 handler 单测**

```go
package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
)

// fakeBootstrapAppService 实现 BootstrapAppService，按预置 token→app 映射与构建结果应答。
type fakeBootstrapAppService struct {
	app       sqlc.App
	resolveErr error
	buildErr   error
}

func (f *fakeBootstrapAppService) ResolveByControlToken(_ context.Context, _ string) (sqlc.App, error) {
	if f.resolveErr != nil {
		return sqlc.App{}, f.resolveErr
	}
	return f.app, nil
}
func (f *fakeBootstrapAppService) Build(_ context.Context, _ sqlc.App) (service.BootstrapResult, error) {
	if f.buildErr != nil {
		return service.BootstrapResult{}, f.buildErr
	}
	return service.BootstrapResult{ManifestYAML: "app:\n  id: a1\n"}, nil
}

// newBootstrapTestRouter 构造仅挂 bootstrap 路由的 gin engine。
func newBootstrapTestRouter(svc BootstrapAppService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterBootstrapRoutes(r, NewBootstrapHandler(svc))
	return r
}

// TestBootstrapMissingToken 验证缺 Bearer token 返回 401。
func TestBootstrapMissingToken(t *testing.T) {
	r := newBootstrapTestRouter(&fakeBootstrapAppService{app: sqlc.App{ID: "a1"}})
	req := httptest.NewRequest(http.MethodGet, "/internal/apps/a1/bootstrap", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestBootstrapTokenAppMismatch 验证 token 所属 app 与 path id 不一致返回 401。
func TestBootstrapTokenAppMismatch(t *testing.T) {
	// token 反查到 a1，但请求 path 为 a2，必须拒绝（防跨 app 拉配置）
	r := newBootstrapTestRouter(&fakeBootstrapAppService{app: sqlc.App{ID: "a1"}})
	req := httptest.NewRequest(http.MethodGet, "/internal/apps/a2/bootstrap", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestBootstrapNotReady 验证 app 未就绪返回 409。
func TestBootstrapNotReady(t *testing.T) {
	r := newBootstrapTestRouter(&fakeBootstrapAppService{app: sqlc.App{ID: "a1"}, buildErr: service.ErrAppNotReady})
	req := httptest.NewRequest(http.MethodGet, "/internal/apps/a1/bootstrap", nil)
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
}

// TestBootstrapHappyPath 验证正常返回 200 与 manifest 内容。
func TestBootstrapHappyPath(t *testing.T) {
	r := newBootstrapTestRouter(&fakeBootstrapAppService{app: sqlc.App{ID: "a1"}})
	req := httptest.NewRequest(http.MethodGet, "/internal/apps/a1/bootstrap", nil)
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "manifest_yaml")
}

// TestBootstrapResolveError 验证 token 反查失败返回 401（不泄露 app 是否存在）。
func TestBootstrapResolveError(t *testing.T) {
	r := newBootstrapTestRouter(&fakeBootstrapAppService{resolveErr: errors.New("no rows")})
	req := httptest.NewRequest(http.MethodGet, "/internal/apps/a1/bootstrap", nil)
	req.Header.Set("Authorization", "Bearer bad")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
```

- [ ] **Step 2: 跑测试**

Run: `go test ./internal/api/handlers/ -run TestBootstrap -v`
Expected: PASS（5 个用例）。

- [ ] **Step 3: Commit**

```bash
git add internal/api/handlers/bootstrap_test.go
git commit -F - <<'EOF'
test(api): 覆盖 bootstrap handler 鉴权与错误映射

用 httptest + fake service 覆盖：缺 token→401、token 与 path app 不匹配→401、
token 反查失败→401（不泄露存在性）、app 未就绪→409、正常→200 且返回 manifest_yaml。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

## Phase 7：token 语义升级

### Task 15: runtime_token 注释升级为 control token

**Files:**
- Modify: `internal/service/app_runtime_token.go`
- Modify: `internal/store/queries/apps.sql`（仅注释）

> 本 spec **不重命名物理列**（避免改 MySQL 基线 + sqlc 重生成的大范围 churn，B5）；仅升级注释，明确该 token 现为 per-app **control token**，三用：bootstrap 拉配置、oc-kb 调 knowledge、manager→oc-ops 调命令。

- [ ] **Step 1: 升级 app_runtime_token.go 顶部注释**

把 `EnsureAppRuntimeToken` 的文档注释（约第 21-22 行）改为：
```go
// EnsureAppRuntimeToken 确保实例拥有 per-app control token（hash + 加密密文存 apps 表）。
// 该 token 三用（k8s 迁移后统一为一把，spec-B B5）：
//   1. pod→manager bootstrap 拉配置（/internal/apps/{id}/bootstrap 鉴权）；
//   2. pod→manager oc-kb 调 knowledge API（manifest.knowledge.app_token）；
//   3. manager→pod oc-ops 调命令（k8s Secret 的 control-token 键，由 spec-A 注入）。
// 已存在密文时优先解密复用，避免重启使旧容器内 token 失效。物理列名沿用 runtime_token_*（不重命名）。
```

- [ ] **Step 2: 升级 apps.sql 注释**

把 `SetAppRuntimeToken` 与 `GetAppByRuntimeTokenHash` 上方注释更新，说明该列现承载 control token（三用），例如在 `GetAppByRuntimeTokenHash` 上方：
```sql
-- name: GetAppByRuntimeTokenHash :one
-- 按 control token（per-app 三用：bootstrap / oc-kb / oc-ops）的 hash 反查当前 app；
-- 不允许请求方传入目标 app/dataset，鉴权即定位。
```

- [ ] **Step 3: 确认无需重生成 + 跑相关测试**

Run: `go test ./internal/service/ -run TestEnsure -v && make openapi-check`
Expected: 现有 runtime token 测试 PASS；openapi-check 干净（仅改注释，无产物变化）。

> 若改 .sql 注释会触发 sqlc 重生成（注释进生成代码），跑项目的 sqlc 生成命令并把生成产物一并提交；否则只提交两个源文件。

- [ ] **Step 4: Commit**

```bash
git add internal/service/app_runtime_token.go internal/store/queries/apps.sql
git commit -F - <<'EOF'
docs(service): runtime_token 注释升级为 per-app control token（三用）

k8s 迁移后该 token 统一为一把 control token（spec-B B5）：bootstrap 拉配置、
oc-kb 调 knowledge、manager→oc-ops 调命令。仅升级 app_runtime_token.go 与
apps.sql 注释说明语义，不重命名物理列（避免基线 + sqlc 重生成的大范围改动）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

## Phase 8：契约文档与收尾

### Task 16: bootstrap HTTP 契约文档

**Files:**
- Create: `docs/bootstrap-http-contract.md`

- [ ] **Step 1: 写契约文档**

写 `docs/bootstrap-http-contract.md`，内容覆盖（参照 `docs/ocops-http-contract.md` 风格）：
- 端点：`GET /internal/apps/{id}/bootstrap`，`Authorization: Bearer <control token>`。
- 鉴权：control token hash 反查 app + path id 一致性校验；失败 401（不泄露存在性）。
- 错误码映射表：401 UNAUTHORIZED / 409 APP_NOT_READY / 500 INTERNAL / 502 STORAGE_UNAVAILABLE（若实现区分了 STS/S3 故障则补）。
- 响应体 schema：`manifest_yaml`、`persona`、`platform_rule`、`skills[]{name,rel_path,url}`、`restore{workspace_url?,state_db_url?,sessions_url?}`、`s3_write{endpoint,region,bucket,prefix,access_key_id,secret_access_key,session_token,expires_at}`。
- 语义约定：只读无副作用、幂等可重调（STS 续期）；首启时 restore 字段省略；api_key 不落 S3/盘。
- spec-A 对齐点：control token 经 k8s Secret（`app-<id>-token` 的 `control-token` 键）注入 pod env；initContainer 用它调本端点；sidecar 用 `s3_write` 凭证 `mc mirror`；endpoint/bucket 来自 manager 配置。

- [ ] **Step 2: Commit**

```bash
git add docs/bootstrap-http-contract.md
git commit -F - <<'EOF'
docs(bootstrap): 新增 bootstrap HTTP 契约文档

固定 GET /internal/apps/{id}/bootstrap 的鉴权、错误码映射、响应体 schema
（manifest_yaml/persona/platform_rule/skills/restore/s3_write）与只读幂等语义，
并标注 spec-A 对齐点（control token Secret 注入、initContainer/sidecar 消费方式）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
```

---

### Task 17: 全量测试与交付校验

**Files:** 无（校验任务）

- [ ] **Step 1: 全量单测**

Run: `go test ./internal/... ./cmd/...`
Expected: 全绿（集成测 `s3_integration_test.go` 在无 MinIO 环境变量时 Skip）。

- [ ] **Step 2: 编译 + vet**

Run: `go build ./... && go vet ./internal/... ./cmd/...`
Expected: 无错误。

- [ ] **Step 3: openapi 同步校验**

Run: `make openapi-check`
Expected: 工作区干净（bootstrap 是 /internal 无 swag 注解端点，不进 openapi）。

- [ ] **Step 4: 真实 MinIO 集成测（本地 k3d，交付前必跑）**

Run（按本地 MinIO 凭证）：
```bash
OC_S3_TEST_ENDPOINT=<minio-endpoint> OC_S3_TEST_BUCKET=oc-apps \
OC_S3_TEST_AK=<ak> OC_S3_TEST_SK=<sk> OC_S3_TEST_STS_ROLE=<role-arn> \
go test ./internal/integrations/storage/ -run 'TestS3RoundTrip|TestSTSPrefixIsolation' -v
```
Expected: PASS。**若 MinIO STS 未就绪导致 TestSTSPrefixIsolation 失败，在交付说明记录实际结果与所需 MinIO STS 配置，不可伪造通过；prefix 隔离是安全关键，须真实验证。**

- [ ] **Step 5: 确认工作区无混入文件**

Run: `git status --short`
Expected: 仅本计划相关的已提交改动；不含未跟踪的 `docs/reports/` 被误加。

---

## 验证范围说明（B6，写入交付）

spec-B 是纯 manager 侧数据层：bootstrap 端点 / token / manifest 渲染用 Go 单测；S3 抽象层（上传/预签名/STS prefix 隔离）对真实 MinIO 集成测。**pod 完整闭环**（initContainer 真去调 bootstrap、sidecar 真用 STS 同步）与**三角色真实浏览器验证**在 spec-A 把数据层接进 pod 编排后，与 A/B/D/E 一起做。本 spec 不单独宣称「pod 闭环已验证可用」——这是对项目「真实环境验证」要求的一次显式、有界偏离（与 spec-E E4 同性质）。

---

## 待 spec-A 衔接点（本计划不做，契约已钉）

- pod spec 渲染：initContainer 调 bootstrap、写 emptyDir、预签名 restore；sidecar `mc mirror` + sqlite `.backup`（用 `s3_write` 凭证 + prefix）。
- k8s Secret 注入 control token（`app-<id>-token` 的 `control-token` 键）。
- 创建流程在建 pod 前 ensure api_key + control token（bootstrap 假设其已就绪）。
- 删除 runtime-agent file API、节点概念、`WriteAppInput` 写卷旧路径、按需删 FSSkillBlobStore。
- `OcOpsResolver` 真实 Service DNS 寻址（spec-E 占位）。
- A/B/D/E 合并后端到端 + 三角色真实浏览器验证（吸收 B6 推迟项）。
