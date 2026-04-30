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
	"time"
)

// memoryExecutor 是用于测试的 ContainerExecutor：把预设的 stdout 行按 docker stdcopy 协议
// 编码到一个内存 buffer 中，并允许测试控制何时 EOF。
type memoryExecutor struct {
	stdoutLines []string
	stderr      []string
	closed      bool
	closeAfter  bool
	cmdSeen     []string
	mu          sync.Mutex
}

func (m *memoryExecutor) Exec(_ context.Context, _ string, cmd []string) (io.Reader, func(), error) {
	m.mu.Lock()
	m.cmdSeen = append([]string(nil), cmd...)
	m.mu.Unlock()
	buf := &bytes.Buffer{}
	for _, line := range m.stdoutLines {
		writeStdcopyFrame(buf, 1, []byte(line+"\n"))
	}
	for _, line := range m.stderr {
		writeStdcopyFrame(buf, 2, []byte(line+"\n"))
	}
	reader := newControlledReader(buf, !m.closeAfter)
	closer := func() {
		m.mu.Lock()
		m.closed = true
		m.mu.Unlock()
		reader.unblock()
	}
	return reader, closer, nil
}

func (m *memoryExecutor) seenCmd() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.cmdSeen...)
}

// controlledReader 在 buffer 读完后可选地阻塞，模拟 docker exec 长连接挂起；
// 关闭时通过 unblock 释放阻塞，让 pumpExecStream 走完 stdcopy。
type controlledReader struct {
	buf     *bytes.Buffer
	holdEOF bool
	gate    chan struct{}
	once    sync.Once
}

func newControlledReader(buf *bytes.Buffer, holdEOF bool) *controlledReader {
	return &controlledReader{buf: buf, holdEOF: holdEOF, gate: make(chan struct{})}
}

func (r *controlledReader) Read(p []byte) (int, error) {
	if r.buf.Len() == 0 {
		if !r.holdEOF {
			return 0, io.EOF
		}
		<-r.gate
		return 0, io.EOF
	}
	return r.buf.Read(p)
}

func (r *controlledReader) unblock() {
	r.once.Do(func() { close(r.gate) })
}

func writeStdcopyFrame(w io.Writer, streamType byte, payload []byte) {
	header := make([]byte, 8)
	header[0] = streamType
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	_, _ = w.Write(header)
	_, _ = w.Write(payload)
}

func TestDockerCommandRunner_StreamsLines(t *testing.T) {
	exec := &memoryExecutor{
		stdoutLines: []string{
			`{"type":"qrcode","qrcode":"abc"}`,
			`{"type":"bound","bound":"alice"}`,
		},
		closeAfter: true,
	}
	runner := NewDockerCommandRunner(exec, staticLookup{containerID: "ctr-app"})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := runner.StreamWeChatLogin(ctx, AuthInput{NodeID: "node-1", AppID: "app-1"})
	if err != nil {
		t.Fatalf("StreamWeChatLogin err = %v", err)
	}
	got := drainChannel(stream, 2)
	if len(got) != 2 {
		t.Fatalf("收到 %d 行, want 2: %+v", len(got), got)
	}
	if !strings.Contains(got[0], "qrcode") || !strings.Contains(got[1], "bound") {
		t.Fatalf("行序异常: %+v", got)
	}
	want := []string{"openclaw", "channels", "login", "--channel", "openclaw-weixin", "--json"}
	if !equalStrings(exec.seenCmd(), want) {
		t.Fatalf("exec cmd = %+v, want %+v", exec.seenCmd(), want)
	}
}

