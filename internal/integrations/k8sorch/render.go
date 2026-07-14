package k8sorch

import (
	"oc-manager/internal/domain"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// 资源命名约定（manager 按 appID 确定性寻址，无需存 pod 标识）。
func deploymentName(appID string) string { return "app-" + appID }
func serviceName(appID string) string    { return "app-" + appID + "-ocops" }
func secretName(appID string) string     { return "app-" + appID + "-token" }
func hpaName(appID string) string        { return deploymentName(appID) }

// appLabels 是资源 ObjectMeta 与 pod template 的完整 label（含分组维度 part-of）。
func appLabels(appID string) map[string]string {
	return map[string]string{"app": appID, "app.kubernetes.io/part-of": "oc-manager"}
}

// selectorLabels 是 Deployment/Service 的 selector：仅 app=<id>，最小且稳定
// （Deployment selector 不可变；分组用的 part-of 不进 selector，避免过度约束/漏选）。
func selectorLabels(appID string) map[string]string {
	return map[string]string{"app": appID}
}

// RenderSecret 渲染 per-app 控制 token Secret（control-token 键）；已绑定飞书时附带飞书凭证 key。
func RenderSecret(spec AppSpec, namespace string) *corev1.Secret {
	data := map[string]string{"control-token": spec.ControlToken}
	// 已绑定飞书：把凭证带入 Secret，保证 app 重建/镜像升级不丢配置（DB 是 source of truth）。
	// FeishuAppSecret 存明文——引擎 FEISHU_APP_SECRET 需明文，buildAppSpec 调用前已解密。
	if spec.FeishuAppID != "" && spec.FeishuAppSecret != "" {
		data["feishu-app-id"] = spec.FeishuAppID
		data["feishu-app-secret"] = spec.FeishuAppSecret
		data["feishu-domain"] = spec.FeishuDomain
	}
	// 已绑定企业微信：带出 bot_id + secret 明文，保证重建/升级不丢配置（DB 是 source of truth）。
	if spec.WorkWeChatBotID != "" && spec.WorkWeChatSecret != "" {
		data["wecom-bot-id"] = spec.WorkWeChatBotID
		data["wecom-secret"] = spec.WorkWeChatSecret
	}
	// 已绑定钉钉：带出 client_id + client_secret 明文，保证重建/升级不丢配置（DB 是 source of truth）。
	if spec.DingtalkClientID != "" && spec.DingtalkClientSecret != "" {
		data["dingtalk-client-id"] = spec.DingtalkClientID
		data["dingtalk-client-secret"] = spec.DingtalkClientSecret
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName(spec.AppID), Namespace: namespace, Labels: appLabels(spec.AppID)},
		Type:       corev1.SecretTypeOpaque,
		StringData: data,
	}
}

// RenderService 渲染 oc-ops Service（OcOpsResolver 寻址目标，port 8080）。
func RenderService(spec AppSpec, namespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: serviceName(spec.AppID), Namespace: namespace, Labels: appLabels(spec.AppID)},
		Spec: corev1.ServiceSpec{
			Selector: selectorLabels(spec.AppID),
			Ports:    []corev1.ServicePort{{Name: "oc-ops", Port: 8080, TargetPort: intstr.FromInt32(8080)}},
		},
	}
}

