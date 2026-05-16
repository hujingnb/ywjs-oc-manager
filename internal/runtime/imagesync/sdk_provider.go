package imagesync

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/registry"
	dockerclient "github.com/docker/docker/client"
)

// LocalDockerSDKProvider 替代旧 LocalDockerCLIProvider，通过 Docker Engine HTTP API
// 完成本机镜像 inspect / save / pull。
//
// 设计动因：manager 是 docker compose 起的容器，容器内没有 docker CLI 但挂载了
// 宿主机 /var/run/docker.sock；老 CLI provider 用 exec.Command("docker", ...) 在
// manager 容器里根本跑不通，且 docker save 默认会落到宿主机文件系统，二次复制成本大。
// SDK provider 全程在 manager 容器内消费 HTTP response 流，规避以上两个问题。
type LocalDockerSDKProvider struct {
	cli       *dockerclient.Client
	authStore RegistryAuthStore
}

// NewLocalDockerSDKProvider 创建 provider。
//
// 参数：
//   - dockerHost：为空时走 client.FromEnv（读 DOCKER_HOST / 默认 unix:///var/run/docker.sock），
//     非空时显式覆盖；测试以及非标准 socket 路径用得着。
//   - configPath：为空时也会调用 LoadRegistryAuthStore("")，文件不存在视为无凭据，
//     只能拉无 auth 的公共镜像；调用方按本地部署形态决定传入路径。
func NewLocalDockerSDKProvider(dockerHost, configPath string) (*LocalDockerSDKProvider, error) {
	opts := []dockerclient.Opt{dockerclient.WithAPIVersionNegotiation()}
	if dockerHost != "" {
		opts = append(opts, dockerclient.WithHost(dockerHost))
	} else {
		opts = append(opts, dockerclient.FromEnv)
	}
	cli, err := dockerclient.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("初始化 docker client 失败: %w", err)
	}
	store, err := LoadRegistryAuthStore(configPath)
	if err != nil {
		return nil, err
	}
	return &LocalDockerSDKProvider{cli: cli, authStore: store}, nil
}

// ImageID 走 ImageInspect API 取镜像 ID，用于跟目标节点 ID 做精确比对。
//
// 镜像不存在时 docker SDK 返回 client.IsErrNotFound 可判别的错误；调用方
// （imagesync.Service）当前把任何 inspect 错误一律向上抛，再由上游 worker 决定是否拉取。
func (p *LocalDockerSDKProvider) ImageID(ctx context.Context, imageRef string) (string, error) {
	inspect, _, err := p.cli.ImageInspectWithRaw(ctx, imageRef)
	if err != nil {
		return "", fmt.Errorf("docker image inspect %s: %w", imageRef, err)
	}
	return inspect.ID, nil
}

// Archive 调 ImageSave 拿到 tar 流；返回的 ReadCloser 是 daemon HTTP body，
// 全程在 manager 容器内，不写宿主机文件。调用方必须 Close 以释放底层连接。
func (p *LocalDockerSDKProvider) Archive(ctx context.Context, imageRef string) (io.ReadCloser, error) {
	rc, err := p.cli.ImageSave(ctx, []string{imageRef})
	if err != nil {
		return nil, fmt.Errorf("docker image save %s: %w", imageRef, err)
	}
	return rc, nil
}

// Pull 调 ImagePull 触发 daemon 从 registry 下载；返回 NDJSON 流，
// 调用方（imagecoord/aggregator）负责解析 progressDetail.{current,total} 做进度聚合。
//
// X-Registry-Auth 由 authStore.AuthFor 决定：无凭据则不写头（匿名拉取），
// 命中凭据则按 docker SDK 期望的 base64url(JSON(authConfig)) 格式编码。
func (p *LocalDockerSDKProvider) Pull(ctx context.Context, imageRef string) (io.ReadCloser, error) {
	opts := image.PullOptions{}
	if auth := p.authStore.AuthFor(imageRef); auth.Username != "" {
		encoded, err := encodeRegistryAuth(auth)
		if err != nil {
			return nil, err
		}
		opts.RegistryAuth = encoded
	}
	rc, err := p.cli.ImagePull(ctx, imageRef, opts)
	if err != nil {
		return nil, fmt.Errorf("docker image pull %s: %w", imageRef, err)
	}
	return rc, nil
}

// encodeRegistryAuth 把 auth 配置编码为 docker daemon 期望的 X-Registry-Auth 格式。
//
// 格式约定（与 docker SDK registry.EncodeAuthConfig 一致）：base64.URLEncoding(JSON(authConfig))；
// 注意一定要用 URL-safe 变体（RFC 4648 §5），不是 StdEncoding——daemon 端解码用的
// 是 base64url，传错会被识别成无效凭据。
func encodeRegistryAuth(auth registry.AuthConfig) (string, error) {
	body, err := json.Marshal(auth)
	if err != nil {
		return "", fmt.Errorf("序列化 docker registry auth 失败: %w", err)
	}
	return base64.URLEncoding.EncodeToString(body), nil
}
