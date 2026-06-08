# 设计文档：文件上传下载单请求限速

**日期：** 2026-06-08
**状态：** 待用户复核

## 背景

知识库文件已经支持企业库、实例库和行业库的上传下载，也支持外部系统通过固定 token 上传行业库文件。当前后端只校验单文件大小，不限制传输速率；大文件上传或下载时，单个请求可能长时间占满入口带宽，影响其他管理后台请求。

前端批量上传已经由 `uploadProgress.run()` 串行执行：会话内用 `for` 循环逐个 `await runner(...)`，单文件失败不阻塞后续文件，取消时当前文件和后续待上传文件都会标记为取消。因此本次不改变前端批量编排。

## 目标

- 对单个文件上传请求增加可配置限速，防止一个大文件上传占满入口带宽。
- 对单个文件下载请求增加可配置限速，防止一个大文件下载占满入口带宽。
- 限速只覆盖用户浏览器或外部系统到 manager-api 的链路。
- 配置未填写或配置为 `0` 时不启用限速，保持历史行为。
- 示例配置先给 `512KB/s`，即 `524288` 字节/秒。
- 保持 service 接口、前端 API 契约和 OpenAPI 路由不变。

## 非目标

- 不做 manager-api 进程级全局限速。
- 不做多副本集群级全局限速。
- 不做按用户、按企业、按 IP 的公平调度。
- 不限制 manager-api 到 RAGFlow 的内部传输。
- 不改变 RAGFlow client 的 multipart 构造方式。
- 不改前端上传串行逻辑、上传进度弹窗或下载入口。
- 不覆盖 runtime 内部知识库上传接口。

## 已确认决策

- 先做单文件/单请求限速，不做全局限速。
- 配置项不使用指针字段；未配置时 Go 零值为 `0`，表示不限速。
- 示例配置和生产 Secret 示例写入 `524288`，由部署配置显式启用。
- 覆盖企业库、实例库、行业库上传下载，以及外部行业库上传。
- 不覆盖 `/api/v1/runtime/knowledge/files`，避免影响 app pod 调 manager 的内部链路。

## 备选方案

### 方案一：handler/helper 显式限速

上传入口在传给 service 前把 `c.Request.Body` 包成限速 reader。下载入口复用统一的 `writeKnowledgeDownload`，在写响应前把文件流包成限速 reader。

优点是改动小、行为清楚、便于测试，且与现有 `prepareKnowledgeOctetStreamUpload` 和 `writeKnowledgeDownload` 两个收口点匹配。代价是每个上传入口需要显式接入上传限速。

这是本设计采用的方案。

### 方案二：Gin middleware 限速

按路由匹配包装 request body 或 response writer。

优点是接入口集中。代价是 response writer 包装容易影响状态码、错误响应和下载头处理；同时上传和下载的业务范围不同，middleware 需要更复杂的路径判断。本次不采用。

### 方案三：Ingress 或 Nginx 限速

在入口网关配置上传和下载限速。

优点是不改业务代码。代价是本地、测试和生产行为容易不一致；下载响应限速依赖网关能力与部署配置。本次不采用。

## 配置设计

在 `internal/config.Config` 增加配置段：

```yaml
transfer_limit:
  upload_bytes_per_sec: 524288
  download_bytes_per_sec: 524288
```

Go 结构：

```go
type TransferLimitConfig struct {
    UploadBytesPerSec   int64 `yaml:"upload_bytes_per_sec"`
    DownloadBytesPerSec int64 `yaml:"download_bytes_per_sec"`
}
```

字段语义：

- 单位是字节/秒。
- 未配置时为 `0`，不限速。
- 显式配置 `0`，不限速。
- 配置正数，按单请求限速。
- 配置负数，启动校验失败。

`applyDefaults()` 不填默认值，避免历史环境升级后自动启用限速。`Validate()` 只校验两个字段不能小于 `0`。

配置文档同步更新：

- `config/manager.example.yaml` 写入 `524288` 示例值。
- `deploy/k8s/prod/secret.example.yaml` 写入 `524288` 示例值。
- `docs/configuration.md` 说明单位、默认值、禁用方式和单请求粒度。

## 后端架构

新增一个小的传输限速组件，放在 `internal/api/handlers/transfer_limit.go`。它不理解知识库业务，只提供通用的 reader 包装能力。

组件使用以下结构承载归一化后的限速值：

```go
type TransferLimitConfig struct {
    UploadBytesPerSec   int64
    DownloadBytesPerSec int64
}
```

handler 持有该结构。零值表示不限速。

组件提供以下 helper：

- `limitUploadBody(c *gin.Context, bytesPerSec int64)`：当限速大于 0 时包装 `c.Request.Body`。
- `limitedReadCloser(stream io.ReadCloser, bytesPerSec int64) io.ReadCloser`：当限速大于 0 时返回限速流，否则返回原始流。

