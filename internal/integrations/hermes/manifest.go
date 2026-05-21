package hermes

import (
	"bytes"

	"gopkg.in/yaml.v3"
)

// Manifest 对应 manifest v2 的完整字段视图。
// v2 新增顶层 routing 映射（模型别名 → 实际模型名）和
// resources.skills 技能包列表；org/app 层规则已移出，仅保留平台规则。
// 字段顺序通过显式 yaml tag 控制；不引入 schema_version。
type Manifest struct {
	App         ManifestApp         `yaml:"app"`
	Credentials ManifestCredentials `yaml:"credentials"`
	Resources   ManifestResources   `yaml:"resources"`
	// Routing 智能路由映射，键为模型别名，值为实际模型名；空时省略。
	Routing map[string]string `yaml:"routing,omitempty"`
}

// ManifestApp 业务元数据。id/name 仅审计日志使用；model 直接进 hermes config.yaml model.default。
type ManifestApp struct {
	ID    string `yaml:"id"`
	Name  string `yaml:"name"`
	Model string `yaml:"model"`
}

// ManifestCredentials 凭证集合；当前仅 openai；微信凭证由 hermes 自管。
type ManifestCredentials struct {
	OpenAI ManifestOpenAI `yaml:"openai"`
}

// ManifestOpenAI OPENAI 凭证；base_url 不带 /v1，由镜像 renderer 自行拼。
type ManifestOpenAI struct {
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
}

// ManifestResources 指向 resources/ 子目录的相对路径集合。
// v2 新增 skills 字段，指向已推送到 input/ 的技能包 tar 相对路径列表；空时省略。
type ManifestResources struct {
	Persona string        `yaml:"persona"`
	Rules   ManifestRules `yaml:"rules"`
	// Skills 技能包相对路径列表，例如 ["resources/skills/weather.tar"]；空时省略。
	Skills []string `yaml:"skills,omitempty"`
}

// ManifestRules 平台层规则文件的相对路径。
// v2 仅保留 platform 一层；org/app 层规则已由版本实例 system_prompt 覆盖。
type ManifestRules struct {
	Platform string `yaml:"platform"`
}

// MarshalManifestYAML 把 Manifest 序列化为 YAML。
// 显式构造 yaml.Encoder 是为了未来需要时方便加 SetIndent 等。
func MarshalManifestYAML(m Manifest) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
