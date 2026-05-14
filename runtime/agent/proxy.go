package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

// dockerProxyPathPrefix 是 manager 调用 agent docker proxy 时使用的统一前缀。
// agent 在转发到本机 docker socket 前必须把它从 path 上裁掉，docker daemon 才能识别请求。
const dockerProxyPathPrefix = "/v1/docker"

// NewDockerProxyHandler 构造 agent 暴露给 manager 的 docker socket 反向代理。
//
// 请求路径 /v1/docker/<rest> 会被改写为 /<rest> 后通过 unix socket 转发到本机 docker daemon。
// 中间件按顺序应用：
//  1. 源 IP 白名单（trustedCIDR != "" 时启用），不命中返回 403；
//  2. Bearer token 校验（agentToken != "" 时启用），缺失或不一致返回 401；
//  3. 创建容器请求的 mount source 路径重写（agent 视角 → 宿主视角，仅当 agentDataRoot
//     与 hostDataRoot 不同时启用，详见 detectHostDataRoot）；
//  4. 路径前缀裁切并交给 ReverseProxy 转发。
//
// 非 /v1/docker 前缀的请求一律 404，避免代理被滥用为通用 HTTP 入口。
// 这里不挂 /healthz 等附加路径，agent main.go 自己的 mux 负责其它端点。
//
// agentDataRoot / hostDataRoot 用于 §3 的 mount source 重写：agent 容器持有的
// dataRoot（默认 /var/lib/oc-agent）实际是宿主某路径的 bind mount；docker daemon
// 在宿主视角解析 mount source，不重写会导致 source 路径不存在 → docker 自动创建
// 空目录占位 → 文件级 mount（如 models.json）退化为目录，legacy OpenClaw 读不到内容
//（Hermes 时代已弃用 file-level mount，但路径重写逻辑保留以向后兼容）。
// 两者相同（直接在宿主跑 / 不在容器内）时跳过重写，行为等价于原代理。
func NewDockerProxyHandler(socketPath string, agentToken any, trustedCIDR, agentDataRoot, hostDataRoot string) http.Handler {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			// docker daemon 只看 path/query，host 用占位即可。
			req.URL.Host = "docker"
			req.URL.Path = strings.TrimPrefix(req.URL.Path, dockerProxyPathPrefix)
			if req.URL.Path == "" {
				req.URL.Path = "/"
			}
			req.Host = "docker"
			// 移除 manager 侧的 Authorization，避免下游 docker daemon 误读。
			req.Header.Del("Authorization")
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeJSONError(w, http.StatusBadGateway, err.Error())
		},
	}
	// 决定是否启用 mount source 重写。空字符串、相同前缀视作不重写。
	rewriteFrom := strings.TrimRight(agentDataRoot, "/")
	rewriteTo := strings.TrimRight(hostDataRoot, "/")
	rewriteEnabled := rewriteFrom != "" && rewriteTo != "" && rewriteFrom != rewriteTo

	var dockerHandler http.Handler = proxy
	if rewriteEnabled {
		dockerHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isCreateContainerRequest(r) {
				if err := rewriteCreateContainerMounts(r, rewriteFrom, rewriteTo); err != nil {
					writeJSONError(w, http.StatusBadRequest, "重写容器 mount 失败: "+err.Error())
					return
				}
			}
			proxy.ServeHTTP(w, r)
		})
	}

	mux := http.NewServeMux()
	mux.Handle(dockerProxyPathPrefix+"/", dockerHandler)
	return wrapAuth(agentToken, trustedCIDR, mux)
}

// isCreateContainerRequest 判断是否为 docker daemon `POST /containers/create` 请求。
// path 形如 `/v1/docker/v1.43/containers/create` 或 `/v1/docker/containers/create`，
// 只 trim 掉 dockerProxyPathPrefix 后判尾部即可，version 段不影响判断。
func isCreateContainerRequest(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	p := strings.TrimPrefix(r.URL.Path, dockerProxyPathPrefix)
	// 去掉 query string
	if i := strings.Index(p, "?"); i >= 0 {
		p = p[:i]
	}
	return strings.HasSuffix(p, "/containers/create")
}