// RenderAICCHPA 渲染 AICC 应用的 autoscaling/v2 HPA。最少保留一个副本维持客服入口，
// 缩容稳定窗口避免瞬时负载下降时过快回收仍在处理会话的 Pod。未配置 external metrics
// adapter 时仅渲染集群内置 CPU/内存指标，避免提交无法解析的无效 HPA。
func RenderAICCHPA(spec AppSpec, namespace string, businessMetrics AICCBusinessMetricsConfig) *autoscalingv2.HorizontalPodAutoscaler {
	minReplicas := int32(1)
	stabilizationWindowSeconds := int32(600)
	metrics := []autoscalingv2.MetricSpec{
		{Type: autoscalingv2.ResourceMetricSourceType, Resource: &autoscalingv2.ResourceMetricSource{
			Name:   corev1.ResourceCPU,
			Target: autoscalingv2.MetricTarget{Type: autoscalingv2.UtilizationMetricType, AverageUtilization: int32Ptr(70)},
		}},
		{Type: autoscalingv2.ResourceMetricSourceType, Resource: &autoscalingv2.ResourceMetricSource{
			Name:   corev1.ResourceMemory,
			Target: autoscalingv2.MetricTarget{Type: autoscalingv2.UtilizationMetricType, AverageUtilization: int32Ptr(75)},
		}},
	}
	if businessMetrics.Enabled() {
		// External 指标按隐藏 app ID 选择，防止一个客服应用的积压把其他应用一并扩容。
		selector := &metav1.LabelSelector{MatchLabels: map[string]string{businessMetrics.AppLabel: spec.AppID}}
		metrics = append(metrics,
			externalMetricSpec(businessMetrics.QueueDepth, selector),
			externalMetricSpec(businessMetrics.Inflight, selector),
		)
	}
	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: hpaName(spec.AppID), Namespace: namespace, Labels: appLabels(spec.AppID)},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       deploymentName(spec.AppID),
			},
			MinReplicas: &minReplicas,
			// MaxReplicas 是 API 必填项。十个副本限制单个客服应用的突发资源占用。
			MaxReplicas: 10,
			Metrics:     metrics,
			Behavior: &autoscalingv2.HorizontalPodAutoscalerBehavior{
				ScaleDown: &autoscalingv2.HPAScalingRules{StabilizationWindowSeconds: &stabilizationWindowSeconds},
			},
		},
	}
}

// externalMetricSpec 将一项安全业务 gauge 转换为 HPA External 指标；target 使用平均值，
// 使 Kubernetes 按当前副本数均摊积压或在飞压力后计算期望副本数。
func externalMetricSpec(metric AICCExternalMetricConfig, selector *metav1.LabelSelector) autoscalingv2.MetricSpec {
	target := metric.TargetAverageValue.DeepCopy()
	return autoscalingv2.MetricSpec{Type: autoscalingv2.ExternalMetricSourceType, External: &autoscalingv2.ExternalMetricSource{
		Metric: autoscalingv2.MetricIdentifier{Name: metric.Name, Selector: selector},
		Target: autoscalingv2.MetricTarget{Type: autoscalingv2.AverageValueMetricType, AverageValue: &target},
	}}
}

// int32Ptr 为 HPA 内嵌可选字段构造独立指针，避免多个指标意外共享可变地址。
func int32Ptr(v int32) *int32 { return &v }

