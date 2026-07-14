package k8sorch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/yaml"

	"oc-manager/internal/domain"
)

// testSpec 是渲染测试的固定 AppSpec，覆盖所有字段以确保 golden 完整。
func testSpec() AppSpec {
	return AppSpec{
		AppID: "a1",
		// 渲染测试默认使用普通应用类型；该字段只参与路由，不应改变资源 YAML。
		AppType:         domain.AppTypeStandard,
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

// TestRenderDeploymentAICC 验证 AICC 运行时使用无状态启动方式：由 oc-bootstrap 初始化，
// Pod 仅保留 hermes 与 oc-ops，不得携带标准应用的 S3 恢复和同步配置。
func TestRenderDeploymentAICC(t *testing.T) {
	spec := testSpec()
	spec.AppType = domain.AppTypeAICC

	dep := RenderDeployment(spec, "oc-apps")
	require.Len(t, dep.Spec.Template.Spec.InitContainers, 1, "AICC 必须只渲染一个初始化容器")
	assert.Equal(t, []string{"oc-bootstrap"}, dep.Spec.Template.Spec.InitContainers[0].Command)
	require.Len(t, dep.Spec.Template.Spec.Containers, 2, "AICC Pod 只能包含 hermes 与 oc-ops")
	assert.NotNil(t, containerByName(dep, "hermes"))
	assert.NotNil(t, containerByName(dep, "oc-ops"))
	assert.Nil(t, containerByName(dep, "s3-sync"))
	for _, c := range append(dep.Spec.Template.Spec.InitContainers, dep.Spec.Template.Spec.Containers...) {
		assert.Nil(t, envByName(&c, "AWS_ACCESS_KEY_ID"), "%s 不得注入 AWS 凭证", c.Name)
		assert.Nil(t, envByName(&c, "AWS_SECRET_ACCESS_KEY"), "%s 不得注入 AWS 凭证", c.Name)
		assert.Nil(t, envByName(&c, "AWS_ENDPOINT_URL"), "%s 不得注入 AWS/S3 endpoint", c.Name)
	}
	assertGolden(t, "deployment-aicc.golden.yaml", dep)
}

// TestRenderDeploymentOmitsEmptyImagePullSecret 覆盖本地公开镜像：空 secret 不能渲染为无效列表项。
func TestRenderDeploymentOmitsEmptyImagePullSecret(t *testing.T) {
	spec := testSpec()
	spec.ImagePullSecret = ""

	dep := RenderDeployment(spec, "oc-apps")

	assert.Empty(t, dep.Spec.Template.Spec.ImagePullSecrets)
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

// TestRenderDeploymentOcOpsReadinessProbe 覆盖 Hermes 恢复窗口：oc-ops 仅监听 TCP
// 并不代表同 Pod api_server 已可读会话，必须通过 healthz 将未恢复实例隔离在 Service 之外。
func TestRenderDeploymentOcOpsReadinessProbe(t *testing.T) {
	dep := RenderDeployment(testSpec(), "oc-apps")
	ocOps := containerByName(dep, "oc-ops")
	require.NotNil(t, ocOps, "渲染结果必须包含 oc-ops 容器")
	require.NotNil(t, ocOps.ReadinessProbe, "oc-ops 必须配置就绪探针")
	require.NotNil(t, ocOps.ReadinessProbe.HTTPGet, "oc-ops 就绪探针必须验证 api_server 可用性")
	assert.Equal(t, "/healthz", ocOps.ReadinessProbe.HTTPGet.Path)
	assert.Equal(t, intstr.FromInt(8080), ocOps.ReadinessProbe.HTTPGet.Port)
}

// TestRenderDeploymentHermesAPIServer 断言 hermes 容器注入 API_SERVER_ENABLED=true。
// hermes 内置 api_server 监听 127.0.0.1:8642，与 gateway 同进程，
// 供 oc-ops 触发 POST /oc/skills/reload 实现免重启热加载 skill。
func TestRenderDeploymentHermesAPIServer(t *testing.T) {
	dep := RenderDeployment(testSpec(), "oc-apps")
	// 从三容器中定位 hermes 容器
	var hermes *corev1.Container
	for i := range dep.Spec.Template.Spec.Containers {
		if dep.Spec.Template.Spec.Containers[i].Name == "hermes" {
			hermes = &dep.Spec.Template.Spec.Containers[i]
			break
		}
	}
	require.NotNil(t, hermes, "渲染结果必须包含名为 hermes 的容器")
	// 收集 env 为 map 便于断言
	envs := map[string]string{}
	for _, e := range hermes.Env {
		envs[e.Name] = e.Value
	}
	assert.Equal(t, "true", envs["API_SERVER_ENABLED"], "hermes 容器必须置 API_SERVER_ENABLED=true 以启动内置 api_server")
}

// envByName 在容器 env 中按名查找一条 EnvVar，未找到返回 nil。
func envByName(c *corev1.Container, name string) *corev1.EnvVar {
	for i := range c.Env {
		if c.Env[i].Name == name {
			return &c.Env[i]
		}
	}
	return nil
}

// containerByName 在 deployment 中按名定位容器，未找到返回 nil。
func containerByName(dep *appsv1.Deployment, name string) *corev1.Container {
	for i := range dep.Spec.Template.Spec.Containers {
		if dep.Spec.Template.Spec.Containers[i].Name == name {
			return &dep.Spec.Template.Spec.Containers[i]
		}
	}
	return nil
}

// TestRenderDeploymentAPIServerKey 断言 hermes 与 oc-ops 两容器都注入了 API_SERVER_KEY，
// 且来源同为 per-app control-token Secret——这是 api_server 能启动（上游硬性要求 key，
// 缺失即拒绝启动，含 loopback 绑定）且 oc-ops 能鉴权调 /api/sessions 的前提。
// 该用例守护真实 pod 验证发现的缺陷：仅设 API_SERVER_ENABLED 而不设 key 会让 api_server
// 拒绝启动、会话功能整体不可用。
func TestRenderDeploymentAPIServerKey(t *testing.T) {
	dep := RenderDeployment(testSpec(), "oc-apps")
	for _, name := range []string{"hermes", "oc-ops"} {
		c := containerByName(dep, name)
		require.NotNil(t, c, "渲染结果必须包含容器 %s", name)
		ev := envByName(c, "API_SERVER_KEY")
		require.NotNil(t, ev, "%s 容器必须注入 API_SERVER_KEY，否则 api_server 拒绝启动 / oc-ops 无法鉴权", name)
		require.NotNil(t, ev.ValueFrom, "%s 的 API_SERVER_KEY 必须来自 Secret 引用", name)
		require.NotNil(t, ev.ValueFrom.SecretKeyRef, "%s 的 API_SERVER_KEY 必须用 SecretKeyRef", name)
		assert.Equal(t, "control-token", ev.ValueFrom.SecretKeyRef.Key,
			"%s 的 API_SERVER_KEY 应复用 per-app control-token", name)
	}
}

// TestRenderService 验证 oc-ops Service 渲染（selector + 8080）。
func TestRenderService(t *testing.T) {
	assertGolden(t, "service.golden.yaml", RenderService(testSpec(), "oc-apps"))
}

// TestRenderSecret 验证 control-token Secret 渲染。
func TestRenderSecret(t *testing.T) {
	assertGolden(t, "secret.golden.yaml", RenderSecret(testSpec(), "oc-apps"))
}

// TestRenderSecretIncludesFeishuKeys 验证 AppSpec 带飞书配置时 Secret 写入三个飞书 key。
func TestRenderSecretIncludesFeishuKeys(t *testing.T) {
	spec := AppSpec{
		AppID:           "app-1",
		ControlToken:    "tok",
		FeishuAppID:     "cli_abc",
		FeishuAppSecret: "plain-secret",
		FeishuDomain:    "feishu",
	}
	sec := RenderSecret(spec, "oc-apps")
	require.Equal(t, "cli_abc", sec.StringData["feishu-app-id"])
	require.Equal(t, "plain-secret", sec.StringData["feishu-app-secret"])
	require.Equal(t, "feishu", sec.StringData["feishu-domain"])
}

// TestRenderSecretOmitsFeishuKeysWhenUnset 验证未绑定飞书时不写飞书 key（optional env 不注入）。
func TestRenderSecretOmitsFeishuKeysWhenUnset(t *testing.T) {
	sec := RenderSecret(AppSpec{AppID: "app-1", ControlToken: "tok"}, "oc-apps")
	_, ok := sec.StringData["feishu-app-id"]
	require.False(t, ok)
}

// TestRenderSecret_WorkWeChatKeys 覆盖已绑定企业微信时 Secret 带出 wecom-bot-id/wecom-secret；
// 未绑定（字段空）时不写这两把 key，保证 optional env 注入语义。
func TestRenderSecret_WorkWeChatKeys(t *testing.T) {
	// 已绑定：两字段非空 → Secret 含两把 key。
	bound := RenderSecret(AppSpec{AppID: "a1", ControlToken: "t", WorkWeChatBotID: "bot-1", WorkWeChatSecret: "sec-1"}, "ns")
	assert.Equal(t, "bot-1", bound.StringData["wecom-bot-id"])
	assert.Equal(t, "sec-1", bound.StringData["wecom-secret"])
	// 未绑定：字段空 → 不写 key（避免空值 env 误启用平台）。
	unbound := RenderSecret(AppSpec{AppID: "a1", ControlToken: "t"}, "ns")
	_, hasBot := unbound.StringData["wecom-bot-id"]
	assert.False(t, hasBot)
}

// TestWorkWechatOptionalEnv 覆盖 hermes 容器永久挂载两条 optional SecretKeyRef env，
// optional=true 保证未绑定时不注入（引擎 getenv 为空→平台不启用）。
func TestWorkWechatOptionalEnv(t *testing.T) {
	envs := workWechatOptionalEnv("a1")
	assert.Len(t, envs, 2)
	assert.Equal(t, "WECOM_BOT_ID", envs[0].Name)
	assert.Equal(t, "WECOM_SECRET", envs[1].Name)
	// optional=true：Secret 缺 key 时 k8s 不报错、不注入该 env。
	assert.True(t, *envs[0].ValueFrom.SecretKeyRef.Optional)
}

// TestDingtalkOptionalEnv 验证钉钉两条 optional SecretKeyRef env 名/key/optional 标记正确。
// 覆盖：未绑定时 Secret 无对应 key 也不报错（optional=true），引擎 getenv 为空 → 钉钉平台不启用。
func TestDingtalkOptionalEnv(t *testing.T) {
	envs := dingtalkOptionalEnv("a1")
	require.Len(t, envs, 2)                             // 钉钉注入两条 env
	assert.Equal(t, "DINGTALK_CLIENT_ID", envs[0].Name) // 第一条对应 AppKey
	assert.Equal(t, "dingtalk-client-id", envs[0].ValueFrom.SecretKeyRef.Key)
	assert.True(t, *envs[0].ValueFrom.SecretKeyRef.Optional)
	assert.Equal(t, "DINGTALK_CLIENT_SECRET", envs[1].Name) // 第二条对应 AppSecret
	assert.Equal(t, "dingtalk-client-secret", envs[1].ValueFrom.SecretKeyRef.Key)
	assert.True(t, *envs[1].ValueFrom.SecretKeyRef.Optional)
}

// TestRenderSecret_Dingtalk 验证已绑定钉钉时 client_id/client_secret 明文写入 Secret StringData。
func TestRenderSecret_Dingtalk(t *testing.T) {
	sec := RenderSecret(AppSpec{
		AppID:                "a1",
		ControlToken:         "tok",
		DingtalkClientID:     "ding-key",
		DingtalkClientSecret: "ding-secret",
	}, "ns")
	assert.Equal(t, "ding-key", sec.StringData["dingtalk-client-id"])        // Client ID 明文带出
	assert.Equal(t, "ding-secret", sec.StringData["dingtalk-client-secret"]) // Client Secret 明文带出
}

// TestRenderDeploymentInjectsFeishuOptionalEnv 验证 hermes 容器永久带三条 optional 飞书 env，
// 未绑定时 Secret 无对应 key、optional=true 使 env 不注入、引擎不启用飞书平台。
func TestRenderDeploymentInjectsFeishuOptionalEnv(t *testing.T) {
	// 复用 testSpec 保证 ResourceLimits 有效（RenderDeployment 调 resource.MustParse，空串 panic）
	dep := RenderDeployment(testSpec(), "oc-apps")
	// 按名定位 hermes 容器，比硬编码索引更健壮
	hermes := containerByName(dep, "hermes")
	require.NotNil(t, hermes, "渲染结果必须包含名为 hermes 的容器")
	// 期望的 feishu env 名称 → Secret key 对应关系
	want := map[string]string{
		"FEISHU_APP_ID":     "feishu-app-id",
		"FEISHU_APP_SECRET": "feishu-app-secret",
		"FEISHU_DOMAIN":     "feishu-domain",
	}
	// 收集 hermes env 中来自 SecretKeyRef 的 key 映射
	found := map[string]string{}
	for _, e := range hermes.Env {
		if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
			found[e.Name] = e.ValueFrom.SecretKeyRef.Key
		}
	}
	for name, key := range want {
		require.Equal(t, key, found[name], "env %s 应来自 secret key %s", name, key)
	}
}
