package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"oc-manager/runtime/agent/config"
)

// enrollResponse 是 manager /agent/enroll 的最小返回体。
type enrollResponse struct {
	NodeID                   string `json:"node_id"`
	AgentToken               string `json:"agent_token"`
	HeartbeatIntervalSeconds int32  `json:"heartbeat_interval_seconds"`
}

// enrollAgent 向 manager 申请注册或刷新节点，并写入本地 state。
func enrollAgent(ctx context.Context, cfg config.Config, agentID, name, hostname, dataRoot, stateDir, dockerAddr, fileAddr, version, caPEM string) (string, string, error) {
	advertiseHost := strings.TrimSpace(cfg.Agent.AdvertiseHost)
	if advertiseHost == "" {
		advertiseHost = strings.TrimSpace(hostname)
	}
	if advertiseHost == "" {
		advertiseHost = "localhost"
	}
	managerURL, err := url.Parse(strings.TrimRight(cfg.Manager.Endpoint, "/") + "/agent/enroll")
	if err != nil {
		return "", "", fmt.Errorf("解析 manager.endpoint 失败: %w", err)
	}
	payload := map[string]any{
		"agent_id":              agentID,
		"name":                  strings.TrimSpace(name),
		"max_apps":              cfg.Agent.MaxApps,
		"agent_docker_endpoint": "https://" + advertiseHost + normalizePort(dockerAddr),
		"agent_file_endpoint":   "https://" + advertiseHost + normalizePort(fileAddr),
		"agent_tls_ca_cert":     caPEM,
		"agent_version":         version,
		"node_data_root":        dataRoot,
		"resource_snapshot":     collectSnapshot(),
		"metadata": map[string]any{
			"hostname": hostname,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", fmt.Errorf("序列化 enroll 请求失败: %w", err)
	}
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs:    managerRootCAs(cfg.Manager),
			MinVersion: tls.VersionTLS12,
			ServerName: managerURL.Hostname(),
		}},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, managerURL.String(), bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("构造 enroll 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.Manager.EnrollmentSecret)
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", "", fmt.Errorf("enroll 失败: %d", resp.StatusCode)
	}
	var result enrollResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("解析 enroll 响应失败: %w", err)
	}
	if err := storeCredentials(stateDir, result.NodeID, result.AgentToken); err != nil {
		return "", "", err
	}
	return result.NodeID, result.AgentToken, nil
}

func managerRootCAs(mgr config.ManagerConfig) *x509.CertPool {
	pool := x509.NewCertPool()
	if mgr.CABundle != "" {
		_ = pool.AppendCertsFromPEM([]byte(mgr.CABundle))
	}
	if mgr.CABundle == "" && !mgr.SkipVerify {
		// 留空时交给系统根；返回空池会导致全部失败，所以这里返回 nil。
		return nil
	}
	return pool
}

func normalizePort(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return addr
	}
	if strings.Count(addr, ":") == 0 {
		return ":" + addr
	}
	return addr[strings.LastIndex(addr, ":"):]
}