限速实现使用现有依赖 `golang.org/x/time/rate`。实现必须保持流式读取，不得把整文件读入内存。读取端按实际读取字节数等待 token，保证单请求平均速率不超过配置。

## 接入范围

上传限速接入以下入口：

- `POST /api/v1/organizations/{orgId}/knowledge`
- `POST /api/v1/apps/{appId}/knowledge`
- `POST /api/v1/industry-knowledge-bases/{industryId}/knowledge`
- `POST /api/v1/external/industry-knowledge/files`

下载限速接入以下入口：

- `GET /api/v1/organizations/{orgId}/knowledge/{documentId}/file`
- `GET /api/v1/apps/{appId}/knowledge/{documentId}/file`
- `GET /api/v1/industry-knowledge-bases/{industryId}/knowledge/{documentId}/file`

不接入以下入口：

- `POST /api/v1/runtime/knowledge/files`

## 数据流

上传流程：

```text
browser/external client
  -> manager handler
  -> Content-Length / MaxBytesReader 现有校验
  -> upload_bytes_per_sec > 0 时包装限速 reader
  -> service.Save*File / ExternalUploadIndustryFile
  -> RAGFlow UploadDocument
```

下载流程：

```text
browser
  -> manager handler
  -> service.Open*File
  -> RAGFlow DownloadDocument 返回 stream
  -> writeKnowledgeDownload 设置响应头
  -> download_bytes_per_sec > 0 时包装限速 reader
  -> io.Copy 写给浏览器
```

当前 RAGFlow `UploadDocument` 会先把读取到的内容写入内存中的 multipart buffer，再发给 RAGFlow。因此本设计会限制“客户端到 manager-api”的读取速度，但不会限制“manager-api 到 RAGFlow”的后续发送速度。这与本次确认的范围一致。

## 错误处理

- 配置负数：manager 启动校验失败，返回明确配置错误。
- 限速等待期间客户端取消请求：按现有 request context 或连接关闭路径结束，不新增业务错误码。
- 下载写响应中断：沿用 `writeKnowledgeDownload` 当前 `c.Error(err)` 行为。
- 上传超出文件大小：仍由现有 `MaxBytesReader` 和文件大小校验返回 `BAD_REQUEST`。
- 权限、容量、RAGFlow 错误：保持现有错误映射不变。

## 测试计划

配置测试：

- 未配置 `transfer_limit` 时上传和下载限速字段都是 `0`。
- 配置 `524288` 时可正确加载。
- 配置负数时 `Validate()` 返回错误。

helper/handler 测试：

- 限速为 `0` 时 reader 行为与原始 reader 一致。
- 限速为正数时上传入口会使用限速 reader。
- `writeKnowledgeDownload` 在下载限速开启时仍返回正确 body、`Content-Length` 和 `Content-Disposition`。
- 外部行业库上传入口会使用上传限速配置。
- runtime 内部知识库上传入口不使用该限速配置。

速率精度不做慢速真实时间断言，避免单测不稳定。可以通过可注入等待函数、fake limiter 或聚焦 helper 行为确认限速路径被调用。真实限速效果在本地用较小速率做一次手工验证。

## 验收标准

- 企业库、实例库、行业库上传在配置正数时按单请求限速。
- 企业库、实例库、行业库下载在配置正数时按单请求限速。
- 外部行业库上传在配置正数时按单请求限速。
- 配置缺省或配置 `0` 时行为与现有一致。
- 配置负数时 manager 启动失败。
- runtime 内部知识库上传不受影响。
- 前端批量上传仍保持串行，无需前端改动。
- 相关 Go 测试通过。
- 本次不改 OpenAPI 路由和请求响应契约，不需要 `make openapi-gen` 或 `make web-types-gen`。

## 影响范围

预计实施会修改：

- `internal/config/config.go`
- `internal/config/loader.go`
- `internal/config/loader_test.go`
- `internal/api/handlers/knowledge.go`
- `internal/api/handlers/industry_knowledge.go`
- `internal/api/handlers/transfer_limit.go`
- `internal/api/handlers/*_test.go`
- `cmd/server/main.go`
- `config/manager.example.yaml`
- `deploy/k8s/prod/secret.example.yaml`
- `docs/configuration.md`

不预计修改：

- `web/src/**`
- `openapi/openapi.yaml`
- `web/src/api/generated.ts`
- service 层接口
- RAGFlow client 接口

## 风险与约束

- 单请求限速不能限制多个并发请求的总带宽；如果多个用户同时上传或下载，总带宽仍会叠加。
- 生产 manager-api 有多个副本时，限速按每个请求生效，不是集群总带宽上限。
- RAGFlow 上传仍可能在 manager 读完整个请求后快速发送到 RAGFlow；本次不解决内部链路限速。
- 真实网络速率会受 TCP、浏览器、反向代理和 RAGFlow 响应行为影响，测试只验证 manager 侧限速逻辑。
