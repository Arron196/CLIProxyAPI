package registry

import "testing"

func TestValidateModelsCatalog_AllowsEmptyQwenAndIFlow(t *testing.T) {
	t.Parallel()

	data := &staticModelsJSON{
		Claude:      []*ModelInfo{{ID: "claude-test"}},
		Gemini:      []*ModelInfo{{ID: "gemini-test"}},
		Vertex:      []*ModelInfo{{ID: "vertex-test"}},
		GeminiCLI:   []*ModelInfo{{ID: "gemini-cli-test"}},
		AIStudio:    []*ModelInfo{{ID: "aistudio-test"}},
		CodexFree:   []*ModelInfo{{ID: "codex-free-test"}},
		CodexTeam:   []*ModelInfo{{ID: "codex-team-test"}},
		CodexPlus:   []*ModelInfo{{ID: "codex-plus-test"}},
		CodexPro:    []*ModelInfo{{ID: "codex-pro-test"}},
		Qwen:        nil,
		IFlow:       nil,
		Kimi:        []*ModelInfo{{ID: "kimi-test"}},
		Antigravity: []*ModelInfo{{ID: "antigravity-test"}},
	}

	if err := validateModelsCatalog(data); err != nil {
		t.Fatalf("validateModelsCatalog() error = %v, want nil", err)
	}
}
