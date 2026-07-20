package promptcompat

import (
	"reflect"
	"testing"
)

func TestParseUploadTags_StringContent(t *testing.T) {
	req := map[string]any{
		"messages": []any{
			map[string]any{
				"role":    "user",
				"content": "hello <||file:report.pdf:user@example.com:file-123||> world",
			},
		},
	}
	got := ParseUploadTags(req)

	if !got.ReturnFileID && !got.ForceUpload {
		// no control tags, expected
	}
	if len(got.ExistingFiles) != 1 {
		t.Fatalf("expected 1 file ref, got %d", len(got.ExistingFiles))
	}
	f := got.ExistingFiles[0]
	if f.Name != "report.pdf" || f.Email != "user@example.com" || f.ID != "file-123" {
		t.Fatalf("unexpected file ref: %+v", f)
	}
	if got.PreferredAccount != "user@example.com" {
		t.Fatalf("expected PreferredAccount user@example.com, got %q", got.PreferredAccount)
	}
	// tag should be cleaned from content
	msg := req["messages"].([]any)[0].(map[string]any)
	if got := msg["content"].(string); got != "hello  world" {
		t.Fatalf("expected cleaned content, got %q", got)
	}
}

func TestParseUploadTags_ControlFlags(t *testing.T) {
	req := map[string]any{
		"messages": []any{
			map[string]any{
				"role":    "user",
				"content": "<||file-upload:True||><||fileid:True||>",
			},
		},
	}
	got := ParseUploadTags(req)
	if !got.ForceUpload {
		t.Error("expected ForceUpload=true")
	}
	if !got.ReturnFileID {
		t.Error("expected ReturnFileID=true")
	}
	if len(got.ExistingFiles) != 0 {
		t.Fatalf("expected 0 file refs, got %d", len(got.ExistingFiles))
	}
	msg := req["messages"].([]any)[0].(map[string]any)
	if msg["content"].(string) != "" {
		t.Fatalf("expected empty content after cleanup, got %q", msg["content"])
	}
}

func TestParseUploadTags_MultimodalContent(t *testing.T) {
	req := map[string]any{
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "see <||file:notes.txt:alice@test.com:f-1||>"},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "http://x"}},
					map[string]any{"type": "text", "text": "<||file-upload:True||>"},
				},
			},
		},
	}
	got := ParseUploadTags(req)
	if len(got.ExistingFiles) != 1 {
		t.Fatalf("expected 1 file ref, got %d", len(got.ExistingFiles))
	}
	if got.ExistingFiles[0].ID != "f-1" {
		t.Fatalf("expected id f-1, got %q", got.ExistingFiles[0].ID)
	}
	if !got.ForceUpload {
		t.Error("expected ForceUpload from second text part")
	}
	parts := req["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if parts[0].(map[string]any)["text"].(string) != "see" {
		t.Fatalf("expected cleaned first text, got %q", parts[0].(map[string]any)["text"])
	}
	if parts[2].(map[string]any)["text"].(string) != "" {
		t.Fatalf("expected empty third text, got %q", parts[2].(map[string]any)["text"])
	}
}

func TestParseUploadTags_DeduplicatesFileIDs(t *testing.T) {
	req := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "<||file:a:u@x.com:dup||>"},
			map[string]any{"role": "assistant", "content": "<||file:b:u@x.com:dup||>"},
			map[string]any{"role": "user", "content": "<||file:c:v@x.com:unique||>"},
		},
	}
	got := ParseUploadTags(req)
	if len(got.ExistingFiles) != 2 {
		t.Fatalf("expected 2 deduped file refs, got %d", len(got.ExistingFiles))
	}
	if got.ExistingFiles[0].ID != "dup" || got.ExistingFiles[1].ID != "unique" {
		t.Fatalf("unexpected order: %+v", got.ExistingFiles)
	}
	// PreferredAccount takes the first file ref's email
	if got.PreferredAccount != "u@x.com" {
		t.Fatalf("expected PreferredAccount u@x.com, got %q", got.PreferredAccount)
	}
}

func TestParseUploadTags_NoMessages(t *testing.T) {
	got := ParseUploadTags(map[string]any{})
	if got.ForceUpload || got.ReturnFileID || len(got.ExistingFiles) != 0 {
		t.Fatalf("expected empty result for empty req, got %+v", got)
	}
	got = ParseUploadTags(nil)
	if got.ForceUpload || got.ReturnFileID {
		t.Fatalf("expected empty result for nil req, got %+v", got)
	}
}

func TestParseUploadTags_PreservesNonTagText(t *testing.T) {
	req := map[string]any{
		"messages": []any{
			map[string]any{
				"role":    "user",
				"content": "normal text <||file:a:b@c.com:f-9||> more text <||fileid:True||> tail",
			},
		},
	}
	got := ParseUploadTags(req)
	msg := req["messages"].([]any)[0].(map[string]any)
	want := "normal text  more text  tail"
	if got := msg["content"].(string); got != want {
		t.Fatalf("expected %q, got %q", want, msg["content"])
	}
	if !got.ReturnFileID {
		t.Error("expected ReturnFileID=true")
	}
}

func TestBuildFileTag(t *testing.T) {
	got := BuildFileTag("report.pdf", "user@example.com", "file-123")
	want := "\n---\n<||file:report.pdf:user@example.com:file-123||>"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestAppendUniqueFileID(t *testing.T) {
	ids := []string{"a", "b"}
	ids = AppendUniqueFileID(ids, "b") // dup
	if !reflect.DeepEqual(ids, []string{"a", "b"}) {
		t.Fatalf("expected no duplicate added, got %v", ids)
	}
	ids = AppendUniqueFileID(ids, "c")
	if !reflect.DeepEqual(ids, []string{"a", "b", "c"}) {
		t.Fatalf("expected c appended, got %v", ids)
	}
	ids = AppendUniqueFileID(ids, "  ")
	if !reflect.DeepEqual(ids, []string{"a", "b", "c"}) {
		t.Fatalf("expected empty id ignored, got %v", ids)
	}
}
