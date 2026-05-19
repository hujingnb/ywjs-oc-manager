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
	// Hermes 时代命令统一为 oc-channel-login --channel weixin,
	// 由镜像内统一入口完成扫码 + 凭证落盘(hermes 自管目录,manager 不再写 .env)。
	ExecAttach(ctx context.Context, containerID string, cmd []string) (io.ReadCloser, error)
	// ExecExitCode 等待上一次 exec 完成并返回 exit code。
	ExecExitCode(ctx context.Context) (int, error)
}

// WeixinEventType 表示扫码登录过程中产生的事件类型。
type WeixinEventType string

const (
	// WeixinEventQRCode 收到二维码 URL(供前端展示)。
	WeixinEventQRCode WeixinEventType = "qrcode"
	// WeixinEventBound 扫码成功,容器内 oc-channel-login 已把凭证落盘到 hermes 自管目录
	// (/opt/data/weixin/accounts/),manager 仅需触发 hermes 重启重新读 platforms 配置。
	WeixinEventBound WeixinEventType = "bound"
	// WeixinEventFailed 登录失败或超时。
	WeixinEventFailed WeixinEventType = "failed"
)

// WeixinEvent 是 runner 推给上层的事件。
// Hermes 时代凭证由容器内 oc-channel-login 自管,manager 不再透传 account_id/token/base_url
// 等字段;Bound 事件只表达"扫码完成"的信号,身份由 BindingResolver 从 plugin state 解析。
type WeixinEvent struct {
	Type      WeixinEventType
	QRCodeURL string // QRCode 类型用
	Error     string // Failed 类型用
}

// WeixinRunner 是微信扫码登录的协调器。
// 通过 docker exec 调用容器内 oc-channel-login --channel weixin 命令,stdcopy 分流:
//   - stderr 行级流,匹配 QR URL → QRCode 事件;其余作为错误信息累积
//   - stdout 累积成单行 JSON → 解析为 ChannelResult{status,reason}:
//     status=bound 触发 Bound 事件,其它 status 或 exit!=0 触发 Failed 事件
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
// Hermes 时代统一命令入口为 oc-channel-login --channel weixin,
// 由镜像内脚本完成扫码 + 凭证落盘到 /opt/data/weixin/accounts/,
// manager 不再解析 token / base_url / user_id 等凭证字段。
func (r *WeixinRunner) StreamWeChatLogin(ctx context.Context, containerID string) (<-chan WeixinEvent, error) {
	stream, err := r.executor.ExecAttach(ctx, containerID, []string{"oc-channel-login", "--channel", "weixin"})
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

		// result 反序列化 oc-channel-login 输出的 JSON,字段对齐 hermes.ChannelResult:
		//   {"status":"bound"|"failed"|"timeout","reason":"..."}
		// status=bound 表示容器内已完成凭证落盘;其它 status 视为失败,reason 写入 Error。
		var result struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(stdoutBytes), &result); err != nil {
			events <- WeixinEvent{
				Type:  WeixinEventFailed,
				Error: fmt.Sprintf("解析 oc-channel-login 输出 JSON 失败: %v", err),
			}
			return
		}
		if result.Status != "bound" {
			reason := result.Reason
			if reason == "" {
				reason = result.Status
			}
			events <- WeixinEvent{Type: WeixinEventFailed, Error: reason}
			return
		}
		events <- WeixinEvent{Type: WeixinEventBound}
	}()
	return events, nil
}
