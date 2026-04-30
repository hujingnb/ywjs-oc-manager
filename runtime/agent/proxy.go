package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
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
//  3. 路径前缀裁切并交给 ReverseProxy 转发。
//
// 非 /v1/docker 前缀的请求一律 404，避免代理被滥用为通用 HTTP 入口。
// 这里不挂 /healthz 等附加路径，agent main.go 自己的 mux 负责其它端点。
func NewDockerProxyHandler(socketPath, agentToken, trustedCIDR string) http.Handler {
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
	mux := http.NewServeMux()
	mux.Handle(dockerProxyPathPrefix+"/", proxy)
	return wrapAuth(agentToken, trustedCIDR, mux)
}

// wrapAuth 把 IP 白名单和 bearer 鉴权两层中间件套在 docker proxy 前面。
// 鉴权失败统一返回 JSON 错误体，便于 manager 上报错误信息。
func wrapAuth(agentToken, trustedCIDR string, next http.Handler) http.Handler {
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
		if agentToken != "" {
			if r.Header.Get("Authorization") != "Bearer "+agentToken {
				writeJSONError(w, http.StatusUnauthorized, "agent token 校验失败")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
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
