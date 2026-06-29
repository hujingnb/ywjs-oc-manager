// Package siteserver 实现公网静态站点服务：按 Host 路由到对象存储前缀并流式返回文件。
package siteserver

import "sync"

// Entry 是一个已发布站点的路由信息（注册表的值）。
type Entry struct {
	SiteID   string // 站点 ID
	S3Prefix string // 当前版本前缀，如 published-sites/<siteID>/<version>/（末尾带 /）
	Status   string // 站点状态；site-server 只服务 active（其余在快照里本不应出现）
}

// Registry 是 Host→Entry 的内存注册表，读多写少：读路径（每请求）用 RLock，
// 写路径（每轮同步一次）用 Lock 整体替换。
type Registry struct {
	mu     sync.RWMutex
	byHost map[string]Entry
}

// NewRegistry 构造空注册表（同步前对任何 host 都 Lookup 失败 → 404）。
func NewRegistry() *Registry {
	return &Registry{byHost: map[string]Entry{}}
}

// Replace 用新快照整体替换注册表（原子换：下线/过期站点在替换后即从路由消失）。
func (r *Registry) Replace(snapshot map[string]Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byHost = snapshot
}

// Lookup 按 host 取站点路由信息；不存在返回 ok=false（调用方据此 404）。
func (r *Registry) Lookup(host string) (Entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.byHost[host]
	return e, ok
}
