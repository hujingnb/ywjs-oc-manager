package hermes

// BuildManifest 从 AppInputData 构造 Manifest（纯函数，无 IO）。
// 供 WriteAppInput（写卷路径）与 bootstrap 端点（HTTP 响应路径）共用，
// 保证两条路径产出的 manifest 完全一致。
func BuildManifest(in AppInputData) Manifest {
	m := Manifest{
		App: ManifestApp{ID: in.AppID, Name: in.AppName, Model: in.Model},
		Credentials: ManifestCredentials{
			OpenAI: ManifestOpenAI{APIKey: in.OpenAIAPIKey, BaseURL: in.OpenAIBaseURL},
		},
		Resources: ManifestResources{
			Persona: "resources/persona.md",
			Rules:   ManifestRules{Platform: "resources/platform-rules.md"},
			Skills:  in.SkillRelPaths,
		},
		Routing: in.Routing,
	}
	// knowledge 仅在 runtime base url 与 app token 同时存在时写入（与原 WriteAppInput 语义一致）。
	if in.KnowledgeRuntimeBaseURL != "" && in.KnowledgeAppToken != "" {
		m.Knowledge = ManifestKnowledge{
			RuntimeBaseURL: in.KnowledgeRuntimeBaseURL,
			AppToken:       in.KnowledgeAppToken,
		}
	}
	return m
}
