package history

import (
	"context"
	"strings"
	"testing"

	"ds2api/internal/auth"
	dsclient "ds2api/internal/deepseek/client"
	"ds2api/internal/promptcompat"
	"ds2api/internal/toolcall"
)

const markerUnderTest = "Q7ZK3M"

type recordingUploader struct {
	uploads []dsclient.UploadFileRequest
}

func (u *recordingUploader) UploadFile(_ context.Context, _ *auth.RequestAuth, req dsclient.UploadFileRequest, _ int) (*dsclient.UploadFileResult, error) {
	u.uploads = append(u.uploads, req)
	return &dsclient.UploadFileResult{ID: "file-" + req.Filename, Filename: req.Filename}, nil
}

func (u *recordingUploader) uploadFor(filename string) (dsclient.UploadFileRequest, bool) {
	for _, req := range u.uploads {
		if req.Filename == filename {
			return req, true
		}
	}
	return dsclient.UploadFileRequest{}, false
}

type enabledCurrentInputConfig struct{ minChars int }

func (c enabledCurrentInputConfig) CurrentInputFileEnabled() bool { return true }
func (c enabledCurrentInputConfig) CurrentInputFileMinChars() int { return c.minChars }

func markerToolMarkupBlock(marker string) string {
	return "<|" + marker + "|tool_calls>\n" +
		"  <|" + marker + "|invoke name=\"read_file\">\n" +
		"    <|" + marker + "|parameter name=\"path\">README.MD</|" + marker + "|parameter>\n" +
		"  </|" + marker + "|invoke>\n" +
		"</|" + marker + "|tool_calls>"
}

func markerStandardRequest(marker string) promptcompat.StandardRequest {
	return promptcompat.StandardRequest{
		Surface:       "openai_chat",
		ResolvedModel: "deepseek-v4.1-flash",
		Messages: []any{
			map[string]any{"role": "user", "content": "first question"},
			map[string]any{"role": "assistant", "content": markerToolMarkupBlock(marker)},
			map[string]any{"role": "user", "content": "second question"},
		},
		FinalPrompt: strings.Repeat("prompt ", 40),
		ToolsRaw: []any{
			map[string]any{"type": "function", "function": map[string]any{
				"name":        "read_file",
				"description": "Read a file",
				"parameters":  map[string]any{"type": "object"},
			}},
		},
		ToolChoice: promptcompat.DefaultToolChoicePolicy(),
		ToolMarker: marker,
	}
}

// TestApplyCurrentInputFileUploadsMarkerFormAndSyncsHistoryText pins the
// P0 invariant for the current-input-file split: the live prompt, the uploaded
// HISTORY.txt, and stdReq.HistoryText (for local archives and WebUI display) all
// carry the caller-specific marker (the upstream must never see the shared EPSE
// keyword, and WebUI inspection must match the actual uploaded context).
func TestApplyCurrentInputFileUploadsMarkerFormAndSyncsHistoryText(t *testing.T) {
	uploader := &recordingUploader{}
	svc := Service{Store: enabledCurrentInputConfig{minChars: 1}, DS: uploader}

	out, err := svc.ApplyCurrentInputFile(context.Background(), &auth.RequestAuth{}, markerStandardRequest(markerUnderTest))
	if err != nil {
		t.Fatalf("ApplyCurrentInputFile: %v", err)
	}
	if !out.CurrentInputFileApplied {
		t.Fatal("expected the current input file to be applied")
	}

	if strings.Contains(out.HistoryText, toolMarkerKeywordForTest()) {
		t.Fatalf("HistoryText must not contain canonical keyword: %q", out.HistoryText)
	}
	if !strings.Contains(out.HistoryText, "<|"+markerUnderTest+"|tool_calls>") {
		t.Fatalf("HistoryText missing the marker shell: %q", out.HistoryText)
	}
	if strings.Contains(out.FinalPrompt, toolMarkerKeywordForTest()) {
		t.Fatalf("live prompt still contains the canonical keyword: %q", out.FinalPrompt)
	}
	if !strings.Contains(out.FinalPrompt, "<|"+markerUnderTest+"|tool_calls>") {
		t.Fatalf("live prompt missing the marker shell: %q", out.FinalPrompt)
	}

	historyUpload, ok := uploader.uploadFor(currentInputFilename)
	if !ok {
		t.Fatalf("HISTORY.txt was not uploaded, uploads=%#v", uploader.uploads)
	}
	uploaded := string(historyUpload.Data)
	if strings.Contains(uploaded, toolMarkerKeywordForTest()) {
		t.Fatalf("uploaded HISTORY.txt still contains the canonical keyword: %q", uploaded)
	}
	if !strings.Contains(uploaded, "<|"+markerUnderTest+"|tool_calls>") {
		t.Fatalf("uploaded HISTORY.txt missing the marker shell: %q", uploaded)
	}

	// TOOLS.txt carries the tool catalog (descriptions only, no tag shells), but
	// it is still rendered through the same marker step so nothing the upstream
	// reads can fall back to the shared keyword.
	toolsUpload, ok := uploader.uploadFor(currentToolsFilename)
	if !ok {
		t.Fatalf("TOOLS.txt was not uploaded, uploads=%#v", uploader.uploads)
	}
	if strings.Contains(string(toolsUpload.Data), toolMarkerKeywordForTest()) {
		t.Fatalf("uploaded TOOLS.txt contains the canonical keyword: %q", string(toolsUpload.Data))
	}
}

