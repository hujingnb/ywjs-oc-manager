// Package service 的资源指标服务封装节点和实例资源趋势查询的权限、范围归一化和 DTO 映射。
package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// ResourceMetricsStore 抽象资源趋势服务需要的 sqlc 查询能力。
type ResourceMetricsStore interface {
	GetRuntimeNode(ctx context.Context, id string) (sqlc.RuntimeNode, error)
	GetApp(ctx context.Context, id string) (sqlc.App, error)
	ListAppsByRuntimeNode(ctx context.Context, arg sqlc.ListAppsByRuntimeNodeParams) ([]sqlc.App, error)
	ListLatestInstanceResourceSamplesByNode(ctx context.Context, runtimeNodeID string) ([]sqlc.InstanceResourceSample, error)
	ListNodeResourceSamples(ctx context.Context, arg sqlc.ListNodeResourceSamplesParams) ([]sqlc.NodeResourceSample, error)
	ListNodeResourceBuckets(ctx context.Context, arg sqlc.ListNodeResourceBucketsParams) ([]sqlc.ListNodeResourceBucketsRow, error)
	ListNodeInstanceResourceSamples(ctx context.Context, arg sqlc.ListNodeInstanceResourceSamplesParams) ([]sqlc.InstanceResourceSample, error)
	ListNodeInstanceResourceBuckets(ctx context.Context, arg sqlc.ListNodeInstanceResourceBucketsParams) ([]sqlc.ListNodeInstanceResourceBucketsRow, error)
	ListInstanceResourceSamples(ctx context.Context, arg sqlc.ListInstanceResourceSamplesParams) ([]sqlc.InstanceResourceSample, error)
	ListInstanceResourceBuckets(ctx context.Context, arg sqlc.ListInstanceResourceBucketsParams) ([]sqlc.ListInstanceResourceBucketsRow, error)
	ListLatestNodeResourceSamples(ctx context.Context, runtimeNodeIds []string) ([]sqlc.NodeResourceSample, error)
}

// ResourceMetricsService 查询 runtime 节点与应用实例资源指标。
type ResourceMetricsService struct {
	// store 提供资源趋势所需的应用、节点和采样查询。
	store ResourceMetricsStore
}

// NewResourceMetricsService 创建资源指标服务。
func NewResourceMetricsService(store ResourceMetricsStore) *ResourceMetricsService {
	return &ResourceMetricsService{store: store}
}

// ResourceTimeRange 表示资源趋势查询已经校验过的时间范围与聚合粒度。
type ResourceTimeRange struct {
	// From 是采样开始时间，包含边界。
	From time.Time
	// To 是采样结束时间，包含边界。
	To time.Time
	// BucketSeconds 为 0 时返回原始采样；非 0 时返回聚合桶。
	BucketSeconds int32
}

// NodeResourceSampleResult 是节点资源趋势对外 DTO。
type NodeResourceSampleResult struct {
	// SampledAt 是采样或聚合桶时间，统一输出 UTC RFC3339。
	SampledAt string `json:"sampled_at"`
	// CPUPercent 是节点 CPU 使用百分比；nil 表示该次采样缺失。
	CPUPercent *float64 `json:"cpu_percent,omitempty"`
	// MemoryUsedBytes 是节点内存已用字节数。
	MemoryUsedBytes *int64 `json:"memory_used_bytes,omitempty"`
	// MemoryTotalBytes 是节点内存总字节数。
	MemoryTotalBytes *int64 `json:"memory_total_bytes,omitempty"`
	// DiskUsedBytes 是节点磁盘已用字节数。
	DiskUsedBytes *int64 `json:"disk_used_bytes,omitempty"`
	// DiskTotalBytes 是节点磁盘总字节数。
	DiskTotalBytes *int64 `json:"disk_total_bytes,omitempty"`
	// NetworkRxBytes 是节点网络接收累计字节数。
	NetworkRxBytes *int64 `json:"network_rx_bytes,omitempty"`
	// NetworkTxBytes 是节点网络发送累计字节数。
	NetworkTxBytes *int64 `json:"network_tx_bytes,omitempty"`
	// InstanceCount 是采样时节点承载的实例数量。
	InstanceCount *int32 `json:"instance_count,omitempty"`
	// LastError 是采样过程的最近错误，空字符串表示没有错误或无采样值。
	LastError string `json:"last_error,omitempty"`
}

