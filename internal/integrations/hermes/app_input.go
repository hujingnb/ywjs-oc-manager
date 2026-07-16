package hermes

import (
	"bytes"
	"context"
	"fmt"
	"io"
)

// AppInputWriter 上传单个 input/ 子文件的能力。
// 在 k8s 路径，pod 配置由 initContainer 调 manager bootstrap 端点交付，
// app_initialize 不直接注入此接口；接口保留供潜在的非 k8s 场景或后续演进。
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
	// KnowledgeRuntimeBaseURL 是容器内访问 manager runtime knowledge API 的地址。
	KnowledgeRuntimeBaseURL string
	// KnowledgeAppToken 是 app 级 runtime token，只能访问当前实例/组织知识库。
	KnowledgeAppToken string

	// WebPublish* 在企业开通发布能力时注入，触发 oc-publish skill 条件渲染。
	WebPublishRuntimeBaseURL string
	WebPublishAppToken       string
	WebPublishBaseDomain     string

	// PersonaText 版本内置提示词（即 version.SystemPrompt），写入 resources/persona.md。
	PersonaText string
	// PlatformRule 平台层规则文本，写入 resources/platform-rules.md。
	PlatformRule string

	// Routing 智能路由映射，透传到 manifest.routing；空 map 时 omitempty 省略。
	Routing map[string]string
	// SkillRelPaths 已推送到 input/ 的版本 skill tar 相对路径列表，
	// 透传到 manifest.resources.skills；空时 omitempty 省略。
	SkillRelPaths []string
	// Capabilities 是本次启动显式授予 runtime 的能力上限；普通应用留空，AICC 固定下发只读集合。
	Capabilities []string

	OrgName   string
	OwnerName string

	// Language 是 hermes bot 对终端用户说话的语言（en/zh）。
	// 空字符串表示「未设置」，由 renderer/hermes 回退平台默认。
	// 对应 manifest.yaml 中 app.language 字段。
	Language string
}

// WriteAppInput 一次性写入 manifest.yaml + resources/persona.md + resources/platform-rules.md。
// v2：不再写入 resources/organization-rules.md 和 resources/application-rules.md；
// 知识库能力通过 manifest.knowledge + oc-kb runtime skill 接入 manager runtime API。
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

	m := BuildManifest(in)
	body, err := MarshalManifestYAML(m)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := w.WriteAppInputFile(ctx, appID, "manifest.yaml", bytes.NewBuffer(body)); err != nil {
		return fmt.Errorf("upload manifest: %w", err)
	}
	return nil
}
