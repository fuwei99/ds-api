package toolcall

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// The three real-world malformed shapes this file pins down all stem from the
// same root cause (a parameter whose CDATA has no usable `]]>`), but they degrade
// differently under the greedy scan and are therefore covered separately:
//
//	swallowedStructure  — one param lacks `]]>`, greedy runs on to the NEXT
//	                      param's `]]>` and eats the shell between them
//	truncatedClose      — `]]` present but `>` missing on every param
//	missingCloseEntirely— trailing params have no close marker at all, greedy
//	                      skips them and leaks the raw bodies into the prompt

// realWorldMissingCloseBadCode is a `patch` call whose first two parameters are
// well-formed but whose two large parameters (`old_string` / `new_string`) have
// no `]]>` at all. Since no `]]>` follows anywhere in the text, the greedy scan
// skips both openers and their ~600-byte bodies land verbatim in the repair
// prompt — the exact token-blowup the slot mechanism exists to prevent.
const realWorldMissingCloseBadCode = `<|EPSE|tool_calls>
  <|EPSE|invoke name="patch">
    <|EPSE|parameter name="mode"><![CDATA[replace]]></|EPSE|parameter>
    <|EPSE|parameter name="path"><![CDATA[/home/u/proj/src/pixel-image-client.mjs]]></|EPSE|parameter>
    <|EPSE|parameter name="old_string"><![CDATA[/
Pixel sprite-sheet image generator — NovelAI-compatible (latent.moe).
NON-gemini. Uses Playwright headed + xvfb to pass Cloudflare, then fetches
/api/novelai/ai/generate-image with a Bearer key. Returns a PNG buffer.*
Env:
LATENT_MOE_KEY      — Bearer token (latsk…), from GitHub secret
PIXEL_IMG_STEPS     — steps (default 14, latent.moe clamps 8-16)/

import { chromium } from "playwright";
import AdmZip from "adm-zip";

const KEY = process.env.LATENT_MOE_KEY || "";</|EPSE|parameter>
    <|EPSE|parameter name="new_string"><![CDATA[/
Pixel sprite-sheet image generator — NovelAI-compatible (latent.moe).
NON-gemini. Uses Playwright headed + xvfb to pass Cloudflare, then fetches
/api/novelai/ai/generate-image with a Bearer key. Returns a PNG buffer.
playwright / adm-zip are lazy-loaded inside generateSpriteSheet() so that
local no-network tests don't need them installed.*
Env:
LATENT_MOE_KEY      — Bearer token (latsk…), from GitHub secret
PIXEL_IMG_STEPS     — steps (default 14, latent.moe clamps 8-16)*/

const KEY = process.env.LATENT_MOE_KEY || "";</|EPSE|parameter>
  </|EPSE|invoke>
</|EPSE|tool_calls>`

// TestMissingCDATACloseEntirelyIsSlotted covers the "no close marker at all"
// shape: greedy leaks the large bodies, bounded slots all four parameters, and
// the round trip is byte-exact.
func TestMissingCDATACloseEntirelyIsSlotted(t *testing.T) {
	// Precondition: the deterministic parser gets nothing but flags the intent,
	// so the repair path is what has to handle this.
	parsed := parseToolCallsDetailedXMLOnly(realWorldMissingCloseBadCode)
	if len(parsed.Calls) != 0 || !parsed.SawToolCallIntent {
		t.Fatalf("precondition: want 0 calls + intent, got calls=%d intent=%v",
			len(parsed.Calls), parsed.SawToolCallIntent)
	}

	greedy := slotCDATAContent(realWorldMissingCloseBadCode)
	if len(greedy.slots) != 2 {
		t.Fatalf("precondition: greedy slot count = %d, want 2 (both long bodies skipped)", len(greedy.slots))
	}
	if !strings.Contains(greedy.skeleton, "import { chromium }") {
		t.Fatal("precondition: greedy should leak the raw parameter body")
	}

	bounded := slotCDATAContentBounded(realWorldMissingCloseBadCode)
	if len(bounded.slots) != 4 {
		t.Fatalf("bounded slot count = %d, want 4", len(bounded.slots))
	}
	if strings.Contains(bounded.skeleton, "import { chromium }") {
		t.Fatalf("raw body leaked into the bounded skeleton:\n%s", bounded.skeleton)
	}
	if got := strings.Count(bounded.skeleton, `<|EPSE|parameter name=`); got != 4 {
		t.Fatalf("parameter nodes in skeleton = %d, want 4", got)
	}
	// The two well-formed parameters keep their `]]>`; the two defective ones
	// keep the defect visible so the model can see what to fix.
	if got := strings.Count(bounded.skeleton, "]]></|EPSE|parameter>"); got != 2 {
		t.Fatalf("well-formed closes in skeleton = %d, want 2", got)
	}
	if got := strings.Count(bounded.skeleton, "DS_SLOT_2</|EPSE|parameter>"); got != 1 {
		t.Fatalf("defective close for slot 2 not preserved:\n%s", bounded.skeleton)
	}

	restored, err := restoreCDATASlotsFor(bounded.skeleton, bounded)
	if err != nil {
		t.Fatalf("restore error: %v", err)
	}
	if restored != realWorldMissingCloseBadCode {
		t.Fatal("bounded slot round trip is not byte-exact")
	}

	degraded, reason := greedySlottingDegraded(greedy, bounded)
	if !degraded || reason != "unslotted_unclosed_cdata" {
		t.Fatalf("degraded=%v reason=%q, want true/unslotted_unclosed_cdata", degraded, reason)
	}
}