// InstanceResourceSampleResult 是应用实例资源趋势对外 DTO。
type InstanceResourceSampleResult struct {
	// SampledAt 是采样或聚合桶时间，统一输出 UTC RFC3339。
	SampledAt string `json:"sampled_at"`
	// ContainerStatus 是容器采样时状态；bucket 查询取桶内最新非空状态。
	ContainerStatus string `json:"container_status,omitempty"`
	// CPUPercent 是实例 CPU 使用百分比；nil 表示该次采样缺失。
	CPUPercent *float64 `json:"cpu_percent,omitempty"`
	// MemoryUsedBytes 是实例内存已用字节数。
	MemoryUsedBytes *int64 `json:"memory_used_bytes,omitempty"`
	// MemoryLimitBytes 是实例内存限制字节数。
	MemoryLimitBytes *int64 `json:"memory_limit_bytes,omitempty"`
	// DiskReadBytes 是实例磁盘读取累计字节数。
	DiskReadBytes *int64 `json:"disk_read_bytes,omitempty"`
	// DiskWriteBytes 是实例磁盘写入累计字节数。
	DiskWriteBytes *int64 `json:"disk_write_bytes,omitempty"`
	// NetworkRxBytes 是实例网络接收累计字节数。
	NetworkRxBytes *int64 `json:"network_rx_bytes,omitempty"`
	// NetworkTxBytes 是实例网络发送累计字节数。
	NetworkTxBytes *int64 `json:"network_tx_bytes,omitempty"`
	// LastError 是采样过程的最近错误，空字符串表示没有错误或无采样值。
	LastError string `json:"last_error,omitempty"`
}

// NodeInstanceResult 是节点抽屉中的应用实例摘要。
type NodeInstanceResult struct {
	// AppID 是实例所属应用 ID。
	AppID string `json:"app_id"`
	// OrgID 是应用所属企业 ID，用于后续前端跳转和权限上下文。
	OrgID string `json:"org_id"`
	// OwnerUserID 是应用所有者用户 ID。
	OwnerUserID string `json:"owner_user_id"`
	// Name 是应用名称。
	Name string `json:"name"`
	// Status 是应用生命周期状态。
	Status string `json:"status"`
	// RuntimeNodeID 是实例所在 runtime 节点 ID。
	RuntimeNodeID string `json:"runtime_node_id"`
	// ContainerID 是 runtime 侧容器 ID；未创建容器时为空。
	ContainerID string `json:"container_id,omitempty"`
	// CurrentResource 是该应用在节点上的最近一次实例资源采样。
	CurrentResource *InstanceResourceSampleResult `json:"current_resource,omitempty"`
}

// NormalizeResourceRange 将 handler 原始查询参数归一化为 service 查询范围。
func NormalizeResourceRange(fromRaw, toRaw, bucketRaw string, now time.Time) (ResourceTimeRange, error) {
	to := now.UTC()
	from := to.Add(-7 * 24 * time.Hour)
	var err error
	if strings.TrimSpace(fromRaw) != "" {
		from, err = time.Parse(time.RFC3339, strings.TrimSpace(fromRaw))
		if err != nil {
			return ResourceTimeRange{}, ErrInvalidResourceRange
		}
		from = from.UTC()
	}
	if strings.TrimSpace(toRaw) != "" {
		to, err = time.Parse(time.RFC3339, strings.TrimSpace(toRaw))
		if err != nil {
			return ResourceTimeRange{}, ErrInvalidResourceRange
		}
		to = to.UTC()
	}
	if from.After(to) {
		return ResourceTimeRange{}, ErrInvalidResourceRange
	}
	bucketSeconds, err := normalizeResourceBucket(bucketRaw)
	if err != nil {
		return ResourceTimeRange{}, err
	}
	return ResourceTimeRange{From: from, To: to, BucketSeconds: bucketSeconds}, nil
}

// ListNodeResources 查询节点资源趋势；节点资源属于平台运维视图，仅平台管理员可读。
func (s *ResourceMetricsService) ListNodeResources(ctx context.Context, principal auth.Principal, nodeID string, r ResourceTimeRange) ([]NodeResourceSampleResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return nil, ErrForbidden
	}
	if _, err := s.store.GetRuntimeNode(ctx, nodeID); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("查询 runtime 节点失败: %w", err)
	}
	if r.BucketSeconds > 0 {
		rows, err := s.store.ListNodeResourceBuckets(ctx, sqlc.ListNodeResourceBucketsParams{
			RuntimeNodeID: nodeID,
			BucketSeconds: int64(r.BucketSeconds),
			FromSampledAt: r.From,
			ToSampledAt:   r.To,
		})
		if err != nil {
			return nil, fmt.Errorf("查询节点资源聚合失败: %w", err)
		}
		return nodeBucketResults(rows), nil
	}
	rows, err := s.store.ListNodeResourceSamples(ctx, sqlc.ListNodeResourceSamplesParams{
		RuntimeNodeID: nodeID,
		FromSampledAt: r.From,
		ToSampledAt:   r.To,
	})
	if err != nil {
		return nil, fmt.Errorf("查询节点资源采样失败: %w", err)
	}
	return nodeSampleResults(rows), nil
}

