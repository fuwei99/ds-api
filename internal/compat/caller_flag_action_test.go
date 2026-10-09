package compat

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// protocolSurfaces lists the exits that normalize an inbound request and record
// whether it carries tool definitions, keyed by path under internal/httpapi.
// Adding a surface is a deliberate act: it has to call the shared step and be
// added here, which is the tripwire this list exists for.
var protocolSurfaces = []string{
	"claude/handler_messages.go",
	"gemini/handler_generate.go",
	"openai/chat/handler_chat.go",
	"openai/chat/vercel_stream.go",
	"openai/responses/responses_handler.go",
}

// Every protocol surface must derive the request's tool presence through
// promptcompat.ApplyCallerFlags, which applies the registered per-key credential
// flags and then reports whether the rewritten body carries tools. The order
// inside that one step is the contract — a flag that strips tools has to run
// before presence is computed — so no surface may read a credential flag, strip
// tool definitions, or compute presence on its own.
//
// This is the property that keeps a new per-key flag a one-file change: the
// surfaces never name a flag, so none of them changes when one is added. It is
// checked against the sources because the property is about the shape of the
// call sites rather than about a value they compute.
func TestProtocolSurfacesShareCallerFlagStep(t *testing.T) {
	root := filepath.Join("..", "httpapi")
	found := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		src := string(raw)

		if strings.Contains(src, "auth.WithToolsPresent(") {
			found[rel] = src
		}
		if strings.Contains(src, ".CredentialFlagForRequest(") {
			t.Errorf("%s reads a credential flag directly; the read belongs to promptcompat.ApplyCallerFlags", rel)
		}
		if strings.Contains(src, "promptcompat.DisableToolsForRequest(") {
			t.Errorf("%s strips tool definitions directly; that rewrite belongs to promptcompat.ApplyCallerFlags", rel)
		}
		if strings.Contains(src, "promptcompat.RequestBodyHasTools(") {
			t.Errorf("%s computes tool presence directly; call promptcompat.ApplyCallerFlags so caller flags are applied first", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s failed: %v", root, err)
	}

	for _, rel := range protocolSurfaces {
		src, ok := found[rel]
		if !ok {
			t.Errorf("%s no longer records tool presence; if the surface moved, update this list", rel)
			continue
		}
		if !strings.Contains(src, "promptcompat.ApplyCallerFlags(") {
			t.Errorf("%s records tool presence without promptcompat.ApplyCallerFlags, so per-key flags would not apply", rel)
		}
	}
	for rel := range found {
		if !containsString(protocolSurfaces, rel) {
			t.Errorf("%s records tool presence but is not in protocolSurfaces; wire it through promptcompat.ApplyCallerFlags and add it to the list", rel)
		}
	}
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
