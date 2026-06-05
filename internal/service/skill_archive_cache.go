// Package service：第三方市场 skill 归档的通用读穿（read-through）缓存。
package service

import (
	"context"
	"io"

	"oc-manager/internal/integrations/storage"
)

// SkillArchiveCache 是 skill 归档的读穿缓存，底层走 LibraryBlobStore（FS 本地 / S3 生产）。
//
// 设计意图：让 clawhub 及未来任意第三方市场的归档下载「成功一次即缓存、后续免回源」，
// 并扛住上游偶发抖动。缓存按 (source, ref, version) 抽象寻址，对具体来源无感知——
// 新增市场只需在自己的回源点传入一个下载闭包即可复用本缓存。
//
// 约束：
//   - 按版本永久缓存（无 TTL）：归档按版本号本应不可变，命中即长期有效。
//   - 缓存是性能优化而非硬依赖：读缓存出错（对象不存在 / 对象存储抖动）一律降级为未命中、
//     回源，绝不因缓存问题让请求失败。
type SkillArchiveCache struct {
	// blobs 是归档对象存储，读写共享 library/ 前缀。
	blobs LibraryBlobStore
}

// NewSkillArchiveCache 构造读穿缓存，包装一个 LibraryBlobStore。
func NewSkillArchiveCache(blobs LibraryBlobStore) *SkillArchiveCache {
	return &SkillArchiveCache{blobs: blobs}
}

// Fetch 取 (source, ref, version) 归档的字节与其在对象存储中的相对路径：
//  1. 先读缓存 OpenLibrarySkill(LibrarySkillKey(...))；命中即返回缓存字节（不回源）。
//  2. 读缓存出错或对象不存在 → 视为未命中，调 fetch 回源。
//  3. fetch 成功 → PutLibrarySkill 写回 → 返回 (data, relPath, nil)。
//  4. fetch 失败 → 原样返回该错误（由调用方分类映射为 502 等），不写缓存。
//
// ext 决定缓存 key 后缀（clawhub=zip，platform=tar）。回源下载与（按需）解压安全校验由
// fetch 闭包负责：校验失败时 fetch 返回错误 → 不会写入缓存，避免缓存到不安全归档。
func (c *SkillArchiveCache) Fetch(
	ctx context.Context,
	source, ref, version, ext string,
	fetch func(ctx context.Context) ([]byte, error),
) (data []byte, relPath string, err error) {
	key := storage.LibrarySkillKey(source, ref, version, ext)
	// 1) 读缓存：命中即返回。任何读失败（不存在 / 抖动）都降级为未命中，继续回源。
	//    额外守卫 rc != nil：个别 LibraryBlobStore 实现可能在「无对象」时返回 (nil, nil)，
	//    若不防护会在 io.ReadAll(nil) 处 panic；此处一并按未命中处理。
	if rc, oerr := c.blobs.OpenLibrarySkill(key); oerr == nil && rc != nil {
		cached, rerr := io.ReadAll(rc)
		_ = rc.Close()
		if rerr == nil {
			return cached, key, nil
		}
		// 读到一半失败：降级回源，不因缓存读异常中断请求。
	}
	// 2) 未命中：回源。失败原样上抛、不写缓存。
	fetched, ferr := fetch(ctx)
	if ferr != nil {
		return nil, "", ferr
	}
	// 3) 写回缓存。写失败不阻断本次请求（归档已在手）：返回确定性 key，
	//    与 Put 成功时一致——极端情况下缓存暂时缺对象，下次回源会再写。
	if written, werr := c.blobs.PutLibrarySkill(source, ref, version, ext, fetched); werr == nil {
		return fetched, written, nil
	}
	return fetched, key, nil
}
