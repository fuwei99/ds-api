package promptcompat

import (
	"strings"
)

var readToolAbnormalMarkers = []string{
	// Cache / unchanged markers
	"unchanged",
	"already available",
	"previous context",
	"no file body",
	"no content",
	"not modified",
	"cached",
	"no changes",
	// Error markers
	"error",
	"failed",
	"not found",
	"does not exist",
	"no such file",
	"permission denied",
	"denied",
}

// ReadToolResultNeedsCacheGuard inspects raw messages and reports whether the
// most recent tool/function result was from a read-like tool and returned an
// empty or abnormal result (e.g. unchanged, cache indicator, or error).
func ReadToolResultNeedsCacheGuard(messages []any) bool {
	if len(messages) == 0 {
		return false
	}

	callIDToName := make(map[string]string)
	recordCall := func(call map[string]any) {
		callID := strings.TrimSpace(asString(call["id"]))
		if callID == "" {
			callID = strings.TrimSpace(asString(call["call_id"]))
		}
		if callID == "" {
			callID = strings.TrimSpace(asString(call["tool_call_id"]))
		}
		name := strings.TrimSpace(asString(call["name"]))
		if fn, ok := call["function"].(map[string]any); ok && name == "" {
			name = strings.TrimSpace(asString(fn["name"]))
		}
		if callID != "" && name != "" {
			callIDToName[callID] = name
		}
	}

	for _, item := range messages {
		msg, ok := item.(map[string]any)
		if !ok {
			continue
		}

		itemType := strings.ToLower(strings.TrimSpace(asString(msg["type"])))
		if itemType == "function_call" || itemType == "tool_call" {
			recordCall(msg)
		}

		role := strings.ToLower(strings.TrimSpace(asString(msg["role"])))
		if role == "assistant" {
			switch calls := msg["tool_calls"].(type) {
			case []any:
				for _, c := range calls {
					if call, ok := c.(map[string]any); ok {
						recordCall(call)
					}
				}
			case []map[string]any:
				for _, call := range calls {
					recordCall(call)
				}
			}
			if fn, ok := msg["function_call"].(map[string]any); ok {
				callID := strings.TrimSpace(asString(fn["id"]))
				if callID == "" {
					callID = strings.TrimSpace(asString(msg["id"]))
				}
				name := strings.TrimSpace(asString(fn["name"]))
				if callID != "" && name != "" {
					callIDToName[callID] = name
				}
			}
		}
	}

	var lastToolMsg map[string]any
	for i := len(messages) - 1; i >= 0; i-- {
		msg, ok := messages[i].(map[string]any)
		if !ok {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(asString(msg["role"])))
		itemType := strings.ToLower(strings.TrimSpace(asString(msg["type"])))
		if role == "tool" || role == "function" || itemType == "function_call_output" || itemType == "tool_result" {
			lastToolMsg = msg
			break
		}
	}

	if lastToolMsg == nil {
		return false
	}

	toolName := strings.TrimSpace(asString(lastToolMsg["name"]))
	if toolName == "" {
		toolName = strings.TrimSpace(asString(lastToolMsg["tool_name"]))
	}
	if toolName == "" {
		callID := strings.TrimSpace(asString(lastToolMsg["tool_call_id"]))
		if callID == "" {
			callID = strings.TrimSpace(asString(lastToolMsg["call_id"]))
		}
		if callID != "" {
			toolName = strings.TrimSpace(callIDToName[callID])
		}
	}

	switch normalizeToolNameForGuard(toolName) {
	case "read", "readfile":
		// Match read-like tool
	default:
		return false
	}

	contentRaw := lastToolMsg["content"]
	if contentRaw == nil {
		contentRaw = lastToolMsg["output"]
	}
	contentStr := strings.TrimSpace(NormalizeOpenAIContentForPrompt(contentRaw))
	if contentStr == "" || contentStr == "{}" || strings.EqualFold(contentStr, "null") {
		return true
	}

	lower := strings.ToLower(contentStr)
	for _, marker := range readToolAbnormalMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}

	return false
}
