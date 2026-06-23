// Package storage 提供 manager 侧的标准 S3 对象存储抽象与 STS 临时凭证签发。
// 仅依赖标准 S3 协议（aws-sdk-go-v2），不绑定 MinIO 私有扩展，便于生产切换任意云 OSS。
package storage

import "path"

// app 与 version 两类数据在 S3 bucket 内的 prefix 约定（父设计 §5.4 / spec-B §4）。
// app 级数据按 appID 分前缀，sidecar 的 STS 写凭证限定到该前缀；
// version 级 skill 是 write-once，manager 上传、pod 预签名只读。
//
// 路径段合法性约定：调用方须保证 appID / versionID / skillName 为合法路径段
//（非空、不含 "/" 分隔符与 ".." 跳转），否则 path.Join 规整可能产生意外 key。
// appID / versionID 来自受控的数据库 UUID，天然满足此约束，无需在此校验。

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

// SessionsKey 返回 sessions 归档对象 key（会话归档数据，与 workspace/state.db 并列的 app 级持久化数据之一，由 sidecar 定期上传）。
func SessionsKey(appID string) string {
	return path.Join("apps", appID, "sessions")
}

// SkillKey 返回 version 级 skill tar 的 key "versions/<versionID>/skills/<name>.tar"。
// 与现有 FSSkillBlobStore 的相对路径布局一致，便于 file_path 列语义平滑迁移。
func SkillKey(versionID, skillName string) string {
	return path.Join("versions", versionID, "skills", skillName+".tar")
}

// KnowledgeUploadPrefix 返回知识库分片上传暂存前缀 "kb-uploads/<uploadID>/"。
// 分片合并完成或会话中止后由 service 层清理；末尾保留 "/" 便于前缀删除。
func KnowledgeUploadPrefix(uploadID string) string {
	return path.Join("kb-uploads", uploadID) + "/"
}

// KnowledgeUploadKey 返回知识库分片上传暂存对象 key "kb-uploads/<uploadID>/<filename>"。
// 合并后的完整对象写在该 key，随后流式推送 RAGFlow 并清理。调用方保证 filename 为合法路径段。
func KnowledgeUploadKey(uploadID, filename string) string {
	return path.Join("kb-uploads", uploadID, filename)
}

// LibrarySkillKey 返回 skill 库共享缓存对象 key：
// library/<source>/<sourceRef>/<version>.<ext>（如 library/platform/weather/1.0.tar）。
// 同一 skill 同版本被多个 app 安装时只存一份。调用方保证各段不含路径分隔符。
func LibrarySkillKey(source, sourceRef, version, ext string) string {
	return path.Join("library", source, sourceRef, version+"."+ext)
}
