//go:build integration

// Package integration 提供需要真实 PostgreSQL 与 Redis 的端到端测试入口。
//
// 跑法：
//
//	make integration-test       # 在 docker compose 中启动后端容器并 go test -tags=integration ./...
//
// 这一文件只承担"汇总测试入口 + 环境检测"职责。具体场景测试分散在各包的 *_integration_test.go 中。
package integration

import (
	"os"
	"testing"
)

// TestIntegrationEnvironmentReady 校验集成测试运行所需的环境变量已就绪。
// 缺失变量时直接跳过整个套件，避免在本地误跑。
func TestIntegrationEnvironmentReady(t *testing.T) {
	required := []string{"INTEGRATION_DATABASE_URL", "INTEGRATION_REDIS_ADDR"}
	for _, name := range required {
		if os.Getenv(name) == "" {
			t.Skipf("跳过集成测试：缺少环境变量 %s", name)
		}
	}
}
