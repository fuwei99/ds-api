package compat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ds2api/internal/sse"
)

// The content-policy fixtures are replayed by this test and by the Node suite in
// tests/node/js_compat_test.js. Both read the same files and must agree
// fragment-for-fragment, so a change to one runtime's cut rules that is not
// mirrored in the other fails here instead of drifting silently into
// production.
func TestGoCompatContentPolicyFixtures(t *testing.T) {
	files, err := filepath.Glob(compatPath("fixtures", "content_policy", "*.json"))
	if err != nil {
		t.Fatalf("glob fixtures failed: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no content policy fixtures found")
	}
	for _, fixturePath := range files {
		name := trimExt(filepath.Base(fixturePath))
		t.Run(name, func(t *testing.T) {
			var fixture struct {
				Fragments []string `json:"fragments"`
			}
			mustLoadJSON(t, fixturePath, &fixture)

			var expected struct {
				Forwarded []string `json:"forwarded"`
				Cut       string   `json:"cut"`
			}
			mustLoadJSON(t, compatPath("expected", "content_policy_"+name+".json"), &expected)

			if len(expected.Forwarded) != len(fixture.Fragments) {
				t.Fatalf("fixture has %d fragments but %d expected forwards",
					len(fixture.Fragments), len(expected.Forwarded))
			}

			policy := sse.ContentPolicy{}
			for i, fragment := range fixture.Fragments {
				got := policy.Feed(fragment)
				if got != expected.Forwarded[i] {
					t.Fatalf("fragment %d (%q): forwarded %q, want %q",
						i, fragment, got, expected.Forwarded[i])
				}
			}
			if got := string(policy.Cut()); got != expected.Cut {
				t.Fatalf("cut = %q, want %q", got, expected.Cut)
			}
		})
	}
}

// The fixture files are the shared contract, so an unreadable or malformed one
// must fail loudly rather than be skipped.
func TestCompatContentPolicyFixturesAreValidJSON(t *testing.T) {
	for _, pattern := range []string{"fixtures/content_policy/*.json", "expected/content_policy_*.json"} {
		files, err := filepath.Glob(compatPath(pattern))
		if err != nil {
			t.Fatalf("glob %s failed: %v", pattern, err)
		}
		for _, path := range files {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s failed: %v", path, err)
			}
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				t.Fatalf("%s is not valid JSON: %v", path, err)
			}
		}
	}
}
