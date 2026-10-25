package claude

import (
	"strings"
	"testing"

	"ds2api/internal/config"
	"ds2api/internal/promptcompat"
)

// Claude 的 prompt 组装已并入 promptcompat：适配层只把 Anthropic 的 content
// blocks 摊成标准消息（content / reasoning_content / tool_calls），工具提示词、
// EPSE 格式规范、底部格式提示与最终渲染都由共享组装路径产出。
//
// 这条测试锁定「同一段历史只渲染一次」。适配层如果既把思考或工具调用写进 content、
// 又把它们留在 reasoning_content / tool_calls 字段里，共享渲染会各输出两遍——
// HISTORY.txt 转录路径尤其明显，因为它对每条消息都调用同一个渲染器。
//
// 计数只数本次历史独有的文本（思考原文、参数值），避免和格式规范里的示例撞车：
// 规范块用真实工具名生成示例，所以工具名不能用来计数。
func TestClaudePromptRendersHistoryExactlyOnce(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{}`)
	store := config.LoadStore()
	req := map[string]any{
		"model": "claude-sonnet-4-5",
		"messages": []any{
			map[string]any{"role": "user", "content": "search for the latest news"},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "thinking", "thinking": "need live search before answering"},
				map[string]any{"type": "text", "text": "searching now"},
				map[string]any{"type": "tool_use", "id": "call_1", "name": "search_web", "input": map[string]any{"query": "latest"}},
			}},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "call_1", "name": "search_web", "content": "headline: something happened"},
			}},
		},
		"tools": []any{
			map[string]any{
				"name":         "search_web",
				"description":  "Search the web",
				"input_schema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}},
			},
		},
	}

	norm, err := normalizeClaudeRequest(store, req)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}

	assertCount := func(where, text, needle string, want int) {
		t.Helper()
		if got := strings.Count(text, needle); got != want {
			t.Fatalf("%s: expected %q exactly %d time(s), got %d:\n%s", where, needle, want, got, text)
		}
	}

	prompt := norm.Standard.FinalPrompt
	assertCount("final prompt", prompt, "need live search before answering", 1)
	assertCount("final prompt", prompt, "<![CDATA[latest]]>", 1)
	assertCount("final prompt", prompt, "You have access to these tools:", 1)
	assertCount("final prompt", prompt, "headline: something happened", 1)

	// 共享链路对同一批消息另有一处渲染（HISTORY.txt 上下文）。它必须看到同样的
	// 单份历史，否则上传的转录会带着重复的思考与工具调用。
	transcript := promptcompat.BuildOpenAICurrentInputContextTranscript(norm.Standard.Messages, norm.Standard.ToolMarker)
	assertCount("history transcript", transcript, "need live search before answering", 1)
	assertCount("history transcript", transcript, "<![CDATA[latest]]>", 1)
	assertCount("history transcript", transcript, "headline: something happened", 1)
}

// 并入共享路径后新落到 Claude 上的共享行为之一（有意保留，不是回归）：内容为空的
// tool_result 现在渲染成 `[Tool]:null`，此前 Claude 自己的组装会把整条 tool 轮丢掉。
// 丢弃会让「工具已执行但没有正文」与「工具根本没执行」在 prompt 里无法区分。
func TestClaudeEmptyToolResultRendersAsToolNull(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{}`)
	store := config.LoadStore()
	req := map[string]any{
		"model": "claude-sonnet-4-5",
		"messages": []any{
			map[string]any{"role": "user", "content": "read the file"},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": "call_1", "name": "read_file", "input": map[string]any{"path": "a.go"}},
			}},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "call_1", "content": ""},
			}},
		},
	}

	norm, err := normalizeClaudeRequest(store, req)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if !containsStr(norm.Standard.FinalPrompt, "[Tool]:null") {
		t.Fatalf("expected an empty tool_result to render as [Tool]:null, got=%q", norm.Standard.FinalPrompt)
	}
}

// 并入共享路径后新落到 Claude 上的共享行为之二（有意保留）：read 类工具返回无正文
// 结果时，共享组装会追加读缓存护栏段落。此前 Claude 用自己的组装实现，没有这段护栏。
func TestClaudeReadToolCacheGuardIsInjected(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{}`)
	store := config.LoadStore()
	req := map[string]any{
		"model": "claude-sonnet-4-5",
		"messages": []any{
			map[string]any{"role": "user", "content": "read the file"},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": "call_1", "name": "read_file", "input": map[string]any{"path": "a.go"}},
			}},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "call_1", "content": "unchanged"},
			}},
		},
		"tools": []any{
			map[string]any{
				"name":         "read_file",
				"description":  "Read a file",
				"input_schema": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
			},
		},
	}

	norm, err := normalizeClaudeRequest(store, req)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if !containsStr(norm.Standard.FinalPrompt, "Read-tool cache guard") {
		t.Fatalf("expected the read-tool cache guard for a body-less read result, got=%q", norm.Standard.FinalPrompt)
	}
}