// rewriteCreateContainerMounts 拦截 create container 请求体，把 HostConfig.Binds
// 与 HostConfig.Mounts[*].Source 中以 from 开头的路径替换成 to 开头。
//
// 实现要点：
//   - 不解析非 JSON body：解码失败时静默回退到原 body，避免破坏 docker daemon 的兼容性；
//   - 替换前缀严格匹配（src == from || src 以 from + "/" 开头），避免 /var/lib/oc-agent-2
//     这种相邻路径被误改；
//   - 重写完成后必须同步 r.ContentLength 与 Content-Length header，否则 ReverseProxy 会
//     用旧长度截断 body，导致 docker daemon 报 "unexpected end of JSON input"。
func rewriteCreateContainerMounts(r *http.Request, from, to string) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	_ = r.Body.Close()

	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		// 不是 JSON：原样回写，保留 docker daemon 原行为。
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		r.Header.Set("Content-Length", strconv.Itoa(len(body)))
		return nil
	}

	hc, _ := doc["HostConfig"].(map[string]any)
	if hc != nil {
		// Binds: ["src:dst[:ro]", ...] 字符串数组
		if binds, ok := hc["Binds"].([]any); ok {
			for i, raw := range binds {
				if s, ok := raw.(string); ok {
					binds[i] = rewriteBindString(s, from, to)
				}
			}
		}
		// Mounts: [{Source, Target, Type, ReadOnly, ...}, ...] 对象数组
		if mounts, ok := hc["Mounts"].([]any); ok {
			for _, raw := range mounts {
				m, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				if src, ok := m["Source"].(string); ok {
					m["Source"] = rewriteSourcePath(src, from, to)
				}
			}
		}
	}

	newBody, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(newBody))
	r.ContentLength = int64(len(newBody))
	r.Header.Set("Content-Length", strconv.Itoa(len(newBody)))
	return nil
}

// rewriteSourcePath 把 src 中以 from 开头的部分换成 to。
// 严格前缀匹配避免误改：src == from（mount 整个 dataRoot）或 src 以 from + "/" 开头。
func rewriteSourcePath(src, from, to string) string {
	if src == from {
		return to
	}
	if strings.HasPrefix(src, from+"/") {
		return to + strings.TrimPrefix(src, from)
	}
	return src
}

// rewriteBindString 处理 docker bind 字符串格式 "src:dst[:opts...]"。
// 只重写 src（第一个冒号之前），其余原样保留。dst 可能含冒号（如 Windows path 在 Linux
// 上不会出现，但保险用 SplitN 限制一次）。
func rewriteBindString(s, from, to string) string {
	if s == "" {
		return s
	}
	parts := strings.SplitN(s, ":", 2)
	if len(parts) < 2 {
		return s
	}
	return rewriteSourcePath(parts[0], from, to) + ":" + parts[1]
}

// wrapAuth 把 IP 白名单和 bearer 鉴权两层中间件套在 docker proxy 前面。
// 鉴权失败统一返回 JSON 错误体，便于 manager 上报错误信息。
func wrapAuth(agentToken any, trustedCIDR string, next http.Handler) http.Handler {
	var allowed *net.IPNet
	if trustedCIDR != "" {
		_, parsed, err := net.ParseCIDR(trustedCIDR)
		if err == nil {
			allowed = parsed
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, dockerProxyPathPrefix+"/") && r.URL.Path != dockerProxyPathPrefix {
			http.NotFound(w, r)
			return
		}
		if allowed != nil {
			ip := remoteIP(r)
			if ip == nil || !allowed.Contains(ip) {
				writeJSONError(w, http.StatusForbidden, "源 IP 不在白名单内")
				return
			}
		}
		if token := agentTokenString(agentToken); token != "" {
			if r.Header.Get("Authorization") != "Bearer "+token {
				writeJSONError(w, http.StatusUnauthorized, "agent token 校验失败")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func agentTokenString(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case func() string:
		if v == nil {
			return ""
		}
		return strings.TrimSpace(v())
	default:
		return ""
	}
}

// remoteIP 从 RemoteAddr 解析 IP；解析失败返回 nil 让上层报 403。
func remoteIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

// writeJSONError 输出统一格式的错误响应。
func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// ensureURLOK 是占位，避免 url 包被裁掉；后续若代理需要解析下游 host 可以使用。
var _ = url.PathEscape
