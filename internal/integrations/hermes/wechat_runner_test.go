package hermes

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeExecutor 模拟 ContainerExecutor 接口,允许测试驱动 stdout/stderr/exit code。
type fakeExecutor struct {
	stdoutFrames [][]byte
	stderrFrames [][]byte
	exitCode     int
	err          error
}

func (f *fakeExecutor) ExecAttach(ctx context.Context, containerID string, cmd []string) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	// stdcopy multiplex 格式:首字节 stream type (1=stdout/2=stderr), 1-3 字节保留, 4-7 字节 BE length。
	buf := &bytes.Buffer{}
	writeFrame := func(stream byte, payload []byte) {
		header := make([]byte, 8)
		header[0] = stream
		binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
		buf.Write(header)
		buf.Write(payload)
	}
	for _, p := range f.stderrFrames {
		writeFrame(2, p)
	}
	for _, p := range f.stdoutFrames {
		writeFrame(1, p)
	}
	return io.NopCloser(buf), nil
}

func (f *fakeExecutor) ExecExitCode(ctx context.Context) (int, error) {
	return f.exitCode, nil
}

// 覆盖正常路径:扫码 → 收 QR 事件 → 收 bound 事件。
// Hermes 时代 oc-channel-login 不再透传 token / base_url 等凭证字段,
// stdout 仅返回 {"status":"bound"},凭证由容器内自管。
func TestStreamWeChatLogin_SuccessYieldsQRThenBound(t *testing.T) {
	exec := &fakeExecutor{
		stderrFrames: [][]byte{[]byte("https://liteapp.weixin.qq.com/q/abc?qrcode=tok&bot_type=3\n")},
		stdoutFrames: [][]byte{[]byte(`{"status":"bound"}` + "\n")},
		exitCode:     0,
	}
	runner := NewWeixinRunner(exec)

	events, err := runner.StreamWeChatLogin(context.Background(), "hermes-app-1")
	require.NoError(t, err)

	var qr, bound *WeixinEvent
	for ev := range events {
		switch ev.Type {
		case WeixinEventQRCode:
			qr = &ev
		case WeixinEventBound:
			bound = &ev
		}
	}
	require.NotNil(t, qr, "应收到 qrcode 事件")
	require.Equal(t, "https://liteapp.weixin.qq.com/q/abc?qrcode=tok&bot_type=3", qr.QRCodeURL)
	require.NotNil(t, bound, "应收到 bound 事件")
}

// 覆盖失败路径:exit != 0 时发 failed 事件,不带 bound。
func TestStreamWeChatLogin_NonZeroExitYieldsFailedEvent(t *testing.T) {
	exec := &fakeExecutor{
		stderrFrames: [][]byte{[]byte("LOGIN_FAILED_OR_TIMEOUT\n")},
		exitCode:     2,
	}
	runner := NewWeixinRunner(exec)
	events, err := runner.StreamWeChatLogin(context.Background(), "hermes-app-1")
	require.NoError(t, err)

	var failed *WeixinEvent
	for ev := range events {
		if ev.Type == WeixinEventFailed {
			failed = &ev
		}
	}
	require.NotNil(t, failed)
	require.Contains(t, failed.Error, "LOGIN_FAILED_OR_TIMEOUT")
}

// 覆盖 status!=bound 路径:stdout 返回 timeout/failed 也应转化为 Failed 事件,
// reason 字段写入 Error 供上层审计记录。
func TestStreamWeChatLogin_StatusNotBoundYieldsFailed(t *testing.T) {
	exec := &fakeExecutor{
		stdoutFrames: [][]byte{[]byte(`{"status":"timeout","reason":"qr expired"}` + "\n")},
		exitCode:     0,
	}
	runner := NewWeixinRunner(exec)
	events, err := runner.StreamWeChatLogin(context.Background(), "hermes-app-1")
	require.NoError(t, err)

	var failed *WeixinEvent
	for ev := range events {
		if ev.Type == WeixinEventFailed {
			failed = &ev
		}
	}
	require.NotNil(t, failed)
	require.Equal(t, "qr expired", failed.Error)
}

// 覆盖 docker exec 启动就失败的场景。
func TestStreamWeChatLogin_ExecAttachError(t *testing.T) {
	exec := &fakeExecutor{err: errors.New("docker daemon down")}
	runner := NewWeixinRunner(exec)
	_, err := runner.StreamWeChatLogin(context.Background(), "hermes-app-1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "docker daemon down")
}
