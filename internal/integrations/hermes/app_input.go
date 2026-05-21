package hermes

import (
	"bytes"
	"context"
	"fmt"
	"io"
)

// AppInputWriter 上传单个 input/ 子文件的能力。
// 实现由 internal/integrations/agent.RuntimeFileClient.UploadAppInputFile 提供。
type AppInputWriter interface {
	WriteAppInputFile(ctx context.Context, appID, relPath string, body io.Reader) error
}

// AppInputData manager 端写入 input/ 所需的全部业务数据。
// v2：移除 OrganizationRule / ApplicationRule；新增 Routing 智能路由映射与
// SkillRelPaths 技能包相对路径列表。占位符替换在 WriteAppInput 内部完成。
type AppInputData struct {
	AppID         string
	AppName       string
	Model         string
	OpenAIAPIKey  string
	OpenAIBaseURL string

	// PersonaText 版本内置提示词（即 version.SystemPrompt），写入 resources/persona.md。
	PersonaText  string
	// PlatformRule 平台层规则文本，写入 resources/platform-rules.md。
	PlatformRule string

	// Routing 智能路由映射，透传到 manifest.routing；空 map 时 omitempty 省略。
	Routing map[string]string
	// SkillRelPaths 已推送到 input/ 的版本 skill tar 相对路径列表，
	// 透传到 manifest.resources.skills；空时 omitempty 省略。
	SkillRelPaths []string

	OrgName   string
	OwnerName string
}

// WriteAppInput 一次性写入 manifest.yaml + resources/persona.md + resources/platform-rules.md。
// v2：不再写入 resources/organization-rules.md 和 resources/application-rules.md；
// 知识库文件由 knowledge_sync 链路单独写入 resources/knowledge/{org,app}/。
//
// 上传顺序：先写 resources/* 后写 manifest.yaml，最大限度避免 oc-entrypoint
// 读到「指向 resources 文件已不存在」的中间态。
func WriteAppInput(ctx context.Context, w AppInputWriter, appID string, in AppInputData) error {
	vars := VariablesFromContext(in.OrgName, in.AppName, in.OwnerName)
	persona, err := RenderPersonaText(in.PersonaText, vars)
	if err != nil {
		return fmt.Errorf("render persona: %w", err)
	}
	platform, err := RenderRuleText(in.PlatformRule, vars)
	if err != nil {
		return fmt.Errorf("render platform rule: %w", err)
	}

	if err := w.WriteAppInputFile(ctx, appID, "resources/persona.md", bytes.NewBufferString(persona)); err != nil {
		return fmt.Errorf("upload persona: %w", err)
	}
	if err := w.WriteAppInputFile(ctx, appID, "resources/platform-rules.md", bytes.NewBufferString(platform)); err != nil {
		return fmt.Errorf("upload platform rules: %w", err)
	}

	m := Manifest{
		App: ManifestApp{ID: in.AppID, Name: in.AppName, Model: in.Model},
		Credentials: ManifestCredentials{
			OpenAI: ManifestOpenAI{APIKey: in.OpenAIAPIKey, BaseURL: in.OpenAIBaseURL},
		},
		Resources: ManifestResources{
			Persona: "resources/persona.md",
			Rules: ManifestRules{
				Platform: "resources/platform-rules.md",
			},
			Skills: in.SkillRelPaths,
		},
		Routing: in.Routing,
	}
	body, err := MarshalManifestYAML(m)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := w.WriteAppInputFile(ctx, appID, "manifest.yaml", bytes.NewBuffer(body)); err != nil {
		return fmt.Errorf("upload manifest: %w", err)
	}
	return nil
}

