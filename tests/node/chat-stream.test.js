'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { EventEmitter } = require('node:events');

const handler = require('../../api/chat-stream.js');
const { handleVercelStream } = require('../../internal/js/chat-stream/vercel_stream.js');
const {
  createToolSieveState,
  processToolSieveChunk,
  flushToolSieve,
} = require('../../internal/js/helpers/stream-tool-sieve.js');
const {
  setCorsHeaders,
} = require('../../internal/js/chat-stream/http_internal.js');
const {
  REPEAT_LOOP_THRESHOLD,
  createRepeatLoopGuard,
  feedRepeatLoopGuard,
} = require('../../internal/js/chat-stream/repeat_guard.js');

const {
  parseChunkForContent,
  resolveToolcallPolicy,
  formatIncrementalToolCallDeltas,
  normalizePreparedToolNames,
  boolDefaultTrue,
  filterIncrementalToolCallDeltasByAllowed,
  resetStreamToolCallState,
  buildUsage,
  estimateTokens,
  shouldSkipPath,
  isNodeStreamSupportedPath,
  extractPathname,
  trimContinuationOverlap,
  createMarkerNormalizer,
} = handler.__test;

function createMockResponse() {
  const headers = new Map();
  return {
    setHeader(key, value) {
      headers.set(String(key).toLowerCase(), value);
    },
    getHeader(key) {
      return headers.get(String(key).toLowerCase());
    },
  };
}

class MockStreamRequest extends EventEmitter {
  constructor() {
    super();
    this.url = '/v1/chat/completions';
    this.headers = { host: 'example.test', 'content-type': 'application/json' };
  }
}

class MockStreamResponse extends EventEmitter {
  constructor() {
    super();
    this.headers = new Map();
    this.statusCode = 0;
    this.chunks = [];
    this.writableEnded = false;
    this.destroyed = false;
  }

  setHeader(key, value) {
    this.headers.set(String(key).toLowerCase(), value);
  }

  getHeader(key) {
    return this.headers.get(String(key).toLowerCase());
  }

  write(chunk) {
    this.chunks.push(Buffer.isBuffer(chunk) ? chunk.toString('utf8') : String(chunk));
    return true;
  }

  end(chunk) {
    if (chunk) {
      this.write(chunk);
    }
    this.writableEnded = true;
  }

  flushHeaders() {}

  flush() {}

  bodyText() {
    return this.chunks.join('');
  }
}

