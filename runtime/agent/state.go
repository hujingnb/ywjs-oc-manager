package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

const (
	agentIDFileName   = "agent-id"
	nodeIDFileName    = "node-id"
	agentTokenFileName = "agent-token"
	stateFileMode     = 0o600
)

// agentState 封装 runtime agent 进程在 state_dir 中持久化的身份和凭证。
type agentState struct {
	dir        string
	agentID    string
	nodeID     string
	agentToken string
}

// loadOrCreateAgentID 确保 agent-id 文件存在；首次启动时生成新的 UUID v4。
func loadOrCreateAgentID(stateDir string) (string, error) {
	path := filepath.Join(stateDir, agentIDFileName)
	if raw, err := os.ReadFile(path); err == nil {
		value := strings.TrimSpace(string(raw))
		if value != "" {
			return value, nil
		}
	}
	id := uuid.NewString()
	if err := os.WriteFile(path, []byte(id+"\n"), stateFileMode); err != nil {
		return "", fmt.Errorf("写入 agent-id 失败: %w", err)
	}
	return id, nil
}

// loadCredentials 读取 node-id 与 agent-token；任一缺失时返回空字符串。
func loadCredentials(stateDir string) (string, string, error) {
	nodeID, err := readTrimmed(filepath.Join(stateDir, nodeIDFileName))
	if err != nil {
		return "", "", err
	}
	agentToken, err := readTrimmed(filepath.Join(stateDir, agentTokenFileName))
	if err != nil {
		return "", "", err
	}
	return nodeID, agentToken, nil
}

// storeCredentials 原子写入 node-id 与 agent-token。
func storeCredentials(stateDir, nodeID, agentToken string) error {
	if err := os.WriteFile(filepath.Join(stateDir, nodeIDFileName), []byte(strings.TrimSpace(nodeID)+"\n"), stateFileMode); err != nil {
		return fmt.Errorf("写入 node-id 失败: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, agentTokenFileName), []byte(strings.TrimSpace(agentToken)+"\n"), stateFileMode); err != nil {
		return fmt.Errorf("写入 agent-token 失败: %w", err)
	}
	return nil
}

func readTrimmed(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("读取 %s 失败: %w", filepath.Base(path), err)
	}
	return strings.TrimSpace(string(raw)), nil
}
