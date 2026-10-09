package compat

import (
	"path/filepath"
	"testing"

	"ds2api/internal/toolcall"
)

// The tool-marker fixtures are replayed by this test and by the Node suite in
// tests/node/js_compat_test.js. Both read the same files and must agree
// chunk-for-chunk, so a change to one runtime's marker normalization that is not
// mirrored in the other fails here instead of drifting silently into
// production.
//
// The marker normalizer (Go internal/toolcall/marker.go, Node
// internal/js/chat-stream/marker.js) is the one place where the per-API-key
// tool-call identifier is folded back to the canonical EPSE keyword, and it is
// the only part of that feature that is implemented twice. Its hold rule — a
// trailing fragment that could still grow into the marker is withheld until the
// next chunk — has to agree byte-for-byte: the Go runtime feeds it to the
// server-side sieve, the Node mirror feeds it to the Vercel stream, and a
// divergence either leaks a half marker into visible text or drops a tool call.
func TestGoCompatToolMarkerFixtures(t *testing.T) {
	files, err := filepath.Glob(compatPath("fixtures", "tool_marker", "*.json"))
	if err != nil {
		t.Fatalf("glob fixtures failed: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no tool marker fixtures found")
	}
	for _, fixturePath := range files {
		name := trimExt(filepath.Base(fixturePath))
		t.Run(name, func(t *testing.T) {
			var fixture struct {
				Marker string   `json:"marker"`
				Chunks []string `json:"chunks"`
			}
			mustLoadJSON(t, fixturePath, &fixture)

			var expected struct {
				Writes []string `json:"writes"`
				Flush  string   `json:"flush"`
				Output string   `json:"output"`
			}
			mustLoadJSON(t, compatPath("expected", "tool_marker_"+name+".json"), &expected)

			if len(expected.Writes) != len(fixture.Chunks) {
				t.Fatalf("fixture has %d chunks but %d expected writes",
					len(fixture.Chunks), len(expected.Writes))
			}

			normalizer := toolcall.NewMarkerNormalizer(fixture.Marker)
			var output string
			for i, chunk := range fixture.Chunks {
				got := normalizer.Write(chunk)
				if got != expected.Writes[i] {
					t.Fatalf("chunk %d (%q): wrote %q, want %q", i, chunk, got, expected.Writes[i])
				}
				output += got
			}
			flushed := normalizer.Flush()
			if flushed != expected.Flush {
				t.Fatalf("flush = %q, want %q", flushed, expected.Flush)
			}
			output += flushed
			if output != expected.Output {
				t.Fatalf("reassembled output = %q, want %q", output, expected.Output)
			}
		})
	}
}
