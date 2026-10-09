package toolcall

import (
	"fmt"
	"strings"
)

// restoreCDATASlots reverses slotCDATAContent on the repaired text using the
// greedy CDATA interpretation. See restoreCDATASlotsWith for the semantics.
func restoreCDATASlots(repaired string, slots []string, token cdataSlotToken) (string, error) {
	return restoreCDATASlotsWith(repaired, slots, token, scanClosedCDATARanges)
}

// restoreCDATASlotsFor reverses the substitution recorded in res, using the same
// CDATA scan that produced it. Mixing scans corrupts the result: a bounded
// skeleton can carry element-bounded unclosed CDATA, and a greedy rescan would
// run past those openers and swallow later placeholders.
func restoreCDATASlotsFor(repaired string, res cdataSlotResult) (string, error) {
	if res.bounded {
		return restoreCDATASlotsWith(repaired, res.slots, res.token, scanBoundedCDATARanges)
	}
	return restoreCDATASlotsWith(repaired, res.slots, res.token, scanClosedCDATARanges)
}

// restoreCDATASlotsWith rescans the repaired text for CDATA regions with the
// given scanner (this is the slot mechanism's own scan, distinct from the repair
// layer, which must never re-judge CDATA boundaries) and, for each region whose
// content is a placeholder, substitutes the original bytes back.
//
// It returns an error (so the caller can drop the whole slot path and fall back
// to the raw, no-slot recovery) when:
//   - a placeholder token appears outside any CDATA region (repair moved it),
//   - a CDATA region carries a stray placeholder prefix without matching a slot,
//   - a placeholder is matched more than once, or
//   - some slot placeholder is missing after repair.
//
// Because matched content is substituted with the exact original bytes, the
// restored CDATA is byte-identical to the input by construction; a mismatch can
// only surface as one of the error conditions above.
func restoreCDATASlotsWith(repaired string, slots []string, token cdataSlotToken, scan func(string) []cdataRange) (string, error) {
	if len(slots) == 0 {
		return repaired, nil
	}
	prefix := token.literalPrefix()
	used := make([]bool, len(slots))

	var b strings.Builder
	b.Grow(len(repaired))
	prev := 0
	for _, r := range scan(repaired) {
		if r.openStart < prev {
			continue
		}
		if strings.Contains(repaired[prev:r.openStart], prefix) {
			return "", fmt.Errorf("cdata slot: placeholder token leaked outside CDATA region")
		}
		// Copy verbatim up to and including the CDATA open marker.
		b.WriteString(repaired[prev:r.contentStart])
		restored, err := matchCDATASlotContent(repaired[r.contentStart:r.contentEnd], slots, used, token)
		if err != nil {
			return "", err
		}
		b.WriteString(restored)
		// Copy the close marker verbatim (empty for an unclosed range).
		b.WriteString(repaired[r.contentEnd : r.contentEnd+r.closeLen])
		prev = r.contentEnd + r.closeLen
	}
	if strings.Contains(repaired[prev:], prefix) {
		return "", fmt.Errorf("cdata slot: placeholder token leaked outside CDATA region")
	}
	b.WriteString(repaired[prev:])

	for i, u := range used {
		if !u {
			return "", fmt.Errorf("cdata slot: placeholder %d missing after repair", i)
		}
	}
	return b.String(), nil
}

// matchCDATASlotContent maps a CDATA region's content back to its original
// bytes. A region whose content is a placeholder (ignoring surrounding
// whitespace, which a repair model may reindent) restores that slot; a region
// that merely embeds the prefix without being a clean placeholder is an anomaly
// and errors out; anything else is genuine content passed through untouched.
func matchCDATASlotContent(content string, slots []string, used []bool, token cdataSlotToken) (string, error) {
	trimmed := strings.TrimSpace(content)
	for i := range slots {
		if trimmed == token.format(i) {
			if used[i] {
				return "", fmt.Errorf("cdata slot: placeholder %d matched more than once", i)
			}
			used[i] = true
			return slots[i], nil
		}
	}
	if strings.Contains(content, token.literalPrefix()) {
		return "", fmt.Errorf("cdata slot: unexpected placeholder token inside CDATA content")
	}
	return content, nil
}
