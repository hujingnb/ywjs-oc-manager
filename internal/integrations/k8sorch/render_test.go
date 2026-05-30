package k8sorch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

// testSpec 是渲染测试的固定 AppSpec，覆盖所有字段以确保 golden 完整。
func testSpec() AppSpec {
	return AppSpec{
		AppID:           "a1",
		HermesImage:     "registry/hermes:v1",
		OpsImage:        "registry/ops:dev",
		ControlToken:    "tok",
		BootstrapURL:    "http://manager-api.ocm.svc:8080/internal/apps/a1/bootstrap",
		ImagePullSecret: "acr-pull",
		Resources: ResourceLimits{
			RequestsCPU:    "250m",
			RequestsMemory: "512Mi",
			LimitsCPU:      "1",
			LimitsMemory:   "2Gi",
		},
	}
}

// assertGolden 把对象序列化为 YAML 与 golden 文件比对；设 UPDATE_GOLDEN=1 时刷新快照。
func assertGolden(t *testing.T, name string, obj any) {
	t.Helper()
	got, err := yaml.Marshal(obj)
	require.NoError(t, err)
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "缺 golden 文件，先用 UPDATE_GOLDEN=1 生成")
	assert.Equal(t, string(want), string(got))
}

// TestRenderDeployment 验证 Deployment 渲染与 golden 一致（含 initContainer/三容器/卷/probe）。
func TestRenderDeployment(t *testing.T) {
	assertGolden(t, "deployment.golden.yaml", RenderDeployment(testSpec(), "oc-apps"))
}

// TestRenderDeploymentOcOpsPythonPath 断言 oc-ops sidecar 显式注入
// PYTHONPATH=/usr/local/lib。oc-ops 用 `python -m uvicorn ocops.server:app`
// 直启、不经 oc-* shim 的 sys.path 注入，缺此 env 会 ModuleNotFoundError: ocops
// 导致 sidecar CrashLoopBackOff，pod 永远到不了 3/3 Ready。
func TestRenderDeploymentOcOpsPythonPath(t *testing.T) {
	dep := RenderDeployment(testSpec(), "oc-apps")
	// 从三容器里定位 oc-ops 容器
	var ocOps *corev1.Container
	for i := range dep.Spec.Template.Spec.Containers {
		if dep.Spec.Template.Spec.Containers[i].Name == "oc-ops" {
			ocOps = &dep.Spec.Template.Spec.Containers[i]
			break
		}
	}
	require.NotNil(t, ocOps, "渲染结果必须包含名为 oc-ops 的容器")
	// 收集 env 为 map 便于断言
	envs := map[string]string{}
	for _, e := range ocOps.Env {
		envs[e.Name] = e.Value
	}
	assert.Equal(t, "/usr/local/lib", envs["PYTHONPATH"], "oc-ops 必须置 PYTHONPATH=/usr/local/lib 才能 import ocops")
}

// TestRenderService 验证 oc-ops Service 渲染（selector + 8080）。
func TestRenderService(t *testing.T) {
	assertGolden(t, "service.golden.yaml", RenderService(testSpec(), "oc-apps"))
}

// TestRenderSecret 验证 control-token Secret 渲染。
func TestRenderSecret(t *testing.T) {
	assertGolden(t, "secret.golden.yaml", RenderSecret(testSpec(), "oc-apps"))
}
