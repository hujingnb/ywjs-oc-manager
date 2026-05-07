package config

// Config 是 runtime agent 进程启动所需的完整配置。
// agent 是独立二进制，配置结构必须收口在 runtime/agent/config，避免依赖 manager 的 internal/config。
type Config struct {
	Agent     AgentConfig     `yaml:"agent"`
	Manager   ManagerConfig   `yaml:"manager"`
	Heartbeat HeartbeatConfig `yaml:"heartbeat"`
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

// ManagerConfig 描述 agent 主动连接 manager 时所需的字段。
// 节点首次 register 完成后，由 ops 把响应中的 node_id 与 agent_token 回填到 yaml；
// 三字段（endpoint / node_id / agent_token）必须同时填齐或同时为空，避免悄悄不发心跳。
type ManagerConfig struct {
	Endpoint   string `yaml:"endpoint"`    // 形如 https://manager.example/api/v1
	NodeID     string `yaml:"node_id"`     // POST /agent/register 响应中的 node_id
	AgentToken string `yaml:"agent_token"` // POST /agent/register 响应中的 agent_token
	CABundle   string `yaml:"ca_bundle"`   // 可选：manager TLS CA PEM 全文；空则信任系统根
	SkipVerify bool   `yaml:"skip_verify"` // 仅本地调试用；生产必须 false
}

// HeartbeatConfig 控制 agent 主动心跳的节奏与失败告警阈值。
// IntervalSeconds 默认 30，最小 5；FailureLogThreshold 默认 5（连续失败到该值打 ERROR）。
type HeartbeatConfig struct {
	IntervalSeconds     int `yaml:"interval_seconds"`
	FailureLogThreshold int `yaml:"failure_log_threshold"`
}
