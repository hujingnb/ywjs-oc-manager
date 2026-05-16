package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"oc-manager/runtime/agent/config"
)

const defaultAgentConfigPath = "config/agent.yaml"

// HealthResponse 是 runtime agent 健康检查响应。
// 后续注册、心跳和文件 API 会复用该服务入口，因此这里先固定最小可观测字段。
type HealthResponse struct {
	Status    string `json:"status"`
	Role      string `json:"role"`
	DataRoot  string `json:"dataRoot"`
	Timestamp string `json:"timestamp"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

// agentOptions 集中描述 agent 进程启动期的全部可调参数。
// 单独抽出便于测试用临时目录、随机端口和短超时值进行覆盖。
type agentOptions struct {
	dataRoot      string
	stateDir      string
	dockerSocket  string
	trustedCIDR   string
	hostname      string
	dockerAddr    string // ":7001"
	fileAddr      string // ":7002"
	dockerProxy   bool   // 是否启用 docker proxy（测试可关闭，避免随机端口冲突）
	enableSignals bool   // 生产场景监听 SIGINT/SIGTERM；测试中由 ctx 控制退出
	// fullConfig 用于子组件（如 heartbeat）需要完整 yaml 配置时透传。
	// 测试构造 opts 时不填该字段则 heartbeat 不启动，避免污染 server-only 测试。
	fullConfig config.Config
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := runHealthcheckCommand(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "healthcheck failed: %v\n", err)
			os.Exit(1)
		}
		return
	}
	configPath := defaultConfigPath()
	cfg, err := config.LoadFile(configPath)
	if err != nil {
		log.Fatalf("加载 agent 配置失败: %v", err)
	}
	opts := agentOptions{
		dataRoot:      cfg.Agent.DataRoot,
		stateDir:      cfg.Agent.StateDir,
		dockerSocket:  cfg.Agent.DockerSocket,
		trustedCIDR:   cfg.Agent.TrustedCIDR,
		hostname:      hostnameOrEmpty(),
		dockerAddr:    cfg.Agent.DockerAddr,
		fileAddr:      cfg.Agent.FileAddr,
		dockerProxy:   true,
		enableSignals: true,
		fullConfig:    cfg,
	}
	if err := runAgent(context.Background(), opts, os.Stdout); err != nil {
		log.Fatalf("agent 退出: %v", err)
	}
}

// defaultConfigPath 保持普通启动和 healthcheck 省略 --config 时的配置路径规则一致。
func defaultConfigPath() string {
	if configPath := strings.TrimSpace(os.Getenv("OC_AGENT_CONFIG")); configPath != "" {
		return configPath
	}
	return defaultAgentConfigPath
}

// runHealthcheckCommand 解析镜像内健康检查参数，并把可测试逻辑委托给 runHealthcheck。
func runHealthcheckCommand(args []string) error {
	flags := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "agent config path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected healthcheck arguments: %s", strings.Join(flags.Args(), " "))
	}
	return runHealthcheck(*configPath)
}

// runHealthcheck 验证容器内 agent 运行依赖和本地 TLS 健康端点，返回错误供测试断言和 main 转 exit code。
func runHealthcheck(configPath string) error {
	if strings.TrimSpace(configPath) == "" {
		configPath = defaultConfigPath()
	}
	cfg, err := config.LoadFile(configPath)
	if err != nil {
		return err
	}
	if err := verifyDockerSocket(cfg.Agent.DockerSocket); err != nil {
		return err
	}
	if err := verifyRegistrationCredentials(cfg.Agent.StateDir); err != nil {
		return err
	}
	client := &http.Client{
		Transport: &http.Transport{
			// agent 使用自签证书；healthcheck 只在容器本地回环访问，证书链不作为存活判断条件。
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, //nolint:gosec
		},
		Timeout: 2 * time.Second,
	}
	if err := verifyLocalHealthz(client, cfg.Agent.FileAddr); err != nil {
		return fmt.Errorf("file endpoint healthz failed: %w", err)
	}
	if err := verifyLocalHealthz(client, cfg.Agent.DockerAddr); err != nil {
		return fmt.Errorf("docker endpoint healthz failed: %w", err)
	}
	return nil
}

// verifyDockerSocket 确认 docker socket 挂载到了容器内且类型是 Unix socket。
func verifyDockerSocket(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("docker socket unavailable: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("docker socket path is not a Unix socket: %s", path)
	}
	return nil
}

// verifyRegistrationCredentials 确认 agent 已完成注册并持久化了 node-id 与 agent-token。
func verifyRegistrationCredentials(stateDir string) error {
	nodeID, token, err := loadCredentials(stateDir)
	if err != nil {
		return fmt.Errorf("registration credentials unavailable: %w", err)
	}
	if strings.TrimSpace(nodeID) == "" || strings.TrimSpace(token) == "" {
		return fmt.Errorf("registration credentials missing in state dir: %s", stateDir)
	}
	return nil
}

// verifyLocalHealthz 以 HTTPS 调用本地 /healthz，要求返回 HTTP 200。
func verifyLocalHealthz(client *http.Client, addr string) error {
	url, err := localHealthzURL(addr)
	if err != nil {
		return err
	}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("%s request failed: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned HTTP %d", url, resp.StatusCode)
	}
	return nil
}

// localHealthzURL 把服务监听地址归一化为容器内可访问的 HTTPS 回环地址。
func localHealthzURL(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("invalid local endpoint address %q: %w", addr, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return "https://" + net.JoinHostPort(host, port) + "/healthz", nil
}

// runAgent 启动 agent 的两个 HTTP 服务并阻塞直到 ctx 取消或收到信号。
//
// 启动顺序：
//  1. 加载或生成自签 TLS 证书；
//  2. 把 CA PEM 以 base64 单行格式写到 stdout，便于运维或自动化 bootstrap 抓取；
//  3. 用 ListenAndServeTLS 起 docker proxy 端口（bearer + CIDR 中间件已包好）；
//  4. 用 ListenAndServeTLS 起文件 API 端口；
//  5. 阻塞，收到 SIGINT/SIGTERM 或 ctx 取消时优雅关闭。
//
// stdout 在生产是 os.Stdout，测试场景由调用方传 *bytes.Buffer，便于断言 CA PEM 输出。
func runAgent(ctx context.Context, opts agentOptions, stdout io.Writer) error {
	// agent 是独立二进制，不走 manager-api 中间件；使用 JSON 格式让容器日志驱动可解析，
	// 不需要 requestIDHandler（无 traceID 上下文）。
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level:     slog.LevelInfo,
		AddSource: true,
	})))

	if err := os.MkdirAll(opts.stateDir, 0o700); err != nil {
		return fmt.Errorf("创建 state 目录失败: %w", err)
	}
	agentID, err := loadOrCreateAgentID(opts.stateDir)
	if err != nil {
		return err
	}
	bundle, err := EnsureSelfSignedCert(opts.stateDir, certHostname(opts))
	if err != nil {
		return fmt.Errorf("初始化 TLS 证书失败: %w", err)
	}
	caBase64 := base64.StdEncoding.EncodeToString(bundle.CACertPEM)
	if _, err := fmt.Fprintf(stdout, "agent-ca-pem-base64: %s\n", caBase64); err != nil {
		return fmt.Errorf("输出 CA PEM 失败: %w", err)
	}

	tokenGetter := func() string {
		_, token, _ := loadCredentials(opts.stateDir)
		return token
	}
	if shouldEnrollOnStartup(opts.fullConfig, tokenGetter()) {
		if err := enrollUntilReady(ctx, opts, agentID, string(bundle.CACertPEM)); err != nil {
			return err
		}
	}
	// 主动心跳：启动后立即尝试；若收到 401 会自动 re-enroll。
	go newHeartbeat(opts.fullConfig, agentID, tokenGetter, opts.stateDir, opts.hostname, string(bundle.CACertPEM), opts.dataRoot, opts.dockerAddr, opts.fileAddr).Run(ctx)

	dataHandler := newHandler(opts.dataRoot, tokenGetter, opts.dockerSocket)
	fileServer := &http.Server{
		Addr:              opts.fileAddr,
		Handler:           dataHandler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	var dockerServer *http.Server
	if opts.dockerProxy {
		dockerHandler := newDockerEntryHandler(opts, dataHandler)
		dockerServer = &http.Server{
			Addr:              opts.dockerAddr,
			Handler:           dockerHandler,
			ReadHeaderTimeout: 5 * time.Second,
		}
	}

	certPath := filepath.Join(opts.stateDir, certFileName)
	keyPath := filepath.Join(opts.stateDir, keyFileName)

	errCh := make(chan error, 2)
	go func() {
		slog.Info("file-api 启动监听", "addr", fileServer.Addr)
		if err := fileServer.ListenAndServeTLS(certPath, keyPath); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("file-api: %w", err)
			return
		}
		errCh <- nil
	}()
	if dockerServer != nil {
		go func() {
			slog.Info("docker-proxy 启动监听（TLS）", "addr", dockerServer.Addr)
			if err := dockerServer.ListenAndServeTLS(certPath, keyPath); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("docker-proxy: %w", err)
				return
			}
			errCh <- nil
		}()
	}

	stop := make(chan os.Signal, 1)
	if opts.enableSignals {
		signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	}
	select {
	case <-ctx.Done():
	case <-stop:
	case err := <-errCh:
		// 任一 server 提前退出立刻关闭另一个并冒泡错误。
		shutdownServers(fileServer, dockerServer)
		return err
	}
	return shutdownServers(fileServer, dockerServer)
}

func shouldEnrollOnStartup(cfg config.Config, token string) bool {
	return strings.TrimSpace(cfg.Manager.Endpoint) != "" || strings.TrimSpace(token) == ""
}

func certHostname(opts agentOptions) string {
	if host := strings.TrimSpace(opts.fullConfig.Agent.AdvertiseHost); host != "" {
		return host
	}
	return opts.hostname
}

func enrollUntilReady(ctx context.Context, opts agentOptions, agentID, caPEM string) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		_, _, err := enrollAgent(ctx, opts.fullConfig, agentID, opts.fullConfig.Agent.Name, opts.hostname, opts.dataRoot, opts.stateDir, opts.dockerAddr, opts.fileAddr, agentVersion, caPEM)
		if err == nil {
			return nil
		}
		slog.Warn("首次 enroll 失败，等待重试", "error", err)
		select {
		case <-ctx.Done():
			return fmt.Errorf("首次 enroll 失败: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func shutdownServers(servers ...*http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var firstErr error
	for _, s := range servers {
		if s == nil {
			continue
		}
		if err := s.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func hostnameOrEmpty() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}

// newDockerEntryHandler 组装 docker proxy 与未来共用 mux 的入口。
// /v1/docker/* 走 docker socket 反向代理；其它 path 透传到 fallback handler，
// 让 healthz / file API ping 等路由可以共用同一进程。
func newDockerEntryHandler(opts agentOptions, fallback http.Handler) http.Handler {
	hostDataRoot, err := detectHostDataRoot(opts.dataRoot)
	if err != nil {
		// detect 失败仅警告，按"不重写"行为继续；docker proxy 退化为透传。
		// legacy OpenClaw 容器的 file-level mount 仍可能撞空目录占位的旧问题
		//（Hermes 时代已弃用 file-level mount，但此路径重写逻辑保留以兼容 legacy）；
		// ops 看到此日志应检查 agent 是否能读 /proc/self/mountinfo。
		fmt.Fprintf(os.Stderr, "agent: detectHostDataRoot 失败，docker proxy 不重写 mount source: %v\n", err)
		hostDataRoot = opts.dataRoot
	}
	if hostDataRoot != opts.dataRoot {
		fmt.Fprintf(os.Stderr, "agent: docker proxy mount 重写启用：%s -> %s\n", opts.dataRoot, hostDataRoot)
	}
	proxy := NewDockerProxyHandler(opts.dockerSocket, func() string {
		_, token, _ := loadCredentials(opts.stateDir)
		return token
	}, opts.trustedCIDR, opts.dataRoot, hostDataRoot)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Path) >= len(dockerProxyPathPrefix) && r.URL.Path[:len(dockerProxyPathPrefix)] == dockerProxyPathPrefix {
			proxy.ServeHTTP(w, r)
			return
		}
		fallback.ServeHTTP(w, r)
	})
}

func newHandler(dataRoot string, agentToken any, dockerSocket string) http.Handler {
	return newHandlerWithDocker(dataRoot, newDockerSocketClient(dockerSocket), agentToken)
}

func newHandlerWithDocker(dataRoot string, docker DockerClient, agentToken any) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, HealthResponse{
			Status:    "ok",
			Role:      "runtime-agent",
			DataRoot:  dataRoot,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/v1/files/ping", withAgentAuth(agentToken, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	}))
	mux.HandleFunc("/v1/images/inspect", withAgentAuth(agentToken, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		image := r.URL.Query().Get("image")
		if image == "" {
			writeError(w, http.StatusBadRequest, "missing image query")
			return
		}
		info, err := docker.InspectImage(r.Context(), image)
		if errors.Is(err, ErrImageNotFound) {
			slog.ErrorContext(r.Context(), "[hujingnb][0] agent:inspectImage not found", "image", image) // todo del
			writeJSON(w, map[string]any{"exists": false, "image": image})
			return
		}
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		slog.ErrorContext(r.Context(), "[hujingnb][0] agent:inspectImage found", "image", image, "id", info.ID) // todo del
		writeJSON(w, map[string]any{"exists": true, "image": image, "info": info})
	}))
	mux.HandleFunc("/v1/images/load", withAgentAuth(agentToken, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		image := r.URL.Query().Get("image")
		if image == "" {
			writeError(w, http.StatusBadRequest, "missing image query")
			return
		}
		// expectedID 是 manager 本地 inspect 到的镜像 ID，用于在 docker load 后
		// 核验 tag 是否指向正确内容，不一致时强制重新打 tag。
		expectedID := r.URL.Query().Get("expected_id")
		slog.ErrorContext(r.Context(), "[hujingnb][8] agent:loadImage start docker load", "image", image) // todo del
		if err := docker.LoadImage(r.Context(), r.Body); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		slog.ErrorContext(r.Context(), "[hujingnb][9] agent:loadImage docker load done, inspecting", "image", image) // todo del
		info, err := docker.InspectImage(r.Context(), image)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		slog.ErrorContext(r.Context(), "[hujingnb][10] agent:loadImage inspect done", "image", image, "inspectedID", info.ID) // todo del
		// docker load 有时在 tag 已被 registry 版本占据时不会更新 tag 指向。
		// 当调用方提供了 expected_id 且 inspect 结果不符，则通过 docker tag 强制修正。
		if expectedID != "" && info.ID != expectedID {
			if tagErr := docker.TagImage(r.Context(), expectedID, image); tagErr != nil {
				writeError(w, http.StatusBadGateway, fmt.Sprintf("re-tag image failed: %v", tagErr))
				return
			}
			// 重新 inspect 确认 tag 已指向正确 ID。
			info, err = docker.InspectImage(r.Context(), image)
			if err != nil {
				writeError(w, http.StatusBadGateway, err.Error())
				return
			}
		}
		writeJSON(w, map[string]any{"loaded": true, "image": image, "info": info})
	}))
	// Sprint 1：scope 化 file API 端点（init/sync/upload/list/download/archive/cleanup 等）。
	// 子路径与 handler 在 scopes.go 里维护，这里只挂载入口。
	mux.Handle("/v1/scopes/", newScopesHandler(dataRoot, agentToken))
	return mux
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("写入 JSON 响应失败", "error", err)
	}
}

func writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(ErrorResponse{Error: message})
}

func withAgentAuth(agentToken any, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := agentTokenString(agentToken)
		if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
			writeError(w, http.StatusUnauthorized, "invalid agent token")
			return
		}
		next(w, r)
	}
}
