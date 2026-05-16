package imagesync

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/docker/docker/api/types/registry"
)

// RegistryAuthStore 把 ~/.docker/config.json 中的静态 auths 字段加载到内存。
//
// 设计取舍：一期只支持 base64(user:pass) 格式，不处理 credentials helper /
// keychain（如 docker-credential-osxkeychain、ecr-login 等）。
// 凭据轮换通过重启 manager 生效，避免在镜像同步路径上引入文件监听 / 热加载复杂度。
type RegistryAuthStore struct {
	// auths 的 key 是 normalized registry hostname（去 https:// 前缀、去尾部 path）。
	// docker.io / index.docker.io / registry-1.docker.io / hub.docker.com 都规一化到 "docker.io"，
	// 这样 image ref 写 "library/foo:tag" 也能命中 hub 凭据。
	auths map[string]registry.AuthConfig
}

// dockerConfigFile 仅解析当前关心的 auths 段；其它字段（credsStore / credHelpers
// / currentContext 等）有意忽略，避免一期承担 cred helper 调用复杂度。
type dockerConfigFile struct {
	Auths map[string]struct {
		Auth string `json:"auth"`
	} `json:"auths"`
}

// LoadRegistryAuthStore 读取并解析 config.json。
//
// 文件不存在视为"无凭据"（返回空 store + nil err），原因：
// 公共镜像（docker hub library/* 等）不需要 auth，缺文件不应阻塞 manager 启动；
// 调用方拉取私仓镜像失败时由 daemon 返回 401，错误链路足以定位是凭据缺失。
func LoadRegistryAuthStore(path string) (RegistryAuthStore, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return RegistryAuthStore{auths: map[string]registry.AuthConfig{}}, nil
		}
		return RegistryAuthStore{}, fmt.Errorf("读取 docker config %s 失败: %w", path, err)
	}
	var raw dockerConfigFile
	if err := json.Unmarshal(body, &raw); err != nil {
		return RegistryAuthStore{}, fmt.Errorf("解析 docker config 失败: %w", err)
	}
	auths := make(map[string]registry.AuthConfig, len(raw.Auths))
	for rawHost, entry := range raw.Auths {
		host := normalizeRegistryHost(rawHost)
		if host == "" || entry.Auth == "" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(entry.Auth)
		if err != nil {
			// 单条解析失败不阻断整个 store 加载，跳过当前条以保证其它 registry 仍可用；
			// 调用方拉取该 registry 镜像时会因缺凭据走匿名拉取，daemon 端会返回 401。
			continue
		}
		idx := strings.IndexByte(string(decoded), ':')
		if idx <= 0 {
			continue
		}
		auths[host] = registry.AuthConfig{
			Username:      string(decoded[:idx]),
			Password:      string(decoded[idx+1:]),
			ServerAddress: host,
		}
	}
	return RegistryAuthStore{auths: auths}, nil
}

// normalizeRegistryHost 把 docker login 写入的多种格式统一成 hostname。
// 例：
//   - "https://index.docker.io/v1/" → "docker.io"
//   - "registry.example.com"        → "registry.example.com"
//   - "http://registry.example.com" → "registry.example.com"
func normalizeRegistryHost(raw string) string {
	host := strings.TrimSpace(raw)
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	if host == "index.docker.io" || host == "registry-1.docker.io" || host == "hub.docker.com" {
		return "docker.io"
	}
	return host
}

// AuthFor 根据镜像引用挑选凭据。
//
// 镜像形如 "registry.example.com/team/app:tag" / "library/foo:tag" / "foo:tag"。
// 未命中时返回零值 AuthConfig，调用方不传 X-Registry-Auth 头，daemon 走匿名拉取
// （public 镜像照常可用，私仓会在 daemon 端 401 失败）。
func (s RegistryAuthStore) AuthFor(image string) registry.AuthConfig {
	host := imageHost(image)
	if cfg, ok := s.auths[host]; ok {
		return cfg
	}
	return registry.AuthConfig{}
}

// imageHost 复用 docker 官方约定：第一段不包含 '.' / ':' 且非 "localhost" 视为
// docker.io 默认仓库；其余把第一段当 registry hostname。
func imageHost(image string) string {
	idx := strings.IndexByte(image, '/')
	if idx < 0 {
		return "docker.io"
	}
	first := image[:idx]
	if !strings.ContainsAny(first, ".:") && first != "localhost" {
		return "docker.io"
	}
	return first
}
