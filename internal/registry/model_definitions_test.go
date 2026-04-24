package registry

import "testing"

func TestCodexModelsIncludeBuiltInGPTImage(t *testing.T) {
	tests := []struct {
		name   string
		models func() []*ModelInfo
	}{
		{name: "free", models: GetCodexFreeModels},
		{name: "team", models: GetCodexTeamModels},
		{name: "plus", models: GetCodexPlusModels},
		{name: "pro", models: GetCodexProModels},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var foundImage *ModelInfo
			var foundMain *ModelInfo
			for _, model := range tt.models() {
				if model == nil {
					continue
				}
				switch model.ID {
				case codexBuiltinImageModelID:
					foundImage = model
				case codexBuiltinImageMainModelID:
					foundMain = model
				}
			}
			if foundImage == nil {
				t.Fatalf("expected %s in codex %s models", codexBuiltinImageModelID, tt.name)
			}
			if foundMain == nil {
				t.Fatalf("expected %s in codex %s models", codexBuiltinImageMainModelID, tt.name)
			}
			if foundImage.Type != "openai" {
				t.Fatalf("type = %q, want %q", foundImage.Type, "openai")
			}
			if foundImage.DisplayName != "GPT Image 2" {
				t.Fatalf("display_name = %q, want %q", foundImage.DisplayName, "GPT Image 2")
			}
			if foundMain.DisplayName != "GPT 5.4 Mini" {
				t.Fatalf("display_name = %q, want %q", foundMain.DisplayName, "GPT 5.4 Mini")
			}
		})
	}
}

func TestLookupStaticModelInfoIncludesCodexBuiltIns(t *testing.T) {
	for _, id := range []string{codexBuiltinImageMainModelID, codexBuiltinImageModelID} {
		info := LookupStaticModelInfo(id)
		if info == nil {
			t.Fatalf("expected lookup for %s to succeed", id)
		}
		if info.ID != id {
			t.Fatalf("id = %q, want %q", info.ID, id)
		}
		if info.Type != "openai" {
			t.Fatalf("type = %q, want %q", info.Type, "openai")
		}
	}
}

func TestLookupStaticModelInfoIncludesSyncedCatalogEntries(t *testing.T) {
	for _, id := range []string{"claude-opus-4-7", "kimi-k2.6"} {
		info := LookupStaticModelInfo(id)
		if info == nil {
			t.Fatalf("expected lookup for %s to succeed", id)
		}
		if info.ID != id {
			t.Fatalf("id = %q, want %q", info.ID, id)
		}
	}
}
