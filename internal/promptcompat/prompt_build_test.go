package promptcompat

import (
	"strings"
	"testing"
)

func TestBuildOpenAIFinalPrompt_HandlerPathIncludesToolRoundtripSemantics(t *testing.T) {
	messages := []any{
		map[string]any{"role": "user", "content": "查北京天气"},
		map[string]any{
			"role": "assistant",
			"tool_calls": []any{
				map[string]any{
					"id": "call_1",
					"function": map[string]any{
						"name":      "get_weather",
						"arguments": "{\"city\":\"beijing\"}",
					},
				},
			},
		},
		map[string]any{
			"role":         "tool",
			"tool_call_id": "call_1",
			"name":         "get_weather",
			"content":      map[string]any{"temp": 18, "condition": "sunny"},
		},
	}
	tools := []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "get_weather",
				"description": "Get weather",
				"parameters": map[string]any{
					"type": "object",
				},
			},
		},
	}

	finalPrompt, toolNames := buildOpenAIFinalPrompt(messages, tools, "", false, DefaultToolReminderConfig())
	if len(toolNames) != 1 || toolNames[0] != "get_weather" {
		t.Fatalf("unexpected tool names: %#v", toolNames)
	}
	if !strings.Contains(finalPrompt, `"condition":"sunny"`) {
		t.Fatalf("handler finalPrompt should preserve tool output content: %q", finalPrompt)
	}
	if !strings.Contains(finalPrompt, "<|EPSE|tool_calls>") {
		t.Fatalf("handler finalPrompt should preserve assistant tool history: %q", finalPrompt)
	}
	if !strings.Contains(finalPrompt, `<|EPSE|invoke name="get_weather">`) {
		t.Fatalf("handler finalPrompt should include tool name history: %q", finalPrompt)
	}
}

func TestBuildOpenAIFinalPrompt_VercelPreparePathKeepsFinalAnswerInstruction(t *testing.T) {
	messages := []any{
		map[string]any{"role": "system", "content": "You are helpful"},
		map[string]any{"role": "user", "content": "请调用工具"},
	}
	tools := []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "search",
				"description": "search docs",
				"parameters": map[string]any{
					"type": "object",
				},
			},
		},
	}

	finalPrompt, _ := buildOpenAIFinalPrompt(messages, tools, "", false, DefaultToolReminderConfig())
	if !strings.Contains(finalPrompt, "请记住：使用工具的唯一正确方式是在回复末尾使用 <|EPSE|tool_calls>...</|EPSE|tool_calls> 代码块。") {
		t.Fatalf("vercel prepare finalPrompt missing final tool-call anchor instruction: %q", finalPrompt)
	}
	if !strings.Contains(finalPrompt, "工具调用格式规范") {
		t.Fatalf("vercel prepare finalPrompt missing xml format instruction: %q", finalPrompt)
	}
	if !strings.Contains(finalPrompt, "请勿使用 Markdown 代码块标记包裹 XML") {
		t.Fatalf("vercel prepare finalPrompt missing no-fence xml instruction: %q", finalPrompt)
	}
	if strings.Contains(finalPrompt, "```json") {
		t.Fatalf("vercel prepare finalPrompt should not require fenced tool calls: %q", finalPrompt)
	}
}

func TestBuildOpenAIPromptWithToolInstructionsOnlyOmitsSchemas(t *testing.T) {
	messages := []any{
		map[string]any{"role": "system", "content": "You are helpful"},
		map[string]any{"role": "user", "content": "请调用工具"},
	}
	tools := []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "search",
				"description": "search docs",
				"parameters": map[string]any{
					"type": "object",
				},
			},
		},
	}

	finalPrompt, toolNames := BuildOpenAIPromptWithToolInstructionsOnly(messages, tools, "", DefaultToolChoicePolicy(), false, DefaultToolReminderConfig())
	if len(toolNames) != 1 || toolNames[0] != "search" {
		t.Fatalf("unexpected tool names: %#v", toolNames)
	}
	if strings.Contains(finalPrompt, "You have access to these tools") || strings.Contains(finalPrompt, "Description: search docs") || strings.Contains(finalPrompt, "Parameters:") || strings.Contains(finalPrompt, "TOOLS.txt") {
		t.Fatalf("tool descriptions and file references should be externalized, got: %q", finalPrompt)
	}
	if !strings.Contains(finalPrompt, "工具调用格式规范") || !strings.Contains(finalPrompt, "请记住：使用工具的唯一正确方式") {
		t.Fatalf("expected tool format instructions to remain in live prompt, got: %q", finalPrompt)
	}
}

