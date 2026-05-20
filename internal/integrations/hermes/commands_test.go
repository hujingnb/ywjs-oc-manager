package hermes

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeExec 记录 ContainerExec 调用的命令并返回预设输出。
type fakeExec struct {
	lastCmd []string
	stdout  string
	stderr  string
	err     error
}

func (f *fakeExec) ContainerExec(_ context.Context, _, _ string, cmd []string) (stdout, stderr io.Reader, err error) {
	f.lastCmd = cmd
	return strings.NewReader(f.stdout), strings.NewReader(f.stderr), f.err
}

// 验证 RunInfo 把 stdout JSON 解码到 Info 结构体，并执行 oc-info 命令。
func TestRunInfo_ParsesJSON(t *testing.T) {
	e := &fakeExec{stdout: `{"variant":"hermes-v2026.5.16","hermes_upstream_ref":"abc","oc_entrypoint_version":"1","built_at":"2026-05-19T00:00:00Z"}` + "\n"}
	info, err := RunInfo(context.Background(), e, "node-1", "container-1")
	require.NoError(t, err)
	assert.Equal(t, "hermes-v2026.5.16", info.Variant)
	assert.Equal(t, "abc", info.HermesUpstreamRef)
	assert.Equal(t, []string{"oc-info"}, e.lastCmd)
}

// 验证 RunChannelStatus 拼出 --channel 参数并解码 bound 字段。
func TestRunChannelStatus_BuildsCmd(t *testing.T) {
	e := &fakeExec{stdout: `{"channel":"weixin","bound":true,"account_id":"x"}` + "\n"}
	s, err := RunChannelStatus(context.Background(), e, "n", "c", "weixin")
	require.NoError(t, err)
	assert.True(t, s.Bound)
	assert.Equal(t, []string{"oc-channel-status", "--channel", "weixin"}, e.lastCmd)
}

// 验证 ContainerExec 返回错误时，RunInfo 把错误透传给调用方。
func TestRunInfo_ExecError(t *testing.T) {
	e := &fakeExec{err: errors.New("docker boom")}
	_, err := RunInfo(context.Background(), e, "n", "c")
	require.Error(t, err)
}
