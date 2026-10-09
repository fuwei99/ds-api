package sse

import (
	"strings"
	"testing"
)

func TestContentPolicyForwardsHealthyText(t *testing.T) {
	policy := ContentPolicy{}
	if got := policy.Feed("普通正文"); got != "普通正文" {
		t.Fatalf("healthy text must be forwarded, got %q", got)
	}
	if got := policy.Cut(); got != CutNone {
		t.Fatalf("healthy text must not cut, got %q", got)
	}
}

func TestContentPolicyCutsToolRoleMarkerAndStaysCut(t *testing.T) {
	policy := ContentPolicy{}
	// A trailing marker prefix is withheld rather than forwarded.
	if got := policy.Feed("好的，[To"); got != "好的，" {
		t.Fatalf("marker prefix must be withheld, got %q", got)
	}
	if got := policy.Feed("ol]:编造内容"); got != "" {
		t.Fatalf("the marker fragment must not be forwarded, got %q", got)
	}
	if got := policy.Cut(); got != CutToolRoleMarker {
		t.Fatalf("cut = %q, want %q", got, CutToolRoleMarker)
	}
	// Once cut, the policy never forwards again.
	if got := policy.Feed("更多虚构内容"); got != "" {
		t.Fatalf("text after the cut must be dropped, got %q", got)
	}
}

func TestContentPolicyCutsRepeatLoop(t *testing.T) {
	policy := ContentPolicy{}
	if got := policy.Feed("visible answer"); got != "visible answer" {
		t.Fatalf("content before the loop must be forwarded, got %q", got)
	}
	for i := 0; i < DefaultRepeatLoopThreshold; i++ {
		policy.Feed("</|EPSE|invoke>")
	}
	if got := policy.Cut(); got != CutRepeatLoop {
		t.Fatalf("cut = %q, want %q", got, CutRepeatLoop)
	}
}

// A fragment that repeats closing tags to the threshold still forwards that
// fragment — the cut is reported to the caller, which stops reading — and every
// later fragment is dropped, so a role marker arriving afterwards never gets to
// claim the cut.
func TestContentPolicyPrefersRepeatLoopOverToolRoleMarker(t *testing.T) {
	policy := ContentPolicy{}
	for i := 0; i < DefaultRepeatLoopThreshold; i++ {
		policy.Feed("</|EPSE|invoke>")
	}
	if got := policy.Cut(); got != CutRepeatLoop {
		t.Fatalf("cut = %q, want %q", got, CutRepeatLoop)
	}
	if got := policy.Feed("[Tool]:编造内容"); got != "" {
		t.Fatalf("nothing may be forwarded once the loop tripped, got %q", got)
	}
	if got := policy.Cut(); got != CutRepeatLoop {
		t.Fatalf("a later rule must not replace the reported cut, got %q", got)
	}
}

func TestContentPolicyCutReasonLogMessages(t *testing.T) {
	if got := CutRepeatLoop.LogMessage(); !strings.Contains(got, "repetition loop") {
		t.Fatalf("repeat-loop message = %q", got)
	}
	if got := CutToolRoleMarker.LogMessage(); !strings.Contains(got, "[Tool]:") {
		t.Fatalf("role-marker message = %q", got)
	}
	if got := CutNone.LogMessage(); got != "" {
		t.Fatalf("CutNone must have no message, got %q", got)
	}
}