func TestBuildOpenAIToolsContextTranscriptContainsOnlyDescriptions(t *testing.T) {
	tools := []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "search",
				"description": "search docs",
				"parameters": map[string]any{
					"type": "object",
				},
			},
		},
	}

	transcript, toolNames := BuildOpenAIToolsContextTranscript(tools, DefaultToolChoicePolicy())
	if len(toolNames) != 1 || toolNames[0] != "search" {
		t.Fatalf("unexpected tool names: %#v", toolNames)
	}
	for _, want := range []string{"# TOOLS.txt", "You have access to these tools", "Tool: search", "Description: search docs", `Parameters: {"type":"object"}`} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("expected tools transcript to contain %q, got: %q", want, transcript)
		}
	}
	if strings.Contains(transcript, "工具调用格式规范") || strings.Contains(transcript, "<|EPSE|tool_calls>") {
		t.Fatalf("tools transcript should not duplicate format instructions, got: %q", transcript)
	}
}

func TestBuildOpenAIFinalPromptIncludesToolInstructions(t *testing.T) {
	messages := []any{
		map[string]any{"role": "system", "content": "You are helpful"},
		map[string]any{"role": "user", "content": "请调用工具"},
	}
	tools := []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "search",
				"description": "search docs",
				"parameters": map[string]any{
					"type": "object",
				},
			},
		},
	}

	finalPrompt, _ := buildOpenAIFinalPrompt(messages, tools, "", false, DefaultToolReminderConfig())
	toolIdx := strings.Index(finalPrompt, "工具调用格式规范")
	if toolIdx < 0 {
		t.Fatalf("expected tool instructions in final prompt, got: %q", finalPrompt)
	}
}

func TestBuildOpenAIFinalPromptReadLikeToolIncludesCacheGuard(t *testing.T) {
	messages := []any{
		map[string]any{"role": "user", "content": "请读取文件"},
		map[string]any{
			"role": "assistant",
			"tool_calls": []any{
				map[string]any{
					"id": "call-1",
					"function": map[string]any{
						"name": "read_file",
					},
				},
			},
		},
		map[string]any{
			"role":         "tool",
			"tool_call_id": "call-1",
			"content":      "unchanged",
		},
	}
	tools := []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "read_file",
				"description": "Read a file",
				"parameters": map[string]any{
					"type": "object",
				},
			},
		},
	}

	finalPrompt, _ := buildOpenAIFinalPrompt(messages, tools, "", false, DefaultToolReminderConfig())
	if !strings.Contains(finalPrompt, "Read-tool cache guard") {
		t.Fatalf("read-like tool prompt missing cache guard: %q", finalPrompt)
	}
	if !strings.Contains(finalPrompt, "provides no file body") {
		t.Fatalf("read-like tool prompt missing no-body handling: %q", finalPrompt)
	}
	if !strings.Contains(finalPrompt, "Do not repeatedly call the same read request") {
		t.Fatalf("read-like tool prompt missing loop guard: %q", finalPrompt)
	}
}

func TestBuildOpenAIFinalPromptReadLikeToolWithNormalContentOmitsCacheGuard(t *testing.T) {
	messages := []any{
		map[string]any{"role": "user", "content": "请读取文件"},
		map[string]any{
			"role": "assistant",
			"tool_calls": []any{
				map[string]any{
					"id": "call-1",
					"function": map[string]any{
						"name": "read_file",
					},
				},
			},
		},
		map[string]any{
			"role":         "tool",
			"tool_call_id": "call-1",
			"content":      "package main\n\nfunc main() {\n\tprintln(\"hello world\")\n}",
		},
	}
	tools := []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "read_file",
				"description": "Read a file",
				"parameters": map[string]any{
					"type": "object",
				},
			},
		},
	}

	finalPrompt, _ := buildOpenAIFinalPrompt(messages, tools, "", false, DefaultToolReminderConfig())
	if strings.Contains(finalPrompt, "Read-tool cache guard") {
		t.Fatalf("successful read tool content should omit cache guard: %q", finalPrompt)
	}
}

