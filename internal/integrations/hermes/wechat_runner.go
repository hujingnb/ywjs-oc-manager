package hermes

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/docker/docker/pkg/stdcopy"
)

// ContainerExecutor 是 docker SDK 的最小抽象,便于测试桩。
// 生产实现位于 internal/integrations/channel/wechat_runner.go(已存在)的瘦适配,
// 此处只声明接口。
type ContainerExecutor interface {
	// ExecAttach 在容器内 exec 命令,返回 multiplex stdout/stderr 流。
	// 命令由实现固定:["/usr/local/bin/oc-weixin-login"]。
	ExecAttach(ctx context.Context, containerID string, cmd []string) (io.ReadCloser, error)
	// ExecExitCode 等待上一次 exec 完成并返回 exit code。
	ExecExitCode(ctx context.Context) (int, error)
}

// WeixinEventType 表示扫码登录过程中产生的事件类型。
type WeixinEventType string

const (
	// WeixinEventQRCode 收到二维码 URL(供前端展示)。
	WeixinEventQRCode WeixinEventType = "qrcode"
	// WeixinEventBound 扫码成功,凭证可用。
	WeixinEventBound WeixinEventType = "bound"
	// WeixinEventFailed 登录失败或超时。
	WeixinEventFailed WeixinEventType = "failed"
)

// WeixinEvent 是 runner 推给上层的事件。
// 不同 Type 用到的字段不同;未用字段保持空值。
type WeixinEvent struct {
	Type      WeixinEventType
	QRCodeURL string // QRCode 类型用
	AccountID string // Bound 类型用 = iLink bot 身份 <hex>@im.bot
	Token     string
	BaseURL   string
	UserID    string
	Error     string // Failed 类型用
}

// WeixinRunner 是微信扫码登录的协调器。
// 通过 docker exec 调用容器内的 oc-weixin-login 脚本,stdcopy 分流:
//   - stdout 累积成单行 JSON → 解析为 Bound 事件
//   - stderr 行级流,匹配 QR URL → QRCode 事件;其余进 Failed.Error
type WeixinRunner struct {
	executor ContainerExecutor
}

// NewWeixinRunner 创建 runner。
func NewWeixinRunner(executor ContainerExecutor) *WeixinRunner {
	return &WeixinRunner{executor: executor}
}

// StreamWeChatLogin 触发一次扫码登录,返回事件 channel。
// channel 在登录结束(成功/失败/超时)后关闭。
// 调用方负责消费 channel 直到关闭;不消费会阻塞 runner goroutine。
func (r *WeixinRunner) StreamWeChatLogin(ctx context.Context, containerID string) (<-chan WeixinEvent, error) {
	stream, err := r.executor.ExecAttach(ctx, containerID, []string{"/usr/local/bin/oc-weixin-login"})
	if err != nil {
		return nil, fmt.Errorf("ExecAttach 失败: %w", err)
	}

	events := make(chan WeixinEvent, 8)
	go func() {
		defer close(events)
		defer stream.Close()

		// 用 io.Pipe + stdcopy.StdCopy 把 multiplex 流拆成两路。
		stdoutR, stdoutW := io.Pipe()
		stderrR, stderrW := io.Pipe()
		copyDone := make(chan error, 1)
		go func() {
			_, e := stdcopy.StdCopy(stdoutW, stderrW, stream)
			stdoutW.Close()
			stderrW.Close()
			copyDone <- e
		}()

		// 同步读 stderr:行级,匹配 QR URL。
		stderrDone := make(chan struct{})
		var stderrText strings.Builder
		go func() {
			defer close(stderrDone)
			scanner := bufio.NewScanner(stderrR)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" {
					continue
				}
				if strings.HasPrefix(line, "https://liteapp.weixin.qq.com/") {
					events <- WeixinEvent{Type: WeixinEventQRCode, QRCodeURL: line}
					continue
				}
				stderrText.WriteString(line)
				stderrText.WriteString("\n")
			}
		}()

		// 同步读 stdout:累积成单字符串,最后整体 JSON 解码。
		stdoutBytes, _ := io.ReadAll(stdoutR)
		<-stderrDone
		<-copyDone

		exitCode, _ := r.executor.ExecExitCode(ctx)
		if exitCode != 0 {
			events <- WeixinEvent{
				Type:  WeixinEventFailed,
				Error: strings.TrimSpace(stderrText.String()),
			}
			return
		}

		// cred 反序列化 oc-weixin-login.py 输出的 JSON。字段名直接绑定到
		// 上游 gateway.platforms.weixin.qr_login 函数的返回值 (account_id /
		// token / base_url / user_id);若上游变更字段名,此处会静默拿到零值。
		var cred struct {
			AccountID string `json:"account_id"`
			Token     string `json:"token"`
			BaseURL   string `json:"base_url"`
			UserID    string `json:"user_id"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(stdoutBytes), &cred); err != nil {
			events <- WeixinEvent{
				Type:  WeixinEventFailed,
				Error: fmt.Sprintf("解析凭证 JSON 失败: %v", err),
			}
			return
		}

		events <- WeixinEvent{
			Type:      WeixinEventBound,
			AccountID: cred.AccountID,
			Token:     cred.Token,
			BaseURL:   cred.BaseURL,
			UserID:    cred.UserID,
		}
	}()
	return events, nil
}
