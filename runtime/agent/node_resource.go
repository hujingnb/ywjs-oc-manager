package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const nodeResourceDockerTimeout = 500 * time.Millisecond

// NodeResourceSnapshot 是 agent 上报给 manager 的节点级资源采样。
// 数值字段使用指针配合 omitempty，便于在局部采集失败时只省略不可用指标。
type NodeResourceSnapshot struct {
	CPUPercent       *float64 `json:"cpu_percent,omitempty"`
	MemoryUsedBytes  *int64   `json:"memory_used_bytes,omitempty"`
	MemoryTotalBytes *int64   `json:"memory_total_bytes,omitempty"`
	DiskUsedBytes    *int64   `json:"disk_used_bytes,omitempty"`
	DiskTotalBytes   *int64   `json:"disk_total_bytes,omitempty"`
	NetworkRxBytes   *int64   `json:"network_rx_bytes,omitempty"`
	NetworkTxBytes   *int64   `json:"network_tx_bytes,omitempty"`
	InstanceCount    *int32   `json:"instance_count,omitempty"`
	LastError        string   `json:"last_error,omitempty"`
}

// nodeResourceCounters 保存跨心跳的累计计数器，用于把 /proc/stat 的累计 tick 转为区间 CPU 使用率。
type nodeResourceCounters struct {
	cpuIdle  uint64
	cpuTotal uint64
}

// parseProcStatCPU 解析 /proc/stat 的 cpu 汇总行，返回 idle 与 total tick。
func parseProcStatCPU(raw string) (idle, total uint64, err error) {
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)
			if len(fields) < 5 {
				return 0, 0, fmt.Errorf("cpu 行字段不足")
			}
			values := make([]uint64, 0, len(fields)-1)
			for _, field := range fields[1:] {
				v, err := strconv.ParseUint(field, 10, 64)
				if err != nil {
					return 0, 0, fmt.Errorf("解析 cpu tick %q 失败: %w", field, err)
				}
				values = append(values, v)
				total += v
			}
			idle = values[3]
			if len(values) > 4 {
				idle += values[4]
			}
			return idle, total, nil
		}
	}
	return 0, 0, fmt.Errorf("缺少 cpu 汇总行")
}

// parseMemInfo 解析 /proc/meminfo，优先使用 MemAvailable；缺失时按 Linux 常见口径估算可用内存。
func parseMemInfo(raw string) (used, total int64, err error) {
	values := map[string]int64{}
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("解析 meminfo %s 失败: %w", key, err)
		}
		values[key] = value * 1024
	}
	total = values["MemTotal"]
	if total <= 0 {
		return 0, 0, fmt.Errorf("缺少 MemTotal")
	}
	available, ok := values["MemAvailable"]
	if !ok {
		available = values["MemFree"] + values["Buffers"] + values["Cached"] + values["SReclaimable"] - values["Shmem"]
	}
	used = total - available
	if used < 0 {
		used = 0
	}
	return used, total, nil
}

// parseNetDev 汇总 /proc/net/dev 中全部网卡的接收与发送字节数。
func parseNetDev(raw string) (rx, tx int64, err error) {
	for _, line := range strings.Split(raw, "\n") {
		before, after, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(before) == "" {
			continue
		}
		fields := strings.Fields(after)
		if len(fields) < 16 {
			return 0, 0, fmt.Errorf("网卡 %s 字段不足", strings.TrimSpace(before))
		}
		rxBytes, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("解析网卡 %s RX 失败: %w", strings.TrimSpace(before), err)
		}
		txBytes, err := strconv.ParseInt(fields[8], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("解析网卡 %s TX 失败: %w", strings.TrimSpace(before), err)
		}
		rx += rxBytes
		tx += txBytes
	}
	return rx, tx, nil
}

// statDiskUsage 用 statfs 采集 dataRoot 所在文件系统的总量和已用字节数。
func statDiskUsage(path string) (used, total int64, err error) {
	if strings.TrimSpace(path) == "" {
		path = "/"
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	total = int64(stat.Blocks) * int64(stat.Bsize)
	free := int64(stat.Bfree) * int64(stat.Bsize)
	used = total - free
	if used < 0 {
		used = 0
	}
	return used, total, nil
}

// collectNodeResource 从宿主 /proc、dataRoot 文件系统和 Docker API 聚合节点资源。
// 单项失败只写入 last_error，避免监控采样异常影响 agent 注册或心跳。
func collectNodeResource(dataRoot string, docker DockerClient, prev *nodeResourceCounters) (NodeResourceSnapshot, nodeResourceCounters) {
	var snapshot NodeResourceSnapshot
	var counters nodeResourceCounters
	var errs []string

	if raw, err := os.ReadFile("/proc/stat"); err != nil {
		errs = append(errs, "cpu: "+err.Error())
	} else if idle, total, err := parseProcStatCPU(string(raw)); err != nil {
		errs = append(errs, "cpu: "+err.Error())
	} else {
		counters.cpuIdle = idle
		counters.cpuTotal = total
		if percent, ok := cpuPercent(idle, total, prev); ok {
			snapshot.CPUPercent = &percent
		}
	}

	if raw, err := os.ReadFile("/proc/meminfo"); err != nil {
		errs = append(errs, "memory: "+err.Error())
	} else if used, total, err := parseMemInfo(string(raw)); err != nil {
		errs = append(errs, "memory: "+err.Error())
	} else {
		snapshot.MemoryUsedBytes = &used
		snapshot.MemoryTotalBytes = &total
	}

	if raw, err := os.ReadFile("/proc/net/dev"); err != nil {
		errs = append(errs, "network: "+err.Error())
	} else if rx, tx, err := parseNetDev(string(raw)); err != nil {
		errs = append(errs, "network: "+err.Error())
	} else {
		snapshot.NetworkRxBytes = &rx
		snapshot.NetworkTxBytes = &tx
	}

	if used, total, err := statDiskUsage(dataRoot); err != nil {
		errs = append(errs, "disk: "+err.Error())
	} else {
		snapshot.DiskUsedBytes = &used
		snapshot.DiskTotalBytes = &total
	}

	if docker != nil {
		// Docker 实例数只是可选监控指标，必须给本地 Docker socket 卡顿留出上限，避免阻断注册或心跳。
		ctx, cancel := context.WithTimeout(context.Background(), nodeResourceDockerTimeout)
		defer cancel()
		if count, err := docker.ListContainers(ctx, "ocm-"); err != nil {
			errs = append(errs, "docker: "+err.Error())
		} else {
			snapshot.InstanceCount = &count
		}
	}

	if len(errs) > 0 {
		snapshot.LastError = strings.Join(errs, "; ")
	}
	return snapshot, counters
}

// cpuPercent 根据相邻两次累计 tick 计算区间使用率；首次采样退化为开机以来平均使用率。
func cpuPercent(idle, total uint64, prev *nodeResourceCounters) (float64, bool) {
	if total == 0 {
		return 0, false
	}
	var idleDelta, totalDelta uint64
	if prev != nil && prev.cpuTotal > 0 && total > prev.cpuTotal && idle >= prev.cpuIdle {
		idleDelta = idle - prev.cpuIdle
		totalDelta = total - prev.cpuTotal
	} else {
		idleDelta = idle
		totalDelta = total
	}
	if totalDelta == 0 || idleDelta > totalDelta {
		return 0, false
	}
	return float64(totalDelta-idleDelta) * 100 / float64(totalDelta), true
}