func TestBuildOpenAIFinalPromptReadLikeToolWithoutToolHistoryOmitsCacheGuard(t *testing.T) {
	messages := []any{
		map[string]any{"role": "user", "content": "请读取文件"},
	}
	tools := []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "read_file",
				"description": "Read a file",
				"parameters": map[string]any{
					"type": "object",
				},
			},
		},
	}

	finalPrompt, _ := buildOpenAIFinalPrompt(messages, tools, "", false, DefaultToolReminderConfig())
	if strings.Contains(finalPrompt, "Read-tool cache guard") {
		t.Fatalf("read-like tool with no tool history should omit cache guard: %q", finalPrompt)
	}
}

func TestBuildOpenAIFinalPromptReadLikeToolWithErrorOrEmptyContentIncludesCacheGuard(t *testing.T) {
	for _, content := range []string{"", "   ", "error: file not found", "null", "{}"} {
		messages := []any{
			map[string]any{"role": "user", "content": "请读取文件"},
			map[string]any{
				"role": "assistant",
				"tool_calls": []any{
					map[string]any{
						"id": "call-1",
						"function": map[string]any{
							"name": "read_file",
						},
					},
				},
			},
			map[string]any{
				"role":         "tool",
				"tool_call_id": "call-1",
				"content":      content,
			},
		}
		tools := []any{
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        "read_file",
					"description": "Read a file",
					"parameters": map[string]any{
						"type": "object",
					},
				},
			},
		}

		finalPrompt, _ := buildOpenAIFinalPrompt(messages, tools, "", false, DefaultToolReminderConfig())
		if !strings.Contains(finalPrompt, "Read-tool cache guard") {
			t.Fatalf("read-like tool with content %q missing cache guard: %q", content, finalPrompt)
		}
	}
}

func TestBuildOpenAIFinalPromptNonReadToolOmitsCacheGuard(t *testing.T) {
	messages := []any{
		map[string]any{"role": "user", "content": "搜索一下"},
	}
	tools := []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "search",
				"description": "Search docs",
				"parameters": map[string]any{
					"type": "object",
				},
			},
		},
	}

	finalPrompt, _ := buildOpenAIFinalPrompt(messages, tools, "", false, DefaultToolReminderConfig())
	if strings.Contains(finalPrompt, "Read-tool cache guard") {
		t.Fatalf("non-read tool prompt should not include read cache guard: %q", finalPrompt)
	}
}

func TestBuildOpenAIFinalPromptWithThinkingKeepsPromptUnchanged(t *testing.T) {
	messages := []any{
		map[string]any{"role": "user", "content": "继续回答上一个问题"},
	}

	finalPromptThinking, _ := buildOpenAIFinalPrompt(messages, nil, "", true, DefaultToolReminderConfig())
	finalPromptPlain, _ := buildOpenAIFinalPrompt(messages, nil, "", false, DefaultToolReminderConfig())
	if finalPromptThinking != finalPromptPlain {
		t.Fatalf("expected thinking flag not to prepend continuation contract, thinking=%q plain=%q", finalPromptThinking, finalPromptPlain)
	}
}

func TestBuildOpenAIFinalPromptSystemDeclaredToolsInjectsFormatSpec(t *testing.T) {
	messages := []any{
		map[string]any{"role": "system", "content": "Available tools: workspace_read_file, workspace_write_file"},
		map[string]any{"role": "user", "content": "read the file"},
	}

	// No top-level tools array: tools are declared inside the system message.
	finalPrompt, toolNames := buildOpenAIFinalPrompt(messages, nil, "", false, DefaultToolReminderConfig())
	if !strings.Contains(finalPrompt, "工具调用格式规范") {
		t.Fatalf("expected format spec injected for system-declared tools, got: %q", finalPrompt)
	}
	if !strings.Contains(finalPrompt, "<|EPSE|tool_calls>") {
		t.Fatalf("expected EPSE tool-call format in prompt, got: %q", finalPrompt)
	}
	if !strings.Contains(finalPrompt, "请记住：使用工具的唯一正确方式") {
		t.Fatalf("expected format anchor instruction, got: %q", finalPrompt)
	}
	// No schemas can be extracted from free-form system text; the descriptions
	// block must not appear and no tool names are returned.
	if strings.Contains(finalPrompt, "You have access to these tools") {
		t.Fatalf("system-declared tools must not inject a synthetic descriptions block, got: %q", finalPrompt)
	}
	if len(toolNames) != 0 {
		t.Fatalf("expected no extracted tool names for system-declared tools, got: %#v", toolNames)
	}
}

