package openai

import (
	"context"
	"strings"
	"testing"

	"ds2api/internal/auth"
	"ds2api/internal/promptcompat"
	"ds2api/internal/toolcall"
)

const markerUnderTest = "Q7ZK3M"

func markerTestTools() []any {
	return []any{
		map[string]any{"type": "function", "function": map[string]any{
			"name":        "read_file",
			"description": "Read a file",
			"parameters":  map[string]any{"type": "object"},
		}},
	}
}

// TestThinkingInjectionRebuildKeepsToolMarker covers the P0 rebuild path:
// ApplyThinkingInjection rebuilds FinalPrompt from the messages, which re-renders
// the tool-call format spec from the canonical EPSE keyword, so the
// caller-specific marker has to be re-applied or the upstream would suddenly see
// the shared keyword again.
func TestThinkingInjectionRebuildKeepsToolMarker(t *testing.T) {
	h := &openAITestSurface{
		Store: mockOpenAIConfig{thinkingInjection: boolPtr(true)},
		DS:    &inlineUploadDSStub{},
	}
	req := map[string]any{
		"model":    "deepseek-v4.1-flash",
		"messages": []any{map[string]any{"role": "user", "content": "hello"}},
		"tools":    markerTestTools(),
	}
	stdReq, err := promptcompat.NormalizeOpenAIChatRequest(h.Store, req, "", markerUnderTest)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if strings.Contains(stdReq.FinalPrompt, toolcall.EPSEKeyword) {
		t.Fatalf("precondition: the normalized prompt must already use the marker: %q", stdReq.FinalPrompt)
	}

	out, err := h.applyCurrentInputFile(context.Background(), &auth.RequestAuth{DeepSeekToken: "token"}, stdReq)
	if err != nil {
		t.Fatalf("apply thinking injection failed: %v", err)
	}
	if !strings.Contains(out.FinalPrompt, promptcompat.ThinkingInjectionMarker) {
		t.Fatalf("precondition: thinking injection did not run: %q", out.FinalPrompt)
	}
	if strings.Contains(out.FinalPrompt, toolcall.EPSEKeyword) {
		t.Fatalf("rebuilt prompt dropped back to the canonical keyword: %q", out.FinalPrompt)
	}
	if !strings.Contains(out.FinalPrompt, "<|"+markerUnderTest+"|tool_calls>") {
		t.Fatalf("rebuilt prompt lost the marker shell: %q", out.FinalPrompt)
	}
}

// TestCurrentInputFileRebuildKeepsToolMarker covers the second P0 rebuild path:
// the current-input-file split rebuilds the live prompt (tool instructions or the
// forced format spec) after the marker was applied.
func TestCurrentInputFileRebuildKeepsToolMarker(t *testing.T) {
	h := &openAITestSurface{
		Store: mockOpenAIConfig{currentInputEnabled: true, currentInputMin: 1},
		DS:    &inlineUploadDSStub{},
	}
	req := map[string]any{
		"model":    "deepseek-v4.1-flash",
		"messages": historySplitTestMessages(),
		"tools":    markerTestTools(),
	}
	stdReq, err := promptcompat.NormalizeOpenAIChatRequest(h.Store, req, "", markerUnderTest)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}

	out, err := h.applyCurrentInputFile(context.Background(), &auth.RequestAuth{DeepSeekToken: "token"}, stdReq)
	if err != nil {
		t.Fatalf("apply current input file failed: %v", err)
	}
	if !out.CurrentInputFileApplied {
		t.Fatal("precondition: the current input file was not applied")
	}
	if strings.Contains(out.FinalPrompt, toolcall.EPSEKeyword) {
		t.Fatalf("rebuilt prompt dropped back to the canonical keyword: %q", out.FinalPrompt)
	}
	if !strings.Contains(out.FinalPrompt, "<|"+markerUnderTest+"|tool_calls>") {
		t.Fatalf("rebuilt prompt lost the marker shell: %q", out.FinalPrompt)
	}
	if strings.Contains(out.HistoryText, toolcall.EPSEKeyword) {
		t.Fatalf("HistoryText must not contain canonical keyword: %q", out.HistoryText)
	}
	if !strings.Contains(out.HistoryText, "<|"+markerUnderTest+"|tool_calls>") {
		t.Fatalf("HistoryText missing marker shell: %q", out.HistoryText)
	}
}
