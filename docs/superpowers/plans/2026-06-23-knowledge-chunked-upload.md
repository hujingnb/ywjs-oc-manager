# 企业知识库分片上传（chunked upload）设计

> 状态：**已实现并本地浏览器全链路验证通过（2026-06-23）**。org 级 77MB PDF 走分片
> （init→10 片×204→complete 202），文件入列、解析推进至「解析中」，MinIO 暂存与 Redis
> 会话均已清理。实现踩坑：aws-sdk-go-v2 UploadPart 需可 seek body 算 SigV4 哈希，已在
> storage.UploadPart 把分片先读入内存再上传。

## 背景与约束

线上大文件知识库上传失败（实测 77MB PDF）。根因排查结论：

- manager 对上传 body 限速 **512KB/s**（`transfer_limit.upload_bytes_per_sec=524288`），
  **不能调高**——线上带宽不够，限速是带宽保护手段。
- 公网入口（nginx 前面那层 LB/WAF）有 **~48s 超时**，在 ocm namespace 之外、kubectl 够不到、
  不可控（nginx 本身已放到 600s 仍不够）。
- 77MB ÷ 512KB/s ≈ 155s ≫ 48s，**单个 HTTP 请求一次传完大文件必然超时**。

三者中带宽不能动、外层超时不可控，所以只能破「单请求一次传完」这一条：**改成分片上传**。

## 目标

1. 大文件（数十 MB ~ 接近 1GB 宣称上限）知识库上传可靠成功。
2. **守住带宽**：顺序逐片上传，聚合速率仍 ≤512KB/s（每片请求仍走现有限速）。
3. **不依赖任何代理超时**：每个分片请求短（8MB ÷ 512KB/s ≈ 16s ≪ 48s）。
4. 小文件保持原直传路径，向后兼容、零回归。

## 地基（已只读核实）

| 设施 | 现状 | 用途 |
|---|---|---|
| S3（aws-sdk-go-v2，`internal/integrations/storage`） | 线上启用，移动云 EOS bucket `ywjs-ocm`；本地 MinIO bucket `oc-apps` | 分片暂存 + 服务端合并 |
| Redis（`internal/redis`，前缀 `ocm:`） | 线上可用 | 跨副本保存分片会话状态 |
| manager 副本数 | 线上 **2**、本地 1 | 决定会话状态必须放 Redis 而非进程内 |
| 限速 `limitUploadBody` | 现成 | 每个分片请求复用 |
| RAGFlow `UploadDocument` | 把整文件读进 `bytes.Buffer` 构 multipart，http 超时 30s | 合并后这一段需改流式 + 提超时 |

## 方案概述

走 **S3 原生 multipart upload** 暂存 + 服务端合并，完成时**流式**推给 RAGFlow：

```
前端切 8MB 片
  │  init
  ▼
POST .../knowledge/uploads {filename,size}
  → manager: S3 CreateMultipartUpload(bucket=ywjs-ocm, key=kb-uploads/<uploadId>/<filename>)
  → Redis 存 {uploadId → s3UploadId,key,orgId,filename,size,parts{}}（TTL 24h）
  → 返回 {uploadId, partSize}
  │  顺序逐片（限速 512KB/s，每片 ~16s）
  ▼
PUT .../uploads/:uploadId/parts/:partNumber  (octet-stream, ≤8MB)
  → manager: S3 UploadPart → 拿 ETag → Redis HSET parts[partNumber]=etag
  │  complete
  ▼
POST .../uploads/:uploadId/complete
  → S3 CompleteMultipartUpload(按 partNumber 排序的 ETag 列表)
  → 流式 GetObject(key) → RAGFlow UploadDocument(streaming, io.Pipe)
  → 建 ragflow_documents 行 + 触发 parse（复用现有 uploadToDataset 后半段）
  → 删除 staging 对象 + Redis key
  → 202 {KnowledgeDocumentResult}
  │  失败/取消
  ▼
DELETE .../uploads/:uploadId → S3 AbortMultipartUpload + 删 Redis key
```