// ListNodeInstances 查询节点上的应用实例列表，并附带每个实例最近一次资源采样。
func (s *ResourceMetricsService) ListNodeInstances(ctx context.Context, principal auth.Principal, nodeID string, limit, offset int32) ([]NodeInstanceResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return nil, ErrForbidden
	}
	if _, err := s.store.GetRuntimeNode(ctx, nodeID); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("查询 runtime 节点失败: %w", err)
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	apps, err := s.store.ListAppsByRuntimeNode(ctx, sqlc.ListAppsByRuntimeNodeParams{RuntimeNodeID: nodeID, Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("查询节点实例失败: %w", err)
	}
	samples, err := s.store.ListLatestInstanceResourceSamplesByNode(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("查询实例最近资源采样失败: %w", err)
	}
	// 按 AppID（string）建索引，便于 O(1) 查找。
	latest := make(map[string]sqlc.InstanceResourceSample, len(samples))
	for _, sample := range samples {
		latest[sample.AppID] = sample
	}
	results := make([]NodeInstanceResult, 0, len(apps))
	for _, app := range apps {
		result := nodeInstanceResult(app)
		if sample, ok := latest[app.ID]; ok {
			current := instanceSampleResult(sample)
			result.CurrentResource = &current
		}
		results = append(results, result)
	}
	return results, nil
}

// ListNodeInstanceResources 查询指定节点上某个应用实例的资源趋势。
func (s *ResourceMetricsService) ListNodeInstanceResources(ctx context.Context, principal auth.Principal, nodeID, appID string, r ResourceTimeRange) ([]InstanceResourceSampleResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return nil, ErrForbidden
	}
	if _, err := s.store.GetRuntimeNode(ctx, nodeID); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("查询 runtime 节点失败: %w", err)
	}
	app, err := s.store.GetApp(ctx, appID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询应用失败: %w", err)
	}
	// app.RuntimeNodeID 是 string；与 nodeID 直接比较。
	if app.DeletedAt.Valid || app.RuntimeNodeID != nodeID {
		return nil, ErrNotFound
	}
	if r.BucketSeconds > 0 {
		rows, err := s.store.ListNodeInstanceResourceBuckets(ctx, sqlc.ListNodeInstanceResourceBucketsParams{
			RuntimeNodeID: nodeID,
			AppID:         appID,
			BucketSeconds: int64(r.BucketSeconds),
			FromSampledAt: r.From,
			ToSampledAt:   r.To,
		})
		if err != nil {
			return nil, fmt.Errorf("查询节点实例资源聚合失败: %w", err)
		}
		return nodeInstanceBucketResults(rows), nil
	}
	rows, err := s.store.ListNodeInstanceResourceSamples(ctx, sqlc.ListNodeInstanceResourceSamplesParams{
		RuntimeNodeID: nodeID,
		AppID:         appID,
		FromSampledAt: r.From,
		ToSampledAt:   r.To,
	})
	if err != nil {
		return nil, fmt.Errorf("查询节点实例资源采样失败: %w", err)
	}
	return instanceSampleResults(rows), nil
}

// ListAppResources 查询应用实例资源趋势；权限沿用应用读权限。
func (s *ResourceMetricsService) ListAppResources(ctx context.Context, principal auth.Principal, appID string, r ResourceTimeRange) ([]InstanceResourceSampleResult, error) {
	app, err := s.store.GetApp(ctx, appID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询应用失败: %w", err)
	}
	if app.DeletedAt.Valid {
		return nil, ErrNotFound
	}
	if !auth.CanViewApp(principal, app.OrgID, app.OwnerUserID) {
		return nil, ErrForbidden
	}
	if r.BucketSeconds > 0 {
		rows, err := s.store.ListInstanceResourceBuckets(ctx, sqlc.ListInstanceResourceBucketsParams{
			AppID:         appID,
			BucketSeconds: int64(r.BucketSeconds),
			FromSampledAt: r.From,
			ToSampledAt:   r.To,
		})
		if err != nil {
			return nil, fmt.Errorf("查询应用资源聚合失败: %w", err)
		}
		return instanceBucketResults(rows), nil
	}
	rows, err := s.store.ListInstanceResourceSamples(ctx, sqlc.ListInstanceResourceSamplesParams{
		AppID:         appID,
		FromSampledAt: r.From,
		ToSampledAt:   r.To,
	})
	if err != nil {
		return nil, fmt.Errorf("查询应用资源采样失败: %w", err)
	}
	return instanceSampleResults(rows), nil
}