// TestReuploadAppliedCurrentInputFileUploadsMarkerForm covers the account-switch
// re-upload: the stored canonical HistoryText must be rendered with the marker on
// the way out again.
func TestReuploadAppliedCurrentInputFileUploadsMarkerForm(t *testing.T) {
	uploader := &recordingUploader{}
	svc := Service{Store: enabledCurrentInputConfig{minChars: 1}, DS: uploader}

	stdReq := markerStandardRequest(markerUnderTest)
	stdReq.CurrentInputFileApplied = true
	stdReq.HistoryText = "# HISTORY.txt\n\n" + markerToolMarkupBlock(toolMarkerKeywordForTest()) + "\n"
	stdReq.CurrentInputFileID = "old-history"
	stdReq.CurrentToolsFileID = ""

	out, err := svc.ReuploadAppliedCurrentInputFile(context.Background(), &auth.RequestAuth{}, stdReq)
	if err != nil {
		t.Fatalf("ReuploadAppliedCurrentInputFile: %v", err)
	}
	if out.CurrentInputFileID == "" || out.CurrentInputFileID == "old-history" {
		t.Fatalf("expected the history file id to be replaced, got %q", out.CurrentInputFileID)
	}
	uploaded, ok := uploader.uploadFor(currentInputFilename)
	if !ok {
		t.Fatalf("HISTORY.txt was not re-uploaded, uploads=%#v", uploader.uploads)
	}
	data := string(uploaded.Data)
	if strings.Contains(data, toolMarkerKeywordForTest()) {
		t.Fatalf("re-uploaded HISTORY.txt still contains the canonical keyword: %q", data)
	}
	if !strings.Contains(data, "<|"+markerUnderTest+"|tool_calls>") {
		t.Fatalf("re-uploaded HISTORY.txt missing the marker shell: %q", data)
	}
}

func TestApplyCurrentInputFileFallbacksToAuthToolMarker(t *testing.T) {
	uploader := &recordingUploader{}
	svc := Service{Store: enabledCurrentInputConfig{minChars: 1}, DS: uploader}

	req := markerStandardRequest("")
	req.ToolMarker = ""
	authInfo := &auth.RequestAuth{ToolMarker: markerUnderTest}

	out, err := svc.ApplyCurrentInputFile(context.Background(), authInfo, req)
	if err != nil {
		t.Fatalf("ApplyCurrentInputFile: %v", err)
	}
	if !out.CurrentInputFileApplied {
		t.Fatal("expected the current input file to be applied")
	}
	if strings.Contains(out.HistoryText, toolMarkerKeywordForTest()) {
		t.Fatalf("HistoryText must not contain canonical keyword: %q", out.HistoryText)
	}
	if !strings.Contains(out.HistoryText, "<|"+markerUnderTest+"|tool_calls>") {
		t.Fatalf("HistoryText missing the marker shell from auth: %q", out.HistoryText)
	}
}

// toolMarkerKeywordForTest keeps the canonical keyword reference in one place.
func toolMarkerKeywordForTest() string {
	return toolcall.EPSEKeyword
}
