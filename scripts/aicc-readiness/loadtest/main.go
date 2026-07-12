// loadtest 对本地 AICC 公开入口施加固定并发的真实访客消息负载，并输出可归档的 JSON 报告。
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultConcurrency = 100
	defaultDuration    = 30 * time.Minute
	defaultTimeout     = 30 * time.Second
	visitorPause       = 2 * time.Second
)

// Config 是一次负载测试的运行参数；公开 token 只从调用方传入，不会写入报告。
type Config struct {
	BaseURL     string
	PublicToken string
	Concurrency int
	Duration    time.Duration
	Timeout     time.Duration
	Output      string
}

// ResourceSnapshot 保存负载工具自身的资源快照，用于识别发生器自身资源耗尽。
type ResourceSnapshot struct {
	Timestamp       time.Time `json:"timestamp"`
	Goroutines      int       `json:"goroutines"`
	HeapAllocBytes  uint64    `json:"heap_alloc_bytes"`
	SysBytes        uint64    `json:"sys_bytes"`
	CPUUserMicros   int64     `json:"cpu_user_micros"`
	CPUSystemMicros int64     `json:"cpu_system_micros"`
	MaxRSSKiB       int64     `json:"max_rss_kib"`
}

// Report 是容量门禁的机器可读结果；所有延迟单位均为毫秒。
type Report struct {
	StartedAt          time.Time        `json:"started_at"`
	FinishedAt         time.Time        `json:"finished_at"`
	Config             ReportConfig     `json:"config"`
	TotalRequests      int64            `json:"total_requests"`
	SuccessfulRequests int64            `json:"successful_requests"`
	SuccessRate        float64          `json:"success_rate_percent"`
	Latency            LatencyReport    `json:"latency_ms"`
	Errors             map[string]int64 `json:"errors"`
	SessionMismatches  int64            `json:"session_mismatches"`
	Process            ProcessResources `json:"process_resources"`
}

// ReportConfig 省略公开 token，防止报告意外泄露可访问公开客服入口的凭证。
type ReportConfig struct {
	BaseURL     string `json:"base_url"`
	Concurrency int    `json:"concurrency"`
	Duration    string `json:"duration"`
	Timeout     string `json:"timeout"`
}

// LatencyReport 保存所有 HTTP 请求的延迟分位数。
type LatencyReport struct {
	P50 int64 `json:"p50"`
	P95 int64 `json:"p95"`
	P99 int64 `json:"p99"`
}

// ProcessResources 将开始和结束快照并列记录，方便比对负载工具资源是否持续增长。
type ProcessResources struct {
	Start ResourceSnapshot `json:"start"`
	End   ResourceSnapshot `json:"end"`
}

// metrics 在多个并发访客之间安全累计请求结果。
type metrics struct {
	mu                sync.Mutex
	total             int64
	success           int64
	latencies         []int64
	errors            map[string]int64
	sessionMismatches int64
}

func main() {
	config, err := parseConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	report := run(config)
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "编码负载报告失败:", err)
		os.Exit(1)
	}
	if config.Output != "" {
		if err := os.WriteFile(config.Output, append(encoded, '\n'), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "写入负载报告失败:", err)
			os.Exit(1)
		}
	}
	fmt.Println(string(encoded))
}

// parseConfig 读取命令行参数，并在开始压测前拒绝无效输入。
func parseConfig() (Config, error) {
	config := Config{}
	flag.StringVar(&config.BaseURL, "base-url", "", "AICC 公开入口基础地址，例如 http://ocm.localhost")
	flag.StringVar(&config.PublicToken, "public-token", "", "AICC 智能体 public token")
	flag.IntVar(&config.Concurrency, "concurrency", defaultConcurrency, "并发虚拟访客数")
	flag.DurationVar(&config.Duration, "duration", defaultDuration, "持续时间")
	flag.DurationVar(&config.Timeout, "timeout", defaultTimeout, "单个 HTTP 请求超时")
	flag.StringVar(&config.Output, "output", "", "可选 JSON 报告文件路径")
	flag.Parse()
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	config.PublicToken = strings.TrimSpace(config.PublicToken)
	if config.BaseURL == "" || config.PublicToken == "" {
		return Config{}, errors.New("-base-url 和 -public-token 均为必填参数")
	}
	parsed, err := url.ParseRequestURI(config.BaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return Config{}, fmt.Errorf("-base-url 必须是完整 HTTP(S) 地址: %q", config.BaseURL)
	}
	if config.Concurrency <= 0 || config.Duration <= 0 || config.Timeout <= 0 {
		return Config{}, errors.New("-concurrency、-duration 和 -timeout 必须大于零")
	}
	return config, nil
}

