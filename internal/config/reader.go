package config

// The request path reads configuration through a small set of purpose-named
// read-only views declared here. A consumer composes the views it actually
// needs:
//
//	type ConfigReader interface {
//		config.ModelAliasReader
//		config.CurrentInputFileReader
//	}
//
// instead of re-listing the individual accessors. Adding an accessor to an
// existing view is then a change to this file alone — no protocol package and
// no test stub has to be touched — and a consumer's dependency stays honest:
// the views it names are the settings it can read.
//
// ModelAliasReader is declared in models.go next to model resolution.

// CurrentInputFileReader exposes the "current input file" injection settings.
type CurrentInputFileReader interface {
	CurrentInputFileEnabled() bool
	CurrentInputFileMinChars() int
}

// ThinkingInjectionReader exposes the thinking-injection settings.
type ThinkingInjectionReader interface {
	ThinkingInjectionEnabled() bool
	ThinkingInjectionPrompt() string
}

// ExpertPromptSegmentReader exposes the expert prompt segmentation settings.
type ExpertPromptSegmentReader interface {
	ExpertPromptSegmentEnabled() bool
	ExpertPromptSegmentMaxChars() int
}

// ExpertTextFileInlineReader exposes the expert text-file inline settings.
type ExpertTextFileInlineReader interface {
	ExpertTextFileInlineEnabled() bool
	ExpertTextFileInlineMaxFileBytes() int
}

// ToolcallPolicyReader exposes the tool-call policy settings.
type ToolcallPolicyReader interface {
	ToolcallMode() string
	ToolcallEarlyEmitConfidence() string
}

// ResponsesStoreReader exposes the Responses API store settings.
type ResponsesStoreReader interface {
	ResponsesStoreTTLSeconds() int
}

// EmbeddingsReader exposes the embeddings provider setting.
type EmbeddingsReader interface {
	EmbeddingsProvider() string
}

// AutoDeleteReader exposes the session auto-delete settings.
type AutoDeleteReader interface {
	AutoDeleteMode() string
	AutoDeleteSessions() bool
}
