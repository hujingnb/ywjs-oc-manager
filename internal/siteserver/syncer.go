package siteserver

import (
	"context"
	"time"
)

// SiteRecord 是 manager 内部端点返回的单条活跃站点记录（与 Plan 4 端点 JSON 字段对应）。
type SiteRecord struct {
	Host     string `json:"host"`
	SiteID   string `json:"site_id"`
	S3Prefix string `json:"s3_prefix"`
	Status   string `json:"status"`
}

// SiteListClient 抽象"从 manager 拉活跃站点列表"的能力（生产用 HTTP 客户端，单测用 fake）。
type SiteListClient interface {
	ListActiveSites(ctx context.Context) ([]SiteRecord, error)
}

// Syncer 周期性轮询 manager 端点并整体刷新注册表；拉取失败保留旧快照。
type Syncer struct {
	client   SiteListClient
	registry *Registry
	interval time.Duration
}

// NewSyncer 构造 syncer；interval<=0 时由 Run 用默认 5s（单测传 0 只调 syncOnce）。
func NewSyncer(client SiteListClient, registry *Registry, interval time.Duration) *Syncer {
	return &Syncer{client: client, registry: registry, interval: interval}
}

// syncOnce 拉一次活跃站点并整体替换注册表；失败直接返回错误、不动注册表（保留旧快照）。
func (s *Syncer) syncOnce(ctx context.Context) error {
	records, err := s.client.ListActiveSites(ctx)
	if err != nil {
		return err // 保留旧快照，由 Run 记录日志后等下一周期
	}
	snapshot := make(map[string]Entry, len(records))
	for _, rec := range records {
		// 双保险：只把 active 纳入路由（manager 也应只返回 active）。
		if rec.Status != "active" {
			continue
		}
		snapshot[rec.Host] = Entry{SiteID: rec.SiteID, S3Prefix: rec.S3Prefix, Status: rec.Status}
	}
	s.registry.Replace(snapshot)
	return nil
}

// Run 阻塞循环：立即同步一次，之后每 interval 同步一次，直到 ctx 取消。
// 单次失败只记日志、不退出（下周期重试），保证 manager 抖动不影响服务。
func (s *Syncer) Run(ctx context.Context, onError func(error)) {
	interval := s.interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if err := s.syncOnce(ctx); err != nil && onError != nil {
		onError(err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.syncOnce(ctx); err != nil && onError != nil {
				onError(err)
			}
		}
	}
}
