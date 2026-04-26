package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

type modelInfo struct {
	ID string `json:"id"`
}

type modelsCatalog struct {
	Claude      []modelInfo `json:"claude"`
	Gemini      []modelInfo `json:"gemini"`
	Vertex      []modelInfo `json:"vertex"`
	GeminiCLI   []modelInfo `json:"gemini-cli"`
	AIStudio    []modelInfo `json:"aistudio"`
	CodexFree   []modelInfo `json:"codex-free"`
	CodexTeam   []modelInfo `json:"codex-team"`
	CodexPlus   []modelInfo `json:"codex-plus"`
	CodexPro    []modelInfo `json:"codex-pro"`
	Qwen        []modelInfo `json:"qwen"`
	IFlow       []modelInfo `json:"iflow"`
	Kimi        []modelInfo `json:"kimi"`
	Antigravity []modelInfo `json:"antigravity"`
}

func main() {
	inputPath := flag.String("input", "", "Path to models catalog JSON")
	flag.Parse()

	if *inputPath == "" {
		exitf("missing -input")
	}

	data, err := os.ReadFile(*inputPath)
	if err != nil {
		exitf("read input: %v", err)
	}

	var catalog modelsCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		exitf("parse json: %v", err)
	}

	requiredSections := []struct {
		name   string
		models []modelInfo
	}{
		{name: "claude", models: catalog.Claude},
		{name: "gemini", models: catalog.Gemini},
		{name: "vertex", models: catalog.Vertex},
		{name: "gemini-cli", models: catalog.GeminiCLI},
		{name: "aistudio", models: catalog.AIStudio},
		{name: "codex-free", models: catalog.CodexFree},
		{name: "codex-team", models: catalog.CodexTeam},
		{name: "codex-plus", models: catalog.CodexPlus},
		{name: "codex-pro", models: catalog.CodexPro},
		{name: "kimi", models: catalog.Kimi},
		{name: "antigravity", models: catalog.Antigravity},
	}

	for _, section := range requiredSections {
		if len(section.models) == 0 {
			exitf("validate models catalog: %s section is empty", section.name)
		}
		for i, model := range section.models {
			if model.ID == "" {
				exitf("validate models catalog: %s[%d].id is empty", section.name, i)
			}
		}
	}
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