func TestBuildOpenAIFinalPromptSystemDeclaredToolsSkipsWhenNoKeyword(t *testing.T) {
	messages := []any{
		map[string]any{"role": "system", "content": "You are a helpful assistant."},
		map[string]any{"role": "user", "content": "hello"},
	}

	finalPrompt, toolNames := buildOpenAIFinalPrompt(messages, nil, "", false, DefaultToolReminderConfig())
	if strings.Contains(finalPrompt, "工具调用格式规范") || strings.Contains(finalPrompt, "<|EPSE|tool_calls>") {
		t.Fatalf("did not expect tool format spec for a request without tools, got: %q", finalPrompt)
	}
	if len(toolNames) != 0 {
		t.Fatalf("expected no tool names, got: %#v", toolNames)
	}
}

func TestBuildOpenAIFinalPromptTopLevelToolsStillGetDescriptions(t *testing.T) {
	// Guard the existing behavior: a body tools array injects descriptions +
	// instructions and is unaffected by the new system-text path.
	messages := []any{
		map[string]any{"role": "system", "content": "You may use tools."},
		map[string]any{"role": "user", "content": "请调用工具"},
	}
	tools := []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "search",
				"description": "search docs",
				"parameters":  map[string]any{"type": "object"},
			},
		},
	}

	finalPrompt, toolNames := buildOpenAIFinalPrompt(messages, tools, "", false, DefaultToolReminderConfig())
	if len(toolNames) != 1 || toolNames[0] != "search" {
		t.Fatalf("unexpected tool names: %#v", toolNames)
	}
	if !strings.Contains(finalPrompt, "You have access to these tools") || !strings.Contains(finalPrompt, "工具调用格式规范") {
		t.Fatalf("top-level tools should inject descriptions and instructions, got: %q", finalPrompt)
	}
}

const toolCallReminderHead = "The only correct format for using tools is <|EPSE|tool_calls>"
const toolCallReminderTail = ".Please do not simplify or modify this framework in any way.The only valid label is EPSE; the use of any label prefixed with DSML is **strictly prohibited**."

func assertReminderImmediatelyBeforeAssistantMarker(t *testing.T, finalPrompt string) {
	t.Helper()
	idx := strings.Index(finalPrompt, toolCallReminderHead)
	if idx < 0 {
		t.Fatalf("expected trailing tool-call format reminder, got: %q", finalPrompt)
	}
	if strings.Count(finalPrompt, toolCallReminderHead) != 1 {
		t.Fatalf("expected exactly one reminder block, got: %q", finalPrompt)
	}
	if !strings.HasSuffix(finalPrompt, toolCallReminderTail+"[Assistant]:") {
		t.Fatalf("reminder must be the last block before [Assistant]:, got: %q", finalPrompt)
	}
	specIdx := strings.Index(finalPrompt, "工具调用格式规范")
	if specIdx < 0 || specIdx > idx {
		t.Fatalf("expected format spec to precede the reminder, got: %q", finalPrompt)
	}
	// The reminder is injected as a system turn; adjacent system turns are merged
	// by prompt rendering, so assert only that the enclosing role block is System.
	if role := enclosingRoleMarker(finalPrompt, idx); role != "[System]:" {
		t.Fatalf("reminder must be rendered inside a system turn, got role %q in: %q", role, finalPrompt)
	}
}

func enclosingRoleMarker(finalPrompt string, idx int) string {
	head := finalPrompt[:idx]
	best := ""
	bestAt := -1
	for _, marker := range []string{"[System]:", "[User]:", "[Assistant]:", "[Tool]:"} {
		if at := strings.LastIndex(head, marker); at > bestAt {
			best, bestAt = marker, at
		}
	}
	return best
}

func TestBuildOpenAIFinalPromptAppendsReminderBeforeAssistantMarker(t *testing.T) {
	messages := []any{
		map[string]any{"role": "system", "content": "You are helpful"},
		map[string]any{"role": "user", "content": "请调用工具"},
	}
	tools := []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "search",
				"description": "search docs",
				"parameters":  map[string]any{"type": "object"},
			},
		},
	}

	finalPrompt, _ := buildOpenAIFinalPrompt(messages, tools, "", false, DefaultToolReminderConfig())
	assertReminderImmediatelyBeforeAssistantMarker(t, finalPrompt)
}

