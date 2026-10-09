package completionruntime

import (
	"ds2api/internal/prompt"
	"ds2api/internal/promptcompat"
)

// ExpertPromptSegmentConfigReader is the minimal interface needed to decide
// whether prompt segmentation should run.
type ExpertPromptSegmentConfigReader interface {
	ExpertPromptSegmentEnabled() bool
	ExpertPromptSegmentMaxChars() int
}

// shouldSegmentExpertPrompt returns max-rune segmented prompts when
// segmentation is enabled and the finalized prompt exceeds the configured rune
// threshold. After the 2.4.0 fast/expert/vision merge there is no separate
// expert mode, so segmentation applies to every model. Returns nil when
// segmentation is not applicable.
func shouldSegmentExpertPrompt(stdReq promptcompat.StandardRequest, opts Options) []string {
	segmentReader := opts.ExpertPromptSegment
	if segmentReader == nil {
		return nil
	}
	if !segmentReader.ExpertPromptSegmentEnabled() {
		return nil
	}
	maxChars := segmentReader.ExpertPromptSegmentMaxChars()
	if maxChars <= 0 {
		return nil
	}
	promptText := stdReq.FinalPrompt
	if len([]rune(promptText)) <= maxChars {
		return nil
	}
	segments := prompt.SplitByMaxRunes(promptText, maxChars)
	if len(segments) <= 1 {
		return nil
	}
	return segments
}
