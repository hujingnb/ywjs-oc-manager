package imagecoord

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPullAggregator_SumLayers 多 layer 进度累加。
// docker pull 是 layer 维度并行,聚合器按 layer 维护 state,每次发事件用合计值。
func TestPullAggregator_SumLayers(t *testing.T) {
	agg := NewPullAggregator()
	require.NoError(t, agg.Feed([]byte(`{"id":"a","status":"Downloading","progressDetail":{"current":100,"total":1000}}`)))
	require.NoError(t, agg.Feed([]byte(`{"id":"b","status":"Downloading","progressDetail":{"current":200,"total":2000}}`)))
	current, total := agg.Snapshot()
	assert.EqualValues(t, 300, current)
	assert.EqualValues(t, 3000, total)
}

// TestPullAggregator_PullCompleteCountsAsFull 收到 "Pull complete" 视该 layer current=total。
// 因为 docker daemon 在 layer 完成后不再重发 Downloading,只发 Pull complete。
func TestPullAggregator_PullCompleteCountsAsFull(t *testing.T) {
	agg := NewPullAggregator()
	require.NoError(t, agg.Feed([]byte(`{"id":"a","status":"Downloading","progressDetail":{"current":500,"total":1000}}`)))
	require.NoError(t, agg.Feed([]byte(`{"id":"a","status":"Pull complete"}`)))
	current, total := agg.Snapshot()
	assert.EqualValues(t, 1000, current)
	assert.EqualValues(t, 1000, total)
}

// TestPullAggregator_IgnoresStatusOnlyLines 不带 progressDetail 的状态行不动累加器。
func TestPullAggregator_IgnoresStatusOnlyLines(t *testing.T) {
	agg := NewPullAggregator()
	require.NoError(t, agg.Feed([]byte(`{"status":"Pulling from library/alpine"}`)))
	current, total := agg.Snapshot()
	assert.EqualValues(t, 0, current)
	assert.EqualValues(t, 0, total)
}

// TestPullAggregator_FeedReader 验证从 io.Reader 持续 feed 的 helper。
func TestPullAggregator_FeedReader(t *testing.T) {
	agg := NewPullAggregator()
	body := strings.Join([]string{
		`{"id":"a","status":"Downloading","progressDetail":{"current":100,"total":500}}`,
		`{"id":"a","status":"Pull complete"}`,
		`{"id":"b","status":"Downloading","progressDetail":{"current":250,"total":500}}`,
	}, "\n") + "\n"
	require.NoError(t, agg.FeedReader(strings.NewReader(body)))
	current, total := agg.Snapshot()
	assert.EqualValues(t, 750, current)
	assert.EqualValues(t, 1000, total)
}