func TestBuildOpenAIFinalPromptAppendsReminderWithoutSystemMessage(t *testing.T) {
	messages := []any{
		map[string]any{"role": "user", "content": "请调用工具"},
	}
	tools := []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "search",
				"description": "search docs",
				"parameters":  map[string]any{"type": "object"},
			},
		},
	}

	finalPrompt, _ := buildOpenAIFinalPrompt(messages, tools, "", false, DefaultToolReminderConfig())
	assertReminderImmediatelyBeforeAssistantMarker(t, finalPrompt)
}

func TestBuildOpenAIPromptWithToolInstructionsOnlyAppendsReminder(t *testing.T) {
	messages := []any{
		map[string]any{"role": "system", "content": "继续会话"},
	}
	tools := []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "search",
				"description": "search docs",
				"parameters":  map[string]any{"type": "object"},
			},
		},
	}

	finalPrompt, _ := BuildOpenAIPromptWithToolInstructionsOnly(messages, tools, "", DefaultToolChoicePolicy(), false, DefaultToolReminderConfig())
	assertReminderImmediatelyBeforeAssistantMarker(t, finalPrompt)
}

func TestBuildOpenAIPromptWithForcedFormatSpecAppendsReminder(t *testing.T) {
	messages := []any{
		map[string]any{"role": "system", "content": "继续会话 使用工具时请参照说明与格式要求，仅使用所列出的工具"},
		map[string]any{"role": "user", "content": "read the file"},
	}

	finalPrompt, _ := BuildOpenAIPromptWithForcedFormatSpec(messages, "", DefaultToolChoicePolicy(), false, DefaultToolReminderConfig())
	assertReminderImmediatelyBeforeAssistantMarker(t, finalPrompt)
}

func TestBuildOpenAIFinalPromptSystemDeclaredToolsAppendsReminder(t *testing.T) {
	messages := []any{
		map[string]any{"role": "system", "content": "Available tools: workspace_read_file"},
		map[string]any{"role": "user", "content": "read the file"},
	}

	finalPrompt, _ := buildOpenAIFinalPrompt(messages, nil, "", false, DefaultToolReminderConfig())
	assertReminderImmediatelyBeforeAssistantMarker(t, finalPrompt)
}

func TestBuildOpenAIFinalPromptWithoutToolsOmitsReminder(t *testing.T) {
	messages := []any{
		map[string]any{"role": "system", "content": "You are a helpful assistant."},
		map[string]any{"role": "user", "content": "hello"},
	}

	finalPrompt, _ := buildOpenAIFinalPrompt(messages, nil, "", false, DefaultToolReminderConfig())
	if strings.Contains(finalPrompt, toolCallReminderHead) {
		t.Fatalf("no-tools request must not carry the tool-call reminder, got: %q", finalPrompt)
	}
}

func TestBuildOpenAIFinalPromptToolChoiceNoneOmitsReminder(t *testing.T) {
	messages := []any{
		map[string]any{"role": "user", "content": "请调用工具"},
	}
	tools := []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "search",
				"description": "search docs",
				"parameters":  map[string]any{"type": "object"},
			},
		},
	}

	finalPrompt, _ := BuildOpenAIPrompt(messages, tools, "", ToolChoicePolicy{Mode: ToolChoiceNone}, false, DefaultToolReminderConfig())
	if strings.Contains(finalPrompt, toolCallReminderHead) {
		t.Fatalf("tool_choice=none must not carry the tool-call reminder, got: %q", finalPrompt)
	}
}

func TestBuildOpenAIFinalPromptTrailingAssistantKeepsReminderBeforeIt(t *testing.T) {
	messages := []any{
		map[string]any{"role": "user", "content": "请调用工具"},
		map[string]any{"role": "assistant", "content": "let me think"},
	}
	tools := []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "search",
				"description": "search docs",
				"parameters":  map[string]any{"type": "object"},
			},
		},
	}

	finalPrompt, _ := buildOpenAIFinalPrompt(messages, tools, "", false, DefaultToolReminderConfig())
	reminderIdx := strings.Index(finalPrompt, toolCallReminderHead)
	assistantIdx := strings.Index(finalPrompt, "[Assistant]:let me think")
	if reminderIdx < 0 || assistantIdx < 0 {
		t.Fatalf("expected reminder and trailing assistant turn, got: %q", finalPrompt)
	}
	if reminderIdx > assistantIdx {
		t.Fatalf("reminder must be inserted before the trailing assistant turn, got: %q", finalPrompt)
	}
}