// run 启动固定数量 worker；每轮都创建一个独立访客、独立 session 和唯一消息。
func run(config Config) Report {
	startedAt := time.Now().UTC()
	startResources := resourceSnapshot()
	ctx, cancel := context.WithTimeout(context.Background(), config.Duration)
	defer cancel()

	results := &metrics{errors: make(map[string]int64)}
	var workers sync.WaitGroup
	for workerID := 0; workerID < config.Concurrency; workerID++ {
		workers.Add(1)
		go func(id int) {
			defer workers.Done()
			for ctx.Err() == nil {
				runVisitor(visitorRequestContext(ctx), config, results, id)
				// 单来源创建会话存在每分钟限流；短暂停顿避免发生器本身制造无意义的 429 洪泛。
				select {
				case <-ctx.Done():
					return
				case <-time.After(visitorPause):
				}
			}
		}(workerID)
	}
	workers.Wait()
	finishedAt := time.Now().UTC()

	results.mu.Lock()
	defer results.mu.Unlock()
	return Report{
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		Config: ReportConfig{
			BaseURL: config.BaseURL, Concurrency: config.Concurrency,
			Duration: config.Duration.String(), Timeout: config.Timeout.String(),
		},
		TotalRequests:      results.total,
		SuccessfulRequests: results.success,
		SuccessRate:        successRate(results.total, results.success),
		Latency: LatencyReport{
			P50: quantile(results.latencies, 0.50),
			P95: quantile(results.latencies, 0.95),
			P99: quantile(results.latencies, 0.99),
		},
		Errors:            results.errors,
		SessionMismatches: results.sessionMismatches,
		Process:           ProcessResources{Start: startResources, End: resourceSnapshot()},
	}
}

// visitorRequestContext 保留调用链上下文值，但不让总压测时限取消已开始的访客请求。
func visitorRequestContext(ctx context.Context) context.Context {
	return context.WithoutCancel(ctx)
}

// runVisitor 完整模拟一次独立公开访客：创建会话、发送唯一消息、读取会话验证未串写。
func runVisitor(ctx context.Context, config Config, results *metrics, workerID int) {
	visitorID := randomID()
	client := newLoadHTTPClient(config)
	// 以每轮唯一访客标识生成来源 IP，避免同一 worker 连续创建不同访客时误触单来源限流。
	forwardedIP := forwardedIPForVisitor(workerID, visitorID)
	sessionToken, ok := createSession(ctx, client, config, results, visitorID, forwardedIP)
	if !ok {
		return
	}
	message := "aicc-load-" + visitorID
	if !sendMessage(ctx, client, config, results, sessionToken, message, forwardedIP) {
		return
	}
	verifySession(ctx, client, config, results, sessionToken, message, forwardedIP)
}

// forwardedIPForVisitor 为每个虚拟访客生成稳定的文档用途公网 IPv4 地址。
func forwardedIPForVisitor(workerID int, visitorID string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", workerID, visitorID)))
	return fmt.Sprintf("11.%d.%d.%d", sum[0], sum[1], sum[2])
}

