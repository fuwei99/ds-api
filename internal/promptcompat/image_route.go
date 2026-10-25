package promptcompat

import (
	"strings"

	"ds2api/internal/config"
)

// RequestContainsImageContent reports whether the current user turn carries any
// image content that should trigger auto-routing to a vision model. It checks
// the latest user message in OpenAI-style chat messages, the Responses input,
// and top-level attachments, but intentionally ignores images in earlier
// conversation history so that follow-up text-only requests switch back to the
// originally selected model.
func RequestContainsImageContent(req map[string]any) bool {
	if len(req) == 0 {
		return false
	}
	if raw, ok := req["input"]; ok && !isPlainString(raw) && containsImageInAny(raw) {
		return true
	}
	if raw, ok := req["attachments"]; ok && containsImageInAny(raw) {
		return true
	}
	if raw, ok := req["messages"].([]any); ok {
		if latest := latestUserMessage(raw); latest != nil {
			if content := latest["content"]; content != nil && containsImageInAny(content) {
				return true
			}
		}
	}
	return false
}

func isPlainString(v any) bool {
	_, ok := v.(string)
	return ok
}

func latestUserMessage(messages []any) map[string]any {
	for i := len(messages) - 1; i >= 0; i-- {
		msg, ok := messages[i].(map[string]any)
		if !ok {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(asString(msg["role"])))
		if role == "user" {
			return msg
		}
	}
	return nil
}

func containsImageInAny(raw any) bool {
	switch x := raw.(type) {
	case []any:
		for _, item := range x {
			if containsImageInAny(item) {
				return true
			}
		}
	case []map[string]any:
		for _, item := range x {
			if isImageContentBlock(item) {
				return true
			}
		}
	case map[string]any:
		if isImageContentBlock(x) {
			return true
		}
		for _, key := range []string{"content", "parts", "image_url", "file", "source"} {
			if containsImageInAny(x[key]) {
				return true
			}
		}
	}
	return false
}

func isImageContentBlock(block map[string]any) bool {
	if block == nil {
		return false
	}
	blockType := strings.ToLower(strings.TrimSpace(asString(block["type"])))
	if blockType == "image_url" || blockType == "input_image" || blockType == "image" {
		return true
	}
	if blockType == "input_file" || blockType == "file" {
		return false
	}
	if _, ok := block["image_url"]; ok {
		return true
	}
	if _, ok := block["image_url"].(map[string]any); ok {
		return true
	}
	if blockType == "" {
		if _, ok := block["url"]; ok {
			return true
		}
	}
	return false
}

// StripImageBlocksFromRequest removes image content blocks from the request's
// messages, input, and attachments containers. It is safe to call after inline
// file preprocessing because document-style file references are preserved.
func StripImageBlocksFromRequest(req map[string]any) {
	if len(req) == 0 {
		return
	}
	for _, key := range []string{"messages", "input", "attachments"} {
		switch x := req[key].(type) {
		case []any:
			req[key] = StripImageBlocksFromMessages(x)
		}
	}
}

// StripImageBlocksFromMessages removes image content blocks from an OpenAI-style
// message array. Elements that look like bare content parts (have a "type" but no
// "role"/"content") are filtered directly; elements that look like messages have
// their content array filtered. Document-style file references such as input_file
// are preserved.
func StripImageBlocksFromMessages(messages []any) []any {
	out := make([]any, 0, len(messages))
	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			out = append(out, raw)
			continue
		}
		if isBareContentPart(msg) {
			if isImageContentBlock(msg) {
				continue
			}
			out = append(out, shallowCopyMap(msg))
			continue
		}
		content := msg["content"]
		stripped := stripImageContent(content)
		if stripped == nil {
			continue
		}
		if text, ok := stripped.(string); ok && strings.TrimSpace(text) == "" {
			continue
		}
		copied := shallowCopyMap(msg)
		copied["content"] = stripped
		out = append(out, copied)
	}
	return out
}

func isBareContentPart(m map[string]any) bool {
	if m == nil {
		return false
	}
	_, hasRole := m["role"]
	_, hasContent := m["content"]
	if hasRole || hasContent {
		return false
	}
	typeStr := strings.ToLower(strings.TrimSpace(asString(m["type"])))
	return typeStr != ""
}

func stripImageContent(v any) any {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			block, ok := item.(map[string]any)
			if !ok {
				out = append(out, item)
				continue
			}
			if isImageContentBlock(block) {
				continue
			}
			out = append(out, block)
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, 0, len(x))
		for _, block := range x {
			if isImageContentBlock(block) {
				continue
			}
			out = append(out, block)
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return v
}

func shallowCopyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// VisionModelEquivalent is kept for backward compatibility. Since the 2.4.0
// fast/expert/vision merge there is no separate vision model, so it always
// returns an empty string.
func VisionModelEquivalent(model string) string {
	return ""
}

// ImageRoute is the image handling decision for one request.
//
// The decision has to be made and applied in a fixed order around the protocol's
// own inline-file preprocessing, so it travels as a value instead of being
// recomputed at each step:
//
//	route := promptcompat.PlanImageRoute(req, store)   // images still present
//	// ... inline-file upload (images and documents) ...
//	route.StripImages(req)                             // before normalization
//	// ... normalization ...
//	route.RestoreModelName(&stdReq)                    // after normalization
//
// Callers that only need the decision can read the fields directly.
type ImageRoute struct {
	// OriginalModel is the model name the client asked for. After the 2.4.0
	// merge every model is vision-capable, so the name is never rewritten; it is
	// kept so the client-visible response model does not change.
	OriginalModel string
	// Rerouted reports that the current user turn carries image content which
	// must reach the model through ref_file_ids, so the raw image blocks are
	// dropped from the prompt after upload.
	Rerouted bool
}

// PlanImageRoute decides how the request's image content is handled. Call it
// before inline-file preprocessing: the image blocks must still be present for
// the upload. A request with no image content, an empty model, or a model the
// store does not know is left alone.
func PlanImageRoute(req map[string]any, store config.ModelAliasReader) ImageRoute {
	if req == nil {
		return ImageRoute{}
	}
	model, _ := req["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		return ImageRoute{}
	}
	if _, ok := config.ResolveModel(store, model); !ok {
		return ImageRoute{OriginalModel: model}
	}
	if !RequestContainsImageContent(req) {
		return ImageRoute{OriginalModel: model}
	}
	return ImageRoute{OriginalModel: model, Rerouted: true}
}

// StripImages removes the request's image content blocks. Call it after
// inline-file preprocessing — so the merged model receives the images through
// ref_file_ids instead of as raw prompt blocks — and before normalization, which
// reads the message array. Document-style file references are preserved. It is a
// no-op when the request was not rerouted.
func (r ImageRoute) StripImages(req map[string]any) {
	if !r.Rerouted {
		return
	}
	StripImageBlocksFromRequest(req)
}

// RestoreModelName puts the model name the client asked for back on the
// standardized request, so the response reports the model the client selected
// rather than the one the merged model resolves to. Call it after normalization.
// It is a no-op when the request was not rerouted.
func (r ImageRoute) RestoreModelName(stdReq *StandardRequest) {
	if !r.Rerouted || r.OriginalModel == "" || stdReq == nil {
		return
	}
	stdReq.RequestedModel = r.OriginalModel
	stdReq.ResponseModel = r.OriginalModel
}