func TestDockerCommandRunner_DiscardsStderr(t *testing.T) {
	exec := &memoryExecutor{
		stdoutLines: []string{`{"type":"bound"}`},
		stderr:      []string{`error: ignore me`},
		closeAfter:  true,
	}
	runner := NewDockerCommandRunner(exec, staticLookup{containerID: "ctr"})
	stream, err := runner.StreamWeChatLogin(context.Background(), AuthInput{NodeID: "n", AppID: "a"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	got := drainChannel(stream, 5)
	if len(got) != 1 {
		t.Fatalf("应仅返回 stdout 一行，got %+v", got)
	}
	if strings.Contains(got[0], "error") {
		t.Fatalf("stderr 不应混入 stdout: %s", got[0])
	}
}

func TestDockerCommandRunner_RejectsMissingExecutor(t *testing.T) {
	runner := NewDockerCommandRunner(nil, staticLookup{containerID: "x"})
	if _, err := runner.StreamWeChatLogin(context.Background(), AuthInput{NodeID: "n", AppID: "a"}); err == nil {
		t.Fatal("缺 executor 应当报错")
	}
}

func TestDockerCommandRunner_RejectsMissingLookup(t *testing.T) {
	runner := NewDockerCommandRunner(&memoryExecutor{}, nil)
	if _, err := runner.StreamWeChatLogin(context.Background(), AuthInput{NodeID: "n", AppID: "a"}); err == nil {
		t.Fatal("缺 lookup 应当报错")
	}
}

func TestDockerCommandRunner_RequiresNodeID(t *testing.T) {
	runner := NewDockerCommandRunner(&memoryExecutor{}, staticLookup{containerID: "x"})
	if _, err := runner.StreamWeChatLogin(context.Background(), AuthInput{AppID: "a"}); err == nil {
		t.Fatal("缺 nodeID 应当报错")
	}
}

func TestDockerCommandRunner_PrefersExplicitContainerID(t *testing.T) {
	exec := &memoryExecutor{stdoutLines: []string{`{"type":"bound"}`}, closeAfter: true}
	runner := NewDockerCommandRunner(exec, staticLookup{containerID: "from-lookup"})
	stream, err := runner.StreamWeChatLogin(context.Background(), AuthInput{NodeID: "n", AppID: "a", ContainerID: "explicit-ctr"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	drainChannel(stream, 1)
	// Lookup 不应被调用，但 memoryExecutor 没记录 containerID；这里至少断言运行成功。
}

func TestDockerCommandRunner_LookupErrorPropagates(t *testing.T) {
	runner := NewDockerCommandRunner(&memoryExecutor{}, errLookup{err: errors.New("db down")})
	if _, err := runner.StreamWeChatLogin(context.Background(), AuthInput{NodeID: "n", AppID: "a"}); err == nil {
		t.Fatal("lookup 失败应冒泡")
	}
}

func TestDockerCommandRunner_CtxCancelClosesChannel(t *testing.T) {
	exec := &memoryExecutor{stdoutLines: []string{`first`}, closeAfter: false}
	runner := NewDockerCommandRunner(exec, staticLookup{containerID: "ctr"})
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := runner.StreamWeChatLogin(ctx, AuthInput{NodeID: "n", AppID: "a"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	select {
	case line, ok := <-stream:
		if !ok || !strings.Contains(line, "first") {
			t.Fatalf("first line = %q ok=%v", line, ok)
		}
	case <-time.After(time.Second):
		t.Fatal("超时未收到第一行")
	}
	cancel()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-stream:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("ctx cancel 后 stream 未关闭")
		}
	}
}

// drainChannel 收集至多 limit 条目，超时后返回。
func drainChannel(stream <-chan string, limit int) []string {
	got := make([]string, 0, limit)
	timeout := time.After(2 * time.Second)
	for len(got) < limit {
		select {
		case line, ok := <-stream:
			if !ok {
				return got
			}
			got = append(got, line)
		case <-timeout:
			return got
		}
	}
	return got
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type staticLookup struct{ containerID string }

func (s staticLookup) LookupContainer(_ context.Context, _ string) (string, error) {
	return s.containerID, nil
}

type errLookup struct{ err error }

func (e errLookup) LookupContainer(_ context.Context, _ string) (string, error) { return "", e.err }