// clientMessageIDForVisitor 从访客唯一标识派生稳定 UUIDv4，使网络超时后的重试仍命中服务端幂等约束。
func clientMessageIDForVisitor(visitorID string) string {
	sum := sha256.Sum256([]byte("aicc-load-message:" + visitorID))
	sum[6] = (sum[6] & 0x0f) | 0x40
	sum[8] = (sum[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(sum[:16])
	return fmt.Sprintf("%s-%s-%s-%s-%s", encoded[0:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:32])
}

// newLoadHTTPClient 为本地压测创建 HTTP client，避免宿主机代理劫持 k3d 的 ocm.localhost 请求。
func newLoadHTTPClient(config Config) *http.Client {
	client := &http.Client{Timeout: config.Timeout}
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || !strings.EqualFold(parsed.Hostname(), "ocm.localhost") {
		return client
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client.Transport = transport
	return client
}

// createSession 创建一个只属于当前虚拟访客的公开会话。
func createSession(ctx context.Context, client *http.Client, config Config, results *metrics, visitorID, forwardedIP string) (string, bool) {
	body := map[string]string{"channel": "web_link", "source_url": "http://loadtest.local/" + visitorID}
	responseBody, ok := performJSON(ctx, client, config, results, http.MethodPost,
		fmt.Sprintf("%s/api/v1/public/aicc/agents/%s/sessions", config.BaseURL, url.PathEscape(config.PublicToken)), body, forwardedIP)
	if !ok {
		return "", false
	}
	var response struct {
		Session struct {
			SessionToken string `json:"session_token"`
		} `json:"session"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil || strings.TrimSpace(response.Session.SessionToken) == "" {
		recordProtocolError(results, "invalid_session_response")
		return "", false
	}
	return response.Session.SessionToken, true
}

// sendMessage 向当前会话发送唯一文本，确保后续读取可以识别消息归属。
func sendMessage(ctx context.Context, client *http.Client, config Config, results *metrics, sessionToken, message, forwardedIP string) bool {
	_, ok := performJSON(ctx, client, config, results, http.MethodPost,
		fmt.Sprintf("%s/api/v1/public/aicc/sessions/%s/messages", config.BaseURL, url.PathEscape(sessionToken)), map[string]string{
			"text":              message,
			"client_message_id": clientMessageIDForVisitor(strings.TrimPrefix(message, "aicc-load-")),
		}, forwardedIP)
	return ok
}

// verifySession 读取会话镜像；自己的唯一消息不存在即视为 session 串写或数据一致性异常。
func verifySession(ctx context.Context, client *http.Client, config Config, results *metrics, sessionToken, message, forwardedIP string) {
	body, ok := performJSON(ctx, client, config, results, http.MethodGet,
		fmt.Sprintf("%s/api/v1/public/aicc/sessions/%s", config.BaseURL, url.PathEscape(sessionToken)), nil, forwardedIP)
	if !ok {
		return
	}
	var response struct {
		Session struct {
			Messages []struct {
				Text string `json:"text"`
			} `json:"messages"`
		} `json:"session"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		recordMismatch(results)
		return
	}
	for _, item := range response.Session.Messages {
		if !validateSessionResponse(message, item.Text) {
			return
		}
	}
	recordMismatch(results)
}

// performJSON 发送一次 JSON 请求并将其统一记入请求数、延迟和错误分类。
func performJSON(ctx context.Context, client *http.Client, config Config, results *metrics, method, endpoint string, payload any, forwardedIP string) ([]byte, bool) {
	var reader io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			recordRequest(results, 0, false, "encode_error")
			return nil, false
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		recordRequest(results, 0, false, "request_error")
		return nil, false
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Forwarded-For", forwardedIP)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	startedAt := time.Now()
	response, err := client.Do(request)
	latency := time.Since(startedAt).Milliseconds()
	if err != nil {
		recordRequest(results, latency, false, requestErrorClass(err))
		return nil, false
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024))
	if readErr != nil {
		recordRequest(results, latency, false, "read_error")
		return nil, false
	}
	success := response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
	if success {
		recordRequest(results, latency, true, "")
	} else {
		recordRequest(results, latency, false, fmt.Sprintf("http_%d", response.StatusCode))
	}
	return body, success
}

// requestErrorClass 将超时与普通网络错误拆分，便于报告定位容量瓶颈。
func requestErrorClass(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "network_error"
}

// recordRequest 预留给统一请求记账，避免各公开接口的统计口径分叉。
func recordRequest(results *metrics, latency int64, success bool, errorClass string) {
	results.mu.Lock()
	defer results.mu.Unlock()
	results.total++
	results.latencies = append(results.latencies, latency)
	if success {
		results.success++
		return
	}
	results.errors[errorClass]++
}

// recordProtocolError 记录 HTTP 已成功但响应体不符合公开接口契约的失败。
func recordProtocolError(results *metrics, errorClass string) {
	results.mu.Lock()
	defer results.mu.Unlock()
	results.errors[errorClass]++
}

// recordMismatch 增加会话串写或读取到错误会话数据的计数。
func recordMismatch(results *metrics) {
	results.mu.Lock()
	results.sessionMismatches++
	results.mu.Unlock()
}

// quantile 使用最近秩算法计算分位数，输入会复制排序而不修改调用方样本。
func quantile(samples []int64, percentile float64) int64 {
	if len(samples) == 0 {
		return 0
	}
	ordered := append([]int64(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	index := int(percentile*float64(len(ordered))+0.999999999) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(ordered) {
		index = len(ordered) - 1
	}
	return ordered[index]
}

// successRate 将成功请求数转换为百分数；零请求不能误报成功。
func successRate(total, success int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(success) * 100 / float64(total)
}

// validateSessionResponse 判断会话镜像中的消息是否属于其他访客；一致则说明找到了当前访客消息。
func validateSessionResponse(expectedMessage, actualMessage string) bool {
	return strings.TrimSpace(expectedMessage) != strings.TrimSpace(actualMessage)
}

// randomID 生成无需外部依赖的高熵访客与消息标识。
func randomID() string {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}

// resourceSnapshot 读取 Go runtime 与 Unix rusage，采集失败时对应数值保留零值。
func resourceSnapshot() ResourceSnapshot {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	snapshot := ResourceSnapshot{Timestamp: time.Now().UTC(), Goroutines: runtime.NumGoroutine(), HeapAllocBytes: memory.HeapAlloc, SysBytes: memory.Sys}
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err == nil {
		snapshot.CPUUserMicros = usage.Utime.Sec*1_000_000 + usage.Utime.Usec
		snapshot.CPUSystemMicros = usage.Stime.Sec*1_000_000 + usage.Stime.Usec
		snapshot.MaxRSSKiB = usage.Maxrss
	}
	return snapshot
}