// RenderDeployment 渲染 app Deployment（replicas=1, Recreate）。普通应用使用 restore 与
// s3-sync 持久化运行时数据；AICC 使用 oc-bootstrap 初始化且不挂载 S3 同步 sidecar。
func RenderDeployment(spec AppSpec, namespace string) *appsv1.Deployment {
	replicas := int32(1)
	// ctrlTokenEnv 从 Secret 挂载 per-app control token，供多个容器复用。
	ctrlTokenEnv := corev1.EnvVar{Name: "OC_CONTROL_TOKEN", ValueFrom: &corev1.EnvVarSource{
		SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName(spec.AppID)},
			Key:                  "control-token",
		},
	}}
	// bootstrapEnv 指向 manager bootstrap 端点，供 initContainer restore 和 sidecar s3-sync 使用。
	bootstrapEnv := corev1.EnvVar{Name: "OC_BOOTSTRAP_URL", Value: spec.BootstrapURL}
	// proxyEnv 为需直连外网的 hermes（微信平台）/ oc-ops（渠道登录）注入代理 env；
	// 留空字段不注入，生产 pod 有外网出口时整组为空。
	proxyEnv := buildProxyEnv(spec.Proxy)
	// dataMount 是 hermes 主目录（app 数据卷）挂载点。
	dataMount := corev1.VolumeMount{Name: "data", MountPath: "/opt/data"}
	// inputMount 是 initContainer restore 写运行时配置的可写挂载点。
	inputMount := corev1.VolumeMount{Name: "oc-input", MountPath: "/opt/oc-input"}
	// inputMountRO 是 hermes 只读消费 oc-input（防止主容器误写共享配置卷）。
	inputMountRO := corev1.VolumeMount{Name: "oc-input", MountPath: "/opt/oc-input", ReadOnly: true}
	// reqs/lims 从 ResourceLimits 字符串解析为 k8s resource.Quantity。
	reqs := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(spec.Resources.RequestsCPU),
		corev1.ResourceMemory: resource.MustParse(spec.Resources.RequestsMemory),
	}
	lims := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(spec.Resources.LimitsCPU),
		corev1.ResourceMemory: resource.MustParse(spec.Resources.LimitsMemory),
	}
	// 本地公开镜像不配置拉取凭证；空名称必须省略，否则 Kubernetes 会接受脏对象但拒绝后续 strategic patch。
	var imagePullSecrets []corev1.LocalObjectReference
	if spec.ImagePullSecret != "" {
		imagePullSecrets = []corev1.LocalObjectReference{{Name: spec.ImagePullSecret}}
	}
	// AICC 运行时由专用镜像在启动期一次性 bootstrap，不依赖标准应用的 S3 恢复与同步。
	// 两种应用都需要 control token 与 manager bootstrap URL 取得 per-app 运行时配置。
	initContainer := corev1.Container{
		Name:         "restore",
		Image:        spec.OpsImage,
		Command:      []string{"oc-restore"},
		Env:          []corev1.EnvVar{ctrlTokenEnv, bootstrapEnv},
		VolumeMounts: []corev1.VolumeMount{inputMount, dataMount},
	}
	if domain.IsAICCAppType(spec.AppType) {
		initContainer.Command = []string{"oc-bootstrap"}
		// AICC bootstrap 只生成 hermes 消费的输入配置，不恢复标准应用的数据目录。
		initContainer.VolumeMounts = []corev1.VolumeMount{inputMount}
	}
	containers := []corev1.Container{
		{
			// hermes：主业务容器，负责 AI 网关逻辑，资源配额受限。
			Name:  "hermes",
			Image: spec.HermesImage,
			// API_SERVER_ENABLED=true：启动 hermes 内置 api_server（127.0.0.1:8642，与 gateway 同进程），
			// 供 oc-ops 触发 POST /oc/skills/reload 免重启热加载 skill 与会话端点转发。
			// API_SERVER_KEY：上游 api_server 即使 enabled 也**硬性要求**配置 key，否则
			// 拒绝启动（含 loopback-only 绑定）。复用 per-app control-token 作为 key——
			// api_server 仅绑 127.0.0.1、只有同 pod 内 oc-ops 可达，per-app 密钥安全足够；
			// oc-ops 容器注入同一 key（见下）后即可鉴权调用 /api/sessions。
			// 飞书三条 env 永久注入 hermes 容器；未绑定时 Secret 无对应 key，optional=true 使
			// env 不注入，引擎 getenv 为空 → 飞书平台不启用。Deployment 模板永不因绑定变化。
			Env: append(append(append(append([]corev1.EnvVar{
				{Name: "HERMES_HOME", Value: "/opt/data"},
				{Name: "API_SERVER_ENABLED", Value: "true"},
				{Name: "API_SERVER_KEY", ValueFrom: ctrlTokenEnv.ValueFrom},
			}, feishuOptionalEnv(spec.AppID)...), workWechatOptionalEnv(spec.AppID)...), dingtalkOptionalEnv(spec.AppID)...), proxyEnv...),
			VolumeMounts: []corev1.VolumeMount{inputMountRO, dataMount},
			Resources:    corev1.ResourceRequirements{Requests: reqs, Limits: lims},
			// readinessProbe：exec hermes gateway status，验证网关真正就绪。
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					Exec: &corev1.ExecAction{Command: []string{"hermes", "gateway", "status"}},
				},
				InitialDelaySeconds: 10,
				PeriodSeconds:       10,
				FailureThreshold:    6,
			},
		},
		{
			// oc-ops：控制平面 API sidecar，复用 hermes 镜像，覆盖 CMD 启动 uvicorn。
			Name:  "oc-ops",
			Image: spec.HermesImage,
			Command: []string{
				"/usr/local/lib/hermes-agent/venv/bin/python",
				"-m", "uvicorn",
				"ocops.server:app",
				"--host", "0.0.0.0",
				"--port", "8080",
			},
			// OC_OPS_TOKEN 复用 ctrlTokenEnv 的 SecretKeyRef 来源。
			// PYTHONPATH=/usr/local/lib：ocops 包在镜像内落点 /usr/local/lib/ocops，
			// 但 uvicorn 直接 `python -m uvicorn ocops.server:app` 启动、不经 oc-* shim
			// 的 sys.path.insert("/usr/local/lib")，故须显式置 PYTHONPATH 让 venv python
			// 能解析 `import ocops`，否则 sidecar 起不来（ModuleNotFoundError: ocops）。
			// API_SERVER_KEY 与 hermes 容器同源（control-token），让 oc-ops 调
			// hermes api_server /api/sessions 时带 Bearer 鉴权（conversation._api_server_key
			// 优先读此 env）；两容器同 key 才能互通。
			Env: append([]corev1.EnvVar{
				{Name: "OC_OPS_TOKEN", ValueFrom: ctrlTokenEnv.ValueFrom},
				{Name: "API_SERVER_KEY", ValueFrom: ctrlTokenEnv.ValueFrom},
				{Name: "PYTHONPATH", Value: "/usr/local/lib"},
			}, proxyEnv...),
			Ports: []corev1.ContainerPort{{ContainerPort: 8080}},
			// readinessProbe：healthz 仅验证同 Pod api_server 的轻量会话读取，
			// 不触发模型或渠道请求。TCP listen 早于 api_server 状态恢复，若只探端口，
			// Pod 会在会话 API 仍返回 500 时提前进入 Service endpoints。
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromInt(8080)},
				},
				InitialDelaySeconds: 10,
				PeriodSeconds:       10,
				FailureThreshold:    6,
			},
			VolumeMounts: []corev1.VolumeMount{dataMount},
		},
	}
	if !domain.IsAICCAppType(spec.AppType) {
		// s3-sync：标准应用的数据持久化 sidecar，preStop 执行最终同步防止数据丢失。
		containers = append(containers, corev1.Container{
			Name:         "s3-sync",
			Image:        spec.OpsImage,
			Command:      []string{"oc-sync"},
			Env:          []corev1.EnvVar{ctrlTokenEnv, bootstrapEnv},
			VolumeMounts: []corev1.VolumeMount{dataMount},
			Lifecycle: &corev1.Lifecycle{
				PreStop: &corev1.LifecycleHandler{
					Exec: &corev1.ExecAction{Command: []string{"oc-presync"}},
				},
			},
		})
	}
	strategy := appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
	var terminationGracePeriodSeconds *int64
	if domain.IsAICCAppType(spec.AppType) {
		// AICC 允许 HPA 同时保有多副本；滚动更新中旧副本持续服务至新副本 Ready。
		maxUnavailable, maxSurge := intstr.FromInt(0), intstr.FromInt(1)
		strategy = appsv1.DeploymentStrategy{
			Type: appsv1.RollingUpdateDeploymentStrategyType,
			RollingUpdate: &appsv1.RollingUpdateDeployment{
				MaxUnavailable: &maxUnavailable,
				MaxSurge:       &maxSurge,
			},
		}
		grace := int64(90)
		terminationGracePeriodSeconds = &grace
		for i := range containers {
			if containers[i].Name == "oc-ops" {
				// HPA 的 CPU/内存 utilization 要求 Pod 内所有常驻容器都有对应 request；
				// oc-ops 为轻量控制 sidecar，使用固定小 request，避免放大客服主进程配额。
				containers[i].Resources.Requests = corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				}
			}
			// 不调用容器内未定义的业务 drain 命令；仅等待 endpoints 摘除传播，
			// 随后由 Kubernetes 在 90 秒终止窗口内发送 SIGTERM 并回收 Pod。
			containers[i].Lifecycle = &corev1.Lifecycle{PreStop: &corev1.LifecycleHandler{Sleep: &corev1.SleepAction{Seconds: 10}}}
		}
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName(spec.AppID),
			Namespace: namespace,
			Labels:    appLabels(spec.AppID),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			// 普通应用保留 Recreate 避免数据卷冲突；AICC 使用上方的零不可用滚动策略。
			Strategy: strategy,
			Selector: &metav1.LabelSelector{MatchLabels: selectorLabels(spec.AppID)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: appLabels(spec.AppID)},
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: terminationGracePeriodSeconds,
					// imagePullSecrets 用于拉取私有镜像仓库。
					ImagePullSecrets: imagePullSecrets,
					InitContainers:   []corev1.Container{initContainer},
					Containers:       containers,
					Volumes: []corev1.Volume{
						// oc-input：initContainer restore 输出 → hermes/oc-ops 消费，生命周期与 pod 同步。
						{Name: "oc-input", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						// data：hermes 运行时数据目录，s3-sync 负责持久化到 S3。
						{Name: "data", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					},
				},
			},
		},
	}
	// 将 AppSpec.Labels 合并到 Deployment 与 pod template 的 label，支持外部选择器扩展。
	for k, v := range spec.Labels {
		dep.Labels[k] = v
		dep.Spec.Template.Labels[k] = v
	}
	return dep
}

