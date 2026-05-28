package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// ClientCache 按 key（通常是 nodeID）缓存并复用「按节点构造的 client」（docker SDK client /
// file client）。
//
// 背景：manager 此前每次 docker/file 操作都新建 *http.Transport 且从不 Close，被遗弃的
// transport 因底层连接 goroutine 持有引用而不会被 GC，其空闲 keep-alive 连接（IdleConnTimeout=0
// 时永不回收）持续堆积，最终占满本机临时端口，导致主动探测报
// "connect: cannot assign requested address"。复用同一 client 让连接池真正生效，把每节点常驻
// 连接数收敛到 MaxIdleConnsPerHost 级别。
//
// 失效策略：调用方传入 (endpoint/token/CA) 派生的指纹；指纹变化（节点 re-register、token 轮换、
// CA 重签）时关闭旧 client 再重建，避免复用到过期配置。
type ClientCache[T any] struct {
	// mu 保护 entries；构造在锁内进行，详见 Get 注释。
	mu      sync.Mutex
	entries map[string]cacheEntry[T]
	// closeFn 在条目被新配置替换时释放旧 client 持有的连接：
	// docker client 传 Close()，file client 传 HTTPClient.CloseIdleConnections()。
	closeFn func(T)
}

// cacheEntry 记录某 key 当前缓存的 client 及其配置指纹。
type cacheEntry[T any] struct {
	fingerprint string
	value       T
}

// NewClientCache 创建空缓存；closeFn 用于回收被替换的旧 client，调用方必须提供。
func NewClientCache[T any](closeFn func(T)) *ClientCache[T] {
	return &ClientCache[T]{
		entries: map[string]cacheEntry[T]{},
		closeFn: closeFn,
	}
}

// Get 返回 key 对应、且指纹匹配的缓存 client；miss 或指纹不一致时用 build 构造新 client 并缓存
// （旧的先经 closeFn 回收）。build 返回错误时不写入缓存，原样透传错误。
//
// 注意：构造在锁内进行——docker/file client 构造是纯 CPU（解析 CA 证书、组装结构体，不发起网络
// I/O，docker SDK 的 API 版本协商也是首次请求时才发生），且仅在首次或配置变更时触发，锁竞争可忽略；
// 锁内构造换来「同一 key 并发首次访问只会构造一个实例」的强保证，避免重复建连。
func (c *ClientCache[T]) Get(key, fingerprint string, build func() (T, error)) (T, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok {
		if e.fingerprint == fingerprint {
			return e.value, nil
		}
		// 配置已变更：关闭旧 client 释放其连接，再重建。
		c.closeFn(e.value)
		delete(c.entries, key)
	}
	v, err := build()
	if err != nil {
		var zero T
		return zero, err
	}
	c.entries[key] = cacheEntry[T]{fingerprint: fingerprint, value: v}
	return v, nil
}

// Fingerprint 把若干配置串拼成稳定指纹（sha256 十六进制），用于检测节点配置是否变更。
// 各段之间写入 \x00 分隔，避免相邻字段拼接歧义（如 "ab"+"" 与 "a"+"b" 撞车）。
func Fingerprint(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
