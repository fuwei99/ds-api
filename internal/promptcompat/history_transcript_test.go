package promptcompat

import (
	"strings"
	"testing"
)

func TestSplitTrailingUserTurnMovesLatestUserTurnOutOfHistory(t *testing.T) {
	messages := []any{
		map[string]any{"role": "system", "content": "system instructions"},
		map[string]any{"role": "user", "content": "first user turn"},
		map[string]any{"role": "assistant", "content": "assistant reply"},
		map[string]any{"role": "user", "content": "latest user turn"},
	}

	leading, text, ok := SplitTrailingUserTurn(messages)
	if !ok {
		t.Fatalf("expected trailing user turn to be split out")
	}
	if text != "latest user turn" {
		t.Fatalf("unexpected trailing user text: %q", text)
	}
	if len(leading) != 3 {
		t.Fatalf("expected the three preceding turns to remain, got %#v", leading)
	}
	transcript := buildOpenAIHistoryTranscript(leading, "")
	if !strings.Contains(transcript, "=== 3. ASSISTANT ===") {
		t.Fatalf("expected leading transcript to end at the assistant turn, got %q", transcript)
	}
	if strings.Contains(transcript, "latest user turn") {
		t.Fatalf("trailing user turn must not stay in the transcript, got %q", transcript)
	}
}

func TestSplitTrailingUserTurnDeclinesWhenLastTurnIsNotUser(t *testing.T) {
	for name, messages := range map[string][]any{
		"assistant": {
			map[string]any{"role": "user", "content": "first user turn"},
			map[string]any{"role": "assistant", "content": "assistant reply"},
		},
		"tool": {
			map[string]any{"role": "user", "content": "first user turn"},
			map[string]any{"role": "assistant", "content": "assistant reply"},
			map[string]any{"role": "tool", "name": "search", "content": "tool result"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, ok := SplitTrailingUserTurn(messages); ok {
				t.Fatalf("expected split to decline for a trailing %s turn", name)
			}
		})
	}
}

// A single-turn request must keep uploading the whole transcript: moving its only
// user turn inline would leave nothing to split out of the live prompt, defeating
// the purpose of current_input_file for long first-turn inputs.
func TestSplitTrailingUserTurnDeclinesWhenNoHistoryWouldRemain(t *testing.T) {
	if _, _, ok := SplitTrailingUserTurn([]any{
		map[string]any{"role": "user", "content": "only turn"},
	}); ok {
		t.Fatalf("expected split to decline for a single user turn")
	}
	// An empty assistant turn produces no transcript entry, so it does not count
	// as remaining history either.
	if _, _, ok := SplitTrailingUserTurn([]any{
		map[string]any{"role": "assistant", "content": ""},
		map[string]any{"role": "user", "content": "only turn"},
	}); ok {
		t.Fatalf("expected split to decline when the only leading turn is empty")
	}
}

// A leading system turn is real history, so the split proceeds and HISTORY.txt
// carries just the system instructions.
func TestSplitTrailingUserTurnKeepsSystemOnlyHistory(t *testing.T) {
	leading, text, ok := SplitTrailingUserTurn([]any{
		map[string]any{"role": "system", "content": "system instructions"},
		map[string]any{"role": "user", "content": "latest user turn"},
	})
	if !ok {
		t.Fatalf("expected split to proceed with a leading system turn")
	}
	if text != "latest user turn" {
		t.Fatalf("unexpected trailing user text: %q", text)
	}
	if len(leading) != 1 {
		t.Fatalf("expected only the system turn to remain, got %#v", leading)
	}
}

func TestSplitTrailingUserTurnDeclinesForEmptyTrailingUserText(t *testing.T) {
	if _, _, ok := SplitTrailingUserTurn([]any{
		map[string]any{"role": "user", "content": "first user turn"},
		map[string]any{"role": "assistant", "content": "assistant reply"},
		map[string]any{"role": "user", "content": "   "},
	}); ok {
		t.Fatalf("expected split to decline for a blank trailing user turn")
	}
}
