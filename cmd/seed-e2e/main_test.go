package main

import (
	"os/exec"
	"testing"
)

// 验证 OCM_E2E 守门：缺这个环境变量时命令必须非零退出，避免误在生产 truncate。
func TestSeedE2E_RejectsMissingOCME2EFlag(t *testing.T) {
	cmd := exec.Command("go", "run", ".")
	// 故意只给最小 PATH / HOME，不带 OCM_E2E、不带 OCM_CONFIG，期望 main 在守门处即退出。
	cmd.Env = []string{"PATH=/usr/local/go/bin:/usr/bin:/bin", "HOME=/tmp"}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("缺 OCM_E2E 应当失败，但成功了；输出：%s", out)
	}
}
