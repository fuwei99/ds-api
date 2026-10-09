package promptcompat

import (
	"fmt"
	"strings"
)

const CurrentInputContextFilename = "HISTORY.txt"

const historyTranscriptTitle = "# HISTORY.txt"
const historyTranscriptSummary = "Prior conversation history and tool progress."

// BuildOpenAIHistoryTranscript renders the prompt-visible transcript (with the
// HISTORY.txt header) for the given messages. The optional toolMarker is the
// caller-specific tool-call marker: assistant history that carries the marker
// form is normalized back to EPSE before the tool-markup probe runs, so the
// entry renders as the canonical shell either way.
func BuildOpenAIHistoryTranscript(messages []any, toolMarker ...string) string {
	return buildOpenAIHistoryTranscript(messages, firstToolMarker(toolMarker))
}

func BuildOpenAICurrentUserInputTranscript(text string, toolMarker ...string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return buildOpenAIHistoryTranscript([]any{
		map[string]any{"role": "user", "content": text},
	}, firstToolMarker(toolMarker))
}

func BuildOpenAICurrentInputContextTranscript(messages []any, toolMarker ...string) string {
	return buildOpenAIHistoryTranscript(messages, firstToolMarker(toolMarker))
}

// SplitTrailingUserTurn splits messages into the leading history and the text of
// the trailing user turn, so callers can upload the history as context while
// keeping the newest user request inline in the live prompt.
//
// It reports ok=false when the last transcript-visible entry is not a user turn,
// or when removing that turn would leave no history to upload at all (a
// single-turn request). In the latter case the caller must keep uploading the
// full transcript, otherwise a long single-turn input would stop being split out
// of the live prompt.
func SplitTrailingUserTurn(messages []any) ([]any, string, bool) {
	last := -1
	for i := len(messages) - 1; i >= 0; i-- {
		msg, isMap := messages[i].(map[string]any)
		if !isMap {
			continue
		}
		role := normalizeOpenAIRoleForPrompt(strings.ToLower(strings.TrimSpace(asString(msg["role"]))))
		if strings.TrimSpace(buildOpenAIHistoryEntry(role, msg, "")) == "" {
			continue
		}
		if role != "user" {
			return nil, "", false
		}
		last = i
		break
	}
	if last < 0 {
		return nil, "", false
	}
	msg, _ := messages[last].(map[string]any)
	text := strings.TrimSpace(NormalizeOpenAIContentForPrompt(msg["content"]))
	if text == "" {
		return nil, "", false
	}
	history := messages[:last]
	// buildOpenAIHistoryTranscript still emits its title/summary header for a
	// slice whose entries all render empty, so count real entries instead of
	// testing the rendered string.
	if countHistoryTranscriptEntries(history) == 0 {
		return nil, "", false
	}
	return history, text, true
}

func countHistoryTranscriptEntries(messages []any) int {
	count := 0
	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role := normalizeOpenAIRoleForPrompt(strings.ToLower(strings.TrimSpace(asString(msg["role"]))))
		if strings.TrimSpace(buildOpenAIHistoryEntry(role, msg, "")) == "" {
			continue
		}
		count++
	}
	return count
}

func buildOpenAIHistoryTranscript(messages []any, marker string) string {
	if len(messages) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(historyTranscriptTitle)
	b.WriteString("\n")
	b.WriteString(historyTranscriptSummary)
	b.WriteString("\n\n")

	entry := 0
	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role := normalizeOpenAIRoleForPrompt(strings.ToLower(strings.TrimSpace(asString(msg["role"]))))
		content := strings.TrimSpace(buildOpenAIHistoryEntry(role, msg, marker))
		if content == "" {
			continue
		}
		entry++
		fmt.Fprintf(&b, "=== %d. %s ===\n%s\n\n", entry, strings.ToUpper(roleLabelForHistory(role)), content)
	}

	transcript := strings.TrimSpace(b.String())
	if transcript == "" {
		return ""
	}
	return transcript + "\n"
}

func buildOpenAIHistoryEntry(role string, msg map[string]any, marker string) string {
	switch role {
	case "assistant":
		return strings.TrimSpace(buildAssistantContentForPrompt(msg, marker))
	case "tool", "function":
		return strings.TrimSpace(buildToolHistoryContent(msg))
	case "system", "user":
		return strings.TrimSpace(NormalizeOpenAIContentForPrompt(msg["content"]))
	default:
		return strings.TrimSpace(NormalizeOpenAIContentForPrompt(msg["content"]))
	}
}

func buildToolHistoryContent(msg map[string]any) string {
	content := strings.TrimSpace(NormalizeOpenAIContentForPrompt(msg["content"]))
	parts := make([]string, 0, 2)
	if name := strings.TrimSpace(asString(msg["name"])); name != "" {
		parts = append(parts, "name="+name)
	}
	if callID := strings.TrimSpace(asString(msg["tool_call_id"])); callID != "" {
		parts = append(parts, "tool_call_id="+callID)
	}
	header := ""
	if len(parts) > 0 {
		header = "[" + strings.Join(parts, " ") + "]"
	}
	switch {
	case header != "" && content != "":
		return header + "\n" + content
	case header != "":
		return header
	default:
		return content
	}
}

func roleLabelForHistory(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case "function":
		return "tool"
	case "":
		return "unknown"
	default:
		return role
	}
}