// feishuOptionalEnv 返回飞书三条 optional SecretKeyRef env（FEISHU_APP_ID / FEISHU_APP_SECRET /
// FEISHU_DOMAIN），供 hermes 容器永久挂载。Optional=true 保证：未绑定飞书时 Secret 无对应 key，
// k8s 不注入该 env（引擎 getenv 为空 → 飞书平台不启用），Deployment 模板无需随绑定状态变化。
func feishuOptionalEnv(appID string) []corev1.EnvVar {
	optionalTrue := true
	ref := func(key string) *corev1.EnvVarSource {
		return &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName(appID)},
			Key:                  key,
			Optional:             &optionalTrue,
		}}
	}
	return []corev1.EnvVar{
		{Name: "FEISHU_APP_ID", ValueFrom: ref("feishu-app-id")},
		{Name: "FEISHU_APP_SECRET", ValueFrom: ref("feishu-app-secret")},
		{Name: "FEISHU_DOMAIN", ValueFrom: ref("feishu-domain")},
	}
}

// workWechatOptionalEnv 返回企业微信两条 optional SecretKeyRef env（WECOM_BOT_ID / WECOM_SECRET），
// 供 hermes 容器永久挂载。Optional=true：未绑定时 Secret 无对应 key，k8s 不注入该 env
// （引擎 getenv 为空 → 企业微信平台不启用），Deployment 模板无需随绑定状态变化。
func workWechatOptionalEnv(appID string) []corev1.EnvVar {
	optionalTrue := true
	ref := func(key string) *corev1.EnvVarSource {
		return &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName(appID)},
			Key:                  key,
			Optional:             &optionalTrue,
		}}
	}
	return []corev1.EnvVar{
		{Name: "WECOM_BOT_ID", ValueFrom: ref("wecom-bot-id")},
		{Name: "WECOM_SECRET", ValueFrom: ref("wecom-secret")},
	}
}

