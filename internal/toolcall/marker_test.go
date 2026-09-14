package toolcall

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func TestDeriveToolMarkerStableDistinctAndAlphabetSafe(t *testing.T) {
	const secret = "server-secret"
	first := DeriveToolMarker(secret, "caller:aaaa")

	if again := DeriveToolMarker(secret, "caller:aaaa"); again != first {
		t.Fatalf("derivation must be stable for the same caller: %q vs %q", first, again)
	}
	if other := DeriveToolMarker(secret, "caller:bbbb"); other == first {
		t.Fatalf("different callers must not share a marker: %q", first)
	}
	if rotated := DeriveToolMarker("other-secret", "caller:aaaa"); rotated == first {
		t.Fatalf("a different server secret must yield a different marker: %q", first)
	}
	if len(first) != markerLength {
		t.Fatalf("unexpected marker length: %q", first)
	}
	for i := 0; i < len(first); i++ {
		if !strings.ContainsRune(markerAlphabet, rune(first[i])) {
			t.Fatalf("marker %q uses a character outside the alphabet", first)
		}
	}
	if first == DeriveToolMarker("", "caller:aaaa") {
		t.Fatal("an empty secret must not reproduce the configured-secret marker")
	}
}

func TestDeriveToolMarkerNeverContainsCanonicalKeyword(t *testing.T) {
	for _, secret := range []string{"", "s", "server-secret"} {
		for i := 0; i < 4000; i++ {
			marker := DeriveToolMarker(secret, "caller:"+strconv.Itoa(i))
			if strings.Contains(marker, EPSEKeyword) {
				t.Fatalf("marker %q contains the canonical keyword (secret=%q)", marker, secret)
			}
		}
	}
}

// TestDeriveToolMarkerReDerivesWhenFirstAttemptHitsKeyword pins the retry path:
// a first-attempt draw that happens to contain the canonical keyword must be
// replaced by a clean re-derivation instead of a partial textual patch.
func TestDeriveToolMarkerReDerivesWhenFirstAttemptHitsKeyword(t *testing.T) {
	const secret = "retry-secret"
	caller := ""
	for i := 0; i < 2_000_000; i++ {
		candidate := "caller:" + strconv.Itoa(i)
		if strings.Contains(deriveToolMarkerAttempt(secret, candidate, 0), EPSEKeyword) {
			caller = candidate
			break
		}
	}
	if caller == "" {
		t.Skip("no first-attempt marker containing the canonical keyword inside the search window")
	}
	first := deriveToolMarkerAttempt(secret, caller, 0)
	marker := DeriveToolMarker(secret, caller)
	if marker == first {
		t.Fatalf("expected a re-derivation, still %q", marker)
	}
	if strings.Contains(marker, EPSEKeyword) {
		t.Fatalf("re-derived marker %q contains the canonical keyword", marker)
	}
	if marker != deriveToolMarkerAttempt(secret, caller, 1) {
		t.Fatalf("re-derivation must use the salted attempt: %q", marker)
	}
}

func TestMarkerNormalizerRewritesMarkerBackToCanonical(t *testing.T) {
	n := NewMarkerNormalizer("q7zk3m")
	if !n.Active() {
		t.Fatal("expected an active normalizer for a non-canonical marker")
	}
	if got := n.Write("a <|q7ZK3m|tool_calls> b"); got != "a <|EPSE|tool_calls> b" {
		t.Fatalf("unexpected rewrite: %q", got)
	}
	if got := n.Flush(); got != "" {
		t.Fatalf("nothing should be withheld: %q", got)
	}
}

func TestMarkerNormalizerInertForEmptyAndCanonicalMarkers(t *testing.T) {
	for _, marker := range []string{"", "   ", EPSEKeyword, "epse"} {
		n := NewMarkerNormalizer(marker)
		if n.Active() {
			t.Fatalf("marker %q must yield an inert normalizer", marker)
		}
		if got := n.Write("<|EPSE|tool_calls>"); got != "<|EPSE|tool_calls>" {
			t.Fatalf("inert normalizer rewrote text: %q", got)
		}
		if got := n.Flush(); got != "" {
			t.Fatalf("inert normalizer flushed text: %q", got)
		}
	}
}

