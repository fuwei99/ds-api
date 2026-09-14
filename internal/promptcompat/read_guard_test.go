package promptcompat

import (
	"testing"
)

func TestReadToolResultNeedsCacheGuard(t *testing.T) {
	t.Run("empty messages", func(t *testing.T) {
		if ReadToolResultNeedsCacheGuard(nil) {
			t.Fatal("expected false for nil messages")
		}
		if ReadToolResultNeedsCacheGuard([]any{}) {
			t.Fatal("expected false for empty messages")
		}
	})

	t.Run("no tool messages", func(t *testing.T) {
		msgs := []any{
			map[string]any{"role": "user", "content": "read the file"},
			map[string]any{"role": "assistant", "content": "ok"},
		}
		if ReadToolResultNeedsCacheGuard(msgs) {
			t.Fatal("expected false when no tool messages exist")
		}
	})

	t.Run("non read tool", func(t *testing.T) {
		msgs := []any{
			map[string]any{"role": "user", "content": "run command"},
			map[string]any{
				"role": "assistant",
				"tool_calls": []any{
					map[string]any{
						"id":       "call-1",
						"function": map[string]any{"name": "bash"},
					},
				},
			},
			map[string]any{
				"role":         "tool",
				"name":         "bash",
				"tool_call_id": "call-1",
				"content":      "error: command failed",
			},
		}
		if ReadToolResultNeedsCacheGuard(msgs) {
			t.Fatal("expected false for non-read tool even with error")
		}
	})

	t.Run("read tool with normal content", func(t *testing.T) {
		msgs := []any{
			map[string]any{
				"role": "assistant",
				"tool_calls": []any{
					map[string]any{
						"id":       "call-1",
						"function": map[string]any{"name": "read_file"},
					},
				},
			},
			map[string]any{
				"role":         "tool",
				"tool_call_id": "call-1",
				"content":      "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"ok\")\n}",
			},
		}
		if ReadToolResultNeedsCacheGuard(msgs) {
			t.Fatal("expected false for normal file content")
		}
	})

	t.Run("empty and null and object variants", func(t *testing.T) {
		variants := []any{
			"",
			"   \n  \t ",
			"null",
			"NULL",
			"Null",
			"{}",
			"  {}  ",
			map[string]any{},
			nil,
		}
		for _, v := range variants {
			msgs := []any{
				map[string]any{
					"role": "assistant",
					"tool_calls": []any{
						map[string]any{
							"id":       "call-1",
							"function": map[string]any{"name": "read_file"},
						},
					},
				},
				map[string]any{
					"role":         "tool",
					"tool_call_id": "call-1",
					"content":      v,
				},
			}
			if !ReadToolResultNeedsCacheGuard(msgs) {
				t.Fatalf("expected true for empty/null variant %#v", v)
			}
		}
	})

	t.Run("cache markers", func(t *testing.T) {
		markers := []string{
			"unchanged",
			"UNCHANGED",
			"File is unchanged from previous turn",
			"already available",
			"Content is already available in conversation history",
			"previous context",
			"Refer to previous context for file content",
			"no file body",
			"Warning: no file body returned",
			"no content",
			"File has no content",
			"not modified",
			"Status: 304 Not Modified",
			"cached",
			"Result served from CACHED entry",
			"no changes",
			"There are NO CHANGES to this file",
		}
		for _, marker := range markers {
			msgs := []any{
				map[string]any{
					"role": "assistant",
					"tool_calls": []any{
						map[string]any{
							"id":       "call-1",
							"function": map[string]any{"name": "read"},
						},
					},
				},
				map[string]any{
					"role":         "tool",
					"tool_call_id": "call-1",
					"content":      marker,
				},
			}
			if !ReadToolResultNeedsCacheGuard(msgs) {
				t.Fatalf("expected true for cache marker %q", marker)
			}
		}
	})

	t.Run("error markers", func(t *testing.T) {
		markers := []string{
			"error",
			"Error opening file",
			"failed",
			"Read operation FAILED",
			"not found",
			"File not found: /path/to/file",
			"does not exist",
			"The specified path does not exist",
			"no such file",
			"open /foo/bar: no such file or directory",
			"permission denied",
			"Access denied: permission denied",
			"denied",
		}
		for _, marker := range markers {
			msgs := []any{
				map[string]any{
					"role": "assistant",
					"tool_calls": []any{
						map[string]any{
							"id":   "call-1",
							"name": "read_file",
						},
					},
				},
				map[string]any{
					"role":         "tool",
					"name":         "read_file",
					"tool_call_id": "call-1",
					"content":      marker,
				},
			}
			if !ReadToolResultNeedsCacheGuard(msgs) {
				t.Fatalf("expected true for error marker %q", marker)
			}
		}
	})

	t.Run("call_id to name backfill", func(t *testing.T) {
		msgs := []any{
			map[string]any{
				"role": "assistant",
				"tool_calls": []any{
					map[string]any{
						"id": "c1",
						"function": map[string]any{
							"name": "ReadFile",
						},
					},
				},
			},
			map[string]any{
				"role":         "tool",
				"tool_call_id": "c1",
				"content":      "unchanged",
			},
		}
		if !ReadToolResultNeedsCacheGuard(msgs) {
			t.Fatal("expected true when tool name is inferred via tool_call_id")
		}
	})

	t.Run("responses normalized tool call shape", func(t *testing.T) {
		msgs := []any{
			map[string]any{
				"type":    "tool_call",
				"call_id": "c2",
				"name":    "read_file",
			},
			map[string]any{
				"type":    "tool_result",
				"call_id": "c2",
				"output":  "file does not exist",
			},
		}
		if !ReadToolResultNeedsCacheGuard(msgs) {
			t.Fatal("expected true for responses normalized tool call shape")
		}
	})

	t.Run("legacy function_call shape", func(t *testing.T) {
		msgs := []any{
			map[string]any{
				"role": "assistant",
				"function_call": map[string]any{
					"name": "read_file",
				},
			},
			map[string]any{
				"role":    "function",
				"name":    "read_file",
				"content": "permission denied",
			},
		}
		if !ReadToolResultNeedsCacheGuard(msgs) {
			t.Fatal("expected true for legacy function_call shape")
		}
	})

	t.Run("last tool is not read even if earlier was abnormal read", func(t *testing.T) {
		msgs := []any{
			map[string]any{
				"role": "assistant",
				"tool_calls": []any{
					map[string]any{
						"id":       "call-read",
						"function": map[string]any{"name": "read_file"},
					},
				},
			},
			map[string]any{
				"role":         "tool",
				"tool_call_id": "call-read",
				"content":      "unchanged",
			},
			map[string]any{
				"role": "assistant",
				"tool_calls": []any{
					map[string]any{
						"id":       "call-bash",
						"function": map[string]any{"name": "bash"},
					},
				},
			},
			map[string]any{
				"role":         "tool",
				"tool_call_id": "call-bash",
				"content":      "hello world",
			},
		}
		if ReadToolResultNeedsCacheGuard(msgs) {
			t.Fatal("expected false when most recent tool result is bash")
		}
	})

	t.Run("last tool is read even if earlier tool was bash", func(t *testing.T) {
		msgs := []any{
			map[string]any{
				"role": "assistant",
				"tool_calls": []any{
					map[string]any{
						"id":       "call-bash",
						"function": map[string]any{"name": "bash"},
					},
				},
			},
			map[string]any{
				"role":         "tool",
				"tool_call_id": "call-bash",
				"content":      "done",
			},
			map[string]any{
				"role": "assistant",
				"tool_calls": []any{
					map[string]any{
						"id":       "call-read",
						"function": map[string]any{"name": "read_file"},
					},
				},
			},
			map[string]any{
				"role":         "tool",
				"tool_call_id": "call-read",
				"content":      "cached",
			},
		}
		if !ReadToolResultNeedsCacheGuard(msgs) {
			t.Fatal("expected true when most recent tool result is abnormal read")
		}
	})
}
