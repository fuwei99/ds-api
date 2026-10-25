'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const chatStream = require('../../api/chat-stream.js');
const deepseekConstants = require('../../internal/js/shared/deepseek-constants.js');
const { parseToolCallsDetailed, parseStandaloneToolCallsDetailed } = require('../../internal/js/helpers/stream-tool-sieve.js');
const {
  CUT_NONE,
  createContentPolicy,
  feedContentPolicy,
  contentPolicyCut,
} = require('../../internal/js/chat-stream/content_policy.js');
const { createMarkerNormalizer } = require('../../internal/js/chat-stream/marker.js');

const { parseChunkForContent, estimateTokens } = chatStream.__test;

const compatRoot = path.resolve(__dirname, '../../tests/compat');

function readJSON(filePath) {
  return JSON.parse(fs.readFileSync(filePath, 'utf8'));
}

test('js shared constants derive client headers from shared json', () => {
  const shared = readJSON(path.resolve(__dirname, '../../internal/deepseek/protocol/constants_shared.json'));
  const client = shared.client;
  assert.equal(deepseekConstants.CLIENT_VERSION, client.version);
  assert.equal(deepseekConstants.BASE_HEADERS['x-client-version'], client.version);
  assert.equal(deepseekConstants.BASE_HEADERS['x-client-platform'], 'web');
  // 真实网页版抓包确认 platform=web 同样携带此头。
  assert.equal(deepseekConstants.BASE_HEADERS['x-client-bundle-id'], 'com.deepseek.chat');
  assert.equal(deepseekConstants.BASE_HEADERS['Content-Type'], 'application/json');
  // web 平台应使用 Chrome User-Agent，与 Go 侧 utls.HelloChrome_Auto 一致。
  assert.ok(deepseekConstants.BASE_HEADERS['User-Agent'].includes('Chrome'), `unexpected user agent=${deepseekConstants.BASE_HEADERS['User-Agent']}`);
  for (const h of ['sec-ch-ua', 'sec-ch-ua-mobile', 'sec-ch-ua-platform', 'sec-fetch-site', 'sec-fetch-mode', 'sec-fetch-dest', 'Referer', 'Origin', 'Accept-Language']) {
    assert.ok(deepseekConstants.BASE_HEADERS[h], `expected browser header missing: ${h}`);
  }
  for (const h of ['accept-encoding', 'accept-charset']) {
    assert.equal(deepseekConstants.BASE_HEADERS[h], undefined, `unexpected header present: ${h}`);
  }
});

test('js compat: sse fixtures', () => {
  const fixtureDir = path.join(compatRoot, 'fixtures', 'sse_chunks');
  const expectedDir = path.join(compatRoot, 'expected');
  const files = fs.readdirSync(fixtureDir).filter((f) => f.endsWith('.json')).sort();
  assert.ok(files.length > 0);

  for (const file of files) {
    const name = file.replace(/\.json$/i, '');
    const fixture = readJSON(path.join(fixtureDir, file));
    const expected = readJSON(path.join(expectedDir, `sse_${name}.json`));
    const got = parseChunkForContent(fixture.chunk, Boolean(fixture.thinking_enabled), fixture.current_type || 'text');
    assert.deepEqual(got.parts, expected.parts, `${name}: parts mismatch`);
    assert.equal(got.finished, expected.finished, `${name}: finished mismatch`);
    assert.equal(got.newType, expected.new_type, `${name}: newType mismatch`);
    assert.equal(Boolean(got.contentFilter), Boolean(expected.content_filter), `${name}: contentFilter mismatch`);
    assert.equal(got.errorMessage || '', expected.error_message || '', `${name}: errorMessage mismatch`);
  }
});

test('js compat: toolcall fixtures', () => {
  const fixtureDir = path.join(compatRoot, 'fixtures', 'toolcalls');
  const expectedDir = path.join(compatRoot, 'expected');
  const files = fs.readdirSync(fixtureDir).filter((f) => f.endsWith('.json')).sort();
  assert.ok(files.length > 0);

  for (const file of files) {
    const name = file.replace(/\.json$/i, '');
      const fixture = readJSON(path.join(fixtureDir, file));
      const expected = readJSON(path.join(expectedDir, `toolcalls_${name}.json`));
      const mode = typeof fixture.mode === 'string' ? fixture.mode.trim().toLowerCase() : '';
      const parser = mode === 'standalone' ? parseStandaloneToolCallsDetailed : parseToolCallsDetailed;
      const got = parser(fixture.text, fixture.tool_names || []);
      assert.deepEqual(got.calls, expected.calls, `${name}: calls mismatch`);
      assert.equal(got.sawToolCallSyntax, expected.sawToolCallSyntax, `${name}: sawToolCallSyntax mismatch`);
      assert.equal(got.rejectedByPolicy, expected.rejectedByPolicy, `${name}: rejectedByPolicy mismatch`);
      assert.deepEqual(got.rejectedToolNames, expected.rejectedToolNames, `${name}: rejectedToolNames mismatch`);
    }
  });