func normalizeResourceBucket(bucketRaw string) (int32, error) {
	switch strings.TrimSpace(bucketRaw) {
	case "":
		return 0, nil
	case "5m":
		return 300, nil
	case "1h":
		return 3600, nil
	default:
		return 0, ErrInvalidResourceRange
	}
}

// formatSampledAt 统一采样时间输出格式；time.Time 零值返回空串以兼容固定字段。
func formatSampledAt(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

// nodeSampleResults 将节点原始采样行批量映射为 DTO。
func nodeSampleResults(rows []sqlc.NodeResourceSample) []NodeResourceSampleResult {
	results := make([]NodeResourceSampleResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, nodeSampleResult(row))
	}
	return results
}

// nodeSampleResult 保留原始采样中的 NULL 语义，缺失指标映射为 nil 指针。
func nodeSampleResult(row sqlc.NodeResourceSample) NodeResourceSampleResult {
	// SampledAt 是 time.Time（非空）。
	result := NodeResourceSampleResult{SampledAt: formatSampledAt(row.SampledAt)}
	if row.CpuPercent.Valid {
		result.CPUPercent = float64Ptr(row.CpuPercent.Float64)
	}
	if row.MemoryUsedBytes.Valid {
		result.MemoryUsedBytes = int64Ptr(row.MemoryUsedBytes.Int64)
	}
	if row.MemoryTotalBytes.Valid {
		result.MemoryTotalBytes = int64Ptr(row.MemoryTotalBytes.Int64)
	}
	if row.DiskUsedBytes.Valid {
		result.DiskUsedBytes = int64Ptr(row.DiskUsedBytes.Int64)
	}
	if row.DiskTotalBytes.Valid {
		result.DiskTotalBytes = int64Ptr(row.DiskTotalBytes.Int64)
	}
	if row.NetworkRxBytes.Valid {
		result.NetworkRxBytes = int64Ptr(row.NetworkRxBytes.Int64)
	}
	if row.NetworkTxBytes.Valid {
		result.NetworkTxBytes = int64Ptr(row.NetworkTxBytes.Int64)
	}
	if row.InstanceCount.Valid {
		// null.Int 内部是 int64；InstanceCount DTO 字段是 *int32。
		result.InstanceCount = int32Ptr(int32(row.InstanceCount.Int64))
	}
	if row.LastError.Valid {
		result.LastError = row.LastError.String
	}
	return result
}

// nodeBucketResults 将节点聚合桶行映射为 DTO，并使用 Has* 字段区分 0 值和缺失值。
// SampledAt 是 time.Time（MySQL FROM_UNIXTIME 结果）；LastError 是 interface{}。
func nodeBucketResults(rows []sqlc.ListNodeResourceBucketsRow) []NodeResourceSampleResult {
	results := make([]NodeResourceSampleResult, 0, len(rows))
	for _, row := range rows {
		result := NodeResourceSampleResult{SampledAt: formatSampledAt(row.SampledAt)}
		if row.HasCpuPercent {
			result.CPUPercent = float64Ptr(row.CpuPercent)
		}
		if row.HasMemoryUsedBytes {
			result.MemoryUsedBytes = int64Ptr(row.MemoryUsedBytes)
		}
		if row.HasMemoryTotalBytes {
			result.MemoryTotalBytes = int64Ptr(row.MemoryTotalBytes)
		}
		if row.HasDiskUsedBytes {
			result.DiskUsedBytes = int64Ptr(row.DiskUsedBytes)
		}
		if row.HasDiskTotalBytes {
			result.DiskTotalBytes = int64Ptr(row.DiskTotalBytes)
		}
		if row.HasNetworkRxBytes {
			result.NetworkRxBytes = int64Ptr(row.NetworkRxBytes)
		}
		if row.HasNetworkTxBytes {
			result.NetworkTxBytes = int64Ptr(row.NetworkTxBytes)
		}
		if row.HasInstanceCount {
			// InstanceCount 在 bucket row 是 int64（SIGNED 聚合结果），转为 int32 DTO。
			result.InstanceCount = int32Ptr(int32(row.InstanceCount))
		}
		if row.HasLastError {
			// LastError 在 bucket row 是 interface{}（MySQL COALESCE 字符串结果）。
			result.LastError = ifaceToString(row.LastError)
		}
		results = append(results, result)
	}
	return results
}

