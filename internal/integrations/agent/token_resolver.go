package agent

import (
	"context"
	"errors"
	"sync"
)

// ErrTokenNotCached 表示 manager 进程目前无法取得指定 nodeID 的 agent token。
//
// token 已加密持久化到 runtime_nodes.agent_token_ciphertext；出现该错误通常说明节点尚未
// enroll 成功或密文丢失，需要重启对应 runtime-agent 触发自动 enroll。
var ErrTokenNotCached = errors.New("agent token 未缓存，需要重启 agent 触发自动注册")

// PersistentTokenLoader 抽象"按 nodeID 从持久化层取出 agent token"。
// TokenResolver 在内存 cache miss 时通过 loader 回填，manager 进程重启后仍能访问节点。
type PersistentTokenLoader interface {
	LoadAgentToken(ctx context.Context, nodeID string) (string, error)
}

// TokenResolver 把 runtime node 的 agent_token 缓存在内存中，按 nodeID 取出。
//
// 设计权衡：
//   - 写入仅由 enroll handler 在注册成功的瞬间触发，进程内是低频写、高频读；
//   - 用 sync.RWMutex 保护 map；并发场景由测试覆盖；
//   - Forget 显式删除某节点缓存，便于后续节点禁用流程主动清除；
//   - 不在错误路径里 panic，让上层 worker 决定如何降级（直接失败 / 入队等待 rotate）。
//
// 在注入 PersistentTokenLoader 时，cache miss 会先查持久化层并回填 cache。
type TokenResolver struct {
	mu     sync.RWMutex
	tokens map[string]string
	loader PersistentTokenLoader
}

// NewTokenResolver 创建一个空的内存 resolver。
func NewTokenResolver() *TokenResolver {
	return &TokenResolver{tokens: map[string]string{}}
}

// SetPersistentLoader 注入持久化层；cache miss 时会先调用它再回填内存 cache。
// nil 时退化到纯内存模式（A 阶段行为）。
func (r *TokenResolver) SetPersistentLoader(loader PersistentTokenLoader) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loader = loader
}

// Set 缓存某个节点的 agent token。
// 调用方应当在 enroll handler 完成事务后再调用，避免缓存了未持久化的 token。
func (r *TokenResolver) Set(nodeID, token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokens[nodeID] = token
}

// Get 返回 nodeID 对应的 agent token。
// 命中内存 cache 直接返回；miss 且 loader 已注入时尝试从持久化层加载并回填；
// 持久化也无值时返回 ErrTokenNotCached。
func (r *TokenResolver) Get(nodeID string) (string, error) {
	r.mu.RLock()
	if token, ok := r.tokens[nodeID]; ok {
		r.mu.RUnlock()
		return token, nil
	}
	loader := r.loader
	r.mu.RUnlock()

	if loader == nil {
		return "", ErrTokenNotCached
	}
	token, err := loader.LoadAgentToken(context.Background(), nodeID)
	if err != nil {
		return "", err
	}
	if token == "" {
		return "", ErrTokenNotCached
	}
	r.mu.Lock()
	r.tokens[nodeID] = token
	r.mu.Unlock()
	return token, nil
}

// Forget 删除某个节点的缓存。节点禁用、轮换 bootstrap 时使用。
func (r *TokenResolver) Forget(nodeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tokens, nodeID)
}
