// Package service 的 RAGFlow 自愈状态簿记。
// HealState 把自愈重试的瞬时状态存入 Redis，通过 TTL 自动过期，不写库。
// 三类 key：attempts（尝试计数）、cooldown（退避冷却标记）、giveup（超上限放弃标记）。
package service

import (
	"context"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// HealStateTTL 控制各类自愈状态键的过期时间。
// 调用方根据业务策略传入，此处仅持有，不做默认值注入。
type HealStateTTL struct {
	// Attempts 是尝试计数键的存活时长（覆盖一轮多次尝试，典型值 6h）。
	// 超过此时长计数器自动清零，等价于重置一轮自愈窗口。
	Attempts time.Duration
	// Giveup 是放弃标记键的存活时长（典型值 7d）。
	// 在此期间不再触发自愈，防止反复重试无效文档消耗资源。
	Giveup time.Duration
}

// HealState 把 RAGFlow 自愈的重试簿记放 Redis（瞬时、自动过期），不落 DB。
// 设计理由：这些状态全为瞬时调度信息，无需审计或持久化；Redis TTL 自动清理，
// 不必手动 GC，故意不落库以降低存储耦合。
type HealState struct {
	// rc 是 go-redis 客户端，复用 manager 全局实例（与 distLocker/replayGuard 共享物理连接）。
	rc redis.Cmdable
	// prefix 是 Redis key 前缀（cfg.Redis.KeyPrefix，例如 "ocm:"），隔离共享 Redis 键空间。
	prefix string
	// ttl 是各类 key 的过期配置，由调用方注入，支持调整而无需修改此文件。
	ttl HealStateTTL
}

// NewHealState 构造自愈状态簿记。
// rc：go-redis 客户端（redis.Cmdable 接口，测试可替换为真实 Redis）。
// keyPrefix：全局 key 前缀（如 "ocm:"），所有 heal key 均带此前缀以隔离键空间。
// ttl：尝试计数与放弃标记的过期时长，由业务层配置注入。
func NewHealState(rc redis.Cmdable, keyPrefix string, ttl HealStateTTL) *HealState {
	return &HealState{rc: rc, prefix: keyPrefix, ttl: ttl}
}

// kAttempts 返回指定文档的尝试计数 Redis key。
// 格式：{prefix}heal:attempts:{docID}
func (h *HealState) kAttempts(doc string) string {
	return h.prefix + "heal:attempts:" + doc
}

// kCooldown 返回指定文档的退避冷却标记 Redis key。
// 格式：{prefix}heal:cooldown:{docID}
func (h *HealState) kCooldown(doc string) string {
	return h.prefix + "heal:cooldown:" + doc
}

// kGiveup 返回指定文档的放弃标记 Redis key。
// 格式：{prefix}heal:giveup:{docID}
func (h *HealState) kGiveup(doc string) string {
	return h.prefix + "heal:giveup:" + doc
}

// RecordAttempt 记录一次自愈尝试：
//  1. INCR 尝试计数（首次调用从 0 起递增，返回 1）。
//  2. 用 Expire 刷新计数 key TTL（INCR 本身不带 TTL）。
//
// 返回本次递增后的计数（int），调用方据此判断是否已达重试上限。
// 设计取舍：本方法只负责「计数」，冷却（退避）由调用方在拿到计数后另行 SetCooldown 决定，
// 二者解耦后自愈编排（healer）可在「达到上限」时选择不设冷却而直接 MarkGivenUp，逻辑更清晰。
// 注意：INCR + Expire 两步非原子，极低概率下进程崩溃可导致计数 key 无 TTL，
// 属可接受风险（超长滞留后业务侧 GivenUp 检查仍可兜底）。
func (h *HealState) RecordAttempt(ctx context.Context, doc string) (int, error) {
	// INCR 原子递增；若 key 不存在则从 0 开始，即首次调用返回 1。
	cnt, err := h.rc.Incr(ctx, h.kAttempts(doc)).Result()
	if err != nil {
		return 0, err
	}

	// 刷新计数 key TTL（覆盖一轮自愈窗口，防止计数 key 永不过期）。
	if err = h.rc.Expire(ctx, h.kAttempts(doc), h.ttl.Attempts).Err(); err != nil {
		return 0, err
	}

	return int(cnt), nil
}

// SetCooldown 设置文档的退避冷却标记：写冷却 key = "1"，TTL = d，到期后 InCooldown 自动返回 false。
// 与 RecordAttempt 拆开后，调用方可按「第 n 次尝试 → 第 n 档退避」自由决定冷却时长，
// 也可在达到上限时跳过冷却（不调用本方法）改为放弃。d 应为正值，调用方负责保证（d<=0 时不应调用）。
func (h *HealState) SetCooldown(ctx context.Context, doc string, d time.Duration) error {
	return h.rc.Set(ctx, h.kCooldown(doc), "1", d).Err()
}

// InCooldown 检查指定文档是否处于退避冷却期。
// 冷却 key 存在（EXISTS > 0）即表示尚在冷却，调用方应暂缓重试。
func (h *HealState) InCooldown(ctx context.Context, doc string) (bool, error) {
	n, err := h.rc.Exists(ctx, h.kCooldown(doc)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// GivenUp 检查指定文档是否已被标记为放弃。
// giveup key 存在（EXISTS > 0）即表示已放弃，自愈调度器应跳过该文档。
func (h *HealState) GivenUp(ctx context.Context, doc string) (bool, error) {
	n, err := h.rc.Exists(ctx, h.kGiveup(doc)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// MarkGivenUp 将指定文档标记为放弃，写入 giveup key = "1"，TTL = ttl.Giveup。
// 到期后标记自动消失，文档可重新进入自愈窗口（避免永久黑名单）。
func (h *HealState) MarkGivenUp(ctx context.Context, doc string) error {
	return h.rc.Set(ctx, h.kGiveup(doc), "1", h.ttl.Giveup).Err()
}