// instanceSampleResults 将实例原始采样行批量映射为 DTO。
func instanceSampleResults(rows []sqlc.InstanceResourceSample) []InstanceResourceSampleResult {
	results := make([]InstanceResourceSampleResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, instanceSampleResult(row))
	}
	return results
}

// instanceSampleResult 保留实例原始采样中的 NULL 语义，缺失指标映射为 nil 指针。
func instanceSampleResult(row sqlc.InstanceResourceSample) InstanceResourceSampleResult {
	result := InstanceResourceSampleResult{SampledAt: formatSampledAt(row.SampledAt)}
	if row.ContainerStatus.Valid {
		result.ContainerStatus = row.ContainerStatus.String
	}
	if row.CpuPercent.Valid {
		result.CPUPercent = float64Ptr(row.CpuPercent.Float64)
	}
	if row.MemoryUsedBytes.Valid {
		result.MemoryUsedBytes = int64Ptr(row.MemoryUsedBytes.Int64)
	}
	if row.MemoryLimitBytes.Valid {
		result.MemoryLimitBytes = int64Ptr(row.MemoryLimitBytes.Int64)
	}
	if row.DiskReadBytes.Valid {
		result.DiskReadBytes = int64Ptr(row.DiskReadBytes.Int64)
	}
	if row.DiskWriteBytes.Valid {
		result.DiskWriteBytes = int64Ptr(row.DiskWriteBytes.Int64)
	}
	if row.NetworkRxBytes.Valid {
		result.NetworkRxBytes = int64Ptr(row.NetworkRxBytes.Int64)
	}
	if row.NetworkTxBytes.Valid {
		result.NetworkTxBytes = int64Ptr(row.NetworkTxBytes.Int64)
	}
	if row.LastError.Valid {
		result.LastError = row.LastError.String
	}
	return result
}

// instanceBucketResults 将应用维度实例聚合桶行映射为 DTO。
func instanceBucketResults(rows []sqlc.ListInstanceResourceBucketsRow) []InstanceResourceSampleResult {
	results := make([]InstanceResourceSampleResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, instanceBucketResult(instanceBucketValues{
			SampledAt:           row.SampledAt,
			ContainerStatus:     row.ContainerStatus,
			HasContainerStatus:  row.HasContainerStatus,
			CPUPercent:          row.CpuPercent,
			HasCPUPercent:       row.HasCpuPercent,
			MemoryUsedBytes:     row.MemoryUsedBytes,
			HasMemoryUsedBytes:  row.HasMemoryUsedBytes,
			MemoryLimitBytes:    row.MemoryLimitBytes,
			HasMemoryLimitBytes: row.HasMemoryLimitBytes,
			DiskReadBytes:       row.DiskReadBytes,
			HasDiskReadBytes:    row.HasDiskReadBytes,
			DiskWriteBytes:      row.DiskWriteBytes,
			HasDiskWriteBytes:   row.HasDiskWriteBytes,
			NetworkRxBytes:      row.NetworkRxBytes,
			HasNetworkRxBytes:   row.HasNetworkRxBytes,
			NetworkTxBytes:      row.NetworkTxBytes,
			HasNetworkTxBytes:   row.HasNetworkTxBytes,
			LastError:           row.LastError,
			HasLastError:        row.HasLastError,
		}))
	}
	return results
}

