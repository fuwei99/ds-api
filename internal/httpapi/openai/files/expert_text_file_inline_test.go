package files

import (
	"context"
	"testing"

	"ds2api/internal/auth"
	"ds2api/internal/httpapi/openai/shared"
)

type expertInlineMockStore struct {
	enabled      *bool
	maxFileBytes int
	modelAliases map[string]string
}

func (m expertInlineMockStore) ModelAliases() map[string]string     { return m.modelAliases }
func (m expertInlineMockStore) ToolcallMode() string                { return "feature_match" }
func (m expertInlineMockStore) ToolcallEarlyEmitConfidence() string { return "high" }
func (m expertInlineMockStore) ResponsesStoreTTLSeconds() int       { return 900 }
func (m expertInlineMockStore) EmbeddingsProvider() string          { return "" }
func (m expertInlineMockStore) AutoDeleteMode() string              { return "none" }
func (m expertInlineMockStore) AutoDeleteSessions() bool            { return false }
func (m expertInlineMockStore) CurrentInputFileEnabled() bool       { return false }
func (m expertInlineMockStore) CurrentInputFileMinChars() int       { return 0 }
func (m expertInlineMockStore) ThinkingInjectionEnabled() bool      { return false }
func (m expertInlineMockStore) ThinkingInjectionPrompt() string     { return "" }
func (m expertInlineMockStore) ExpertPromptSegmentEnabled() bool    { return true }
func (m expertInlineMockStore) ExpertPromptSegmentMaxChars() int    { return 160000 }
func (m expertInlineMockStore) ExpertTextFileInlineEnabled() bool {
	if m.enabled == nil {
		return true
	}
	return *m.enabled
}
func (m expertInlineMockStore) ExpertTextFileInlineMaxFileBytes() int {
	if m.maxFileBytes > 0 {
		return m.maxFileBytes
	}
	return 3 * 1024 * 1024
}
func (m expertInlineMockStore) AutoRouteVisionEnabled() bool { return false }

type mapContentStore struct {
	data map[string]*storedContent
}

type storedContent struct {
	filename string
	mimeType string
	data     []byte
}

func (m *mapContentStore) Store(id string, filename, mimeType string, data []byte) error {
	if m.data == nil {
		m.data = make(map[string]*storedContent)
	}
	m.data[id] = &storedContent{filename: filename, mimeType: mimeType, data: data}
	return nil
}

func (m *mapContentStore) Read(id string) (string, string, []byte, error) {
	if c, ok := m.data[id]; ok {
		return c.filename, c.mimeType, c.data, nil
	}
	return "", "", nil, ErrFileNotFound
}

func boolPtr(b bool) *bool { return &b }

// After the 2.4.0 fast/expert/vision merge every model resolves to the single
// upstream "default" model_type, which forwards file references via
// ref_file_ids. Text files are therefore no longer inlined into the prompt, so
// PreprocessInlineTextFilesForExpert is a no-op for every supported model.

func TestPreprocessInlineTextFilesForExpert_MergedModelIsNoOp(t *testing.T) {
	store := &mapContentStore{data: map[string]*storedContent{
		"file-1": {filename: "notes.txt", mimeType: "text/plain", data: []byte("hello")},
	}}
	h := &Handler{Store: expertInlineMockStore{}, ContentStore: store}
	req := map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "read this"},
					map[string]any{"type": "input_file", "file_id": "file-1"},
				},
			},
		},
	}
	if err := h.PreprocessInlineTextFilesForExpert(context.Background(), &auth.RequestAuth{}, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content := req["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("expected content unchanged, got %d parts", len(content))
	}
	if shared.AsString(content[1].(map[string]any)["file_id"]) != "file-1" {
		t.Errorf("expected file_id to remain")
	}
}

func TestPreprocessInlineTextFilesForExpert_MissingFileIsNoOp(t *testing.T) {
	h := &Handler{Store: expertInlineMockStore{}, ContentStore: &mapContentStore{}}
	req := map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_file", "file_id": "missing"},
				},
			},
		},
	}
	if err := h.PreprocessInlineTextFilesForExpert(context.Background(), &auth.RequestAuth{}, req); err != nil {
		t.Fatalf("expected no error for merged model, got %v", err)
	}
}

func TestPreprocessInlineTextFilesForExpert_TopLevelRefsRemain(t *testing.T) {
	store := &mapContentStore{data: map[string]*storedContent{
		"file-1": {filename: "notes.txt", mimeType: "text/plain", data: []byte("top level content")},
	}}
	h := &Handler{Store: expertInlineMockStore{}, ContentStore: store}
	req := map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{"role": "user", "content": "summarize"},
		},
		"file_ids": []any{"file-1"},
	}
	if err := h.PreprocessInlineTextFilesForExpert(context.Background(), &auth.RequestAuth{}, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req["messages"].([]any)[0].(map[string]any)["content"] != "summarize" {
		t.Fatalf("expected top-level refs to remain untouched")
	}
}

func TestPreprocessInlineTextFilesForExpert_Disabled(t *testing.T) {
	store := &mapContentStore{data: map[string]*storedContent{
		"file-1": {filename: "notes.txt", mimeType: "text/plain", data: []byte("hello")},
	}}
	h := &Handler{Store: expertInlineMockStore{enabled: boolPtr(false)}, ContentStore: store}
	req := map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_file", "file_id": "file-1"},
				},
			},
		},
	}
	if err := h.PreprocessInlineTextFilesForExpert(context.Background(), &auth.RequestAuth{}, req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content := req["messages"].([]any)[0].(map[string]any)["content"].([]any)
	block := content[0].(map[string]any)
	if block["file_id"] != "file-1" {
		t.Errorf("expected file unchanged when disabled, got %v", block)
	}
}