// dingtalkOptionalEnv 返回钉钉两条 optional SecretKeyRef env（DINGTALK_CLIENT_ID / DINGTALK_CLIENT_SECRET），
// 供 hermes 容器永久挂载。Optional=true：未绑定时 Secret 无对应 key，k8s 不注入该 env
// （引擎 getenv 为空 → 钉钉平台不启用），Deployment 模板无需随绑定状态变化。
func dingtalkOptionalEnv(appID string) []corev1.EnvVar {
	optionalTrue := true
	ref := func(key string) *corev1.EnvVarSource {
		return &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName(appID)},
			Key:                  key,
			Optional:             &optionalTrue,
		}}
	}
	return []corev1.EnvVar{
		{Name: "DINGTALK_CLIENT_ID", ValueFrom: ref("dingtalk-client-id")},
		{Name: "DINGTALK_CLIENT_SECRET", ValueFrom: ref("dingtalk-client-secret")},
	}
}

// buildProxyEnv 把 ProxyEnv 转成容器 env 列表；空字段不产生 env（保持 pod 干净，
// 生产无代理时整组为空）。NO_PROXY 只在配了任一代理时才有意义，故也仅非空时注入。
func buildProxyEnv(p ProxyEnv) []corev1.EnvVar {
	var envs []corev1.EnvVar
	if p.HTTPProxy != "" {
		envs = append(envs, corev1.EnvVar{Name: "HTTP_PROXY", Value: p.HTTPProxy})
	}
	if p.HTTPSProxy != "" {
		envs = append(envs, corev1.EnvVar{Name: "HTTPS_PROXY", Value: p.HTTPSProxy})
	}
	if p.NoProxy != "" {
		envs = append(envs, corev1.EnvVar{Name: "NO_PROXY", Value: p.NoProxy})
	}
	return envs
}
