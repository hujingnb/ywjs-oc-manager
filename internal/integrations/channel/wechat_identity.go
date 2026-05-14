package channel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/docker/docker/pkg/stdcopy"
)

// BindingResolver 抽象在 runtime 容器内取出真实账号标识的能力。
//
// 实测：Hermes weixin plugin 与 legacy OpenClaw weixin plugin 共用相同的 state 文件
// 路径（/root/.openclaw/openclaw-weixin/accounts/...）；Hermes 容器通过 bind mount
// apps/<id>/weixin → /root/.openclaw/openclaw-weixin/ 持久化 plugin state，含字段：
//
//	{"token":"<sensitive>","userId":"<openid>@im.wechat","baseUrl":"...","savedAt":"..."}
//
// manager 收到 bound 事件后必须再调一次本接口从 plugin state 取 userId 补到
// channel_bindings.bound_identity。
type BindingResolver interface {
	ResolveWeChatBoundIdentity(ctx context.Context, nodeID, containerID string) (string, error)
}

// DockerBindingResolver 用既有 ContainerExecutor 通过 docker exec 读容器内 plugin state 文件。
type DockerBindingResolver struct {
	executor ContainerExecutor
}

// NewDockerBindingResolver 构造 resolver。executor 必须非 nil；
// 复用 wechat_runner.go 的 ContainerExecutor，避免再启一条 docker SDK 链路。
func NewDockerBindingResolver(executor ContainerExecutor) *DockerBindingResolver {
	return &DockerBindingResolver{executor: executor}
}

// ResolveWeChatBoundIdentity 在容器内 cat 出 accounts.json + accounts/<name>.json 取 userId。
//
// 步骤（路径沿用 legacy OpenClaw weixin plugin 约定，Hermes 容器通过 bind mount 保持相同路径）：
//  1. cat /root/.openclaw/openclaw-weixin/accounts.json → ["account-name"] JSON 数组
//  2. cat /root/.openclaw/openclaw-weixin/accounts/<account-name>.json → 含 userId 的 JSON
//  3. 返回 userId 字段（OpenID 形态）
//
// 任一步骤失败返回原始错误；空数组 / 缺 userId 返回 ErrIdentityUnavailable。
// 调用方在收到 ErrIdentityUnavailable 时不应把 binding 标记 failed，仅留 BoundIdentity 为空，
// 等下次 PollAuth 重试（plugin 写文件可能慢于 stdout 报告 bound）。
func (r *DockerBindingResolver) ResolveWeChatBoundIdentity(ctx context.Context, nodeID, containerID string) (string, error) {
	if r.executor == nil {
		return "", errors.New("container executor 未配置")
	}
	if containerID == "" {
		return "", errors.New("containerID 不能为空")
	}

	// Step 1：拿 account 名列表。
	accountsRaw, err := r.execCat(ctx, nodeID, containerID,
		"/root/.openclaw/openclaw-weixin/accounts.json")
	if err != nil {
		return "", fmt.Errorf("读 accounts.json 失败: %w", err)
	}
	var accounts []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(accountsRaw)), &accounts); err != nil {
		return "", fmt.Errorf("accounts.json 不是 JSON 数组: %w", err)
	}
	if len(accounts) == 0 {
		return "", ErrIdentityUnavailable
	}

	// 取第一个账号；v1 假设单 binding 单账号。
	name := accounts[0]
	if name == "" || strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("非法 account 名: %q", name)
	}

	// Step 2：读对应 account JSON 取 userId。
	accountRaw, err := r.execCat(ctx, nodeID, containerID,
		fmt.Sprintf("/root/.openclaw/openclaw-weixin/accounts/%s.json", name))
	if err != nil {
		return "", fmt.Errorf("读 accounts/%s.json 失败: %w", name, err)
	}
	var account struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal([]byte(accountRaw), &account); err != nil {
		return "", fmt.Errorf("解析 account JSON 失败: %w", err)
	}
	if account.UserID == "" {
		return "", ErrIdentityUnavailable
	}
	return account.UserID, nil
}

// ErrIdentityUnavailable 表示 plugin state 中尚无可用 userId（accounts 为空或缺字段）。
// 调用方应等下次 polling 重试，不应把 binding 推到 failed。
var ErrIdentityUnavailable = errors.New("plugin state userId 暂不可用")

// execCat 通过 docker exec 跑 cat 拿文件内容（multiplexed stdout 流）。
func (r *DockerBindingResolver) execCat(ctx context.Context, nodeID, containerID, path string) (string, error) {
	reader, closer, err := r.executor.Exec(ctx, nodeID, containerID, []string{"cat", path})
	if err != nil {
		return "", err
	}
	var closeOnce sync.Once
	defer closeOnce.Do(func() {
		if closer != nil {
			closer()
		}
	})

	stdout, stdoutW := io.Pipe()
	demuxDone := make(chan error, 1)
	go func() {
		_, err := stdcopy.StdCopy(stdoutW, io.Discard, reader)
		_ = stdoutW.Close()
		demuxDone <- err
	}()

	var sb strings.Builder
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		sb.Write(sc.Bytes())
		sb.WriteByte('\n')
	}
	<-demuxDone
	return strings.TrimSpace(sb.String()), nil
}