func TestMarkerNormalizerRewritesMarkerSplitByChar(t *testing.T) {
	n := NewMarkerNormalizer("Q7ZK3M")
	var b strings.Builder
	for _, r := range "<|Q7ZK3M|tool_calls>" {
		b.WriteString(n.Write(string(r)))
	}
	b.WriteString(n.Flush())
	if got := b.String(); got != "<|EPSE|tool_calls>" {
		t.Fatalf("unexpected cross-chunk rewrite: %q", got)
	}
}

func TestMarkerNormalizerNeverForwardsPartialMarker(t *testing.T) {
	n := NewMarkerNormalizer("Q7ZK3M")
	var b strings.Builder
	for _, r := range "<|Q7ZK3M|invoke>" {
		b.WriteString(n.Write(string(r)))
		if strings.Contains(b.String(), "Q7Z") {
			t.Fatalf("partial marker leaked: %q", b.String())
		}
	}
	b.WriteString(n.Flush())
	if got := b.String(); got != "<|EPSE|invoke>" {
		t.Fatalf("unexpected rewrite: %q", got)
	}
}

func TestMarkerNormalizerHoldsPartialMarkerUntilFlush(t *testing.T) {
	n := NewMarkerNormalizer("Q7ZK3M")
	if got := n.Write("tail Q7Z"); got != "tail " {
		t.Fatalf("expected the partial marker to be withheld, got %q", got)
	}
	if got := n.Flush(); got != "Q7Z" {
		t.Fatalf("expected the withheld fragment on flush, got %q", got)
	}
}

func TestApplyToolMarkerRoundTrip(t *testing.T) {
	const marker = "Q7ZK3M"
	prompt := "规范：使用 <|EPSE|tool_calls> 包裹 <|EPSE|invoke>，并以 </|EPSE|tool_calls> 收尾。"
	applied := ApplyToolMarker(prompt, marker)
	if strings.Contains(applied, EPSEKeyword) {
		t.Fatalf("applied prompt still contains the canonical keyword: %q", applied)
	}
	if !strings.Contains(applied, "<|"+marker+"|tool_calls>") {
		t.Fatalf("applied prompt missing the marker shell: %q", applied)
	}
	if got := NormalizeMarkerText(applied, marker); got != prompt {
		t.Fatalf("round trip mismatch:\n got %q\nwant %q", got, prompt)
	}
}

func TestToolMarkerHelpersAreNoOpsWithoutAMarker(t *testing.T) {
	text := "keep <|EPSE|tool_calls> as is"
	for _, marker := range []string{"", "   ", EPSEKeyword} {
		if got := ApplyToolMarker(text, marker); got != text {
			t.Fatalf("ApplyToolMarker(%q) must be a no-op, got %q", marker, got)
		}
		if got := NormalizeMarkerText(text, marker); got != text {
			t.Fatalf("NormalizeMarkerText(%q) must be a no-op, got %q", marker, got)
		}
	}
}

// TestRepairPromptRendersMarker 验证修复链路不再把共享 EPSE 关键字发给上游，
// 且解析修复输出前会先把标识归一化回 EPSE。
func TestRepairPromptRendersMarker(t *testing.T) {
	const marker = "Q7ZK3M"
	badCode := `<|` + marker + `|tool_calls><|` + marker + `|invoke name="read_file"><|` + marker + `|parameter name="path">README.MD</|` + marker + `|parameter></|` + marker + `|invoke></|` + marker + `|tool_calls>`

	prompt, _, _ := BuildToolCallRepairPrompt(badCode, marker)
	if strings.Contains(prompt, EPSEKeyword) {
		t.Fatalf("repair prompt still contains the canonical keyword: %q", prompt)
	}
	if !strings.Contains(prompt, "<|"+marker+"|parameter>") {
		t.Fatalf("repair prompt missing the marker form: %q", prompt)
	}

	calls, ok := RepairToolCallsWithLLM(t.Context(), badCode, func(context.Context, string) (string, error) {
		return badCode, nil
	}, marker)
	if !ok || len(calls) != 1 {
		t.Fatalf("expected one repaired call, ok=%v calls=%#v", ok, calls)
	}
	if calls[0].Name != "read_file" {
		t.Fatalf("unexpected repaired call: %#v", calls[0])
	}
	if got, _ := calls[0].Input["path"].(string); got != "README.MD" {
		t.Fatalf("unexpected repaired parameter: %#v", calls[0].Input)
	}
}
