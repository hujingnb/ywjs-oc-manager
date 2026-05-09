package channel

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/docker/docker/pkg/stdcopy"
	"github.com/stretchr/testify/require"
)

// frameStdout 把 raw 内容包成 docker stdcopy 的 multiplexed stream（stdout 流）。
func frameStdout(t *testing.T, payload string) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	header := []byte{byte(stdcopy.Stdout), 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(header[4:8], uint32(len(payload)))
	buf.Write(header)
	buf.WriteString(payload)
	return &buf
}

// scriptedExecutor 按调用顺序返回预设的 stdout 流。
type scriptedExecutor struct {
	mu       sync.Mutex
	calls    []scriptedExecCall
	scripts  [][]byte
	scriptIx int
	err      error
}

type scriptedExecCall struct {
	nodeID, containerID string
	cmd                 []string
}

func (e *scriptedExecutor) Exec(_ context.Context, nodeID, containerID string, cmd []string) (io.Reader, func(), error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, scriptedExecCall{nodeID: nodeID, containerID: containerID, cmd: append([]string(nil), cmd...)})
	if e.err != nil {
		return nil, func() {}, e.err
	}
	if e.scriptIx >= len(e.scripts) {
		return nil, func() {}, errors.New("scriptedExecutor: out of scripts")
	}
	body := e.scripts[e.scriptIx]
	e.scriptIx++
	// 每个 chunk 帧化输出
	var buf bytes.Buffer
	header := []byte{byte(stdcopy.Stdout), 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(header[4:8], uint32(len(body)))
	buf.Write(header)
	buf.Write(body)
	return &buf, func() {}, nil
}

func TestResolveWeChatBoundIdentity_HappyPath(t *testing.T) {
	executor := &scriptedExecutor{
		scripts: [][]byte{
			[]byte(`["cba246d422f5-im-bot"]`),
			[]byte(`{"token":"sensitive","userId":"o9cq800xszCM8jyoS9YpRKpvAN9c@im.wechat","baseUrl":"https://ilinkai.weixin.qq.com","savedAt":"2026-05-02T15:00:22.500Z"}`),
		},
	}
	resolver := NewDockerBindingResolver(executor)
	got, err := resolver.ResolveWeChatBoundIdentity(context.Background(), "node-1", "ctr-1")
	require.NoError(t, err)
	want := "o9cq800xszCM8jyoS9YpRKpvAN9c@im.wechat"
	require.Equal(t, want, got)
	require.Equal(t, 2, len(executor.calls))
	require.True(t, strings.Contains(executor.calls[0].cmd[1], "accounts.json"))
	require.True(t, strings.Contains(executor.calls[1].cmd[1], "cba246d422f5-im-bot.json"))
}

func TestResolveWeChatBoundIdentity_EmptyAccountsList(t *testing.T) {
	executor := &scriptedExecutor{scripts: [][]byte{[]byte(`[]`)}}
	resolver := NewDockerBindingResolver(executor)
	_, err := resolver.ResolveWeChatBoundIdentity(context.Background(), "n", "c")
	require.ErrorIs(t, err, ErrIdentityUnavailable)
}

func TestResolveWeChatBoundIdentity_AccountMissingUserID(t *testing.T) {
	executor := &scriptedExecutor{
		scripts: [][]byte{
			[]byte(`["a"]`),
			[]byte(`{"token":"x","baseUrl":"u"}`),
		},
	}
	resolver := NewDockerBindingResolver(executor)
	_, err := resolver.ResolveWeChatBoundIdentity(context.Background(), "n", "c")
	require.ErrorIs(t, err, ErrIdentityUnavailable)
}

func TestResolveWeChatBoundIdentity_RejectsContainerlessCall(t *testing.T) {
	resolver := NewDockerBindingResolver(&scriptedExecutor{})
	_, err := resolver.ResolveWeChatBoundIdentity(context.Background(), "n", "")
	require.Error(t, err)
}

func TestResolveWeChatBoundIdentity_RejectsMalformedAccountName(t *testing.T) {
	executor := &scriptedExecutor{scripts: [][]byte{[]byte(`["bad/name"]`)}}
	resolver := NewDockerBindingResolver(executor)
	_, err := resolver.ResolveWeChatBoundIdentity(context.Background(), "n", "c")
	require.Error(t, err)
}

func TestResolveWeChatBoundIdentity_PropagatesExecError(t *testing.T) {
	executor := &scriptedExecutor{err: errors.New("docker proxy down")}
	resolver := NewDockerBindingResolver(executor)
	_, err := resolver.ResolveWeChatBoundIdentity(context.Background(), "n", "c")
	if err == nil || !strings.Contains(err.Error(), "docker proxy down") {
		t.Fatalf("应透传 exec error，得 %v", err)
	}
}
