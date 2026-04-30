package agent

import (
	"errors"
	"sync"
)

// ErrTokenNotCached 表示 manager 进程目前没有缓存指定 nodeID 的 agent token。
//
// 第一版（Phase A）spec §A2 已知妥协：agent_token 只放在 manager 进程内 map 中，
// 不入库加密；进程重启后必须 rotate-bootstrap 让 agent 重新注册才能拿到新 token。
// 这个错误是 worker handler / docker client 构造路径上识别"需要 rotate"的信号。
var ErrTokenNotCached = errors.New("agent token 未缓存，需要 rotate-bootstrap 让 agent 重新注册")

// TokenResolver 把 runtime node 的 agent_token 缓存在内存中，按 nodeID 取出。
//
// 设计权衡：
//   - 写入仅由 register handler 在注册成功的瞬间触发，进程内是低频写、高频读；
//   - 用 sync.RWMutex 保护 map；并发场景由测试覆盖；
//   - Forget 显式删除某节点缓存，便于后续节点禁用流程主动清除；
//   - 不在错误路径里 panic，让上层 worker 决定如何降级（直接失败 / 入队等待 rotate）。
//
// Phase B6 会引入加密入库的持久化方案，本类型保持接口不变即可平替实现。
type TokenResolver struct {
	mu     sync.RWMutex
	tokens map[string]string
}

// NewTokenResolver 创建一个空的内存 resolver。
func NewTokenResolver() *TokenResolver {
	return &TokenResolver{tokens: map[string]string{}}
}

// Set 缓存某个节点的 agent token。
// 调用方应当在 register handler 完成事务后再调用，避免缓存了未持久化的 token。
func (r *TokenResolver) Set(nodeID, token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokens[nodeID] = token
}

// Get 返回 nodeID 对应的 agent token；未缓存时返回 ErrTokenNotCached。
// 调用方需要识别该错误并把节点状态推为 unreachable 或入队 rotate-bootstrap。
func (r *TokenResolver) Get(nodeID string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	token, ok := r.tokens[nodeID]
	if !ok {
		return "", ErrTokenNotCached
	}
	return token, nil
}

// Forget 删除某个节点的缓存。节点禁用、轮换 bootstrap 时使用。
func (r *TokenResolver) Forget(nodeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tokens, nodeID)
}