// TestRepairToolCallsWithLLMRecoversMissingCloseEntirely is the end-to-end
// result: a model that supplies the two missing `]]>` yields one complete patch
// call whose four parameter values are byte-identical to the originals.
func TestRepairToolCallsWithLLMRecoversMissingCloseEntirely(t *testing.T) {
	bounded := slotCDATAContentBounded(realWorldMissingCloseBadCode)
	var seen string
	invoke := func(_ context.Context, prompt string) (string, error) {
		seen = prompt
		skeleton := prompt[strings.LastIndex(prompt, "<|EPSE|tool_calls>"):]
		for i := range bounded.slots {
			ph := "DS_SLOT_" + strconv.Itoa(i)
			skeleton = strings.ReplaceAll(skeleton,
				ph+"</|EPSE|parameter>", ph+"]]></|EPSE|parameter>")
		}
		return skeleton, nil
	}

	calls, ok := RepairToolCallsWithLLM(context.Background(), realWorldMissingCloseBadCode, invoke)
	if !ok || len(calls) != 1 {
		t.Fatalf("expected one repaired call, got ok=%v calls=%d", ok, len(calls))
	}
	if calls[0].Name != "patch" {
		t.Fatalf("call name = %q, want patch", calls[0].Name)
	}
	if len(calls[0].Input) != 4 {
		t.Fatalf("parameter count = %d, want 4 (input=%#v)", len(calls[0].Input), calls[0].Input)
	}
	// Every value must be byte-identical to the corresponding slot body.
	wantValues := map[string]string{
		"mode":       bounded.slots[0],
		"path":       bounded.slots[1],
		"old_string": bounded.slots[2],
		"new_string": bounded.slots[3],
	}
	for key, want := range wantValues {
		got, _ := calls[0].Input[key].(string)
		if got != want {
			t.Fatalf("parameter %q not restored byte-exactly:\n got (%d): %q\nwant (%d): %q",
				key, len(got), got, len(want), want)
		}
	}
	// old_string and new_string differ only by the lazy-load sentence; make sure
	// the restore did not collapse them into the same value.
	if calls[0].Input["old_string"] == calls[0].Input["new_string"] {
		t.Fatal("old_string and new_string must stay distinct")
	}
	if strings.Contains(seen, "import { chromium }") {
		t.Fatal("prompt must not carry raw parameter bodies")
	}
}

// TestRepairToolCallsWithLLMMissingCloseNotFixedDegrades pins the safety side of
// this shape: a model that copies the skeleton without supplying `]]>` must
// degrade cleanly instead of emitting a call with DS_SLOT_ literals as values.
func TestRepairToolCallsWithLLMMissingCloseNotFixedDegrades(t *testing.T) {
	invoke := func(_ context.Context, prompt string) (string, error) {
		return prompt[strings.LastIndex(prompt, "<|EPSE|tool_calls>"):], nil
	}
	calls, ok := RepairToolCallsWithLLM(context.Background(), realWorldMissingCloseBadCode, invoke)
	if ok || len(calls) != 0 {
		t.Fatalf("expected clean degradation, got ok=%v calls=%#v", ok, calls)
	}
}