test('js compat: token fixtures', () => {
  const fixture = readJSON(path.join(compatRoot, 'fixtures', 'token_cases.json'));
  const expected = readJSON(path.join(compatRoot, 'expected', 'token_cases.json'));
  const expectedByName = new Map(expected.cases.map((c) => [c.name, c.tokens]));
  for (const c of fixture.cases) {
    assert.ok(expectedByName.has(c.name), `missing expected case: ${c.name}`);
    const got = estimateTokens(c.text);
    assert.equal(got, expectedByName.get(c.name), `${c.name}: tokens mismatch`);
  }
});

// Replays the same fixtures as TestGoCompatContentPolicyFixtures in
// internal/compat/content_policy_compat_test.go. Both runtimes must agree
// fragment-for-fragment, so a cut rule that changes on one side without the
// other fails here instead of drifting silently into production.
test('js compat: content policy fixtures', () => {
  const fixtureDir = path.join(compatRoot, 'fixtures', 'content_policy');
  const expectedDir = path.join(compatRoot, 'expected');
  const files = fs.readdirSync(fixtureDir).filter((f) => f.endsWith('.json')).sort();
  assert.ok(files.length > 0);

  for (const file of files) {
    const name = file.replace(/\.json$/i, '');
    const fixture = readJSON(path.join(fixtureDir, file));
    const expected = readJSON(path.join(expectedDir, `content_policy_${name}.json`));
    assert.equal(
      fixture.fragments.length,
      expected.forwarded.length,
      `${name}: fixture has ${fixture.fragments.length} fragments but ${expected.forwarded.length} expected forwards`,
    );

    const policy = createContentPolicy();
    fixture.fragments.forEach((fragment, i) => {
      const got = feedContentPolicy(policy, fragment);
      assert.equal(got, expected.forwarded[i], `${name}: fragment ${i} (${fragment}) forwarded ${JSON.stringify(got)}, want ${JSON.stringify(expected.forwarded[i])}`);
    });
    assert.equal(contentPolicyCut(policy), expected.cut, `${name}: cut mismatch`);
    assert.equal(policy.cut === CUT_NONE, expected.cut === CUT_NONE, `${name}: CUT_NONE agreement`);
  }
});

// Replays the same fixtures as TestGoCompatToolMarkerFixtures in
// internal/compat/tool_marker_compat_test.go. The tool-call marker normalizer is
// the one part of the per-API-key EPSE randomization implemented twice (Go
// internal/toolcall/marker.go, Node internal/js/chat-stream/marker.js); both
// runtimes must agree chunk-for-chunk or a half marker leaks into visible text /
// a tool call is lost on one side only.
test('js compat: tool marker fixtures', () => {
  const fixtureDir = path.join(compatRoot, 'fixtures', 'tool_marker');
  const expectedDir = path.join(compatRoot, 'expected');
  const files = fs.readdirSync(fixtureDir).filter((f) => f.endsWith('.json')).sort();
  assert.ok(files.length > 0);

  for (const file of files) {
    const name = file.replace(/\.json$/i, '');
    const fixture = readJSON(path.join(fixtureDir, file));
    const expected = readJSON(path.join(expectedDir, `tool_marker_${name}.json`));
    assert.equal(
      fixture.chunks.length,
      expected.writes.length,
      `${name}: fixture has ${fixture.chunks.length} chunks but ${expected.writes.length} expected writes`,
    );

    const normalizer = createMarkerNormalizer(fixture.marker);
    let output = '';
    fixture.chunks.forEach((chunk, i) => {
      const got = normalizer.write(chunk);
      assert.equal(got, expected.writes[i], `${name}: chunk ${i} (${JSON.stringify(chunk)}) wrote ${JSON.stringify(got)}, want ${JSON.stringify(expected.writes[i])}`);
      output += got;
    });
    const flushed = normalizer.flush();
    assert.equal(flushed, expected.flush, `${name}: flush mismatch`);
    output += flushed;
    assert.equal(output, expected.output, `${name}: reassembled output mismatch`);
  }
});