为什么用 S3 multipart 而不是「本地盘暂存」或「多个小对象再拼」：

- 本地盘：2 副本下分片散落不同 pod，需 sticky session（且入口不全归我们），脆弱 → 否决。
- 多小对象 + 自己拼：可行但要自管顺序与清理；S3 multipart 由对象存储服务端合并、一次
  Complete 搞定、SDK 原生支持，更省事。
- S3 分片最小 5MB（除最后一片）→ 选 **8MB** 满足约束且每片 ~16s 远小于 48s。

## 后端设计

### 新接口（org 与 app 两套，path 对称）

```
POST   /api/v1/organizations/:orgId/knowledge/uploads               初始化 → {uploadId, partSize}
PUT    /api/v1/organizations/:orgId/knowledge/uploads/:uploadId/parts/:partNumber  上传分片（octet-stream）
POST   /api/v1/organizations/:orgId/knowledge/uploads/:uploadId/complete            合并+推RAGFlow → 202
DELETE /api/v1/organizations/:orgId/knowledge/uploads/:uploadId                     中止+清理
# app 级同样在 /api/v1/apps/:appId/knowledge/uploads/... 下复制
```

权限：复用知识库现有鉴权（org_admin 写组织库、app owner 写实例库），谓词仍在
`internal/auth/authorizer.go`，不在 handler 内联判断。

### 分片会话状态（Redis）

- Key：`ocm:kbupload:{uploadId}`（Hash：s3UploadId、key、bucket、orgId/appId、filename、size、createdBy、scope）。
- Key：`ocm:kbupload:{uploadId}:parts`（Hash：partNumber → ETag）。
- TTL 24h，complete/abort 时显式删除。
- init 与 complete 用现有 `RedisDistLocker` 防并发竞态（同一 uploadId 重复 complete）。

### 存储层扩展

`internal/integrations/storage` 的 `ObjectStore` 增加 multipart 方法（或新建
`MultipartUploader` 接口）：`CreateMultipartUpload / UploadPart / CompleteMultipartUpload /
AbortMultipartUpload`，基于 aws-sdk-go-v2 原生 API 实现。staging 前缀 `kb-uploads/<uploadId>/`。

### RAGFlow 推送改流式（关键，避免 OOM）

- 现 `UploadDocument` 把整文件读进 `bytes.Buffer`——1GB 文件会撑爆 manager（2 副本、内存有限）。
- 改为 `io.Pipe` + `multipart.Writer` 边读边写：`GetObject` 的 reader → pipe → RAGFlow POST，
  内存只留缓冲块。
- 这段是 manager→RAGFlow、集群内、**不受 512KB/s 限速也不受 48s 公网超时**；但要把这次调用的
  http 超时从 30s 提到足够大（按文件大小或固定上限，如 10min）——通过 per-call context 控制，
  不动全局 `ragflow.request_timeout`。

### 小文件兼容

- 前端对 **< 8MB** 的文件继续走现有 `POST .../knowledge`（octet-stream 直传），后端零改动。
- 仅 ≥8MB 走分片，避免给小文件加无谓的三次往返。

### 边界与清理

- 单文件硬上限仍 1GB（沿用 `maxKnowledgeUploadBytes`，在 init 用 size 预校验）。
- 孤儿清理：会话 TTL 到期 + 定时任务扫 `kb-uploads/` 下超期前缀 AbortMultipartUpload（可放后续）。
- 重复 complete、缺片、partNumber 不连续 → 明确 4xx 错误码。

## 前端设计（Vue3 + Pinia + Naive UI，原生 XHR）

