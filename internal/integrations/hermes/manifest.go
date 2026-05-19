package hermes

import (
	"bytes"

	"gopkg.in/yaml.v3"
)

// Manifest 对应 spec §4.2 manifest.yaml 的完整字段视图。
// 字段顺序通过显式 yaml tag 控制；不引入 schema_version。
type Manifest struct {
	App         ManifestApp         `yaml:"app"`
	Credentials ManifestCredentials `yaml:"credentials"`
	Resources   ManifestResources   `yaml:"resources"`
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
type ManifestResources struct {
	Persona string        `yaml:"persona"`
	Rules   ManifestRules `yaml:"rules"`
}

// ManifestRules 三层规则的相对路径。
type ManifestRules struct {
	Platform     string `yaml:"platform"`
	Organization string `yaml:"organization"`
	Application  string `yaml:"application"`
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
