package config

// Config 是 runtime agent 进程启动所需的完整配置。
type Config struct {
	Agent     AgentConfig     `yaml:"agent"`
	Manager   ManagerConfig   `yaml:"manager"`
	Heartbeat HeartbeatConfig `yaml:"heartbeat"`
}

// AgentConfig 描述 runtime agent 本进程的监听、身份和鉴权参数。
type AgentConfig struct {
	Name          string `yaml:"name"`
	AdvertiseHost string `yaml:"advertise_host"`
	MaxApps       *int32 `yaml:"max_apps"`
	DataRoot      string `yaml:"data_root"`
	StateDir      string `yaml:"state_dir"`
	DockerSocket  string `yaml:"docker_socket"`
	TrustedCIDR   string `yaml:"trusted_cidr"`
	DockerAddr    string `yaml:"docker_addr"`
	FileAddr      string `yaml:"file_addr"`
}

// ManagerConfig 描述 agent 主动连接 manager 时所需的字段。
type ManagerConfig struct {
	Endpoint         string `yaml:"endpoint"`          // 形如 https://manager.example/api/v1
	EnrollmentSecret string `yaml:"enrollment_secret"` // 共享 enrollment secret
	CABundle         string `yaml:"ca_bundle"`         // 可选：manager TLS CA PEM 全文；空则信任系统根
	SkipVerify       bool   `yaml:"skip_verify"`       // 仅本地调试用；生产必须 false
}

// HeartbeatConfig 控制 agent 主动心跳的节奏与失败告警阈值。
type HeartbeatConfig struct {
	IntervalSeconds      int `yaml:"interval_seconds"`
	FailureLogThreshold int `yaml:"failure_log_threshold"`
}
