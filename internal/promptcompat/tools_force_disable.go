package promptcompat

// toolsForceDisabledRequestKey is an internal request-map marker set by
// DisableToolsForRequest. It is never forwarded downstream: the normalized
// request map only feeds prompt building and payload assembly, and this key is
// not part of the completion payload.
const toolsForceDisabledRequestKey = "_tools_force_disabled"

// toolDefinitionFields are the body fields that declare callable tools. They are
// removed wholesale so no downstream stage can pick a tool schema back up.
var toolDefinitionFields = []string{"tools", "tool_choice", "functions", "function_call"}

// DisableToolsForRequest enforces the per-API-key "force disable tool calling"
// policy on an already adapter-normalized request body.
//
// It is the single entry point for the policy so every protocol adapter shares
// one behavior:
//
//  1. Every tool definition field (`tools`, `tool_choice`, `functions`,
//     `function_call`) is removed, so the request carries no tool definition to
//     inject, upload as TOOLS.txt, or pass through.
//  2. The request is marked as force-disabled. RequestBodyHasTools reports false
//     for a marked request, so the system-text keyword scan ("tools" / "tool" /
//     "functions" inside a role=system message) can no longer classify the
//     request as a tools request, and the account pool routing follows the
//     no-tools path.
//  3. Tool prompt injection is skipped: the normalizers resolve the tool policy
//     to ToolChoiceNone, which makes both the tool description/instruction
//     injection and the tool-call format spec injection no-ops.
func DisableToolsForRequest(req map[string]any) {
	if req == nil {
		return
	}
	for _, field := range toolDefinitionFields {
		delete(req, field)
	}
	req[toolsForceDisabledRequestKey] = true
}

// ToolsForceDisabledInRequest reports whether DisableToolsForRequest was applied
// to the given normalized request body.
func ToolsForceDisabledInRequest(req map[string]any) bool {
	if len(req) == 0 {
		return false
	}
	disabled, _ := req[toolsForceDisabledRequestKey].(bool)
	return disabled
}
