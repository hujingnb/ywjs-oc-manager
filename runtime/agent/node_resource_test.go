package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNodeResourceCollectorParsesLinuxProcFiles 验证节点资源采集器能解析 Linux /proc 文本的正常路径。
func TestNodeResourceCollectorParsesLinuxProcFiles(t *testing.T) {
	dir := t.TempDir()
	statPath := filepath.Join(dir, "stat")
	memInfoPath := filepath.Join(dir, "meminfo")
	netDevPath := filepath.Join(dir, "net_dev")

	require.NoError(t, os.WriteFile(statPath, []byte("cpu  100 20 30 400 50 6 7 0 0 0\n"), 0o600))
	require.NoError(t, os.WriteFile(memInfoPath, []byte("MemTotal:       1024000 kB\nMemFree:         100000 kB\nBuffers:          20000 kB\nCached:          300000 kB\nSReclaimable:     40000 kB\nShmem:            10000 kB\n"), 0o600))
	require.NoError(t, os.WriteFile(netDevPath, []byte("Inter-|   Receive                                                |  Transmit\n face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n  lo: 1000 1 0 0 0 0 0 0 2000 1 0 0 0 0 0 0\neth0: 3000 2 0 0 0 0 0 0 4000 2 0 0 0 0 0 0\n"), 0o600))

	statRaw, err := os.ReadFile(statPath)
	require.NoError(t, err)
	memInfoRaw, err := os.ReadFile(memInfoPath)
	require.NoError(t, err)
	netDevRaw, err := os.ReadFile(netDevPath)
	require.NoError(t, err)

	idle, total, err := parseProcStatCPU(string(statRaw))
	require.NoError(t, err)
	assert.Equal(t, uint64(450), idle)
	assert.Equal(t, uint64(613), total)

	used, totalMemory, err := parseMemInfo(string(memInfoRaw))
	require.NoError(t, err)
	assert.Equal(t, int64(587776000), used)
	assert.Equal(t, int64(1048576000), totalMemory)

	rx, tx, err := parseNetDev(string(netDevRaw))
	require.NoError(t, err)
	assert.Equal(t, int64(4000), rx)
	assert.Equal(t, int64(6000), tx)
}
