'use strict';

// The ordered set of cut rules applied to the raw text channel of one upstream
// stream, mirroring internal/sse/content_policy.go. Keep the rule order and the
// reason strings in sync with the Go side.
//
// Text is fed in arbitrary SSE fragments. feedContentPolicy returns the prefix
// that is safe to forward downstream; once any rule fires it returns '' forever,
// and contentPolicyCut reports which rule fired so the caller can stop reading
// upstream and run its normal finish path.
//
// Rules are evaluated in declaration order and the first one that fires wins, so
// a fragment that both repeats closing tags and introduces a role marker reports
// the repetition loop.
//
// Adding a cut rule is a change to this file (plus the guard it uses): declare
// the guard, feed it in feedContentPolicy, and give it a reason. Every caller
// picks it up through contentPolicyCut.

const {
  createRepeatLoopGuard,
  feedRepeatLoopGuard,
} = require('./repeat_guard');
const {
  createToolRoleMarkerGuard,
  feedToolRoleMarkerGuard,
} = require('./tool_role_marker_guard');

const CUT_NONE = '';
const CUT_REPEAT_LOOP = 'repeat_loop';
const CUT_TOOL_ROLE_MARKER = 'tool_role_marker';

function createContentPolicy() {
  return {
    repeatGuard: createRepeatLoopGuard(),
    toolRoleGuard: createToolRoleMarkerGuard(),
    cut: CUT_NONE,
  };
}

// feedContentPolicy consumes the next raw text fragment and returns the prefix
// that is safe to forward downstream. A trailing fragment that could still grow
// into a matched pattern is withheld until a later feed.
function feedContentPolicy(policy, text) {
  if (!policy || policy.cut !== CUT_NONE) {
    return '';
  }
  const kept = feedToolRoleMarkerGuard(policy.toolRoleGuard, text);
  const roleCut = policy.toolRoleGuard.tripped === true;
  if (kept) {
    feedRepeatLoopGuard(policy.repeatGuard, kept);
  }
  if (policy.repeatGuard.tripped) {
    // The repetition rule wins when both fire, matching the Go side.
    policy.cut = CUT_REPEAT_LOOP;
  } else if (roleCut) {
    policy.cut = CUT_TOOL_ROLE_MARKER;
  }
  return kept;
}

// feedContentPolicyTail consumes the end-of-stream tail — the fragment the marker
// normalizer withheld and releases once the upstream stream has ended — and routes
// it to the repeat-loop rule only. Nothing is forwarded by this call; the caller
// appends the tail to the accumulated text itself.
//
// The tail must never be fed to the role-marker guard. It is only ever a proper
// prefix of the caller's tool marker, whose alphabet contains no `[`, so it can
// neither complete nor extend `[Tool]:`. Feeding it to that guard would instead
// concatenate it with the fragment the guard is deliberately withholding and
// release that fragment as forwardable text, leaking a marker prefix such as
// `[To` into the client content — the exact leak the guard exists to prevent. The
// Go runtime never feeds an end-of-stream tail at all, so skipping the role guard
// here also keeps the two runtimes aligned.
//
// The repeat-loop rule still sees the tail, so a repetition that only completes at
// the end of the stream is caught exactly as it was before the policy existed.
function feedContentPolicyTail(policy, text) {
  if (!policy || !text) {
    return;
  }
  feedRepeatLoopGuard(policy.repeatGuard, text);
  if (policy.cut === CUT_NONE && policy.repeatGuard.tripped) {
    policy.cut = CUT_REPEAT_LOOP;
  }
}

// contentPolicyCut reports which rule ended forwarding, or CUT_NONE while the
// policy is still forwarding.
function contentPolicyCut(policy) {
  return policy ? policy.cut : CUT_NONE;
}

module.exports = {
  CUT_NONE,
  CUT_REPEAT_LOOP,
  CUT_TOOL_ROLE_MARKER,
  createContentPolicy,
  feedContentPolicy,
  feedContentPolicyTail,
  contentPolicyCut,
};
