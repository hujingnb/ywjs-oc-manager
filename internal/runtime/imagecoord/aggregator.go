package imagecoord

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// PullAggregator 把 docker daemon 的 NDJSON pull 流聚合为字节级总进度。
// 不是线程安全:Feed 应在单 goroutine 串行调用,Snapshot 仅在同一 goroutine 读取。
type PullAggregator struct {
	layers map[string]layerState
}

// layerState 记录单 layer 的最近一次 progress + 是否已完成。
type layerState struct {
	current int64
	total   int64
}

// NewPullAggregator 创建空聚合器。
func NewPullAggregator() *PullAggregator {
	return &PullAggregator{layers: map[string]layerState{}}
}

// pullEvent 是 docker daemon NDJSON 的最小子集,仅解析需要的字段。
type pullEvent struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	ProgressDetail struct {
		Current int64 `json:"current"`
		Total   int64 `json:"total"`
	} `json:"progressDetail"`
}

// Feed 解析单行 NDJSON 并更新 layer 状态。
// 不带 progressDetail 也不是 Pull complete 的纯状态行被忽略。
// 解析失败返回 error,但不污染已有 layers state。
func (a *PullAggregator) Feed(line []byte) error {
	if len(line) == 0 {
		return nil
	}
	var ev pullEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return fmt.Errorf("解析 docker pull NDJSON: %w", err)
	}
	if ev.ID == "" {
		// 全局状态行(如 "Pulling from library/alpine"),无 layer ID,不计入。
		return nil
	}
	if ev.Status == "Pull complete" || ev.Status == "Already exists" {
		// daemon 不再重发 progressDetail,把已知 total 同步到 current;
		// 若该 layer 之前从未上报 total,视作 0/0(对总和无贡献)。
		st := a.layers[ev.ID]
		st.current = st.total
		a.layers[ev.ID] = st
		return nil
	}
	if ev.ProgressDetail.Total == 0 {
		// "Waiting" / "Verifying Checksum" 等中间态没有 progressDetail.total,
		// 此时只更新 current(若 daemon 给了),不动 total 估值。
		st := a.layers[ev.ID]
		if ev.ProgressDetail.Current > st.current {
			st.current = ev.ProgressDetail.Current
		}
		a.layers[ev.ID] = st
		return nil
	}
	a.layers[ev.ID] = layerState{
		current: ev.ProgressDetail.Current,
		total:   ev.ProgressDetail.Total,
	}
	return nil
}

// FeedReader 持续读取 r 直到 EOF,逐行调 Feed。
// 单行解析错误不中断后续读取,只返回最后一次错误(若有)。
func (a *PullAggregator) FeedReader(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var lastErr error
	for scanner.Scan() {
		if err := a.Feed(scanner.Bytes()); err != nil {
			lastErr = err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取 docker pull 流: %w", err)
	}
	return lastErr
}

// Snapshot 返回当前累计的 (current, total)。
func (a *PullAggregator) Snapshot() (int64, int64) {
	var current, total int64
	for _, st := range a.layers {
		current += st.current
		total += st.total
	}
	return current, total
}
