package promptcompat

import (
	"testing"

	"ds2api/internal/config"
)

type fakeRouteConfigReader struct {
	aliases map[string]string
}

func (f *fakeRouteConfigReader) ModelAliases() map[string]string {
	return f.aliases
}

func TestVisionModelEquivalent(t *testing.T) {
	// After the 2.4.0 fast/expert/vision merge there is no separate vision
	// model, so the equivalent is always empty.
	for _, model := range []string{
		"deepseek-v4.1-flash",
		"deepseek-v4.1-flash-search",
		"deepseek-v4.1-flash-nothinking",
		"gpt-4o",
		"",
	} {
		if got := VisionModelEquivalent(model); got != "" {
			t.Errorf("VisionModelEquivalent(%q)=%q want empty", model, got)
		}
	}
}

func TestRequestContainsImageContent(t *testing.T) {
	imageURLReq := map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "hello"},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,abc"}},
				},
			},
		},
	}
	if !RequestContainsImageContent(imageURLReq) {
		t.Error("expected image_url block to be detected")
	}

	inputImageReq := map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_image", "file_id": "file-123"},
				},
			},
		},
	}
	if !RequestContainsImageContent(inputImageReq) {
		t.Error("expected input_image block to be detected")
	}

	textOnlyReq := map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
	}
	if RequestContainsImageContent(textOnlyReq) {
		t.Error("expected text-only request to have no images")
	}

	documentReq := map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_file", "file_id": "file-456"},
				},
			},
		},
	}
	if RequestContainsImageContent(documentReq) {
		t.Error("expected input_file block not to be treated as image")
	}

	historicalImageReq := map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,abc"}},
				},
			},
			map[string]any{
				"role":    "assistant",
				"content": "I see the image",
			},
			map[string]any{
				"role":    "user",
				"content": "now just text",
			},
		},
	}
	if RequestContainsImageContent(historicalImageReq) {
		t.Error("expected historical image in earlier user message not to trigger routing")
	}
}

func TestStripImageBlocksFromMessages(t *testing.T) {
	messages := []any{
		map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "text", "text": "hello"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,abc"}},
				map[string]any{"type": "input_image", "file_id": "file-123"},
			},
		},
		map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_file", "file_id": "file-456"},
			},
		},
	}
	stripped := StripImageBlocksFromMessages(messages)
	if len(stripped) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(stripped))
	}
	first, _ := stripped[0].(map[string]any)
	firstContent, _ := first["content"].([]any)
	if len(firstContent) != 1 {
		t.Fatalf("expected 1 content block in first message, got %d", len(firstContent))
	}
	if text := asString(firstContent[0].(map[string]any)["text"]); text != "hello" {
		t.Fatalf("expected remaining text block 'hello', got %q", text)
	}
	second, _ := stripped[1].(map[string]any)
	secondContent, _ := second["content"].([]any)
	if len(secondContent) != 1 {
		t.Fatalf("expected input_file block to be preserved, got %d blocks", len(secondContent))
	}
	if fileID := asString(secondContent[0].(map[string]any)["file_id"]); fileID != "file-456" {
		t.Fatalf("expected preserved file_id 'file-456', got %q", fileID)
	}
}

func TestStripImageBlocksFromMessagesBareContentParts(t *testing.T) {
	messages := []any{
		map[string]any{"type": "input_text", "text": "hello"},
		map[string]any{"type": "input_image", "file_id": "file-123"},
		map[string]any{"type": "input_file", "file_id": "file-456"},
	}
	stripped := StripImageBlocksFromMessages(messages)
	if len(stripped) != 2 {
		t.Fatalf("expected 2 items, got %d", len(stripped))
	}
	first, _ := stripped[0].(map[string]any)
	if asString(first["type"]) != "input_text" {
		t.Fatalf("expected input_text to be preserved, got %v", first)
	}
	second, _ := stripped[1].(map[string]any)
	if asString(second["type"]) != "input_file" {
		t.Fatalf("expected input_file to be preserved, got %v", second)
	}
}

func TestPlanImageRoute(t *testing.T) {
	store := &fakeRouteConfigReader{
		aliases: config.DefaultModelAliases(),
	}
	req := map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,abc"}},
				},
			},
		},
	}
	route := PlanImageRoute(req, store)
	if !route.Rerouted {
		t.Fatal("expected reroute")
	}
	if route.OriginalModel != "deepseek-v4.1-flash" {
		t.Fatalf("expected original model deepseek-v4.1-flash, got %q", route.OriginalModel)
	}
	if req["model"] != "deepseek-v4.1-flash" {
		t.Fatalf("expected model rewritten to deepseek-v4.1-flash, got %q", req["model"])
	}
}

func TestPlanImageRoutePreservesNoThinking(t *testing.T) {
	store := &fakeRouteConfigReader{
		aliases: config.DefaultModelAliases(),
	}
	req := map[string]any{
		"model": "deepseek-v4.1-flash-nothinking",
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,abc"}},
				},
			},
		},
	}
	route := PlanImageRoute(req, store)
	if !route.Rerouted {
		t.Fatal("expected reroute")
	}
	if req["model"] != "deepseek-v4.1-flash-nothinking" {
		t.Fatalf("expected model rewritten to deepseek-v4.1-flash-nothinking, got %q", req["model"])
	}
}

