package shared

import (
	"net/http"
	"strings"
)

// ModerationBlockedThinkingPhrase is the refusal text the upstream emits in the
// reasoning/thinking channel when the input content is intercepted by review.
const ModerationBlockedThinkingPhrase = "你好，这个问题我暂时无法回答，让我们换个话题再聊聊吧"

// IsModerationBlockedThinking reports whether the thinking chain carries the
// upstream moderation refusal text.
func IsModerationBlockedThinking(thinking string) bool {
	return strings.Contains(thinking, ModerationBlockedThinkingPhrase)
}

func ShouldWriteUpstreamEmptyOutputError(text, thinking string) bool {
	return strings.TrimSpace(text) == ""
}

func UpstreamEmptyOutputDetail(contentFilter bool, text, thinking string) (int, string, string) {
	_ = text
	if contentFilter {
		return http.StatusBadRequest, "Upstream content filtered the response and returned no output.", "content_filter"
	}
	if thinking != "" {
		if IsModerationBlockedThinking(thinking) {
			return http.StatusBadRequest, "输入内容被审核拦截", "input_content_blocked"
		}
		return http.StatusTooManyRequests, "Upstream account hit a rate limit and returned reasoning without visible output.", "upstream_empty_output"
	}
	return http.StatusServiceUnavailable, "Upstream service is unavailable and returned no output.", "upstream_unavailable"
}

func WriteUpstreamEmptyOutputError(w http.ResponseWriter, text, thinking string, contentFilter bool) bool {
	if !ShouldWriteUpstreamEmptyOutputError(text, thinking) {
		return false
	}
	status, message, code := UpstreamEmptyOutputDetail(contentFilter, text, thinking)
	WriteOpenAIErrorWithCode(w, status, message, code)
	return true
}
