package config

// Config 是 runtime agent 进程启动所需的完整配置。
// agent 是独立二进制，配置结构必须收口在 runtime/agent/config，避免依赖 manager 的 internal/config。
type Config struct {
	Agent AgentConfig `yaml:"agent"`
}

// AgentConfig 描述 runtime agent 本进程的监听、存储和鉴权参数。
// token 与 trusted_cidr 允许为空，以兼容本地调试或受信网络内的最小部署。
type AgentConfig struct {
	DataRoot     string `yaml:"data_root"`
	StateDir     string `yaml:"state_dir"`
	DockerSocket string `yaml:"docker_socket"`
	Token        string `yaml:"token"`
	TrustedCIDR  string `yaml:"trusted_cidr"`
	DockerAddr   string `yaml:"docker_addr"`
	FileAddr     string `yaml:"file_addr"`
}