func TestPlanImageRouteAlwaysOnForImages(t *testing.T) {
	// After the merge the auto-route setting is gone from the UI; images must
	// always be routed to the unified vision-capable model.
	store := &fakeRouteConfigReader{
		aliases: config.DefaultModelAliases(),
	}
	req := map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,abc"}},
				},
			},
		},
	}
	route := PlanImageRoute(req, store)
	if !route.Rerouted {
		t.Fatal("expected reroute for image content even when the setting is off")
	}
	if req["model"] != "deepseek-v4.1-flash" {
		t.Fatalf("expected model unchanged, got %q", req["model"])
	}
}

func TestPlanImageRouteNoImage(t *testing.T) {
	store := &fakeRouteConfigReader{
		aliases: config.DefaultModelAliases(),
	}
	req := map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
	}
	route := PlanImageRoute(req, store)
	if route.Rerouted {
		t.Fatal("expected no reroute without images")
	}
}

func TestPlanImageRouteMergedModelStillStripsImages(t *testing.T) {
	store := &fakeRouteConfigReader{
		aliases: config.DefaultModelAliases(),
	}
	req := map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,abc"}},
				},
			},
		},
	}
	route := PlanImageRoute(req, store)
	if !route.Rerouted {
		t.Fatal("expected image content to be flagged for stripping")
	}
	if req["model"] != "deepseek-v4.1-flash" {
		t.Fatalf("expected model unchanged, got %q", req["model"])
	}
}

func TestPlanImageRouteAlias(t *testing.T) {
	store := &fakeRouteConfigReader{
		aliases: config.DefaultModelAliases(),
	}
	req := map[string]any{
		"model": "gpt-4o",
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,abc"}},
				},
			},
		},
	}
	route := PlanImageRoute(req, store)
	if !route.Rerouted {
		t.Fatal("expected reroute for alias")
	}
	if route.OriginalModel != "gpt-4o" {
		t.Fatalf("expected original model gpt-4o, got %q", route.OriginalModel)
	}
	if req["model"] != "gpt-4o" {
		t.Fatalf("expected model unchanged (no separate vision model), got %q", req["model"])
	}
}

func TestStripImageBlocksFromRequest(t *testing.T) {
	req := map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "hello"},
					map[string]any{"type": "input_image", "file_id": "file-123"},
				},
			},
		},
		"input": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,abc"}},
				},
			},
		},
	}
	StripImageBlocksFromRequest(req)
	msgs, _ := req["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	input, _ := req["input"].([]any)
	if len(input) != 0 {
		t.Fatalf("expected input stripped to 0 messages, got %d", len(input))
	}
}

// StripImages runs after inline-file preprocessing, so the image block must go
// while the document reference the upload produced stays.
func TestImageRouteStripImagesKeepsDocumentReferences(t *testing.T) {
	store := &fakeRouteConfigReader{aliases: config.DefaultModelAliases()}
	req := map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "describe this"},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,abc"}},
					map[string]any{"type": "input_file", "file": map[string]any{"file_id": "ref_file_1"}},
				},
			},
		},
	}
	route := PlanImageRoute(req, store)
	if !route.Rerouted {
		t.Fatal("expected reroute")
	}
	route.StripImages(req)

	msgs, _ := req["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("expected the message to survive, got %#v", req["messages"])
	}
	content, _ := msgs[0].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("expected only the image block to be dropped, got %#v", content)
	}
	for _, raw := range content {
		if block, _ := raw.(map[string]any); block["type"] == "image_url" {
			t.Fatalf("image block must not survive StripImages: %#v", content)
		}
	}
}

// RestoreModelName runs after normalization and is what keeps the client-visible
// response model equal to the model the client asked for.
func TestImageRouteRestoreModelName(t *testing.T) {
	store := &fakeRouteConfigReader{aliases: config.DefaultModelAliases()}
	req := map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{
				"role":    "user",
				"content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,abc"}}},
			},
		},
	}
	route := PlanImageRoute(req, store)
	if !route.Rerouted {
		t.Fatal("expected reroute")
	}
	stdReq := StandardRequest{RequestedModel: "resolved-model", ResponseModel: "resolved-model"}
	route.RestoreModelName(&stdReq)
	if stdReq.RequestedModel != "deepseek-v4.1-flash" || stdReq.ResponseModel != "deepseek-v4.1-flash" {
		t.Fatalf("expected the client model restored, got %q/%q", stdReq.RequestedModel, stdReq.ResponseModel)
	}
}

// A request without image content must be left completely alone by both steps.
func TestImageRouteIsInertWithoutImages(t *testing.T) {
	store := &fakeRouteConfigReader{aliases: config.DefaultModelAliases()}
	req := map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
	}
	route := PlanImageRoute(req, store)
	if route.Rerouted {
		t.Fatal("a text-only request must not reroute")
	}
	route.StripImages(req)
	msgs, _ := req["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("StripImages must be a no-op when not rerouted, got %#v", req["messages"])
	}
	stdReq := StandardRequest{RequestedModel: "resolved-model", ResponseModel: "resolved-model"}
	route.RestoreModelName(&stdReq)
	if stdReq.RequestedModel != "resolved-model" || stdReq.ResponseModel != "resolved-model" {
		t.Fatalf("RestoreModelName must be a no-op when not rerouted, got %q/%q", stdReq.RequestedModel, stdReq.ResponseModel)
	}
}
