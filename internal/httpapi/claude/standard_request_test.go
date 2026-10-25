package claude

import (
	"strings"
	"testing"

	"ds2api/internal/config"
)

func TestNormalizeClaudeRequest(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{}`)
	store := config.LoadStore()
	req := map[string]any{
		"model": "claude-opus-4-6",
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
		"stream": true,
		"tools": []any{
			map[string]any{"name": "search", "description": "Search"},
		},
	}
	norm, err := normalizeClaudeRequest(store, req)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if norm.Standard.ResolvedModel == "" {
		t.Fatalf("expected resolved model")
	}
	if !norm.Standard.Stream {
		t.Fatalf("expected stream=true")
	}
	if len(norm.Standard.ToolNames) == 0 {
		t.Fatalf("expected tool names")
	}
	if norm.Standard.ToolsRaw == nil {
		t.Fatalf("expected ToolsRaw preserved for downstream normalization")
	}
	if norm.Standard.FinalPrompt == "" {
		t.Fatalf("expected non-empty final prompt")
	}
}

func TestNormalizeClaudeRequestSupportsCamelCaseInputSchemaPromptInjection(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{}`)
	store := config.LoadStore()
	req := map[string]any{
		"model": "claude-sonnet-4-5",
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
		"tools": []any{
			map[string]any{
				"name":        "todowrite",
				"description": "Write todos",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"todos": map[string]any{"type": "array"}}},
			},
		},
	}
	norm, err := normalizeClaudeRequest(store, req)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if !containsStr(norm.Standard.FinalPrompt, `"type":"array"`) {
		t.Fatalf("expected inputSchema to be injected into prompt, got=%q", norm.Standard.FinalPrompt)
	}
}

func TestNormalizeClaudeRequestInjectsToolsIntoExistingSystemMessage(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{}`)
	store := config.LoadStore()
	req := map[string]any{
		"model": "claude-sonnet-4-5",
		"messages": []any{
			map[string]any{"role": "system", "content": "baseline rule"},
			map[string]any{"role": "user", "content": "hello"},
		},
		"tools": []any{
			map[string]any{"name": "search", "description": "Search"},
		},
	}

	norm, err := normalizeClaudeRequest(store, req)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}

	if !containsStr(norm.Standard.FinalPrompt, "You have access to these tools") {
		t.Fatalf("expected tool prompt injected into final prompt, got=%q", norm.Standard.FinalPrompt)
	}
	if !containsStr(norm.Standard.FinalPrompt, "baseline rule") {
		t.Fatalf("expected existing system message preserved, got=%q", norm.Standard.FinalPrompt)
	}
}

func TestNormalizeClaudeRequestInjectsToolsIntoTopLevelSystem(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{}`)
	store := config.LoadStore()
	req := map[string]any{
		"model":  "claude-sonnet-4-5",
		"system": "top-level system",
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
		"tools": []any{
			map[string]any{"name": "search", "description": "Search"},
		},
	}

	norm, err := normalizeClaudeRequest(store, req)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}

	if !containsStr(norm.Standard.FinalPrompt, "top-level system") {
		t.Fatalf("expected top-level system preserved, got=%q", norm.Standard.FinalPrompt)
	}
	if !containsStr(norm.Standard.FinalPrompt, "You have access to these tools") {
		t.Fatalf("expected tool prompt injected, got=%q", norm.Standard.FinalPrompt)
	}
}

const claudeReminderHead = "The only correct format for using tools is <|EPSE|tool_calls>"
const claudeReminderTail = ".Please do not simplify or modify this framework in any way.The only valid label is EPSE; the use of any label prefixed with DSML is **strictly prohibited**."

func TestNormalizeClaudeRequestAppendsToolCallReminderBeforeAssistantMarker(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{}`)
	store := config.LoadStore()
	for name, req := range map[string]map[string]any{
		"top_level_system": {
			"model":  "claude-sonnet-4-5",
			"system": "top-level system",
			"messages": []any{
				map[string]any{"role": "user", "content": "hello"},
			},
			"tools": []any{map[string]any{"name": "search", "description": "Search"}},
		},
		"system_message": {
			"model": "claude-sonnet-4-5",
			"messages": []any{
				map[string]any{"role": "system", "content": "baseline rule"},
				map[string]any{"role": "user", "content": "hello"},
			},
			"tools": []any{map[string]any{"name": "search", "description": "Search"}},
		},
		"no_system": {
			"model": "claude-sonnet-4-5",
			"messages": []any{
				map[string]any{"role": "user", "content": "hello"},
			},
			"tools": []any{map[string]any{"name": "search", "description": "Search"}},
		},
	} {
		norm, err := normalizeClaudeRequest(store, req)
		if err != nil {
			t.Fatalf("%s: normalize failed: %v", name, err)
		}
		prompt := norm.Standard.FinalPrompt
		if !containsStr(prompt, claudeReminderHead) {
			t.Fatalf("%s: expected trailing tool-call reminder, got=%q", name, prompt)
		}
		if !strings.HasSuffix(prompt, claudeReminderTail+"[Assistant]:") {
			t.Fatalf("%s: reminder must be the last block before [Assistant]:, got=%q", name, prompt)
		}
		if strings.Count(prompt, claudeReminderHead) != 1 {
			t.Fatalf("%s: expected exactly one reminder block, got=%q", name, prompt)
		}
	}
}

func TestNormalizeClaudeRequestWithoutToolsOmitsToolCallReminder(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{}`)
	store := config.LoadStore()
	req := map[string]any{
		"model": "claude-sonnet-4-5",
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
	}

	norm, err := normalizeClaudeRequest(store, req)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if containsStr(norm.Standard.FinalPrompt, claudeReminderHead) {
		t.Fatalf("no-tools request must not carry the reminder, got=%q", norm.Standard.FinalPrompt)
	}
}
