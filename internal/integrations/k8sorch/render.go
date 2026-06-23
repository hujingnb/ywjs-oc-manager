package k8sorch

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// 资源命名约定（manager 按 appID 确定性寻址，无需存 pod 标识）。
func deploymentName(appID string) string { return "app-" + appID }
func serviceName(appID string) string    { return "app-" + appID + "-ocops" }
func secretName(appID string) string     { return "app-" + appID + "-token" }

// appLabels 是资源 ObjectMeta 与 pod template 的完整 label（含分组维度 part-of）。
func appLabels(appID string) map[string]string {
	return map[string]string{"app": appID, "app.kubernetes.io/part-of": "oc-manager"}
}

// selectorLabels 是 Deployment/Service 的 selector：仅 app=<id>，最小且稳定
// （Deployment selector 不可变；分组用的 part-of 不进 selector，避免过度约束/漏选）。
func selectorLabels(appID string) map[string]string {
	return map[string]string{"app": appID}
}

// RenderSecret 渲染 per-app 控制 token Secret（control-token 键）。
func RenderSecret(spec AppSpec, namespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName(spec.AppID), Namespace: namespace, Labels: appLabels(spec.AppID)},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{"control-token": spec.ControlToken},
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

// RenderDeployment 渲染 app Deployment（replicas=1, Recreate, initContainer restore +
// hermes + oc-ops + sidecar s3-sync，emptyDir oc-input + data）。
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
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName(spec.AppID),
			Namespace: namespace,
			Labels:    appLabels(spec.AppID),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			// Recreate 策略：旧 pod 先完全停止再启新 pod，避免数据卷冲突。
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Selector: &metav1.LabelSelector{MatchLabels: selectorLabels(spec.AppID)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: appLabels(spec.AppID)},
				Spec: corev1.PodSpec{
					// imagePullSecrets 用于拉取私有镜像仓库。
					ImagePullSecrets: []corev1.LocalObjectReference{{Name: spec.ImagePullSecret}},
					// initContainer restore：从 manager bootstrap 拉取运行时配置写入 oc-input。
					InitContainers: []corev1.Container{{
						Name:    "restore",
						Image:   spec.OpsImage,
						Command: []string{"oc-restore"},
						Env:     []corev1.EnvVar{ctrlTokenEnv, bootstrapEnv},
						VolumeMounts: []corev1.VolumeMount{inputMount, dataMount},
					}},
					Containers: []corev1.Container{
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
							Env: append([]corev1.EnvVar{
								{Name: "HERMES_HOME", Value: "/opt/data"},
								{Name: "API_SERVER_ENABLED", Value: "true"},
								{Name: "API_SERVER_KEY", ValueFrom: ctrlTokenEnv.ValueFrom},
							}, proxyEnv...),
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
							Ports:        []corev1.ContainerPort{{ContainerPort: 8080}},
							VolumeMounts: []corev1.VolumeMount{dataMount},
						},
						{
							// s3-sync：数据持久化 sidecar，preStop 执行最终同步防止数据丢失。
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
						},
					},
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
