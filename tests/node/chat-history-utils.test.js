'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

async function loadUtils() {
  return import('../../webui/src/features/chatHistory/chatHistoryUtils.js');
}

test('chat history strict parser merges current input file placeholder', async () => {
  const {
    buildListModeMessages,
  } = await loadUtils();
  const t = (key) => key;
  const item = {
    messages: [{
      role: 'system',
      content: '继续会话',
    }],
    history_text: [
      'Userhello',
      'Assistanthi',
    ].join(''),
  };

  const result = buildListModeMessages(item, t);
  assert.equal(result.historyMerged, true);
  assert.deepEqual(result.messages, [
    { role: 'user', content: 'hello' },
    { role: 'assistant', content: 'hi' },
  ]);
});

test('chat history strict parser merges legacy user-role placeholder', async () => {
  const {
    buildListModeMessages,
  } = await loadUtils();
  const t = (key) => key;
  const item = {
    messages: [{
      role: 'user',
      content: '继续会话',
    }],
    history_text: [
      'Userhello',
      'Assistanthi',
    ].join(''),
  };

  const result = buildListModeMessages(item, t);
  assert.equal(result.historyMerged, true);
  assert.deepEqual(result.messages, [
    { role: 'user', content: 'hello' },
    { role: 'assistant', content: 'hi' },
  ]);
});

test('chat history strict parser inserts history after system messages', async () => {
  const {
    buildListModeMessages,
  } = await loadUtils();
  const t = (key) => key;
  const item = {
    messages: [
      { role: 'system', content: 'policy' },
      { role: 'user', content: 'latest' },
    ],
    history_text: [
      'Userold',
      'Assistantdone',
    ].join(''),
  };

  const result = buildListModeMessages(item, t);
  assert.equal(result.historyMerged, true);
  assert.deepEqual(result.messages, [
    { role: 'system', content: 'policy' },
    { role: 'user', content: 'old' },
    { role: 'assistant', content: 'done' },
    { role: 'user', content: 'latest' },
  ]);
});

test('chat history keeps the inline trailing user turn after the merged history', async () => {
  const {
    buildListModeMessages,
  } = await loadUtils();
  const t = (key) => key;
  const item = {
    messages: [
      { role: 'system', content: '继续会话' },
      { role: 'user', content: 'latest' },
    ],
    history_text: [
      '# HISTORY.txt',
      'Prior conversation history and tool progress.',
      '',
      '=== 1. SYSTEM ===',
      'policy',
      '',
      '=== 2. USER ===',
      'hello',
      '',
      '=== 3. ASSISTANT ===',
      'hi',
      '',
    ].join('\n'),
  };

  const result = buildListModeMessages(item, t);
  assert.equal(result.historyMerged, true);
  assert.deepEqual(result.messages, [
    { role: 'system', content: 'policy' },
    { role: 'user', content: 'hello' },
    { role: 'assistant', content: 'hi' },
    { role: 'user', content: 'latest' },
  ]);
});

test('chat history drops the tools variant of the continuation placeholder', async () => {
  const {
    buildListModeMessages,
  } = await loadUtils();
  const t = (key) => key;
  const item = {
    messages: [
      { role: 'system', content: '继续会话 使用工具时请参照说明与格式要求，仅使用所列出的工具' },
      { role: 'user', content: 'latest' },
    ],
    history_text: [
      'Userhello',
      'Assistanthi',
    ].join(''),
  };

  const result = buildListModeMessages(item, t);
  assert.equal(result.historyMerged, true);
  assert.deepEqual(result.messages, [
    { role: 'user', content: 'hello' },
    { role: 'assistant', content: 'hi' },
    { role: 'user', content: 'latest' },
  ]);
});

test('chat history transcript parser replaces current input file placeholder', async () => {
  const {
    buildListModeMessages,
  } = await loadUtils();
  const t = (key) => key;
  const item = {
    messages: [{
      role: 'system',
      content: '继续会话',
    }],
    history_text: [
      '# HISTORY.txt',
      'Prior conversation history and tool progress.',
      '',
      '=== 1. SYSTEM ===',
      'policy',
      '',
      '=== 2. USER ===',
      'hello',
      '',
      '=== 3. ASSISTANT ===',
      'hi',
      '',
      '=== 4. TOOL ===',
      '[name=search_web tool_call_id=call_1]',
      '{"ok":true}',
      '',
      '=== 5. USER ===',
      'latest',
      '',
    ].join('\n'),
  };

  const result = buildListModeMessages(item, t);
  assert.equal(result.historyMerged, true);
  assert.deepEqual(result.messages, [
    { role: 'system', content: 'policy' },
    { role: 'user', content: 'hello' },
    { role: 'assistant', content: 'hi' },
    { role: 'tool', content: '[name=search_web tool_call_id=call_1]\n{"ok":true}' },
    { role: 'user', content: 'latest' },
  ]);
});
