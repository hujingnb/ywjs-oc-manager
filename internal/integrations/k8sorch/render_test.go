package k8sorch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
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

// TestRenderDeploymentProxy 断言：配了 Proxy 时 hermes 与 oc-ops 容器都注入
// HTTP_PROXY/HTTPS_PROXY/NO_PROXY（hermes 微信平台 / oc-ops 渠道登录直连外网需要）；
// 空 Proxy（生产默认）时两容器都不出现任何代理 env，保持 pod 干净。
func TestRenderDeploymentProxy(t *testing.T) {
	// 取容器 env 为 map 的小工具
	envsOf := func(dep *appsv1.Deployment, name string) map[string]string {
		m := map[string]string{}
		for _, c := range dep.Spec.Template.Spec.Containers {
			if c.Name == name {
				for _, e := range c.Env {
					m[e.Name] = e.Value
				}
			}
		}
		return m
	}
	// 配了代理：hermes 与 oc-ops 都应注入三个代理 env
	spec := testSpec()
	spec.Proxy = ProxyEnv{HTTPProxy: "http://p:7890", HTTPSProxy: "http://p:7890", NoProxy: "localhost,.svc"}
	dep := RenderDeployment(spec, "oc-apps")
	for _, cname := range []string{"hermes", "oc-ops"} {
		envs := envsOf(dep, cname)
		assert.Equal(t, "http://p:7890", envs["HTTP_PROXY"], cname+" 应注入 HTTP_PROXY")
		assert.Equal(t, "http://p:7890", envs["HTTPS_PROXY"], cname+" 应注入 HTTPS_PROXY")
		assert.Equal(t, "localhost,.svc", envs["NO_PROXY"], cname+" 应注入 NO_PROXY")
	}
	// 空代理（生产默认）：两容器都不应出现任何代理 env
	depNoProxy := RenderDeployment(testSpec(), "oc-apps")
	for _, cname := range []string{"hermes", "oc-ops"} {
		envs := envsOf(depNoProxy, cname)
		_, hasHTTP := envs["HTTP_PROXY"]
		_, hasHTTPS := envs["HTTPS_PROXY"]
		_, hasNo := envs["NO_PROXY"]
		assert.False(t, hasHTTP || hasHTTPS || hasNo, cname+" 空 Proxy 时不应注入任何代理 env")
	}
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