| 文件 | 改动 |
|---|---|
| `web/src/api/fileChunking.ts`（新增） | `File.slice` 切片工具，`PART_SIZE=8MB`、`CHUNK_THRESHOLD=8MB` |
| `web/src/api/hooks/useKnowledge.ts` | `useUploadOrgKnowledge`/`app` 版：≥阈值时 init→顺序 PUT 每片（复用 `xhrUpload` 带进度+signal）→complete；失败/取消调 DELETE 清理 |
| `web/src/stores/uploadProgress.ts` | `UploadItem` 加 `uploadedBytes/totalBytes`（分片聚合进度）；单文件进度=已传片字节和÷总字节 |
| `web/src/pages/knowledge/OrgKnowledgePage.vue` / `pages/apps/AppKnowledgeTab.vue` | 接新上传流程；多文件仍串行、单文件内分片仍串行（守带宽） |
| `web/src/components/UploadProgressModal.vue` | 进度展示沿用「X MB / Y MB」，底层换成分片聚合；失败详情不变 |

取消：AbortSignal 触发后，对已 init 的会话发 DELETE 中止，避免 EOS 留半成品 multipart。

## OpenAPI / 类型同步

新 handler 改动后必须 `make openapi-gen` + `make web-types-gen`，生成物随代码一起提交
（`openapi/openapi.yaml`、`web/src/api/generated.ts`）。请求体 DTO 放
`internal/api/handlers/dto.go` 导出命名，响应用 `service.*Result`。

## 测试

- 后端单测：multipart store（CreateUploadPartComplete 往返，用 MinIO 或 mock）、session
  manager（Redis 存取/TTL/并发锁）、complete 流程（缺片报错、流式推 RAGFlow 用 fake client）。
  按项目规范用 testify、每个子测试带中文场景注释。
- 前端单测：fileChunking 切片边界（恰好整除、最后一片、<阈值不切）。
- 真机验证：本地 k3d 浏览器传 77MB PDF 成功（202、列表出现、解析状态流转）；三角色可见性沿用。

## 配置 / 部署

- 线上 S3 已启用（EOS `ywjs-ocm`）、Redis 已配——无需新增基础设施。
- ✅ **EOS multipart 已实测通过**（2026-06-23）：用线上凭证经公网端点
  `eos-beijing-2.cmecloud.cn` 跑了一次完整往返——CreateMultipartUpload / UploadPart×2
  （6MB 非末片 + 1MB 末片）/ CompleteMultipartUpload / HeadObject（合并大小 7MB 正确）/
  DeleteObject / AbortMultipartUpload 全部成功（uploadId 形如 `2~...`，EOS 为 Ceph RGW 实现）。
  测试对象已清理。**最大不确定项消除，主方案可行，无需退化备选。**
- 本地 `make local-up` 与线上发版均走现有镜像构建流程，无新增。

## 阶段拆分

1. **存储层**：ObjectStore multipart 方法 + 单测。
2. **会话 + service**：Redis 会话管理 + org/app 的 init/part/complete/abort service 方法 +
   uploadToDataset 后半段复用 + RAGFlow 流式推送改造 + 单测。
3. **handler + 路由 + DTO + OpenAPI**：四个新接口接上鉴权与限速。
4. **前端**：切片工具 + hook 分片流程 + store 进度 + 两个页面 + 类型生成。
5. **联调验证**：本地浏览器全链路；EOS multipart 兼容性实测；三角色覆盖。

## 风险 / 取舍

- ~~**EOS multipart 兼容性**是最大不确定项~~ → **已实测通过（2026-06-23）**，主方案可行；
  退化备选（多小对象 + io.MultiReader 拼接流式推 RAGFlow）暂不需要，保留作备案。
- v1 **不做断点续传**（失败即 abort 重传整文件）；分片已 ack，续传可作后续增强。
- manager→RAGFlow 内存：靠流式 io.Pipe 控制，不再全量 buffer。
- 客户端上行带宽若 < ~170KB/s，单个 8MB 片仍可能超 48s——极端弱网才会触发，可调小 PART_SIZE 缓解。