// nodeInstanceBucketResults 将节点维度实例聚合桶行映射为 DTO，避免误用应用维度 bucket 查询。
func nodeInstanceBucketResults(rows []sqlc.ListNodeInstanceResourceBucketsRow) []InstanceResourceSampleResult {
	results := make([]InstanceResourceSampleResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, instanceBucketResult(instanceBucketValues{
			SampledAt:           row.SampledAt,
			ContainerStatus:     row.ContainerStatus,
			HasContainerStatus:  row.HasContainerStatus,
			CPUPercent:          row.CpuPercent,
			HasCPUPercent:       row.HasCpuPercent,
			MemoryUsedBytes:     row.MemoryUsedBytes,
			HasMemoryUsedBytes:  row.HasMemoryUsedBytes,
			MemoryLimitBytes:    row.MemoryLimitBytes,
			HasMemoryLimitBytes: row.HasMemoryLimitBytes,
			DiskReadBytes:       row.DiskReadBytes,
			HasDiskReadBytes:    row.HasDiskReadBytes,
			DiskWriteBytes:      row.DiskWriteBytes,
			HasDiskWriteBytes:   row.HasDiskWriteBytes,
			NetworkRxBytes:      row.NetworkRxBytes,
			HasNetworkRxBytes:   row.HasNetworkRxBytes,
			NetworkTxBytes:      row.NetworkTxBytes,
			HasNetworkTxBytes:   row.HasNetworkTxBytes,
			LastError:           row.LastError,
			HasLastError:        row.HasLastError,
		}))
	}
	return results
}

// instanceBucketValues 统一承接两类实例 bucket sqlc 行，避免用长参数列表丢失 Has* 语义。
// SampledAt 是 time.Time（MySQL FROM_UNIXTIME 结果，非空）。
// ContainerStatus / LastError 是 interface{}（MySQL COALESCE 跨类型表达式）。
type instanceBucketValues struct {
	SampledAt           time.Time
	ContainerStatus     interface{}
	HasContainerStatus  bool
	CPUPercent          float64
	HasCPUPercent       bool
	MemoryUsedBytes     int64
	HasMemoryUsedBytes  bool
	MemoryLimitBytes    int64
	HasMemoryLimitBytes bool
	DiskReadBytes       int64
	HasDiskReadBytes    bool
	DiskWriteBytes      int64
	HasDiskWriteBytes   bool
	NetworkRxBytes      int64
	HasNetworkRxBytes   bool
	NetworkTxBytes      int64
	HasNetworkTxBytes   bool
	LastError           interface{}
	HasLastError        bool
}

// instanceBucketResult 根据 Has* 字段决定是否输出指标指针，保留 0 值作为有效采样值。
// ContainerStatus / LastError 为 interface{}，通过 ifaceToString 转换。
func instanceBucketResult(row instanceBucketValues) InstanceResourceSampleResult {
	result := InstanceResourceSampleResult{SampledAt: formatSampledAt(row.SampledAt)}
	if row.HasContainerStatus {
		result.ContainerStatus = ifaceToString(row.ContainerStatus)
	}
	if row.HasCPUPercent {
		result.CPUPercent = float64Ptr(row.CPUPercent)
	}
	if row.HasMemoryUsedBytes {
		result.MemoryUsedBytes = int64Ptr(row.MemoryUsedBytes)
	}
	if row.HasMemoryLimitBytes {
		result.MemoryLimitBytes = int64Ptr(row.MemoryLimitBytes)
	}
	if row.HasDiskReadBytes {
		result.DiskReadBytes = int64Ptr(row.DiskReadBytes)
	}
	if row.HasDiskWriteBytes {
		result.DiskWriteBytes = int64Ptr(row.DiskWriteBytes)
	}
	if row.HasNetworkRxBytes {
		result.NetworkRxBytes = int64Ptr(row.NetworkRxBytes)
	}
	if row.HasNetworkTxBytes {
		result.NetworkTxBytes = int64Ptr(row.NetworkTxBytes)
	}
	if row.HasLastError {
		result.LastError = ifaceToString(row.LastError)
	}
	return result
}

// nodeInstanceResult 将应用行映射为节点实例摘要，不暴露密钥、prompt 等无关字段。
func nodeInstanceResult(app sqlc.App) NodeInstanceResult {
	result := NodeInstanceResult{
		AppID:         app.ID,
		OrgID:         app.OrgID,
		OwnerUserID:   app.OwnerUserID,
		Name:          app.Name,
		Status:        app.Status,
		// RuntimeNodeID 是 string（非空）。
		RuntimeNodeID: app.RuntimeNodeID,
	}
	if app.ContainerID.Valid {
		result.ContainerID = app.ContainerID.String
	}
	return result
}

// float64Ptr 为可选数值 DTO 字段生成稳定指针。
func float64Ptr(v float64) *float64 { return &v }

// int64Ptr 为可选数值 DTO 字段生成稳定指针。
func int64Ptr(v int64) *int64 { return &v }

// int32Ptr 为可选数值 DTO 字段生成稳定指针。
func int32Ptr(v int32) *int32 { return &v }