function jsonResponse(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

function sseResponse(lines) {
  const encoder = new TextEncoder();
  return new Response(new ReadableStream({
    start(controller) {
      for (const line of lines) {
        controller.enqueue(encoder.encode(line));
      }
      controller.close();
    },
  }), {
    status: 200,
    headers: { 'content-type': 'text/event-stream' },
  });
}

function parseSSEDataFrames(body) {
  return body
    .split('\n\n')
    .map((frame) => frame.trim())
    .filter((frame) => frame.startsWith('data:'))
    .map((frame) => frame.slice(5).trim());
}

async function runMockVercelStream(upstreamLines, prepareOverrides = {}) {
  return runMockVercelStreamSequence([upstreamLines], prepareOverrides);
}

async function runMockVercelStreamSequence(upstreamSequences, prepareOverrides = {}) {
  const originalFetch = global.fetch;
  const fetchURLs = [];
  const fetchBodies = [];
  let completionCalls = 0;
  let continueCalls = 0;
  const prepareBody = {
    session_id: 'chatcmpl-test',
    lease_id: 'lease-test',
    model: 'gpt-test',
    final_prompt: 'hello',
    thinking_enabled: false,
    search_enabled: false,
    tool_names: [],
    deepseek_token: 'deepseek-token',
    pow_header: 'pow-header',
    payload: { prompt: 'hello' },
    ...prepareOverrides,
  };
  global.fetch = async (url, init = {}) => {
    const textURL = String(url);
    fetchURLs.push(textURL);
    if (init && init.body) {
      fetchBodies.push(JSON.parse(String(init.body)));
    }
    if (textURL.includes('__stream_prepare=1')) {
      return jsonResponse(prepareBody);
    }
    if (textURL.includes('__stream_pow=1')) {
      return jsonResponse({ pow_header: 'pow-header-refreshed' });
    }
    if (textURL.includes('__stream_release=1')) {
      return jsonResponse({ success: true });
    }
    if (textURL.includes('/continue')) {
      const idx = Math.min(continueCalls + 1, upstreamSequences.length - 1);
      continueCalls += 1;
      return sseResponse(upstreamSequences[idx]);
    }
    const idx = Math.min(completionCalls, upstreamSequences.length - 1);
    completionCalls += 1;
    return sseResponse(upstreamSequences[idx]);
  };
  try {
    const req = new MockStreamRequest();
    const res = new MockStreamResponse();
    const payload = { model: 'gpt-test', stream: true };
    await handleVercelStream(req, res, Buffer.from(JSON.stringify(payload)), payload);
    return { res, frames: parseSSEDataFrames(res.bodyText()), fetchURLs, fetchBodies };
  } finally {
    global.fetch = originalFetch;
  }
}

test('chat-stream exposes parser test hooks', () => {
  assert.equal(typeof parseChunkForContent, 'function');
  assert.equal(typeof resolveToolcallPolicy, 'function');
});

test('vercel stream emits Go-parity empty-output failure on DONE', async () => {
  const { frames } = await runMockVercelStream(['data: [DONE]\n\n']);
  assert.equal(frames.length, 2);
  const failed = JSON.parse(frames[0]);
  assert.equal(failed.status_code, 503);
  assert.equal(failed.error.type, 'service_unavailable_error');
  assert.equal(failed.error.code, 'upstream_unavailable');
  assert.equal(frames[1], '[DONE]');
});

test('vercel stream retries empty output once and keeps one terminal frame', async () => {
  const { frames, fetchURLs, fetchBodies } = await runMockVercelStreamSequence([
    ['data: [DONE]\n\n'],
    ['data: {"p":"response/content","v":"visible"}\n\n', 'data: [DONE]\n\n'],
  ]);
  const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
  const completionBodies = fetchBodies.filter((body) => Object.hasOwn(body, 'prompt'));
  assert.equal(fetchURLs.filter((url) => url === 'https://chat.deepseek.com/api/v0/chat/completion').length, 2);
  assert.equal(fetchURLs.filter((url) => url.includes('__stream_pow=1')).length, 1);
  assert.equal(frames.filter((frame) => frame === '[DONE]').length, 1);
  assert.equal(parsed[0].choices[0].delta.content, 'visible');
  assert.equal(parsed[1].choices[0].finish_reason, 'stop');
  assert.equal(parsed[0].id, parsed[1].id);
  assert.match(completionBodies[1].prompt, /Previous reply had no visible output\. Please regenerate the visible final answer or tool call now\.$/);
});

test('vercel stream retries thinking-only output once', async () => {
  const { frames, fetchURLs, fetchBodies } = await runMockVercelStreamSequence([
    ['data: {"response_message_id":42,"p":"response/thinking_content","v":"plan"}\n\n', 'data: [DONE]\n\n'],
    ['data: {"p":"response/content","v":"visible"}\n\n', 'data: [DONE]\n\n'],
  ], { thinking_enabled: true });
  const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
  const completionBodies = fetchBodies.filter((body) => Object.hasOwn(body, 'prompt'));
  assert.equal(fetchURLs.filter((url) => url === 'https://chat.deepseek.com/api/v0/chat/completion').length, 2);
  assert.equal(frames.filter((frame) => frame === '[DONE]').length, 1);
  assert.equal(completionBodies[1].parent_message_id, 42);
  assert.equal(parsed[0].choices[0].delta.reasoning_content, 'plan');
  assert.equal(parsed[1].choices[0].delta.content, 'visible');
  assert.equal(parsed[2].choices[0].finish_reason, 'stop');
});

test('vercel stream reports input moderation block from thinking without switching accounts', async () => {
  const phrase = '你好，这个问题我暂时无法回答，让我们换个话题再聊聊吧';
  const line = `data: {"response_message_id":42,"p":"response/thinking_content","v":${JSON.stringify(phrase)}}\n\n`;
  const { frames, fetchURLs } = await runMockVercelStreamSequence([
    [line, 'data: [DONE]\n\n'],
    [line, 'data: [DONE]\n\n'],
  ], { thinking_enabled: true });
  assert.equal(fetchURLs.filter((url) => url.includes('__stream_switch=1')).length, 0);
  const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
  const failed = parsed.find((frame) => frame.status_code);
  assert.ok(failed, `expected failure frame, frames=${JSON.stringify(frames)}`);
  assert.equal(failed.status_code, 400);
  assert.equal(failed.error.code, 'input_content_blocked');
  assert.equal(failed.error.message, '输入内容被审核拦截');
  assert.equal(frames.at(-1), '[DONE]');
});

test('vercel stream switches managed account after empty retry exhaustion', async () => {
  const originalFetch = global.fetch;
  const fetchURLs = [];
  const completionBodies = [];
  const completionAuth = [];
  let completionCalls = 0;
  global.fetch = async (url, init = {}) => {
    const textURL = String(url);
    fetchURLs.push(textURL);
    if (textURL.includes('__stream_prepare=1')) {
      return jsonResponse({
        session_id: 'chatcmpl-test',
        lease_id: 'lease-test',
        model: 'gpt-test',
        final_prompt: 'hello',
        thinking_enabled: true,
        search_enabled: false,
        tool_names: [],
        deepseek_token: 'token-1',
        pow_header: 'pow-1',
        payload: { chat_session_id: 'session-1', prompt: 'hello', ref_file_ids: ['file-1'] },
      });
    }
    if (textURL.includes('__stream_pow=1')) {
      return jsonResponse({ pow_header: 'pow-retry' });
    }
    if (textURL.includes('__stream_switch=1')) {
      return jsonResponse({
        session_id: 'session-2',
        lease_id: 'lease-test',
        model: 'gpt-test',
        final_prompt: 'hello',
        thinking_enabled: true,
        search_enabled: false,
        tool_names: [],
        deepseek_token: 'token-2',
        pow_header: 'pow-2',
        payload: { chat_session_id: 'session-2', prompt: 'hello', ref_file_ids: ['file-2'] },
      });
    }
    if (textURL.includes('__stream_release=1')) {
      return jsonResponse({ success: true });
    }
    if (textURL === 'https://chat.deepseek.com/api/v0/chat/completion') {
      completionBodies.push(JSON.parse(String(init.body)));
      completionAuth.push(init.headers.authorization);
      completionCalls += 1;
      if (completionCalls <= 2) {
        return sseResponse([`data: {"response_message_id":${40 + completionCalls},"p":"response/thinking_content","v":"plan"}\n\n`, 'data: [DONE]\n\n']);
      }
      return sseResponse(['data: {"p":"response/content","v":"visible"}\n\n', 'data: [DONE]\n\n']);
    }
    throw new Error(`unexpected fetch url: ${textURL}`);
  };
  try {
    const req = new MockStreamRequest();
    const res = new MockStreamResponse();
    const payload = { model: 'gpt-test', stream: true };
    await handleVercelStream(req, res, Buffer.from(JSON.stringify(payload)), payload);
    const frames = parseSSEDataFrames(res.bodyText());
    const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
    assert.equal(fetchURLs.filter((url) => url.includes('__stream_switch=1')).length, 1);
    assert.equal(completionBodies.length, 3);
    assert.match(completionBodies[1].prompt, /Previous reply had no visible output/);
    assert.equal(completionBodies[1].parent_message_id, 41);
    assert.equal(completionBodies[2].prompt, 'hello');
    assert.deepEqual(completionBodies[2].ref_file_ids, ['file-2']);
    assert.deepEqual(completionAuth, ['Bearer token-1', 'Bearer token-1', 'Bearer token-2']);
    assert.equal(parsed.at(-1).choices[0].finish_reason, 'stop');
  } finally {
    global.fetch = originalFetch;
  }
});

test('vercel stream coalesces many small content deltas while keeping one choice', async () => {
  const lines = Array.from({ length: 100 }, () => `data: ${JSON.stringify({ p: 'response/content', v: '字' })}\n\n`);
  lines.push('data: [DONE]\n\n');
  const { frames } = await runMockVercelStream(lines);
  const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
  const contentFrames = parsed.filter((item) => item.choices?.[0]?.delta?.content);
  const content = contentFrames.map((item) => item.choices[0].delta.content).join('');
  assert.equal(content, '字'.repeat(100));
  assert.ok(contentFrames.length < 100, `expected fewer than 100 content frames, got ${contentFrames.length}`);
  for (const item of parsed) {
    assert.equal(item.choices.length, 1);
  }
});

test('vercel stream flushes reasoning before content and before stop', async () => {
  const { frames } = await runMockVercelStream([
    `data: ${JSON.stringify({ p: 'response/fragments', o: 'APPEND', v: [
      { type: 'THINK', content: '思考' },
      { type: 'THINK', content: '过程' },
      { type: 'RESPONSE', content: '回答' },
    ] })}\n\n`,
    'data: [DONE]\n\n',
  ], { thinking_enabled: true });
  const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
  const reasoning = parsed.map((item) => item.choices?.[0]?.delta?.reasoning_content || '').join('');
  const content = parsed.map((item) => item.choices?.[0]?.delta?.content || '').join('');
  assert.equal(reasoning, '思考过程');
  assert.equal(content, '回答');
  assert.equal(parsed.at(-1).choices[0].finish_reason, 'stop');
});

test('vercel stream exhausts DeepSeek continue before synthetic retry', async () => {
  const { frames, fetchURLs, fetchBodies } = await runMockVercelStreamSequence([
    [
      'data: {"response_message_id":7,"v":{"response":{"message_id":7,"status":"WIP","auto_continue":true}}}\n\n',
      'data: [DONE]\n\n',
    ],
    ['data: {"p":"response/content","v":"continued"}\n\n', 'data: [DONE]\n\n'],
  ]);
  const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
  assert.equal(fetchURLs.filter((url) => url === 'https://chat.deepseek.com/api/v0/chat/completion').length, 1);
  assert.equal(fetchURLs.filter((url) => url === 'https://chat.deepseek.com/api/v0/chat/continue').length, 1);
  assert.equal(fetchURLs.filter((url) => url.includes('__stream_pow=1')).length, 1);
  assert.equal(parsed[0].choices[0].delta.content, 'continued');
  assert.equal(parsed[1].choices[0].finish_reason, 'stop');
  assert.equal(fetchBodies.some((body) => String(body.prompt || '').includes('Previous reply had no visible output')), false);
});

function erroringSSEResponse(lines, err) {
  const encoder = new TextEncoder();
  let idx = 0;
  return new Response(new ReadableStream({
    pull(controller) {
      if (idx < lines.length) {
        controller.enqueue(encoder.encode(lines[idx]));
        idx += 1;
        return;
      }
      controller.error(err);
    },
  }), {
    status: 200,
    headers: { 'content-type': 'text/event-stream' },
  });
}

test('vercel stream resumes interrupted partial stream via continue', async () => {
  const originalFetch = global.fetch;
  const fetchURLs = [];
  let continueCalls = 0;
  global.fetch = async (url, init = {}) => {
    const textURL = String(url);
    fetchURLs.push(textURL);
    if (textURL.includes('__stream_prepare=1')) {
      return jsonResponse({
        session_id: 'chatcmpl-resume',
        lease_id: 'lease-test',
        model: 'gpt-test',
        final_prompt: 'hello',
        thinking_enabled: false,
        search_enabled: false,
        tool_names: [],
        deepseek_token: 'token-1',
        pow_header: 'pow-1',
        payload: { chat_session_id: 'session-1', prompt: 'hello' },
      });
    }
    if (textURL.includes('__stream_pow=1')) {
      return jsonResponse({ pow_header: 'pow-2' });
    }
    if (textURL.includes('__stream_release=1')) {
      return jsonResponse({ success: true });
    }
    if (textURL === 'https://chat.deepseek.com/api/v0/chat/continue') {
      continueCalls += 1;
      return sseResponse([
        'data: {"response_message_id":8,"p":"response/content","v":"-resumed-part"}\n\n',
        'data: {"p":"response/status","v":"FINISHED"}\n\n',
      ]);
    }
    if (textURL === 'https://chat.deepseek.com/api/v0/chat/completion') {
      return erroringSSEResponse(
        ['data: {"response_message_id":7,"p":"response/content","v":"partial-code"}\n\n'],
        new Error('connection reset by peer'),
      );
    }
    throw new Error(`unexpected fetch url: ${textURL}`);
  };
  try {
    const req = new MockStreamRequest();
    const res = new MockStreamResponse();
    const payload = { model: 'gpt-test', stream: true };
    await handleVercelStream(req, res, Buffer.from(JSON.stringify(payload)), payload);
    const frames = parseSSEDataFrames(res.bodyText());
    const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
    assert.equal(continueCalls, 1);
    const content = parsed.filter((item) => item.choices?.[0]?.delta?.content).map((item) => item.choices[0].delta.content).join('');
    assert.equal(content, 'partial-code-resumed-part');
    assert.equal(parsed.at(-1).choices[0].finish_reason, 'stop');
    assert.equal(frames.at(-1), '[DONE]');
  } finally {
    global.fetch = originalFetch;
  }
});

test('vercel stream surfaces interruption when continue resume fails', async () => {
  const originalFetch = global.fetch;
  global.fetch = async (url, init = {}) => {
    const textURL = String(url);
    if (textURL.includes('__stream_prepare=1')) {
      return jsonResponse({
        session_id: 'chatcmpl-fail',
        lease_id: 'lease-test',
        model: 'gpt-test',
        final_prompt: 'hello',
        thinking_enabled: false,
        search_enabled: false,
        tool_names: [],
        deepseek_token: 'token-1',
        pow_header: 'pow-1',
        payload: { chat_session_id: 'session-1', prompt: 'hello' },
      });
    }
    if (textURL.includes('__stream_pow=1')) {
      return jsonResponse({ pow_header: 'pow-2' });
    }
    if (textURL.includes('__stream_release=1')) {
      return jsonResponse({ success: true });
    }
    if (textURL === 'https://chat.deepseek.com/api/v0/chat/continue') {
      return jsonResponse({ error: 'rate limited' }, 429);
    }
    if (textURL === 'https://chat.deepseek.com/api/v0/chat/completion') {
      return erroringSSEResponse(
        ['data: {"response_message_id":7,"p":"response/content","v":"partial-code"}\n\n'],
        new Error('connection reset by peer'),
      );
    }
    throw new Error(`unexpected fetch url: ${textURL}`);
  };
  try {
    const req = new MockStreamRequest();
    const res = new MockStreamResponse();
    const payload = { model: 'gpt-test', stream: true };
    await handleVercelStream(req, res, Buffer.from(JSON.stringify(payload)), payload);
    const frames = parseSSEDataFrames(res.bodyText());
    const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
    const errorFrame = parsed.find((item) => item.error);
    assert.ok(errorFrame, `expected an error frame, got ${JSON.stringify(parsed)}`);
    assert.equal(errorFrame.status_code, 502);
    assert.equal(errorFrame.error.code, 'upstream_interrupted');
  } finally {
    global.fetch = originalFetch;
  }
});

test('vercel stream continues direct quasi_status incomplete before final tool call', async () => {
  const { frames, fetchURLs } = await runMockVercelStreamSequence([
    [
      'data: {"response_message_id":7,"p":"response/content","v":"<tool_calls><invoke name=\\"write_file\\"><parameter name=\\"content\\"><![CDATA[part-one"}\n\n',
      'data: {"p":"response/quasi_status","v":"INCOMPLETE"}\n\n',
      'data: [DONE]\n\n',
    ],
    [
      'data: {"response_message_id":8,"p":"response/content","v":"-part-two]]></parameter></invoke></tool_calls>"}\n\n',
      'data: {"p":"response/status","v":"FINISHED"}\n\n',
      'data: [DONE]\n\n',
    ],
  ], { tool_names: ['write_file'] });
  const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
  const toolDelta = parsed.find((item) => item.choices?.[0]?.delta?.tool_calls);
  assert.equal(fetchURLs.filter((url) => url === 'https://chat.deepseek.com/api/v0/chat/continue').length, 1);
  assert.ok(toolDelta);
  const args = JSON.parse(toolDelta.choices[0].delta.tool_calls[0].function.arguments);
  assert.equal(args.content, 'part-one-part-two');
  assert.equal(parsed.at(-1).choices[0].finish_reason, 'tool_calls');
});

test('vercel stream intercepts EPSE tool calls without client tools', async () => {
  const { frames } = await runMockVercelStream([
    'data: {"p":"response/content","v":"<|EPSE|tool_calls><|EPSE|invoke name=\\"search\\"><|EPSE|parameter name=\\"q\\">golang</|EPSE|parameter></|EPSE|invoke></|EPSE|tool_calls>"}\n\n',
    'data: [DONE]\n\n',
  ]);
  const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
  const toolDelta = parsed.find((item) => item.choices?.[0]?.delta?.tool_calls);
  assert.ok(toolDelta, `expected tool_calls delta, frames=${JSON.stringify(frames)}`);
  const content = parsed.map((item) => item.choices?.[0]?.delta?.content || '').join('');
  assert.equal(content.includes('<|EPSE|'), false, `expected no raw EPSE leak, content=${JSON.stringify(content)}`);
  assert.equal(parsed.at(-1).choices[0].finish_reason, 'tool_calls');
});

test('marker normalizer rewrites the caller marker back to EPSE', () => {
  const normalizer = createMarkerNormalizer('Q7ZK3M');
  assert.equal(normalizer.active, true);
  assert.equal(normalizer.write('a <|Q7ZK3M|tool_calls> b'), 'a <|EPSE|tool_calls> b');
  assert.equal(normalizer.flush(), '');
});

test('marker normalizer is inert for empty and canonical markers', () => {
  for (const marker of ['', '   ', 'EPSE', 'epse', undefined, null]) {
    const normalizer = createMarkerNormalizer(marker);
    assert.equal(normalizer.active, false, `marker=${String(marker)}`);
    assert.equal(normalizer.write('<|EPSE|tool_calls>'), '<|EPSE|tool_calls>');
    assert.equal(normalizer.flush(), '');
  }
});

test('marker normalizer rewrites a marker split one character at a time', () => {
  const normalizer = createMarkerNormalizer('Q7ZK3M');
  let out = '';
  for (const ch of '<|Q7ZK3M|tool_calls>') {
    out += normalizer.write(ch);
  }
  out += normalizer.flush();
  assert.equal(out, '<|EPSE|tool_calls>');
});

test('marker normalizer never forwards a partial marker', () => {
  const normalizer = createMarkerNormalizer('Q7ZK3M');
  let out = '';
  for (const ch of '<|Q7ZK3M|invoke>') {
    out += normalizer.write(ch);
    assert.equal(out.includes('Q7Z'), false, `partial marker leaked: ${JSON.stringify(out)}`);
  }
  out += normalizer.flush();
  assert.equal(out, '<|EPSE|invoke>');
});

test('marker normalizer holds a partial marker and releases it on flush', () => {
  const normalizer = createMarkerNormalizer('Q7ZK3M');
  assert.equal(normalizer.write('tail Q7Z'), 'tail ');
  assert.equal(normalizer.flush(), 'Q7Z');
});

test('marker normalizer matches the marker case-insensitively', () => {
  const normalizer = createMarkerNormalizer('q7zk3m');
  assert.equal(normalizer.write('<|q7ZK3m|invoke>'), '<|EPSE|invoke>');
});

test('vercel stream normalizes a marker tool call split across one-char chunks', async () => {
  const marker = 'Q7ZK3M';
  const raw = `<|${marker}|tool_calls><|${marker}|invoke name="search"><|${marker}|parameter name="q">golang</|${marker}|parameter></|${marker}|invoke></|${marker}|tool_calls>`;
  const lines = [...raw].map((ch) => `data: ${JSON.stringify({ p: 'response/content', v: ch })}\n\n`);
  lines.push('data: [DONE]\n\n');

  const { frames } = await runMockVercelStream(lines, { tool_marker: marker });
  const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
  const toolDelta = parsed.find((item) => item.choices?.[0]?.delta?.tool_calls);
  assert.ok(toolDelta, `expected tool_calls delta, frames=${JSON.stringify(frames)}`);
  const args = JSON.parse(toolDelta.choices[0].delta.tool_calls[0].function.arguments);
  assert.equal(args.q, 'golang');
  const content = parsed.map((item) => item.choices?.[0]?.delta?.content || '').join('');
  assert.equal(content.includes(marker), false, `expected no marker leak, content=${JSON.stringify(content)}`);
  assert.equal(content.includes('<|EPSE|'), false, `expected no EPSE leak, content=${JSON.stringify(content)}`);
  assert.equal(parsed.at(-1).choices[0].finish_reason, 'tool_calls');
});

test('vercel stream does not leak the marker into visible prose', async () => {
  const { frames } = await runMockVercelStream([
    'data: {"p":"response/content","v":"see <|Q7ZK3M|tool_calls> in the docs"}\n\n',
    'data: [DONE]\n\n',
  ], { tool_marker: 'Q7ZK3M' });
  const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
  const content = parsed.map((item) => item.choices?.[0]?.delta?.content || '').join('');
  assert.equal(content.includes('Q7ZK3M'), false, `expected no marker leak, content=${JSON.stringify(content)}`);
  assert.equal(content.includes('in the docs'), true, `expected prose kept, content=${JSON.stringify(content)}`);
});

test('vercel stream drops text after tool call block', async () => {
  const { frames } = await runMockVercelStream([
    'data: {"p":"response/content","v":"前置文本\\n<tool_calls>\\n  <invoke name=\\"read_file\\">\\n    <parameter name=\\"path\\">README.MD</parameter>\\n  </invoke>\\n</tool_calls>\\n尾巴文本"}\n\n',
    'data: [DONE]\n\n',
  ]);
  const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
  const toolDelta = parsed.find((item) => item.choices?.[0]?.delta?.tool_calls);
  assert.ok(toolDelta, `expected tool_calls delta, frames=${JSON.stringify(frames)}`);
  const content = parsed.map((item) => item.choices?.[0]?.delta?.content || '').join('');
  assert.equal(content.includes('前置文本'), true, `expected pre-block text streamed, content=${JSON.stringify(content)}`);
  assert.equal(content.includes('尾巴文本'), false, `expected post-block text dropped, content=${JSON.stringify(content)}`);
  assert.equal(parsed.at(-1).choices[0].finish_reason, 'tool_calls');
});



test('vercel stream usage completion_tokens does not double-count visible output', async () => {
  const sample = 'abcdefghijklmnopqrst';
  const { frames } = await runMockVercelStream([
    `data: ${JSON.stringify({ p: 'response/content', v: sample })}\n\n`,
    'data: [DONE]\n\n',
  ]);
  const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
  const terminal = parsed.find((item) => Array.isArray(item.choices) && item.choices[0] && item.choices[0].finish_reason);
  assert.ok(terminal);
  assert.equal(terminal.usage.completion_tokens, 5);
});

test('vercel stream reuses prior PoW when refresh fails', async () => {
  const originalFetch = global.fetch;
  const fetchURLs = [];
  const completionPowHeaders = [];
  let completionCalls = 0;
  global.fetch = async (url, init = {}) => {
    const textURL = String(url);
    fetchURLs.push(textURL);
    if (textURL.includes('__stream_prepare=1')) {
      return jsonResponse({
        session_id: 'chatcmpl-test',
        lease_id: 'lease-test',
        model: 'gpt-test',
        final_prompt: 'hello',
        thinking_enabled: false,
        search_enabled: false,
        tool_names: [],
        deepseek_token: 'deepseek-token',
        pow_header: 'pow-header-initial',
        payload: { prompt: 'hello' },
      });
    }
    if (textURL.includes('__stream_pow=1')) {
      return jsonResponse({}, 500);
    }
    if (textURL.includes('__stream_release=1')) {
      return jsonResponse({ success: true });
    }
    if (textURL === 'https://chat.deepseek.com/api/v0/chat/completion') {
      completionPowHeaders.push(init.headers['x-ds-pow-response']);
      completionCalls += 1;
      if (completionCalls === 1) {
        return sseResponse(['data: [DONE]\n\n']);
      }
      return sseResponse(['data: {"p":"response/content","v":"visible"}\n\n', 'data: [DONE]\n\n']);
    }
    throw new Error(`unexpected fetch url: ${textURL}`);
  };
  try {
    const req = new MockStreamRequest();
    const res = new MockStreamResponse();
    const payload = { model: 'gpt-test', stream: true };
    await handleVercelStream(req, res, Buffer.from(JSON.stringify(payload)), payload);
    const frames = parseSSEDataFrames(res.bodyText());
    const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
    assert.deepEqual(completionPowHeaders, ['pow-header-initial', 'pow-header-initial']);
    assert.equal(fetchURLs.filter((url) => url.includes('__stream_pow=1')).length, 1);
    assert.equal(parsed[0].choices[0].delta.content, 'visible');
    assert.equal(parsed[1].choices[0].finish_reason, 'stop');
  } finally {
    global.fetch = originalFetch;
  }
});

test('vercel stream emits content_filter failure when upstream filters empty output', async () => {
  const { frames } = await runMockVercelStream(['data: {"code":"content_filter"}\n\n']);
  assert.equal(frames.length, 2);
  const failed = JSON.parse(frames[0]);
  assert.equal(failed.status_code, 400);
  assert.equal(failed.error.type, 'invalid_request_error');
  assert.equal(failed.error.code, 'content_filter');
  assert.equal(frames[1], '[DONE]');
});

test('vercel stream keeps stop finish when content_filter arrives after visible text', async () => {
  const { frames } = await runMockVercelStream([
    'data: {"p":"response/content","v":"hello"}\n\n',
    'data: {"code":"content_filter"}\n\n',
  ]);
  const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
  assert.equal(parsed[0].choices[0].delta.content, 'hello');
  assert.equal(parsed[1].choices[0].finish_reason, 'stop');
  assert.equal(parsed[1].usage.completion_tokens, 1);
});

test('resolveToolcallPolicy defaults to feature-match + early emit when prepare flags missing', () => {
  const policy = resolveToolcallPolicy(
    {},
    [{ type: 'function', function: { name: 'read_file', parameters: { type: 'object' } } }],
  );
  assert.deepEqual(policy.toolNames, ['read_file']);
  assert.equal(policy.toolSieveEnabled, true);
  assert.equal(policy.emitEarlyToolDeltas, true);
});

test('resolveToolcallPolicy ignores prepare flags and keeps early emit enabled', () => {
  const policy = resolveToolcallPolicy(
    {
      tool_names: [' prepped_tool ', '', null],
      toolcall_feature_match: false,
      toolcall_early_emit_high: false,
    },
    [{ type: 'function', function: { name: 'fallback_tool', parameters: { type: 'object' } } }],
  );
  assert.deepEqual(policy.toolNames, ['prepped_tool']);
  assert.equal(policy.toolSieveEnabled, true);
  assert.equal(policy.emitEarlyToolDeltas, true);
});

test('normalizePreparedToolNames filters empty values', () => {
  assert.deepEqual(normalizePreparedToolNames([' a ', '', null, 'b']), ['a', 'b']);
});

test('boolDefaultTrue keeps false only when explicitly false', () => {
  assert.equal(boolDefaultTrue(false), false);
  assert.equal(boolDefaultTrue(true), true);
  assert.equal(boolDefaultTrue(undefined), true);
});

test('filterIncrementalToolCallDeltasByAllowed keeps unknown name and follow-up args', () => {
  const seen = new Map();
  const filtered = filterIncrementalToolCallDeltasByAllowed(
    [
      { index: 0, name: 'not_in_schema' },
      { index: 0, arguments: '{"x":1}' },
    ],
    ['read_file'],
    seen,
  );
  assert.deepEqual(filtered, [
    { index: 0, name: 'not_in_schema' },
    { index: 0, arguments: '{"x":1}' },
  ]);
  assert.equal(seen.get(0), 'not_in_schema');
});

test('filterIncrementalToolCallDeltasByAllowed keeps allowed name and args', () => {
  const seen = new Map();
  const filtered = filterIncrementalToolCallDeltasByAllowed(
    [
      { index: 0, name: 'read_file' },
      { index: 0, arguments: '{"path":"README.MD"}' },
    ],
    ['read_file'],
    seen,
  );
  assert.deepEqual(filtered, [
    { index: 0, name: 'read_file' },
    { index: 0, arguments: '{"path":"README.MD"}' },
  ]);
});

test('incremental and final tool formatting share stable id via idStore', () => {
  const idStore = new Map();
  const incremental = formatIncrementalToolCallDeltas([{ index: 0, name: 'read_file' }], idStore);
  const { formatOpenAIStreamToolCalls } = require('../../internal/js/helpers/stream-tool-sieve.js');
  const finalCalls = formatOpenAIStreamToolCalls([{ name: 'read_file', input: { path: 'README.MD' } }], idStore);
  assert.equal(incremental.length, 1);
  assert.equal(finalCalls.length, 1);
  assert.equal(incremental[0].id, finalCalls[0].id);
});

test('resetStreamToolCallState gives each completed block a fresh id', () => {
  const idStore = new Map();
  const first = formatIncrementalToolCallDeltas([{ index: 0, name: 'read_file' }], idStore);
  resetStreamToolCallState(idStore);
  const second = formatIncrementalToolCallDeltas([{ index: 0, name: 'search' }], idStore);
  assert.equal(first.length, 1);
  assert.equal(second.length, 1);
  assert.notEqual(first[0].id, second[0].id);
});

test('formatIncrementalToolCallDeltas drops empty deltas (Go parity)', () => {
  const idStore = new Map();
  const formatted = formatIncrementalToolCallDeltas([{ index: 0 }], idStore);
  assert.deepEqual(formatted, []);
});

test('parseChunkForContent keeps split response/content fragments inside response array', () => {
  const chunk = {
    p: 'response',
    v: [
      { p: 'response/content', v: '{"' },
      { p: 'response/content', v: 'tool_calls":[{"name":"read_file","input":{"path":"README.MD"}}]}' },
    ],
  };
  const parsed = parseChunkForContent(chunk, false, 'text');
  assert.equal(parsed.finished, false);
  assert.equal(parsed.newType, 'text');
  assert.equal(parsed.parts.length, 2);
  const combined = parsed.parts.map((p) => p.text).join('');
  assert.equal(combined, '{"tool_calls":[{"name":"read_file","input":{"path":"README.MD"}}]}');
});

test('parseChunkForContent + sieve passes JSON tool payload through as text (XML-only)', () => {
  const chunk = {
    p: 'response',
    v: [
      { p: 'response/content', v: '{"' },
      { p: 'response/content', v: 'tool_calls":[{"name":"read_file","input":{"path":"README.MD"}}]}' },
    ],
  };
  const parsed = parseChunkForContent(chunk, false, 'text');
  const state = createToolSieveState();
  const events = [];
  for (const part of parsed.parts) {
    events.push(...processToolSieveChunk(state, part.text, ['read_file']));
  }
  events.push(...flushToolSieve(state, ['read_file']));

  const hasToolCalls = events.some((evt) => evt.type === 'tool_calls' && evt.calls && evt.calls.length > 0);
  const leakedText = events
    .filter((evt) => evt.type === 'text' && evt.text)
    .map((evt) => evt.text)
    .join('');

  // JSON payloads are no longer intercepted — they pass through as text.
  assert.equal(hasToolCalls, false);
  assert.equal(leakedText.includes('tool_calls'), true);
});

test('parseChunkForContent consumes nested item.v array payloads', () => {
  const chunk = {
    p: 'response',
    v: [
      { p: 'response/content', v: ['A', 'B'] },
      { p: 'response/content', v: [{ content: 'C', type: 'RESPONSE' }] },
    ],
  };
  const parsed = parseChunkForContent(chunk, false, 'text');
  assert.equal(parsed.finished, false);
  assert.equal(parsed.parts.map((p) => p.text).join(''), 'ABC');
});

test('parseChunkForContent detects nested status FINISHED in array payload', () => {
  const chunk = {
    p: 'response',
    v: [{ p: 'status', v: 'FINISHED' }],
  };
  const parsed = parseChunkForContent(chunk, false, 'text');
  assert.equal(parsed.finished, true);
  assert.deepEqual(parsed.parts, []);
});

test('parseChunkForContent ignores fragment-scoped status FINISHED and routes tool content to thinking', () => {
  const chunk = {
    p: 'response/fragments/-1',
    v: [
      { p: 'status', v: 'FINISHED' },
      { p: 'content', v: '已搜索到相关图片' },
    ],
  };
  const parsed = parseChunkForContent(chunk, true, 'thinking');
  assert.equal(parsed.finished, false);
  assert.deepEqual(parsed.parts, [{ text: '已搜索到相关图片', type: 'thinking' }]);
});

test('parseChunkForContent routes TOOL_SEARCH fragment content to thinking', () => {
  const chunk = {
    p: 'response',
    o: 'BATCH',
    v: [
      {
        p: 'fragments',
        o: 'APPEND',
        v: [{ id: 3, type: 'TOOL_SEARCH', status: 'WIP', content: '正在搜索图片' }],
      },
    ],
  };
  const parsed = parseChunkForContent(chunk, true, 'thinking');
  assert.equal(parsed.finished, false);
  assert.deepEqual(parsed.parts, [{ text: '正在搜索图片', type: 'thinking' }]);
});

test('parseChunkForContent still stops on top-level response/status FINISHED', () => {
  const parsed = parseChunkForContent({ p: 'response/status', o: 'SET', v: 'FINISHED' }, true, 'text');
  assert.equal(parsed.finished, true);
});


test('parseChunkForContent ignores items without v to match Go parser behavior', () => {
  const chunk = {
    p: 'response',
    v: [{ type: 'RESPONSE', content: 'no-v-content' }],
  };
  const parsed = parseChunkForContent(chunk, false, 'text');
  assert.equal(parsed.finished, false);
  assert.deepEqual(parsed.parts, []);
});

test('parseChunkForContent handles response/fragments APPEND with thinking and response transitions', () => {
  const chunk = {
    p: 'response/fragments',
    o: 'APPEND',
    v: [
      { type: 'THINK', content: '思考中' },
      { type: 'RESPONSE', content: '结论' },
    ],
  };
  const parsed = parseChunkForContent(chunk, true, 'thinking');
  assert.equal(parsed.finished, false);
  assert.equal(parsed.newType, 'text');
  assert.deepEqual(parsed.parts, [
    { text: '思考中', type: 'thinking' },
    { text: '结论', type: 'text' },
  ]);
});

test('parseChunkForContent flips sticky cursor inside response BATCH fragments (THINK then RESPONSE)', () => {
  const chunk = {
    p: 'response',
    o: 'BATCH',
    v: [
      { p: 'fragments', o: 'APPEND', v: [{ type: 'THINK', content: 'deep thought' }] },
      { p: 'fragments', o: 'APPEND', v: [{ type: 'RESPONSE', content: 'answer' }] },
    ],
  };
  const parsed = parseChunkForContent(chunk, true, 'thinking');
  assert.equal(parsed.finished, false);
  assert.equal(parsed.newType, 'text');
  assert.deepEqual(parsed.parts, [
    { text: 'deep thought', type: 'thinking' },
    { text: 'answer', type: 'text' },
  ]);
});

test('parseChunkForContent routes bare relative fragments/-1/content by sticky cursor', () => {
  const thinking = parseChunkForContent({ p: 'fragments/-1/content', o: 'APPEND', v: ' thought' }, true, 'thinking');
  assert.equal(thinking.finished, false);
  assert.equal(thinking.newType, 'thinking');
  assert.deepEqual(thinking.parts, [{ text: ' thought', type: 'thinking' }]);

  const body = parseChunkForContent({ p: 'fragments/-1/content', o: 'APPEND', v: ' body' }, true, 'text');
  assert.equal(body.finished, false);
  assert.equal(body.newType, 'text');
  assert.deepEqual(body.parts, [{ text: ' body', type: 'text' }]);
});

test('parseChunkForContent keeps nested fragments BATCH v payload without content field', () => {
  const chunk = {
    p: 'response',
    o: 'BATCH',
    v: [
      { p: 'accumulated_token_usage', v: 2016 },
      {
        p: 'fragments',
        o: 'BATCH',
        v: [
          { p: '-1/content', o: 'APPEND', v: '        pass\n```' },
          { p: '', v: [{ id: 4, type: 'TIP', content: '提示', style: 'WARNING', hide_on_wip: true }] },
        ],
      },
      { p: 'quasi_status', o: 'SET', v: 'FINISHED' },
    ],
  };
  const parsed = parseChunkForContent(chunk, true, 'text');
  assert.equal(parsed.finished, false);
  assert.equal(parsed.newType, 'text');
  assert.equal(parsed.parts.length, 1);
  assert.deepEqual(parsed.parts[0], { text: '        pass\n```', type: 'text' });
});

test('parseChunkForContent drops THINK replay after body started (one-size-fits-all)', () => {
  const replay = parseChunkForContent(
    { v: { response: { message_id: 2, fragments: [{ id: 2, type: 'THINK', content: 'We need answer.' }] } } },
    true,
    'text',
  );
  assert.equal(replay.newType, 'text');
  assert.deepEqual(replay.parts, []);

  const followUp = parseChunkForContent({ v: 'total order' }, true, replay.newType);
  assert.equal(followUp.newType, 'text');
  assert.deepEqual(followUp.parts, [{ text: 'total order', type: 'text' }]);
});

test('parseChunkForContent drops all thinking increments after body started (one-size-fits-all)', () => {
  let currentType = 'thinking';
  const emitted = [];

  const step = (chunk) => {
    const parsed = parseChunkForContent(chunk, true, currentType);
    currentType = parsed.newType;
    emitted.push(...parsed.parts);
  };

  step({ p: 'response/fragments', o: 'APPEND', v: [{ type: 'THINK', content: '第一段思考' }] });
  step({ p: 'response/fragments', o: 'APPEND', v: [{ type: 'RESPONSE', content: '正文开始' }] });
  step({ p: 'response/content', v: '正文结尾' });
  // The upstream bug: reasoning keeps arriving after the body began.
  step({ p: 'response/thinking_content', v: '残余思考不该出现' });
  step({ p: 'response/fragments', o: 'APPEND', v: [{ type: 'THINK', content: '更不该出现' }] });

  assert.deepEqual(
    emitted,
    [
      { text: '第一段思考', type: 'thinking' },
      { text: '正文开始', type: 'text' },
      { text: '正文结尾', type: 'text' },
    ],
  );
  assert.equal(currentType, 'text');
});

test('parseChunkForContent does not recover thinking_content as body text after body started', () => {
  const parsed = parseChunkForContent({ p: 'response/thinking_content', v: 'leaked reasoning' }, true, 'text');
  assert.equal(parsed.finished, false);
  assert.equal(parsed.newType, 'text');
  assert.deepEqual(parsed.parts, []);
});

test('parseChunkForContent drops leaked </think> thinking increment after body started', () => {
  const parsed = parseChunkForContent({ p: 'response/thinking_content', v: 'leaked</think>still leaked' }, true, 'text');
  assert.equal(parsed.newType, 'text');
  assert.deepEqual(parsed.parts, []);
});

test('parseChunkForContent keeps THINK before RESPONSE in the same reasoning chunk', () => {
  const chunk = {
    p: 'response/fragments',
    o: 'APPEND',
    v: [
      { type: 'THINK', content: 'deep thought' },
      { type: 'RESPONSE', content: 'answer' },
    ],
  };
  const parsed = parseChunkForContent(chunk, true, 'thinking');
  assert.equal(parsed.newType, 'text');
  assert.deepEqual(parsed.parts, [
    { text: 'deep thought', type: 'thinking' },
    { text: 'answer', type: 'text' },
  ]);
});

test('parseChunkForContent drops thinking content when thinking is disabled', () => {
  const thinking = parseChunkForContent(
    { p: 'response/thinking_content', v: 'hidden thought' },
    false,
    'text',
  );
  assert.equal(thinking.finished, false);
  assert.equal(thinking.newType, 'thinking');
  assert.deepEqual(thinking.parts, []);

  const hiddenContinuation = parseChunkForContent(
    { v: 'still hidden' },
    false,
    thinking.newType,
  );
  assert.equal(hiddenContinuation.newType, 'thinking');
  assert.deepEqual(hiddenContinuation.parts, []);

  const answer = parseChunkForContent(
    { p: 'response/content', v: 'visible answer' },
    false,
    hiddenContinuation.newType,
  );
  assert.equal(answer.newType, 'text');
  assert.deepEqual(answer.parts, [{ text: 'visible answer', type: 'text' }]);
});

test('parseChunkForContent supports wrapped response.fragments object shape', () => {
  const chunk = {
    p: 'response',
    v: {
      response: {
        fragments: [
          { type: 'RESPONSE', content: 'A' },
          { type: 'RESPONSE', content: 'B' },
        ],
      },
    },
  };
  const parsed = parseChunkForContent(chunk, false, 'text');
  assert.equal(parsed.finished, false);
  assert.equal(parsed.parts.map((p) => p.text).join(''), 'AB');
});

test('parseChunkForContent reads object-shaped response/content payloads (Go parity)', () => {
  const parsed = parseChunkForContent({ p: 'response/content', v: { text: 'vision text' } }, false, 'text', true);
  assert.equal(parsed.parsed, true);
  assert.deepEqual(parsed.parts, [{ text: 'vision text', type: 'text' }]);
});

test('parseChunkForContent preserves space-only content tokens', () => {
  const chunk = {
    p: 'response/content',
    v: ' ',
  };
  const parsed = parseChunkForContent(chunk, false, 'text');
  assert.equal(parsed.finished, false);
  assert.deepEqual(parsed.parts, [{ text: ' ', type: 'text' }]);
});

test('parseChunkForContent strips citation and reference markers from fragment content', () => {
  const chunk = {
    p: 'response/fragments',
    o: 'APPEND',
    v: [
      { type: 'RESPONSE', content: '广州天气 [citation:1] [reference:12] 多云' },
    ],
  };
  const parsed = parseChunkForContent(chunk, false, 'text');
  assert.equal(parsed.finished, false);
  assert.deepEqual(parsed.parts, [{ text: '广州天气   多云', type: 'text' }]);
});

test('parseChunkForContent strips leaked thought control markers from content', () => {
  const chunk = {
    p: 'response/content',
    v: '<|▁of▁thought|>A<| of_thought |>B<| end_of_thought |>C',
  };
  const parsed = parseChunkForContent(chunk, false, 'text');
  assert.equal(parsed.finished, false);
  assert.deepEqual(parsed.parts, [{ text: 'ABC', type: 'text' }]);
});

test('parseChunkForContent strips fullwidth-delimited leaked control markers from content', () => {
  const fw = '\uff5c';
  const chunk = {
    p: 'response/content',
    v: `<${fw}begin▁of▁sentence${fw}>A<${fw}▁of▁thought${fw}>B<${fw} end_of_sentence ${fw}>C`,
  };
  const parsed = parseChunkForContent(chunk, false, 'text');
  assert.equal(parsed.finished, false);
  assert.deepEqual(parsed.parts, [{ text: 'ABC', type: 'text' }]);
});

test('parseChunkForContent detects content_filter status and ignores upstream output tokens', () => {
  const chunk = {
    p: 'response',
    v: [
      { p: 'status', v: 'CONTENT_FILTER' },
      { p: 'accumulated_token_usage', v: 77 },
    ],
  };
  const parsed = parseChunkForContent(chunk, false, 'text');
  assert.equal(parsed.parsed, true);
  assert.equal(parsed.finished, true);
  assert.equal(parsed.contentFilter, true);
  assert.equal(parsed.outputTokens, 0);
  assert.deepEqual(parsed.parts, []);
});

test('parseChunkForContent keeps error branches distinct from content_filter status', () => {
  const chunk = {
    error: { message: 'boom' },
    code: 'content_filter',
    accumulated_token_usage: 88,
  };
  const parsed = parseChunkForContent(chunk, false, 'text');
  assert.equal(parsed.parsed, true);
  assert.equal(parsed.finished, true);
  assert.equal(parsed.contentFilter, false);
  assert.equal(parsed.errorMessage.length > 0, true);
  assert.equal(parsed.outputTokens, 0);
  assert.deepEqual(parsed.parts, []);
});

test('parseChunkForContent ignores output tokens on FINISHED lines', () => {
  const parsed = parseChunkForContent(
    { p: 'response/status', v: 'FINISHED', accumulated_token_usage: 190 },
    false,
    'text',
  );
  assert.equal(parsed.parsed, true);
  assert.equal(parsed.finished, true);
  assert.equal(parsed.contentFilter, false);
  assert.equal(parsed.outputTokens, 0);
  assert.deepEqual(parsed.parts, []);
});

test('parseChunkForContent ignores output tokens from response BATCH status snapshots', () => {
  const parsed = parseChunkForContent(
    {
      p: 'response',
      o: 'BATCH',
      v: [
        { p: 'accumulated_token_usage', v: 190 },
        { p: 'quasi_status', v: 'FINISHED' },
      ],
    },
    false,
    'text',
  );
  assert.equal(parsed.parsed, true);
  assert.equal(parsed.finished, false);
  assert.equal(parsed.contentFilter, false);
  assert.equal(parsed.outputTokens, 0);
  assert.deepEqual(parsed.parts, []);
});

test('parseChunkForContent matches FINISHED case-insensitively on status paths', () => {
  const parsed = parseChunkForContent(
    { p: 'response/status', v: ' finished ', accumulated_token_usage: 190 },
    false,
    'text',
  );
  assert.equal(parsed.parsed, true);
  assert.equal(parsed.finished, true);
  assert.equal(parsed.contentFilter, false);
  assert.equal(parsed.outputTokens, 0);
  assert.deepEqual(parsed.parts, []);
});

test('parseChunkForContent filters INCOMPLETE status text without stopping stream', () => {
  const parsed = parseChunkForContent(
    { p: 'response/status', v: 'INCOMPLETE', accumulated_token_usage: 190 },
    false,
    'text',
  );
  assert.equal(parsed.parsed, true);
  assert.equal(parsed.finished, false);
  assert.equal(parsed.contentFilter, false);
  assert.equal(parsed.outputTokens, 0);
  assert.deepEqual(parsed.parts, []);
});

test('parseChunkForContent strips leaked CONTENT_FILTER suffix and preserves line breaks', () => {
  const leaked = parseChunkForContent(
    { p: 'response/content', v: '正常输出CONTENT_FILTER你好，这个问题我暂时无法回答' },
    false,
    'text',
  );
  assert.deepEqual(leaked.parts, [{ text: '正常输出', type: 'text' }]);

  const newlineTail = parseChunkForContent(
    { p: 'response/content', v: 'line1\nCONTENT_FILTERblocked' },
    false,
    'text',
  );
  assert.deepEqual(newlineTail.parts, [{ text: 'line1\n', type: 'text' }]);

  const newlineOnly = parseChunkForContent(
    { p: 'response/content', v: '\nCONTENT_FILTERblocked' },
    false,
    'text',
  );
  assert.deepEqual(newlineOnly.parts, [{ text: '\n', type: 'text' }]);
});

test('estimateTokens preserves whitespace-only strings and buildUsage accepts output token overrides', () => {
  assert.equal(estimateTokens('   '), 1);
  assert.equal(estimateTokens('\n'), 1);

  const usage = buildUsage('abcd', 'ef', 'gh', 99);
  assert.equal(usage.prompt_tokens, 1);
  assert.equal(usage.completion_tokens, 99);
  assert.equal(usage.total_tokens, 100);
  assert.equal(usage.completion_tokens_details.reasoning_tokens, 1);
});

test('shouldSkipPath skips dynamic response/fragments/*/status paths only', () => {
  assert.equal(shouldSkipPath('response/fragments/-16/status'), true);
  assert.equal(shouldSkipPath('response/fragments/8/status'), true);
  assert.equal(shouldSkipPath('response/status'), false);
});

test('node stream path guard allows OpenAI v1 and root alias chat completions paths', () => {
  assert.equal(isNodeStreamSupportedPath('/v1/chat/completions'), true);
  assert.equal(isNodeStreamSupportedPath('/v1/chat/completions?x=1'), true);
  assert.equal(isNodeStreamSupportedPath('/chat/completions'), true);
  assert.equal(isNodeStreamSupportedPath('/chat/completions?x=1'), true);
  assert.equal(isNodeStreamSupportedPath('/v1beta/models/gemini-2.5-flash:streamGenerateContent'), false);
  assert.equal(isNodeStreamSupportedPath('/anthropic/v1/messages'), false);
});

test('extractPathname strips query only', () => {
  assert.equal(extractPathname('/v1/chat/completions?stream=true'), '/v1/chat/completions');
  assert.equal(extractPathname('/v1beta/models/gemini-2.5-flash:streamGenerateContent?key=1'), '/v1beta/models/gemini-2.5-flash:streamGenerateContent');
  assert.equal(extractPathname('/chat/completions?stream=true'), '/chat/completions');
});

test('setCorsHeaders reflects requested third-party headers and blocks internal-only headers', () => {
  const res = createMockResponse();
  setCorsHeaders(res, {
    headers: {
      origin: 'app://obsidian.md',
      'access-control-request-headers': 'authorization, x-stainless-os, x-stainless-runtime, x-ds2-internal-token',
      'access-control-request-private-network': 'true',
    },
  });

  assert.equal(res.getHeader('access-control-allow-origin'), 'app://obsidian.md');
  assert.equal(res.getHeader('access-control-allow-private-network'), 'true');
  assert.equal(res.getHeader('access-control-max-age'), '600');

  const allowHeaders = String(res.getHeader('access-control-allow-headers') || '').toLowerCase();
  assert.equal(allowHeaders.includes('authorization'), true);
  assert.equal(allowHeaders.includes('x-stainless-os'), true);
  assert.equal(allowHeaders.includes('x-stainless-runtime'), true);
  assert.equal(allowHeaders.includes('x-ds2-internal-token'), false);

  const vary = String(res.getHeader('vary') || '').toLowerCase();
  assert.equal(vary.includes('origin'), true);
  assert.equal(vary.includes('access-control-request-headers'), true);
  assert.equal(vary.includes('access-control-request-private-network'), true);
});

test('trimContinuationOverlap preserves short normal tokens and trims long snapshots', () => {
  assert.equal(trimContinuationOverlap('我们被问到', '我们'), '我们');
  const existing = '我们被问到：这是一个很长的续答快照前缀，用来验证去重逻辑不会误伤正常 token。';
  const incoming = `${existing}继续分析`;
  assert.equal(trimContinuationOverlap(existing, incoming), '继续分析');
});

test('vercel stream detects muted JSON and switches account', async () => {
  const originalFetch = global.fetch;
  const fetchURLs = [];
  let completionCalls = 0;
  global.fetch = async (url, init = {}) => {
    const textURL = String(url);
    fetchURLs.push(textURL);
    if (textURL.includes('__stream_prepare=1')) {
      return jsonResponse({
        session_id: 'chatcmpl-test',
        lease_id: 'lease-test',
        model: 'gpt-test',
        final_prompt: 'hello',
        thinking_enabled: false,
        search_enabled: false,
        tool_names: [],
        deepseek_token: 'token-1',
        pow_header: 'pow-1',
        payload: { prompt: 'hello' },
      });
    }
    if (textURL.includes('__stream_switch=1')) {
      return jsonResponse({
        session_id: 'chatcmpl-test',
        lease_id: 'lease-test',
        model: 'gpt-test',
        final_prompt: 'hello',
        thinking_enabled: false,
        search_enabled: false,
        tool_names: [],
        deepseek_token: 'token-2',
        pow_header: 'pow-2',
        payload: { prompt: 'hello' },
      });
    }
    if (textURL.includes('__stream_release=1')) {
      return jsonResponse({ success: true });
    }
    if (textURL === 'https://chat.deepseek.com/api/v0/chat/completion') {
      completionCalls += 1;
      if (completionCalls === 1) {
        return jsonResponse({
          code: 0,
          data: {
            biz_code: 5,
            biz_msg: 'user is muted',
            biz_data: { mute_until: 1234567890 },
          },
        });
      }
      return sseResponse(['data: {"p":"response/content","v":"visible"}\n\n', 'data: [DONE]\n\n']);
    }
    throw new Error(`unexpected fetch url: ${textURL}`);
  };
  try {
    const req = new MockStreamRequest();
    const res = new MockStreamResponse();
    const payload = { model: 'gpt-test', stream: true };
    await handleVercelStream(req, res, Buffer.from(JSON.stringify(payload)), payload);
    const frames = parseSSEDataFrames(res.bodyText());
    const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
    assert.equal(fetchURLs.filter((url) => url.includes('__stream_switch=1')).length, 1);
    assert.equal(completionCalls, 2);
    assert.equal(parsed[0].choices[0].delta.content, 'visible');
    assert.equal(parsed[1].choices[0].finish_reason, 'stop');
  } finally {
    global.fetch = originalFetch;
  }
});

test('vercel stream loops account switches across multiple muted accounts', async () => {
  const originalFetch = global.fetch;
  const fetchURLs = [];
  let completionCalls = 0;
  let switchCalls = 0;
  global.fetch = async (url) => {
    const textURL = String(url);
    fetchURLs.push(textURL);
    if (textURL.includes('__stream_prepare=1')) {
      return jsonResponse({
        session_id: 'chatcmpl-test',
        lease_id: 'lease-test',
        model: 'gpt-test',
        final_prompt: 'hello',
        thinking_enabled: false,
        search_enabled: false,
        tool_names: [],
        deepseek_token: 'token-1',
        pow_header: 'pow-1',
        payload: { prompt: 'hello' },
      });
    }
    if (textURL.includes('__stream_switch=1')) {
      switchCalls += 1;
      return jsonResponse({
        session_id: 'chatcmpl-test',
        lease_id: 'lease-test',
        model: 'gpt-test',
        final_prompt: 'hello',
        thinking_enabled: false,
        search_enabled: false,
        tool_names: [],
        deepseek_token: `token-${switchCalls + 1}`,
        pow_header: `pow-${switchCalls + 1}`,
        payload: { prompt: 'hello' },
      });
    }
    if (textURL.includes('__stream_release=1')) {
      return jsonResponse({ success: true });
    }
    if (textURL === 'https://chat.deepseek.com/api/v0/chat/completion') {
      completionCalls += 1;
      if (completionCalls <= 2) {
        return jsonResponse({
          code: 0,
          data: {
            biz_code: 5,
            biz_msg: 'user is muted',
            biz_data: { mute_until: 1234567890 },
          },
        });
      }
      return sseResponse(['data: {"p":"response/content","v":"visible"}\n\n', 'data: [DONE]\n\n']);
    }
    throw new Error(`unexpected fetch url: ${textURL}`);
  };
  try {
    const req = new MockStreamRequest();
    const res = new MockStreamResponse();
    const payload = { model: 'gpt-test', stream: true };
    await handleVercelStream(req, res, Buffer.from(JSON.stringify(payload)), payload);
    const frames = parseSSEDataFrames(res.bodyText());
    const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
    assert.equal(switchCalls, 2);
    assert.equal(completionCalls, 3);
    assert.equal(parsed[0].choices[0].delta.content, 'visible');
    assert.equal(parsed[1].choices[0].finish_reason, 'stop');
  } finally {
    global.fetch = originalFetch;
  }
});

test('vercel stream detects USER_IS_BANNED JSON and switches account', async () => {
  const originalFetch = global.fetch;
  const fetchURLs = [];
  const fetchBodies = [];
  let completionCalls = 0;
  global.fetch = async (url, init = {}) => {
    const textURL = String(url);
    fetchURLs.push(textURL);
    if (init.body) {
      fetchBodies.push(JSON.parse(Buffer.from(init.body).toString('utf-8')));
    }
    if (textURL.includes('__stream_prepare=1')) {
      return jsonResponse({
        session_id: 'chatcmpl-test',
        lease_id: 'lease-test',
        model: 'gpt-test',
        final_prompt: 'hello',
        thinking_enabled: false,
        search_enabled: false,
        tool_names: [],
        deepseek_token: 'token-1',
        pow_header: 'pow-1',
        payload: { prompt: 'hello' },
      });
    }
    if (textURL.includes('__stream_switch=1')) {
      return jsonResponse({
        session_id: 'chatcmpl-test',
        lease_id: 'lease-test',
        model: 'gpt-test',
        final_prompt: 'hello',
        thinking_enabled: false,
        search_enabled: false,
        tool_names: [],
        deepseek_token: 'token-2',
        pow_header: 'pow-2',
        payload: { prompt: 'hello' },
      });
    }
    if (textURL.includes('__stream_release=1')) {
      return jsonResponse({ success: true });
    }
    if (textURL === 'https://chat.deepseek.com/api/v0/chat/completion') {
      completionCalls += 1;
      if (completionCalls === 1) {
        return jsonResponse({
          code: 0,
          data: {
            biz_code: 10,
            biz_msg: 'USER_IS_BANNED',
            biz_data: null,
          },
        });
      }
      return sseResponse(['data: {"p":"response/content","v":"visible"}\n\n', 'data: [DONE]\n\n']);
    }
    throw new Error(`unexpected fetch url: ${textURL}`);
  };
  try {
    const req = new MockStreamRequest();
    const res = new MockStreamResponse();
    const payload = { model: 'gpt-test', stream: true };
    await handleVercelStream(req, res, Buffer.from(JSON.stringify(payload)), payload);
    const frames = parseSSEDataFrames(res.bodyText());
    const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
    const switchCalls = fetchBodies.filter((body) => body.banned === true);
    assert.equal(switchCalls.length, 1);
    assert.equal(switchCalls[0].banned_reason, '账户已被停用');
    assert.equal(fetchURLs.filter((url) => url.includes('__stream_switch=1')).length, 1);
    assert.equal(completionCalls, 2);
    assert.equal(parsed[0].choices[0].delta.content, 'visible');
    assert.equal(parsed[1].choices[0].finish_reason, 'stop');
  } finally {
    global.fetch = originalFetch;
  }
});

test('vercel stream does not consume SSE body during mute detection', async () => {
  const originalFetch = global.fetch;
  const fetchURLs = [];
  global.fetch = async (url, init = {}) => {
    const textURL = String(url);
    fetchURLs.push(textURL);
    if (textURL.includes('__stream_prepare=1')) {
      return jsonResponse({
        session_id: 'chatcmpl-test',
        lease_id: 'lease-test',
        model: 'gpt-test',
        final_prompt: 'hello',
        thinking_enabled: false,
        search_enabled: false,
        tool_names: [],
        deepseek_token: 'deepseek-token',
        pow_header: 'pow-header',
        payload: { prompt: 'hello' },
      });
    }
    if (textURL.includes('__stream_release=1')) {
      return jsonResponse({ success: true });
    }
    if (textURL === 'https://chat.deepseek.com/api/v0/chat/completion') {
      return sseResponse(['data: {"p":"response/content","v":"hello from stream"}\n\n', 'data: [DONE]\n\n']);
    }
    throw new Error(`unexpected fetch url: ${textURL}`);
  };
  try {
    const req = new MockStreamRequest();
    const res = new MockStreamResponse();
    const payload = { model: 'gpt-test', stream: true };
    await handleVercelStream(req, res, Buffer.from(JSON.stringify(payload)), payload);
    const frames = parseSSEDataFrames(res.bodyText());
    const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
    assert.equal(fetchURLs.filter((url) => url.includes('__stream_switch=1')).length, 0);
    assert.equal(parsed[0].choices[0].delta.content, 'hello from stream');
    assert.equal(parsed[1].choices[0].finish_reason, 'stop');
  } finally {
    global.fetch = originalFetch;
  }
});

test('repeat loop guard trips only on consecutive protocol closing tags', () => {
  const tripped = (fragments, threshold) => {
    const guard = createRepeatLoopGuard(threshold);
    let hit = false;
    for (const fragment of fragments) {
      if (feedRepeatLoopGuard(guard, fragment)) {
        hit = true;
      }
    }
    return hit;
  };
  const N = REPEAT_LOOP_THRESHOLD;
  const shorthand = '</|EPSE>';
  const invokeClose = '</|EPSE|invoke>';
  const toolCallsClose = '</|EPSE|tool_calls>';

  assert.equal(tripped([shorthand.repeat(N - 1)]), false);
  assert.equal(tripped([shorthand.repeat(N)]), true);
  assert.equal(tripped([`${shorthand}\n`.repeat(N)]), true);
  // The observed loop alternates between two different closing shells.
  assert.equal(tripped([`${invokeClose}\n${toolCallsClose}\n`.repeat(N)]), true);
  assert.equal(tripped([`${shorthand.repeat(N - 1)}x${shorthand.repeat(N - 1)}`]), false);
  // Split across SSE fragments must still be counted.
  const fragments = [];
  for (let i = 0; i < N; i += 1) {
    fragments.push('</|', 'EPSE>', '</|EP', 'SE|invoke>', '</|EPSE|tool', '_calls>');
  }
  assert.equal(tripped(fragments), true);
  // The observed loop also emits the normalized canonical shells, which are
  // plain XML closing tags. Any long run of non-HTML closing tags must trip now.
  assert.equal(tripped(['</invoke>\n</parameter>\n'.repeat(N)]), true);
  assert.equal(tripped(['</think>\n'.repeat(N * 3)]), true);
  assert.equal(tripped(['</parameter>\n'.repeat(N * 3)]), true);
  // Common HTML/SVG closing tags legitimately stack in deeply nested markup and
  // are excluded by name, so a long run of them must not trip.
  assert.equal(tripped(['</div>\n'.repeat(N * 3)]), false);
  assert.equal(tripped(['</span>\n'.repeat(N * 3)]), false);
  assert.equal(tripped(['</li>\n'.repeat(N * 3)]), false);
  assert.equal(tripped(['</td>\n'.repeat(N * 3)]), false);
  assert.equal(tripped(['</DIV >\n'.repeat(N * 3)]), false);
  // Over-closing and format-explaining prose are the longest legitimate runs.
  assert.equal(tripped([`<|EPSE|parameter name="a"><![CDATA[v]]></|EPSE|parameter></|EPSE|parameter></|EPSE|parameter>${invokeClose}${toolCallsClose}`]), false);
  // Six variants is the documented longest legitimate run; keep it below the
  // threshold so format-explaining prose is not cut.
  const variants = [
    shorthand, '</|EPSE|parameter>', invokeClose, toolCallsClose,
    '</epse|parameter>', '</|EPSE parameter>',
  ].join('\n');
  assert.equal(tripped([`闭合外壳的各种写法：\n${variants}\n`]), false);
  // A well-formed tool call breaks every deeper level up with an opening tag, so
  // it must never trip.
  let call = '<|EPSE|tool_calls>';
  for (let i = 0; i < 10; i += 1) {
    call += '<|EPSE|invoke name="Bash">';
    for (let j = 0; j < 5; j += 1) {
      call += `<|EPSE|parameter name="command"><![CDATA[pwd]]>${shorthand}`;
    }
    call += invokeClose;
  }
  call += toolCallsClose;
  assert.equal(tripped([call]), false);
  // An unterminated `</|` must not be buffered forever.
  assert.equal(tripped([`</|${'a'.repeat(1024)}`]), false);
});

test('vercel stream cuts upstream repeat loop and finishes with 200', async () => {
  const lines = ['data: {"response_message_id":91,"p":"response/content","v":"visible answer"}\n\n'];
  const loopTags = ['</|EPSE|invoke>', '</|EPSE|tool_calls>'];
  for (let i = 0; i < REPEAT_LOOP_THRESHOLD + 2; i += 1) {
    lines.push(`data: ${JSON.stringify({ p: 'response/content', v: `${loopTags[i % loopTags.length]}\n` })}\n\n`);
  }
  lines.push('data: {"p":"response/content","v":"never consumed tail"}\n\n');
  lines.push('data: [DONE]\n\n');

  const { res, frames, fetchURLs } = await runMockVercelStream(lines);
  const parsed = frames.filter((frame) => frame !== '[DONE]').map((frame) => JSON.parse(frame));
  assert.equal(res.statusCode, 200);
  assert.equal(frames.at(-1), '[DONE]');
  assert.equal(parsed.some((item) => item.status_code), false, `expected no error frame, frames=${JSON.stringify(frames)}`);
  assert.equal(parsed.at(-1).choices[0].finish_reason, 'stop');
  const content = parsed.map((item) => item.choices?.[0]?.delta?.content || '').join('');
  assert.equal(content.includes('visible answer'), true, `expected pre-loop content kept, content=${JSON.stringify(content)}`);
  assert.equal(content.includes('never consumed tail'), false, `expected post-loop chunks dropped, content=${JSON.stringify(content)}`);
  // The loop must not be resumed via the continue endpoint.
  assert.equal(fetchURLs.filter((url) => url.includes('/continue')).length, 0);
});

