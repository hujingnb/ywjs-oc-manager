// Package service：第三方市场 skill 归档的通用读穿（read-through）缓存。
package service

import (
	"context"
	"fmt"
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
//   - 读缓存出错（对象不存在 / 对象存储抖动）一律降级为未命中、回源，绝不因缓存读异常让请求失败。
//   - 写缓存行为由调用方按场景选择：Fetch 写失败 best-effort 忽略；FetchAndPersist 写失败返回错误。
type SkillArchiveCache struct {
	// blobs 是归档对象存储，读写共享 library/ 前缀。
	blobs LibraryBlobStore
}

// NewSkillArchiveCache 构造读穿缓存，包装一个 LibraryBlobStore。
func NewSkillArchiveCache(blobs LibraryBlobStore) *SkillArchiveCache {
	return &SkillArchiveCache{blobs: blobs}
}

// fetchInternal 是读穿实现：先读缓存（命中即返回）；未命中回源、写回。
// persistWrite 决定写缓存失败的处理：
//   - true：写失败返回错误——供「relPath 会被持久化、下游(bootstrap 下发 / Reinstall)强依赖该对象存在」的安装/版本场景；
//   - false：写失败 best-effort 忽略、仍返回回源字节——供「relPath 不被持久化、缓存仅作加速」的临时取数（如市场下载）。
func (c *SkillArchiveCache) fetchInternal(
	ctx context.Context,
	source, ref, version, ext string,
	persistWrite bool,
	fetch func(ctx context.Context) ([]byte, error),
) (data []byte, relPath string, err error) {
	key := storage.LibrarySkillKey(source, ref, version, ext)
	// 1) 读缓存：命中即返回。任何读失败（不存在 / 抖动 / (nil,nil)）都降级为未命中，继续回源。
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
	// 3) 写回缓存。
	written, werr := c.blobs.PutLibrarySkill(source, ref, version, ext, fetched)
	if werr != nil {
		if persistWrite {
			// relPath 会被持久化、下游强依赖该对象存在：写失败必须上报，
			// 避免留下指向空对象的 cached_tar_path（否则 pod 重启下发 / Reinstall 会失败）。
			return nil, "", fmt.Errorf("写回 skill 归档缓存失败: %w", werr)
		}
		// 仅加速、relPath 不被持久化：写失败不阻断，返回回源字节与确定性 key。
		return fetched, key, nil
	}
	return fetched, written, nil
}

// Fetch 读穿取归档，写回 best-effort（写失败不阻断、缓存非硬依赖）。
// 供「relPath 不被持久化、缓存仅作加速」的临时取数（如市场下载）使用。
func (c *SkillArchiveCache) Fetch(
	ctx context.Context,
	source, ref, version, ext string,
	fetch func(ctx context.Context) ([]byte, error),
) (data []byte, relPath string, err error) {
	return c.fetchInternal(ctx, source, ref, version, ext, false, fetch)
}

// FetchAndPersist 读穿取归档，写回必须成功（写失败返回错误）。
// 供安装/版本等「relPath 会被持久化、下游(bootstrap 下发 / Reinstall)强依赖该对象存在」的场景使用。
func (c *SkillArchiveCache) FetchAndPersist(
	ctx context.Context,
	source, ref, version, ext string,
	fetch func(ctx context.Context) ([]byte, error),
) (data []byte, relPath string, err error) {
	return c.fetchInternal(ctx, source, ref, version, ext, true, fetch)
}
