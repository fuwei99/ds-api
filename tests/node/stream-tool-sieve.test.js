'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

const {
  extractToolNames,
  createToolSieveState,
  processToolSieveChunk,
  flushToolSieve,
  parseToolCalls,
  parseToolCallsDetailed,
  parseStandaloneToolCalls,
  formatOpenAIStreamToolCalls,
} = require('../../internal/js/helpers/stream-tool-sieve.js');

function runSieve(chunks, toolNames) {
  const state = createToolSieveState();
  const events = [];
  for (const chunk of chunks) {
    events.push(...processToolSieveChunk(state, chunk, toolNames));
  }
  events.push(...flushToolSieve(state, toolNames));
  return events;
}

function collectText(events) {
  return events
    .filter((evt) => evt.type === 'text' && evt.text)
    .map((evt) => evt.text)
    .join('');
}

test('extractToolNames keeps only declared tool names (Go parity)', () => {
  const names = extractToolNames([
    { function: { description: 'no name tool' } },
    { function: { name: ' read_file ' } },
    { function: { name: 'read_file' } },
    {},
  ]);
  assert.deepEqual(names, ['read_file']);
});

test('parseToolCalls parses XML markup tool call', () => {
  const payload = '<tool_calls><invoke name="read_file"><parameter name="path">README.MD</parameter></invoke></tool_calls>';
  const calls = parseToolCalls(payload, ['read_file']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'read_file');
  assert.deepEqual(calls[0].input, { path: 'README.MD' });
});

test('parseToolCalls parses EPSE shell as XML-compatible tool call', () => {
  const payload = '<|EPSE|tool_calls><|EPSE|invoke name="read_file"><|EPSE|parameter name="path">README.MD</|EPSE|parameter></|EPSE|invoke></|EPSE|tool_calls>';
  const calls = parseToolCalls(payload, ['read_file']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'read_file');
  assert.deepEqual(calls[0].input, { path: 'README.MD' });
});

test('parseToolCalls tolerates fullwidth closing slash in EPSE wrapper', () => {
  const payload = '<|EPSE|tool_calls><|EPSE|invoke name="execute_code"><|EPSE|parameter name="code"><![CDATA[print("hi")]]></|EPSE|parameter></|EPSE|invoke><／EPSE|tool_calls>';
  const calls = parseToolCalls(payload, ['execute_code']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'execute_code');
  assert.deepEqual(calls[0].input, { code: 'print("hi")' });
});

test('parseToolCalls tolerates sentencepiece separator and fullwidth terminator', () => {
  const payload = '<|EPSE▁tool_calls|><|EPSE▁invoke▁name="execute_code"><|EPSE▁parameter▁name="code"><![CDATA[print("hi")]]></|EPSE▁parameter></|EPSE▁invoke></|EPSE▁tool_calls＞';
  const calls = parseToolCalls(payload, ['execute_code']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'execute_code');
  assert.deepEqual(calls[0].input, { code: 'print("hi")' });
});

test('parseToolCalls tolerates fullwidth opening delimiter and Unicode attribute confusables', () => {
  const payload = '＜|EPSE　tool_calls＞＜|EPSE　invoke　name＝“execute_code”＞＜|EPSE　parameter　name＝“code”＞<![CDATA[print("hi")]]>＜／EPSE|parameter＞＜／EPSE|invoke＞＜／EPSE|tool_calls＞';
  const calls = parseToolCalls(payload, ['execute_code']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'execute_code');
  assert.deepEqual(calls[0].input, { code: 'print("hi")' });
});

test('parseToolCalls canonicalizes confusable candidate shell only', () => {
  const payload = '<|\u200b\uff24\u0405\u039cL|to\u03bfl\uff3fcalls><|\ufeffEPSE|inv\u03bfk\u0435 n\u0430me\uff1d\u201cexecute_code\u201d><|\u200bEPSE|par\u0430meter n\u0430me\uff1d\u201ccode\u201d><![\ufeff\u0421D\u0410T\u0410[print("hi")]]></|\u200bEPSE|par\u0430meter></|\u200bEPSE|inv\u03bfk\u0435></|\u200b\uff24\u0405\u039cL|to\u03bfl\uff3fcalls>';
  const calls = parseToolCalls(payload, ['execute_code']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'execute_code');
  assert.deepEqual(calls[0].input, { code: 'print("hi")' });
});

test('parseToolCalls parses hyphenated EPSE shell with here-doc CDATA', () => {
  const payload = `<epse-tool-calls>
<epse-invoke name="Bash">
<epse-parameter name="command"><![CDATA[git commit -m "$(cat <<'EOF'
docs: add missing directory entries and package descriptions to architecture docs
Fill gaps identified in architecture audit: add artifacts/ and static/ to
directory tree, and document 7 auxiliary internal/ packages (textclean,
claudeconv, compat, rawsample, devcapture, util, version) in Section 3.

Co-Authored-By: Claude Opus 4.7 noreply@anthropic.com
EOF
)"]]></epse-parameter>
<epse-parameter name="description"><![CDATA[Create commit with architecture doc updates]]></epse-parameter>
</epse-invoke>
</epse-tool-calls>`;
  const calls = parseToolCalls(payload, ['Bash']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'Bash');
  assert.equal(calls[0].input.description, 'Create commit with architecture doc updates');
  assert.equal(calls[0].input.command.includes('git commit -m "$(cat <<\'EOF\''), true);
  assert.equal(calls[0].input.command.includes('Co-Authored-By: Claude Opus 4.7'), true);
});

test('parseToolCalls parses underscored EPSE shell (Vercel parity)', () => {
  const payload = `<epse_tool_calls>
<epse_invoke name="search_web">
<epse_parameter name="query"><![CDATA[2026年5月 热点事件]]></epse_parameter>
<epse_parameter name="topic"><![CDATA[news]]></epse_parameter>
</epse_invoke>
<epse_invoke name="eval_javascript">
<epse_parameter name="code"><![CDATA[1 + 1]]></epse_parameter>
</epse_invoke>
</epse_tool_calls>`;
  const calls = parseToolCalls(payload, ['search_web', 'eval_javascript']);
  assert.equal(calls.length, 2);
  assert.equal(calls[0].name, 'search_web');
  assert.deepEqual(calls[0].input, { query: '2026年5月 热点事件', topic: 'news' });
  assert.equal(calls[1].name, 'eval_javascript');
  assert.deepEqual(calls[1].input, { code: '1 + 1' });
});

test('parseToolCalls parses arbitrary-prefixed tool markup shells', () => {
  const samples = [
    '<abc|tool_calls><abc|invoke name="Read"><abc|parameter name="file_path">README.md</abc|parameter></abc|invoke></abc|tool_calls>',
    '<vendor_tool_calls><vendor_invoke name="Read"><vendor_parameter name="file_path">README.md</vendor_parameter></vendor_invoke></vendor_tool_calls>',
    '<agent - tool_calls><agent - invoke name="Read"><agent - parameter name="file_path">README.md</agent - parameter></agent - invoke></agent - tool_calls>',
  ];
  for (const payload of samples) {
    const calls = parseToolCalls(payload, ['Read']);
    assert.equal(calls.length, 1);
    assert.equal(calls[0].name, 'Read');
    assert.deepEqual(calls[0].input, { file_path: 'README.md' });
  }
});

test('parseToolCalls parses camel-prefixed tool markup shell', () => {
  const payload = '<DSmartToolCalls><DSmartInvoke name="Bash"><DSmartParameter name="command"><![CDATA[git push]]></DSmartParameter><DSmartParameter name="description"><![CDATA[Push dev branch to origin]]></DSmartParameter></DSmartInvoke></DSmartToolCalls>';
  const calls = parseToolCalls(payload, ['Bash']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'Bash');
  assert.deepEqual(calls[0].input, {
    command: 'git push',
    description: 'Push dev branch to origin',
  });
});

test('parseToolCalls ignores camel-prefixed tool markup lookalike', () => {
  const payload = '<DSmartToolCallsExtra><DSmartInvoke name="Bash"><DSmartParameter name="command">git push</DSmartParameter></DSmartInvoke></DSmartToolCallsExtra>';
  const calls = parseToolCalls(payload, ['Bash']);
  assert.equal(calls.length, 0);
});

test('parseToolCalls parses fullwidth EPSE shell drift', () => {
  const payload = `<ｅｐＳＥ|tool_calls>
  <ｅｐＳＥ|invoke name="Read">
    <ｅｐＳＥ|parameter name="file_path"＞<![CDATA[/Users/aq/Desktop/myproject/Personal_Blog/README.md]]＞</ｅｐＳＥ|parameter>
  </ｅｐＳＥ|invoke>
  <ｅｐＳＥ|invoke name="Read">
    <ｅｐＳＥ|parameter name="file_path"＞<![CDATA[/Users/aq/Desktop/myproject/Personal_Blog/index.html]]＞</ｅｐＳＥ|parameter>
  </ｅｐＳＥ|invoke>
</ｅｐＳＥ|tool_calls>`;
  const calls = parseToolCalls(payload, ['Read']);
  assert.equal(calls.length, 2);
  assert.equal(calls[0].name, 'Read');
  assert.deepEqual(calls[0].input, { file_path: '/Users/aq/Desktop/myproject/Personal_Blog/README.md' });
  assert.equal(calls[1].name, 'Read');
  assert.deepEqual(calls[1].input, { file_path: '/Users/aq/Desktop/myproject/Personal_Blog/index.html' });
});

test('parseToolCalls parses CJK-angle EPS drift', () => {
  const payload = `<EPS|tool_calls>
<EPS|invoke name="Bash">
<EPS|parameter name="description"|>〈![CDATA[Show commits on local dev not on origin/dev]]〉〈/EPS|parameter〉
<EPS|parameter name="command"|>〈![CDATA[git log --oneline origin/dev..dev]]〉〈/EPS|parameter〉
〈/EPS|invoke〉
<EPS|invoke name="Bash">
<EPS|parameter name="description"|>〈![CDATA[Show commits on origin/dev not on local dev]]〉〈/EPS|parameter〉
<EPS|parameter name="command"|>〈![CDATA[git log --oneline dev..origin/dev]]〉〈/EPS|parameter〉
〈/EPS|invoke〉
<EPS|invoke name="Bash">
<EPS|parameter name="description"|>〈![CDATA[Check tracking branch status]]〉〈/EPS|parameter〉
<EPS|parameter name="command"|>〈![CDATA[git status -b --short]]〉〈/EPS|parameter〉
〈/EPS|invoke〉
〈/EPS|tool_calls〉`;
  const calls = parseToolCalls(payload, ['Bash']);
  assert.equal(calls.length, 3);
  assert.equal(calls[0].name, 'Bash');
  assert.equal(calls[0].input.command, 'git log --oneline origin/dev..dev');
  assert.equal(calls[1].input.description, 'Show commits on origin/dev not on local dev');
  assert.equal(calls[2].input.command, 'git status -b --short');
});

test('parseToolCalls parses fullwidth-bang EPSE drift', () => {
  const payload = `<！EPSE！tool_calls>
  <！EPSE！invoke name=“Bash”>
  <！EPSE！parameter name=“command”><！[CDATA[lsof -i :4321 -t]]><！/EPSE！parameter>
  <！EPSE！parameter name=“description”><！[CDATA[Verify port 4321 is free]]><！/EPSE！parameter>
  <！/EPSE！invoke>
  <！/EPSE！tool_calls>`;
  const calls = parseToolCalls(payload, ['Bash']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'Bash');
  assert.equal(calls[0].input.command, 'lsof -i :4321 -t');
  assert.equal(calls[0].input.description, 'Verify port 4321 is free');
});

test('parseToolCalls parses ideographic-comma EPSE drift', () => {
  const payload = `<、EPSE、tool_calls>
  <、EPSE、invoke name="Bash">
    <、EPSE、parameter name="command"><、[CDATA[git commit -m "$(cat <<'EOF'
feat: expand fullwidth bang separator and curly quote tolerance in EPSE tool parsing

Co-Authored-By: Claude Opus 4.6 noreply@anthropic.com
EOF
)"]]><、/EPSE、parameter>
    <、EPSE、parameter name="description"><、[CDATA[Create commit with staged changes]]><、/EPSE、parameter>
  <、/EPSE、invoke>
<、/EPSE、tool_calls>`;
  const calls = parseToolCalls(payload, ['Bash']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'Bash');
  assert.equal(calls[0].input.command.includes('git commit -m "$(cat <<\'EOF\''), true);
  assert.equal(calls[0].input.command.includes('Co-Authored-By: Claude Opus 4.6 noreply@anthropic.com'), true);
  assert.equal(calls[0].input.description, 'Create commit with staged changes');
});

test('parseToolCalls parses EPSE control separator drift', () => {
  for (const sep of ['␂', '\x02']) {
    const payload = `<EPSE${sep}tool_calls>
  <EPSE${sep}invoke name="Read">
    <EPSE${sep}parameter name="file_path"><![CDATA[/tmp/input.txt]]></EPSE${sep}parameter>
  </EPSE${sep}invoke>
</EPSE${sep}tool_calls>`;
    const calls = parseToolCalls(payload, ['Read']);
    assert.equal(calls.length, 1);
    assert.equal(calls[0].name, 'Read');
    assert.deepEqual(calls[0].input, { file_path: '/tmp/input.txt' });
  }
});

test('parseToolCalls parses arbitrary-prefixed tool tags', () => {
  const payload = `<proto💥tool_calls>
  <proto💥invoke name="Read">
    <proto💥parameter name="file_path"><![CDATA[/tmp/input.txt]]></proto💥parameter>
  </proto💥invoke>
</proto💥tool_calls>`;
  const calls = parseToolCalls(payload, ['Read']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'Read');
  assert.deepEqual(calls[0].input, { file_path: '/tmp/input.txt' });
});

test('parseToolCalls allows all-empty parameter payloads', () => {
  const payload = `<T|EPSE|tool_calls>
  <T|EPSE|invoke name="TaskOutput">
    <T|EPSE|parameter name="task_id"></T|EPSE|parameter>
    <T|EPSE|parameter name="block"></T|EPSE|parameter>
    <T|EPSE|parameter name="timeout"></T|EPSE|parameter>
  </T|EPSE|invoke>
</T|EPSE|tool_calls>`;
  const calls = parseToolCalls(payload, ['TaskOutput']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'TaskOutput');
  assert.deepEqual(calls[0].input, { task_id: '', block: '', timeout: '' });
});

test('parseToolCalls ignores bare hyphenated tool_calls lookalike', () => {
  const payload = '<tool-calls><invoke name="Bash"><parameter name="command">pwd</parameter></invoke></tool-calls>';
  const calls = parseToolCalls(payload, ['Bash']);
  assert.equal(calls.length, 0);
});

test('parseToolCalls tolerates EPSE trailing pipe tag terminator', () => {
  const payload = [
    '<|EPSE|tool_calls| ',
    '  <|EPSE|invoke name="terminal">',
    '    <|EPSE|parameter name="command"><![CDATA[find "/home" -type d]]></|EPSE|parameter>',
    '    <|EPSE|parameter name="timeout"><![CDATA[10]]></|EPSE|parameter>',
    '  </|EPSE|invoke>',
    '</|EPSE|tool_calls>',
  ].join('\n');
  const calls = parseToolCalls(payload, ['terminal']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'terminal');
  assert.deepEqual(calls[0].input, { command: 'find "/home" -type d', timeout: 10 });
});

test('parseToolCalls tolerates EPSE trailing novel separator tag terminator', () => {
  const payload = [
    '<EPSEtool_calls※>',
    '  <EPSEinvoke name="Bash"※>',
    '    <EPSEparameter name="command"※><![CDATA[pwd]]></EPSEparameter※>',
    '  </EPSEinvoke※>',
    '</EPSEtool_calls※>',
  ].join('\n');
  const calls = parseToolCalls(payload, ['Bash']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'Bash');
  assert.deepEqual(calls[0].input, { command: 'pwd' });
});

test('parseToolCalls tolerates extra leading less-than before EPSE tags', () => {
  const payload = [
    '<<|EPSE|tool_calls>',
    '  <<|EPSE|invoke name="Bash">',
    '    <<|EPSE|parameter name="command"><![CDATA[pwd]]></|EPSE|parameter>',
    '  </|EPSE|invoke>',
    '</|EPSE|tool_calls>',
  ].join('\n');
  const calls = parseToolCalls(payload, ['Bash']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'Bash');
  assert.deepEqual(calls[0].input, { command: 'pwd' });
});

test('parseToolCalls tolerates repeated EPSE prefix noise', () => {
  const payload = [
    '<<EPSE|EPSE|tool_calls>',
    '  <<EPSE|EPSE|invoke name="Bash">',
    '    <<EPSE|EPSE|parameter name="command"><![CDATA[git status]]></EPSE|EPSE|parameter>',
    '  </EPSE|EPSE|invoke>',
    '</EPSE|EPSE|tool_calls>',
  ].join('\n');
  const calls = parseToolCalls(payload, ['Bash']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'Bash');
  assert.deepEqual(calls[0].input, { command: 'git status' });
});

test('parseToolCalls tolerates EPSE space-separator typo', () => {
  const payload = '<|EPSE tool_calls><|EPSE invoke name="Read"><|EPSE parameter name="file_path"><![CDATA[/tmp/input.txt]]></|EPSE parameter></|EPSE invoke></|EPSE tool_calls>';
  const calls = parseToolCalls(payload, ['Read']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'Read');
  assert.deepEqual(calls[0].input, { file_path: '/tmp/input.txt' });
});

test('parseToolCalls supports EPSE shorthand parameter tag', () => {
  const payload = [
    '<|EPSE|tool_calls>',
    '  <|EPSE|invoke name="terminal">',
    '    <|EPSE|parameter name="command"><![CDATA[cd D:/ds2api && ls scripts/ && echo \'=== workflows ===\' && ls -R .github/workflows/ 2>/dev/null && echo \'=== version 相关 ===\' && ls -la VERSION 2>/dev/null; cat VERSION 2>/dev/null; echo \'---\'; grep -rn "VERSION\\|version" scripts/*.sh scripts/*.go 2>/dev/null | head -40]]></|EPSE>',
    '    <|EPSE parameter name="workdir"><![CDATA[D:/ds2api]]></|EPSE>',
    '  </|EPSE|invoke>',
    '</|EPSE|tool_calls>',
  ].join('\n');
  const calls = parseToolCalls(payload, ['terminal']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'terminal');
  assert.equal(calls[0].input.workdir, 'D:/ds2api');
  assert.ok(calls[0].input.command.includes('cd D:/ds2api'));
});

test('parseToolCalls supports EPSE shorthand parameter without local name', () => {
  const payload = '<|EPSE|tool_calls><|EPSE|invoke name="Bash"><|EPSE name="command"><![CDATA[pwd]]></|EPSE></|EPSE|invoke></|EPSE|tool_calls>';
  const calls = parseToolCalls(payload, ['Bash']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'Bash');
  assert.deepEqual(calls[0].input, { command: 'pwd' });
});

test('parseToolCalls rejects EPSE shorthand parameter without name attribute', () => {
  const payload = '<|EPSE|tool_calls><|EPSE|invoke name="Bash"><|EPSE foo="bar">pwd</|EPSE></|EPSE|invoke></|EPSE|tool_calls>';
  const calls = parseToolCalls(payload, ['Bash']);
  assert.equal(calls.length, 0);
});

test('parseToolCalls ignores EPSE space lookalike tag names', () => {
  const payload = '<|EPSE tool_calls_extra><|EPSE invoke name="Read"><|EPSE parameter name="file_path">/tmp/input.txt</|EPSE parameter></|EPSE invoke></|EPSE tool_calls_extra>';
  const calls = parseToolCalls(payload, ['Read']);
  assert.equal(calls.length, 0);
});

test('parseToolCalls tolerates collapsed EPSE tag names', () => {
  const todos = [
    '[x] 检查 toolcalls_format.go 格式化逻辑',
    '[x] 检查 toolcalls_parse.go 解析逻辑',
    '[x] 检查 toolcalls_xml.go 和 toolcalls_epse.go',
    '[x] 检查 toolcalls_markup.go 和 toolcalls_json_repair.go',
    '[x] 检查 prompt/tool_calls.go 注入逻辑',
    '[x] 检查 toolstream 流式解析',
    '[x] 查看测试文件确认预期行为',
    '[x] 给出调查结论',
  ].join('\n');
  const payload = `<EPSEtool_calls><EPSEinvoke name="update_todo_list"><EPSEparameter name="todos"><![CDATA[${todos}]]></EPSEparameter></EPSEinvoke></EPSEtool_calls>`;
  const calls = parseToolCalls(payload, ['update_todo_list']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'update_todo_list');
  assert.equal(calls[0].input.todos, todos);
});

test('parseToolCalls ignores collapsed EPSE lookalike tag names', () => {
  const payload = '<EPSEtool_calls_extra><EPSEinvoke name="update_todo_list"><EPSEparameter name="todos">x</EPSEparameter></EPSEinvoke></EPSEtool_calls_extra>';
  const calls = parseToolCalls(payload, ['update_todo_list']);
  assert.equal(calls.length, 0);
});

test('parseToolCalls rejects confusable near-miss tag names', () => {
  const payload = '<tool_calls><inv\u03bfker name="execute_code"><parameter name="code">pwd</parameter></inv\u03bfker></tool_calls>';
  const calls = parseToolCalls(payload, ['execute_code']);
  assert.equal(calls.length, 0);
});

test('parseToolCalls keeps canonical XML examples inside EPSE CDATA', () => {
  const content = '<tool_calls><invoke name="demo"><parameter name="value">x</parameter></invoke></tool_calls>';
  const payload = `<|EPSE|tool_calls><|EPSE|invoke name="write_file"><|EPSE|parameter name="path">notes.md</|EPSE|parameter><|EPSE|parameter name="content"><![CDATA[${content}]]></|EPSE|parameter></|EPSE|invoke></|EPSE|tool_calls>`;
  const calls = parseToolCalls(payload, ['write_file']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'write_file');
  assert.deepEqual(calls[0].input, { path: 'notes.md', content });
});

test('parseToolCalls preserves simple inline markup inside CDATA as text', () => {
  const payload = '<tool_calls><invoke name="Write"><parameter name="description"><![CDATA[<b>urgent</b>]]></parameter></invoke></tool_calls>';
  const calls = parseToolCalls(payload, ['Write']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].input.description, '<b>urgent</b>');
});

test('parseToolCalls keeps confusable markup examples inside CDATA as text', () => {
  const value = '<inv\u03bfke>literal</inv\u03bfke>';
  const payload = `<tool_calls><invoke name="Write"><parameter name="description"><![\u200b\u0421D\u0410T\u0410[${value}]]></parameter></invoke></tool_calls>`;
  const calls = parseToolCalls(payload, ['Write']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].input.description, value);
});

test('parseToolCalls recovers when CDATA never closes inside a valid wrapper', () => {
  const payload = '<tool_calls><invoke name="Write"><parameter name="content"><![CDATA[hello world</parameter></invoke></tool_calls>';
  const calls = parseToolCalls(payload, ['Write']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'Write');
  assert.equal(calls[0].input.content, 'hello world');
});

// A parameter whose CDATA has no `]]>` of its own must not absorb the following
// parameter: the unbounded CDATA-end search would otherwise terminate on the
// NEXT parameter's `]]>`, dropping that parameter and leaking
// `</|EPSE|parameter><|EPSE|parameter name="...">` into the first value.
test('parseToolCalls bounds an unclosed CDATA to its own parameter element', () => {
  const command = '$files = Get-ChildItem -Recurse -Filter *.go -File\nWrite-Host "TOTAL: $($all.Count)"';
  const payload = [
    '<|EPSE|tool_calls>',
    '  <|EPSE|invoke name="pwsh">',
    `    <|EPSE|parameter name="command"><![CDATA[${command}</|EPSE|parameter>`,
    '    <|EPSE|parameter name="description"><![CDATA[Count duplicate error strings]]></|EPSE|parameter>',
    '  </|EPSE|invoke>',
    '</|EPSE|tool_calls>',
  ].join('\n');

  const calls = parseToolCalls(payload, ['pwsh']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'pwsh');
  assert.deepEqual(Object.keys(calls[0].input).sort(), ['command', 'description']);
  assert.equal(calls[0].input.command, command);
  assert.equal(calls[0].input.description, 'Count duplicate error strings');
});

test('parseToolCalls bounds consecutive unclosed CDATA parameters', () => {
  const payload = '<|EPSE|tool_calls><|EPSE|invoke name="Bash">'
    + '<|EPSE|parameter name="command"><![CDATA[echo one</|EPSE|parameter>'
    + '<|EPSE|parameter name="description"><![CDATA[two</|EPSE|parameter>'
    + '<|EPSE|parameter name="workdir"><![CDATA[/tmp]]></|EPSE|parameter>'
    + '</|EPSE|invoke></|EPSE|tool_calls>';
  const calls = parseToolCalls(payload, ['Bash']);
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0].input, { command: 'echo one', description: 'two', workdir: '/tmp' });
});

// The negative that bounds the repair: a value quoting a complete tool-call
// example also carries `<|EPSE|parameter ...><![CDATA[` inside its CDATA body,
// but it never escaped its own element, so it must come back byte-exact.
test('parseToolCalls keeps a quoted tool-call example intact', () => {
  const content = [
    '# How to call a tool',
    '',
    '```xml',
    '<|EPSE|tool_calls>',
    '  <|EPSE|invoke name="Bash">',
    '    <|EPSE|parameter name="command"><![CDATA[echo hi]]></|EPSE|parameter>',
    '  </|EPSE|invoke>',
    '</|EPSE|tool_calls>',
    '```',
  ].join('\n');
  const payload = '<|EPSE|tool_calls><|EPSE|invoke name="Write">'
    + `<|EPSE|parameter name="content"><![CDATA[${content}]]></|EPSE|parameter>`
    + '<|EPSE|parameter name="file_path"><![CDATA[docs/tools.md]]></|EPSE|parameter>'
    + '</|EPSE|invoke></|EPSE|tool_calls>';
  const calls = parseToolCalls(payload, ['Write']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].input.content, content);
  assert.equal(calls[0].input.file_path, 'docs/tools.md');
});

test('sieve bounds an unclosed CDATA to its own parameter element', () => {
  const command = '$files = Get-ChildItem -Recurse\nWrite-Host "TOTAL"';
  const events = runSieve([
    '<|EPSE|tool_calls>\n',
    '  <|EPSE|invoke name="pwsh">\n',
    `    <|EPSE|parameter name="command"><![CDATA[${command}`,
    '</|EPSE|parameter>\n',
    '    <|EPSE|parameter name="description"><![CDATA[Count duplicate error strings]]></|EPSE|parameter>\n',
    '  </|EPSE|invoke>\n',
    '</|EPSE|tool_calls>',
  ], ['pwsh']);

  const calls = events.flatMap((evt) => (Array.isArray(evt.calls) ? evt.calls : []));
  assert.equal(calls.length, 1);
  assert.equal(collectText(events), '');
  assert.deepEqual(Object.keys(calls[0].input).sort(), ['command', 'description']);
  assert.equal(calls[0].input.command, command);
  assert.equal(calls[0].input.description, 'Count duplicate error strings');
});

test('parseToolCalls supports JSON scalar parameters', () => {
  const payload = '<tool_calls><invoke name="configure"><parameter name="count">123</parameter><parameter name="max_tokens"><![CDATA[256]]></parameter><parameter name="enabled">true</parameter></invoke></tool_calls>';
  const calls = parseToolCalls(payload, ['configure']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'configure');
  assert.equal(calls[0].input.count, 123);
  assert.equal(calls[0].input.max_tokens, 256);
  assert.equal(calls[0].input.enabled, true);
});

test('parseToolCalls treats item-only parameter body as array', () => {
  const payload = [
    '<|EPSE|tool_calls>',
    '<|EPSE|invoke name="AskUserQuestion">',
    '<|EPSE|parameter name="questions">',
    '<item>',
    '<question><![CDATA[What would you like to do next?]]></question>',
    '<header><![CDATA[Next step]]></header>',
    '<options>',
    '<item><label><![CDATA[Run tests]]></label><description><![CDATA[Run the test suite]]></description></item>',
    '<item><label><![CDATA[Other task]]></label><description><![CDATA[Something else entirely]]></description></item>',
    '</options>',
    '<multiSelect>false</multiSelect>',
    '</item>',
    '</|EPSE|parameter>',
    '</|EPSE|invoke>',
    '</|EPSE|tool_calls>',
  ].join('\n');
  const calls = parseToolCalls(payload, ['AskUserQuestion']);
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0].input.questions, [
    {
      question: 'What would you like to do next?',
      header: 'Next step',
      options: [
        { label: 'Run tests', description: 'Run the test suite' },
        { label: 'Other task', description: 'Something else entirely' },
      ],
      multiSelect: false,
    },
  ]);
});

test('parseToolCalls treats CDATA item-only body as array', () => {
  const todos = '<br>  <item><br>    <activeForm>Testing EnterWorktree tool</activeForm><br>    <content>Test EnterWorktree tool</content><br>    <status>in_progress</status><br>  </item><br>  <item><br>    <activeForm>Testing TodoWrite tool</activeForm><br>    <content>Test TodoWrite tool</content><br>    <status>completed</status><br>  </item><br>';
  const payload = `<|EPSE|tool_calls><|EPSE|invoke name="TodoWrite"><|EPSE|parameter name="todos"><![CDATA[${todos}]]></|EPSE|parameter></|EPSE|invoke></|EPSE|tool_calls>`;
  const calls = parseToolCalls(payload, ['TodoWrite']);
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0].input.todos, [
    {
      activeForm: 'Testing EnterWorktree tool',
      content: 'Test EnterWorktree tool',
      status: 'in_progress',
    },
    {
      activeForm: 'Testing TodoWrite tool',
      content: 'Test TodoWrite tool',
      status: 'completed',
    },
  ]);
});

test('parseToolCalls treats single-item CDATA body as array', () => {
  const payload = '<tool_calls><invoke name="TodoWrite"><parameter name="todos"><![CDATA[<item>one</item>]]></parameter></invoke></tool_calls>';
  const calls = parseToolCalls(payload, ['TodoWrite']);
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0].input.todos, ['one']);
});

test('parseToolCalls treats loose JSON list as array', () => {
  for (const [label, body] of [
    ['plain text', '{"content":"Test TodoWrite tool","status":"completed"}, {"content":"Another task","status":"pending"}'],
    ['cdata', '<![CDATA[{"content":"Test TodoWrite tool","status":"completed"}, {"content":"Another task","status":"pending"}]]>'],
  ]) {
    const payload = `<tool_calls><invoke name="TodoWrite"><parameter name="todos">${body}</parameter></invoke></tool_calls>`;
    const calls = parseToolCalls(payload, ['TodoWrite']);
    assert.equal(calls.length, 1, label);
    assert.deepEqual(calls[0].input.todos, [
      { content: 'Test TodoWrite tool', status: 'completed' },
      { content: 'Another task', status: 'pending' },
    ]);
  }
});

test('parseToolCalls keeps preserved text parameters as text', () => {
  const payload = '<tool_calls><invoke name="Write"><parameter name="content"><![CDATA[{"content":"Test TodoWrite tool","status":"completed"}, {"content":"Another task","status":"pending"}]]></parameter></invoke></tool_calls>';
  const calls = parseToolCalls(payload, ['Write']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].input.content, '{"content":"Test TodoWrite tool","status":"completed"}, {"content":"Another task","status":"pending"}');
});

test('formatOpenAIStreamToolCalls normalizes camelCase inputSchema string fields', () => {
  const formatted = formatOpenAIStreamToolCalls([
    { name: 'Write', input: { content: { message: 'hi' }, taskId: 1 } },
  ], new Map(), [
    { name: 'Write', inputSchema: { type: 'object', properties: { content: { type: 'string' }, taskId: { type: 'string' } } } },
  ]);
  assert.equal(formatted.length, 1);
  const args = JSON.parse(formatted[0].function.arguments);
  assert.equal(args.content, '{"message":"hi"}');
  assert.equal(args.taskId, '1');
});

test('formatOpenAIStreamToolCalls preserves arrays when schema says array', () => {
  const todos = [{ content: 'x', status: 'pending', priority: 'high' }];
  const formatted = formatOpenAIStreamToolCalls([
    { name: 'todowrite', input: { todos } },
  ], new Map(), [
    { name: 'todowrite', inputSchema: { type: 'object', properties: { todos: { type: 'array', items: { type: 'object' } } } } },
  ]);
  assert.equal(formatted.length, 1);
  const args = JSON.parse(formatted[0].function.arguments);
  assert.deepEqual(args.todos, todos);
});

test('parseToolCalls treats CDATA object fragment as object', () => {
  const fragment = '<question><![CDATA[Pick one]]></question><options><item><label><![CDATA[A]]></label></item><item><label><![CDATA[B]]></label></item></options>';
  const payload = `<tool_calls><invoke name="AskUserQuestion"><parameter name="questions"><![CDATA[${fragment}]]></parameter></invoke></tool_calls>`;
  const calls = parseToolCalls(payload, ['AskUserQuestion']);
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0].input.questions, {
    question: 'Pick one',
    options: [
      { label: 'A' },
      { label: 'B' },
    ],
  });
});

test('parseToolCalls normalizes mixed EPSE and XML tool tags', () => {
  // Models commonly mix EPSE wrapper tags with canonical inner tags.
  const payload = '<|EPSE|tool_calls><invoke name="read_file"><|EPSE|parameter name="path">README.MD</|EPSE|parameter></invoke></|EPSE|tool_calls>';
  const calls = parseToolCalls(payload, ['read_file']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'read_file');
  assert.deepEqual(calls[0].input, { path: 'README.MD' });
});

test('parseToolCalls skips prose mention of same wrapper variant', () => {
  const payload = [
    'Summary: support canonical <tool_calls> and EPSE <|EPSE|tool_calls> wrappers.',
    '',
    '<|EPSE|tool_calls>',
    '<|EPSE|invoke name="Bash">',
    '<|EPSE|parameter name="command"><![CDATA[git status]]></|EPSE|parameter>',
    '</|EPSE|invoke>',
    '</|EPSE|tool_calls>',
  ].join('\n');
  const calls = parseToolCalls(payload, ['Bash']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'Bash');
  assert.equal(calls[0].input.command, 'git status');
});

test('parseToolCalls ignores inline markdown tool example', () => {
  const payload = '示例：`<tool_calls><invoke name="read_file"><parameter name="path">README.md</parameter></invoke></tool_calls>`';
  const calls = parseToolCalls(payload, ['read_file']);
  assert.equal(calls.length, 0);
});

test('parseToolCalls preserves backticks inside tool parameters', () => {
  const payload = '<tool_calls><invoke name="Bash"><parameter name="command">echo `date`</parameter></invoke></tool_calls>';
  const calls = parseToolCalls(payload, ['Bash']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].input.command, 'echo `date`');
});

test('sieve emits tool_calls after prose mentions same wrapper variant', () => {
  const events = runSieve([
    'Summary: support canonical <tool_calls> and EPSE <|EPSE|tool_calls> wrappers.\n\n',
    '<|EPSE|tool_calls>\n',
    '<|EPSE|invoke name="Bash">\n',
    '<|EPSE|parameter name="command"><![CDATA[git status]]></|EPSE|parameter>\n',
    '</|EPSE|invoke>\n',
    '</|EPSE|tool_calls>',
  ], ['Bash']);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].name, 'Bash');
  assert.equal(finalCalls[0].input.command, 'git status');
  assert.equal(collectText(events).includes('Summary:'), true);
});

test('sieve ignores markdown documentation examples', () => {
  const events = runSieve([
    '解析器支持多种工具调用格式。\n\n',
    '入口函数 `ParseToolCalls(text, availableToolNames)` 会返回调用列表。\n\n',
    '核心流程会解析 XML 格式的 `<tool_calls>` / `<invoke>` 标记。\n\n',
    '### 标准 XML 结构\n',
    '```xml\n',
    '<tool_calls>\n',
    '  <invoke name="read_file">\n',
    '    <parameter name="path">config.json</parameter>\n',
    '  </invoke>\n',
    '</tool_calls>\n',
    '```\n\n',
    'EPSE 风格形如 `<invoke name="tool">...</invoke>`，也可能提到 `<tool_calls>` 包裹。\n',
  ], ['read_file']);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  const text = collectText(events);
  assert.equal(finalCalls.length, 0);
  assert.equal(text.includes('标准 XML 结构'), true);
  assert.equal(text.includes('EPSE 风格'), true);
});

test('sieve ignores inline markdown tool example split across chunks', () => {
  const events = runSieve([
    '示例：`',
    '<tool_calls><invoke name="read_file"><parameter name="path">README.md</parameter></invoke></tool_calls>',
    '` 完毕。',
  ], ['read_file']);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  const text = collectText(events);
  assert.equal(finalCalls.length, 0);
  assert.equal(text.includes('<tool_calls>'), true);
  assert.equal(text.includes('完毕'), true);
});

test('sieve emits real tool after unclosed inline markdown in same chunk', () => {
  const events = runSieve([
    'note with stray ` before real call <tool_calls><invoke name="read_file"><parameter name="path">real.md</parameter></invoke></tool_calls>',
  ], ['read_file']);
  const text = collectText(events);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].input.path, 'real.md');
  assert.equal(text.includes('stray ` before real call'), true);
});

test('sieve emits real tool after unclosed inline markdown across chunks', () => {
  const events = runSieve([
    'note with stray ` before real call ',
    '<tool_calls><invoke name="read_file"><parameter name="path">real.md</parameter></invoke></tool_calls>',
  ], ['read_file']);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].input.path, 'real.md');
});

test('sieve emits real tool after split inline markdown tool example closes', () => {
  const events = runSieve([
    '示例：`',
    '<tool_calls><invoke name="read_file"><parameter name="path">README.md</parameter></invoke></tool_calls>',
    '` ',
    '<tool_calls><invoke name="read_file"><parameter name="path">real.md</parameter></invoke></tool_calls>',
  ], ['read_file']);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].input.path, 'real.md');
});

test('sieve emits tool_calls for EPSE space-separator typo', () => {
  const events = runSieve([
    '准备读取文件。\n',
    '<|EPSE tool_calls>\n',
    '<|EPSE invoke name="Read">\n',
    '<|EPSE parameter name="file_path"><![CDATA[/tmp/input.txt]]></|EPSE parameter>\n',
    '</|EPSE invoke>\n',
    '</|EPSE tool_calls>',
  ], ['Read']);
  const text = collectText(events);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].name, 'Read');
  assert.equal(finalCalls[0].input.file_path, '/tmp/input.txt');
  assert.equal(text.includes('准备读取文件'), true);
  assert.equal(text.includes('<|EPSE invoke'), false);
});

test('sieve emits tool_calls for fullwidth closing slash and preserves suffix text', () => {
  const input = '<|EPSE|tool_calls><|EPSE|invoke name="execute_code"><|EPSE|parameter name="code"><![CDATA[print("hi")]]></|EPSE|parameter></|EPSE|invoke><／EPSE|tool_calls> sao cụm này lại đc trả là 1 message';
  const events = runSieve([input], ['execute_code']);
  const text = collectText(events);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].name, 'execute_code');
  assert.deepEqual(finalCalls[0].input, { code: 'print("hi")' });
  assert.equal(text, ' sao cụm này lại đc trả là 1 message');
});

test('sieve emits tool_calls for sentencepiece separator and fullwidth terminator', () => {
  const input = '<|EPSE▁tool_calls|><|EPSE▁invoke▁name="execute_code"><|EPSE▁parameter▁name="code"><![CDATA[print("hi")]]></|EPSE▁parameter></|EPSE▁invoke></|EPSE▁tool_calls＞ suffix';
  const events = runSieve([input], ['execute_code']);
  const text = collectText(events);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].name, 'execute_code');
  assert.deepEqual(finalCalls[0].input, { code: 'print("hi")' });
  assert.equal(text, ' suffix');
});

test('sieve emits tool_calls for fullwidth opening delimiter and Unicode attribute confusables', () => {
  const input = '＜|EPSE　tool_calls＞＜|EPSE　invoke　name＝“execute_code”＞＜|EPSE　parameter　name＝“code”＞<![CDATA[print("hi")]]>＜／EPSE|parameter＞＜／EPSE|invoke＞＜／EPSE|tool_calls＞ suffix';
  const events = runSieve([input], ['execute_code']);
  const text = collectText(events);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].name, 'execute_code');
  assert.deepEqual(finalCalls[0].input, { code: 'print("hi")' });
  assert.equal(text, ' suffix');
});

test('sieve emits tool_calls for confusable candidate shell and preserves suffix text', () => {
  const input = '<|\u200b\uff24\u0405\u039cL|to\u03bfl\uff3fcalls><|\ufeffEPSE|inv\u03bfk\u0435 n\u0430me\uff1d\u201cexecute_code\u201d><|\u200bEPSE|par\u0430meter n\u0430me\uff1d\u201ccode\u201d><![\ufeff\u0421D\u0410T\u0410[print("hi")]]></|\u200bEPSE|par\u0430meter></|\u200bEPSE|inv\u03bfk\u0435></|\u200b\uff24\u0405\u039cL|to\u03bfl\uff3fcalls> suffix';
  const events = runSieve([input], ['execute_code']);
  const text = collectText(events);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].name, 'execute_code');
  assert.deepEqual(finalCalls[0].input, { code: 'print("hi")' });
  assert.equal(text, ' suffix');
});

test('sieve repairs confusable missing opening wrapper and preserves suffix text', () => {
  const events = runSieve([
    '<inv\u03bfk\u0435 n\u0430me="read_file">\n',
    '  <par\u0430meter n\u0430me="path"><![\u200b\u0421D\u0410T\u0410[README.md]]></par\u0430meter>\n',
    '</inv\u03bfk\u0435>\n',
    '</to\u03bfl_calls> trailing prose',
  ], ['read_file']);
  const text = collectText(events);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].name, 'read_file');
  assert.deepEqual(finalCalls[0].input, { path: 'README.md' });
  assert.equal(text, ' trailing prose');
});

test('sieve emits tool_calls for EPSE trailing pipe tag terminator', () => {
  const events = runSieve([
    '<|EPSE|tool_calls| \n',
    '<|EPSE|invoke name="terminal">\n',
    '<|EPSE|parameter name="command"><![CDATA[find "/home" -type d]]></|EPSE|parameter>\n',
    '<|EPSE|parameter name="timeout"><![CDATA[10]]></|EPSE|parameter>\n',
    '</|EPSE|invoke>\n',
    '</|EPSE|tool_calls>',
  ], ['terminal']);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  const text = collectText(events);
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].name, 'terminal');
  assert.deepEqual(finalCalls[0].input, { command: 'find "/home" -type d', timeout: 10 });
  assert.equal(text.toLowerCase().includes('epse'), false);
});

test('sieve emits tool_calls for EPSE control separator drift', () => {
  for (const sep of ['␂', '\x02']) {
    const events = runSieve([
      `<EPSE${sep}tool`,
      '_calls>\n',
      `<EPSE${sep}invoke name="Read">\n`,
      `<EPSE${sep}parameter name="file_path"><![CDATA[/tmp/input.txt]]></EPSE${sep}parameter>\n`,
      `</EPSE${sep}invoke>\n`,
      `</EPSE${sep}tool_calls>`,
    ], ['Read']);
    const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
    assert.equal(finalCalls.length, 1);
    assert.equal(finalCalls[0].name, 'Read');
    assert.equal(finalCalls[0].input.file_path, '/tmp/input.txt');
    const text = collectText(events);
    assert.equal(text.toLowerCase().includes('epse'), false);
    assert.equal(text.includes(sep), false);
  }
});

test('sieve emits tool_calls for arbitrary-prefixed tool tags', () => {
  const events = runSieve([
    '<proto💥tool',
    '_calls>\n',
    '<proto💥invoke name="Read">\n',
    '<proto💥parameter name="file_path"><![CDATA[/tmp/input.txt]]></proto💥parameter>\n',
    '</proto💥invoke>\n',
    '</proto💥tool_calls>',
  ], ['Read']);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].name, 'Read');
  assert.equal(finalCalls[0].input.file_path, '/tmp/input.txt');
  const text = collectText(events);
  assert.equal(text.includes('proto'), false);
  assert.equal(text.includes('💥'), false);
});

test('sieve emits tool_calls for CJK-angle EPS drift', () => {
  const events = runSieve([
    '<EPS|tool_calls>\n',
    '<EPS|invoke name="Bash">\n',
    '<EPS|parameter name="description"|>〈![CDATA[Check tracking branch status]]〉〈/EPS|parameter〉\n',
    '<EPS|parameter name="command"|>〈![CDATA[git status -b --short]]〉〈/EPS|parameter〉\n',
    '〈/EPS|invoke〉\n',
    '〈/EPS|tool_calls〉',
  ], ['Bash']);
  const finalCalls = events.flatMap((evt) => (evt.type === 'tool_calls' ? evt.calls : []));
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].name, 'Bash');
  assert.equal(finalCalls[0].input.command, 'git status -b --short');
  assert.equal(collectText(events), '');
});

test('sieve emits tool_calls for fullwidth-bang EPSE drift', () => {
  const events = runSieve([
    '<！EPSE！tool_calls>\n',
    '  <！EPSE！invoke name=“Bash”>\n',
    '  <！EPSE！parameter name=“command”><！[CDATA[lsof -i :4321 -t]]><！/EPSE！parameter>\n',
    '  <！EPSE！parameter name=“description”><！[CDATA[Verify port 4321 is free]]><！/EPSE！parameter>\n',
    '  <！/EPSE！invoke>\n',
    '  <！/EPSE！tool_calls>',
  ], ['Bash']);
  const finalCalls = events.flatMap((evt) => (evt.type === 'tool_calls' ? evt.calls : []));
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].name, 'Bash');
  assert.equal(finalCalls[0].input.command, 'lsof -i :4321 -t');
  assert.equal(collectText(events), '');
});

test('sieve emits tool_calls for ideographic-comma EPSE drift', () => {
  const events = runSieve([
    '<、EPSE、tool_calls>\n',
    '  <、EPSE、invoke name="Bash">\n',
    "    <、EPSE、parameter name=\"command\"><、[CDATA[git commit -m \"$(cat <<'EOF'\n",
    'feat: expand fullwidth bang separator and curly quote tolerance in EPSE tool parsing\n\n',
    'Co-Authored-By: Claude Opus 4.6 noreply@anthropic.com\n',
    'EOF\n',
    ')"]]><、/EPSE、parameter>\n',
    '    <、EPSE、parameter name="description"><、[CDATA[Create commit with staged changes]]><、/EPSE、parameter>\n',
    '  <、/EPSE、invoke>\n',
    '<、/EPSE、tool_calls>',
  ], ['Bash']);
  const finalCalls = events.flatMap((evt) => (evt.type === 'tool_calls' ? evt.calls : []));
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].name, 'Bash');
  assert.equal(finalCalls[0].input.command.includes('git commit -m'), true);
  assert.equal(collectText(events), '');
});

test('sieve emits all-empty arbitrary-prefixed tool tags without leaking text', () => {
  const payload = [
    '<T|EPSE|tool_calls>\n',
    '  <T|EPSE|invoke name="TaskOutput">\n',
    '    <T|EPSE|parameter name="task_id"></T|EPSE|parameter>\n',
    '    <T|EPSE|parameter name="block"></T|EPSE|parameter>\n',
    '    <T|EPSE|parameter name="timeout"></T|EPSE|parameter>\n',
    '  </T|EPSE|invoke>\n',
    '</T|EPSE|tool_calls>',
  ].join('');
  for (const chunks of [[payload], payload.match(/.{1,8}/gs)]) {
    const events = runSieve(chunks, ['TaskOutput']);
    const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
    assert.equal(finalCalls.length, 1);
    assert.equal(finalCalls[0].name, 'TaskOutput');
    assert.deepEqual(finalCalls[0].input, { task_id: '', block: '', timeout: '' });
    assert.equal(collectText(events), '');
  }
});

test('sieve emits tool_calls for extra leading less-than EPSE tags without leaking prefix', () => {
  const events = runSieve([
    '<<|EPSE|tool_calls>\n',
    '<<|EPSE|invoke name="Bash">\n',
    '<<|EPSE|parameter name="command"><![CDATA[pwd]]></|EPSE|parameter>\n',
    '</|EPSE|invoke>\n',
    '</|EPSE|tool_calls>',
  ], ['Bash']);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  const text = collectText(events);
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].name, 'Bash');
  assert.deepEqual(finalCalls[0].input, { command: 'pwd' });
  assert.equal(text.includes('<'), false);
});

test('sieve keeps EPSE space lookalike tag names as text', () => {
  const input = '<|EPSE tool_calls_extra><|EPSE invoke name="Read"><|EPSE parameter name="file_path">/tmp/input.txt</|EPSE parameter></|EPSE invoke></|EPSE tool_calls_extra>';
  const events = runSieve([input], ['Read']);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(finalCalls.length, 0);
  assert.equal(collectText(events), input);
});

test('sieve emits tool_calls for collapsed EPSE tag names and preserves prefix text', () => {
  const todos = [
    '[x] 检查 toolcalls_format.go 格式化逻辑',
    '[x] 检查 toolcalls_parse.go 解析逻辑',
    '[x] 检查 toolcalls_xml.go 和 toolcalls_epse.go',
    '[x] 检查 toolcalls_markup.go 和 toolcalls_json_repair.go',
    '[x] 检查 prompt/tool_calls.go 注入逻辑',
    '[x] 检查 toolstream 流式解析',
    '[x] 查看测试文件确认预期行为',
    '[x] 给出调查结论',
  ].join('\n');
  const events = runSieve([
    '[]\n',
    '<EPSEtool_calls>\n',
    '<EPSEinvoke name="update_todo_list">\n',
    `<EPSEparameter name="todos"><![CDATA[${todos}]]></EPSEparameter>\n`,
    '</EPSEinvoke>\n',
    '</EPSEtool_calls>',
  ], ['update_todo_list']);
  const text = collectText(events);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].name, 'update_todo_list');
  assert.equal(finalCalls[0].input.todos, todos);
  assert.equal(text, '[]\n');
});

test('sieve keeps collapsed EPSE lookalike tag names as text', () => {
  const input = '<EPSEtool_calls_extra><EPSEinvoke name="update_todo_list"><EPSEparameter name="todos">x</EPSEparameter></EPSEinvoke></EPSEtool_calls_extra>';
  const events = runSieve([input], ['update_todo_list']);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(finalCalls.length, 0);
  assert.equal(collectText(events), input);
});

test('sieve keeps confusable near-miss wrappers as text', () => {
  const input = '<to\u03bfl_callz><inv\u03bfke name="read_file"><parameter name="path">README.md</parameter></inv\u03bfke></to\u03bfl_callz>';
  const events = runSieve([input], ['read_file']);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(finalCalls.length, 0);
  assert.equal(collectText(events), input);
});

test('sieve preserves review body with alias mentions before real EPSE tool calls', () => {
  const events = runSieve([
    "Done reviewing the diff. Here's my analysis before we commit:\n\n",
    'Summary of Changes\n',
    'EPSE wrapper variant support — recognize aliases (<epse|tool_calls>, <|tool_calls>) alongside canonical <tool_calls> and <|EPSE|tool_calls> wrappers.\n\n',
    '<|EPSE|tool_calls>\n',
    '<|EPSE|invoke name="Bash">\n',
    '<|EPSE|parameter name="command"><![CDATA[git add docs/toolcall-semantics.md internal/toolstream/tool_sieve_xml.go]]></|EPSE|parameter>\n',
    '<|EPSE|parameter name="description"><![CDATA[Stage all relevant changed files]]></|EPSE|parameter>\n',
    '</|EPSE|invoke>\n',
    '<|EPSE|invoke name="Bash">\n',
    '<|EPSE|parameter name="command"><![CDATA[git commit -m "$(cat <<\'EOF\'\nfeat(toolstream): expand EPSE wrapper detection\n\nSupport EPSE wrapper aliases: <epse|tool_calls> and <|tool_calls> alongside existing canonical wrappers.\nEOF\n)"]]></|EPSE|parameter>\n',
    '<|EPSE|parameter name="description"><![CDATA[Create commit with all staged changes]]></|EPSE|parameter>\n',
    '</|EPSE|invoke>\n',
    '</|EPSE|tool_calls>',
  ], ['Bash']);
  const text = collectText(events);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(finalCalls.length, 2);
  assert.equal(text.includes('<|EPSE|tool_calls> wrappers'), true);
  assert.equal(text.includes('Summary of Changes'), true);
  assert.equal(text.includes('git add docs/toolcall-semantics.md'), false);
});

test('sieve preserves Chinese review body with inline EPSE mention before real tool call', () => {
  const events = runSieve([
    '# Context from my IDE setup:\n\n## My request for Codex:\n',
    '基于我的审查，这是工作区更改的总结和提交。\n\n## 审查报告\n\n### 文档\n\nAPI.md 中的工具调用部分缺少针对新 EPSE 别名的更新——它只提到了 `',
    '<|EPSE|tool_calls>` 和 canonical `<tool_calls>`。由于这涉及 API 兼容性和文档准确性，需要在下游进行记录。\n\n',
    '### 代码\n\n所有更改现在一致地处理四个 EPSE wrapper 变体。\n\n现在提交已暂存的更改。\n\n',
    '<|EPSE|tool_calls>\n',
    '  <|EPSE|invoke name="Bash">\n',
    '    <|EPSE|parameter name="command"><![CDATA[git commit -m "$(cat <<\'EOF\'\nfeat: expand EPSE tool-call alias and fence handling\nEOF\n)"]]></|EPSE|parameter>\n',
    '    <|EPSE|parameter name="description"><![CDATA[Commit staged changes]]></|EPSE|parameter>\n',
    '  </|EPSE|invoke>\n',
    '</|EPSE|tool_calls>\n\n补充',
  ], ['Bash']);
  const text = collectText(events);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(finalCalls.length, 1);
  assert.equal(text.includes('它只提到了 `<|EPSE|tool_calls>` 和 canonical `<tool_calls>`。由于这涉及 API 兼容性'), true);
  assert.equal(text.includes('补充'), true);
  assert.equal(text.includes('<|EPSE|invoke'), false);
});

test('sieve captures hyphenated EPSE shell with here-doc CDATA', () => {
  const events = runSieve([
    '<epse-tool-calls>\n',
    '<epse-invoke name="Bash">\n',
    '<epse-parameter name="command"><![CDATA[git commit -m "$(cat <<\'EOF\'\n',
    'docs: add missing directory entries and package descriptions to architecture docs\n',
    'Fill gaps identified in architecture audit: add artifacts/ and static/ to\n',
    'directory tree, and document 7 auxiliary internal/ packages (textclean,\n',
    'claudeconv, compat, rawsample, devcapture, util, version) in Section 3.\n\n',
    'Co-Authored-By: Claude Opus 4.7 noreply@anthropic.com\n',
    'EOF\n',
    ')"]]></epse-parameter>\n',
    '<epse-parameter name="description"><![CDATA[Create commit with architecture doc updates]]></epse-parameter>\n',
    '</epse-invoke>\n',
    '</epse-tool-calls>',
  ], ['Bash']);
  const text = collectText(events);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].input.command.includes('git commit -m "$(cat <<\'EOF\''), true);
  assert.equal(finalCalls[0].input.command.includes('Co-Authored-By: Claude Opus 4.7'), true);
  assert.equal(text.includes('epse-tool-calls'), false);
  assert.equal(text.includes('git commit -m'), false);
});

test('parseToolCalls ignores JSON tool_calls payload (XML-only)', () => {
  const payload = JSON.stringify({
    tool_calls: [{ name: 'read_file', input: { path: 'README.MD' } }],
  });
  const calls = parseToolCalls(payload, ['read_file']);
  assert.equal(calls.length, 0);
});

test('parseToolCalls ignores tool_call payloads that exist only inside fenced code blocks', () => {
  const text = [
    'I will call a tool now.',
    '```xml',
    '<tool_calls><invoke name="read_file"><parameter name="path">README.md</parameter></invoke></tool_calls>',
    '```',
  ].join('\n');
  const calls = parseToolCalls(text, ['read_file']);
  assert.equal(calls.length, 0);
});

test('parseToolCalls keeps unknown schema names when toolNames is provided', () => {
  const payload = '<tool_calls><invoke name="not_in_schema"><parameter name="q">go</parameter></invoke></tool_calls>';
  const calls = parseToolCalls(payload, ['search']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].name, 'not_in_schema');
});

test('sieve emits tool_calls for XML tool call payload', () => {
  const events = runSieve(
    ['<tool_calls><invoke name="read_file"><parameter name="path">README.MD</parameter></invoke></tool_calls>'],
    ['read_file'],
  );
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].name, 'read_file');
});

test('sieve emits tool_calls when XML tag spans multiple chunks', () => {
  const events = runSieve(
    [
      '<tool_calls><invoke name="read_file">',
      '<parameter name="path">README.MD</parameter></invoke></tool_calls>',
    ],
    ['read_file'],
  );
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].name, 'read_file');
});

test('sieve emits tool_calls when EPSE tag spans multiple chunks', () => {
  const events = runSieve(
    [
      '<|EPSE|tool',
      '_calls><|EPSE|invoke name="read_file">',
      '<|EPSE|parameter name="path">README.MD</|EPSE|parameter></|EPSE|invoke></|EPSE|tool_calls>',
    ],
    ['read_file'],
  );
  const leakedText = collectText(events);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(leakedText, '');
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].name, 'read_file');
});

test('sieve emits tool_calls when fullwidth EPSE prefix variant spans multiple chunks', () => {
  const events = runSieve(
    [
      '<|EPSE|tool',
      '_calls>\n',
      '<|EPSE|invoke name="Bash">\n',
      '<|EPSE|parameter name="command"><![CDATA[ls -la /Users/aq/Desktop/myproject/ds2api/]]></|EPSE|parameter>\n',
      '<|EPSE|parameter name="description"><![CDATA[List project root contents]]></|EPSE|parameter>\n',
      '</|EPSE|invoke>\n',
      '<|EPSE|invoke name="Bash">\n',
      '<|EPSE|parameter name="command"><![CDATA[cat /Users/aq/Desktop/myproject/ds2api/package.json 2>/dev/null || echo "No package.json found"]]></|EPSE|parameter>\n',
      '<|EPSE|parameter name="description"><![CDATA[Check for existing package.json]]></|EPSE|parameter>\n',
      '</|EPSE|invoke>\n',
      '</|EPSE|tool_calls>',
    ],
    ['Bash'],
  );
  const leakedText = collectText(events);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(leakedText, '');
  assert.equal(finalCalls.length, 2);
  assert.equal(finalCalls[0].name, 'Bash');
  assert.equal(finalCalls[1].name, 'Bash');
});

test('sieve keeps long XML tool calls buffered until the closing tag arrives', () => {
  const longContent = 'x'.repeat(4096);
  const splitAt = longContent.length / 2;
  const events = runSieve(
    [
      '<tool_calls>\n  <invoke name="write_to_file">\n    <parameter name="content"><![CDATA[',
      longContent.slice(0, splitAt),
      longContent.slice(splitAt),
      ']]></parameter>\n  </invoke>\n</tool_calls>',
    ],
    ['write_to_file'],
  );
  const leakedText = collectText(events);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(leakedText, '');
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].name, 'write_to_file');
  assert.equal(finalCalls[0].input.content, longContent);
});

test('sieve recovers when CDATA never closes inside a valid wrapper', () => {
  const events = runSieve(
    [
      '<tool_calls>\n  <invoke name="Write">\n    <parameter name="content"><![CDATA[',
      'hello world',
      '</parameter>\n  </invoke>\n</tool_calls>',
    ],
    ['Write'],
  );
  const leakedText = collectText(events);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].name, 'Write');
  assert.equal(finalCalls[0].input.content, 'hello world');
  assert.equal(leakedText, '');
});

test('sieve keeps CDATA tool examples buffered until the outer closing tag arrives', () => {
  const content = [
    '# DS2API 4.0 更新内容',
    '',
    'x'.repeat(4096),
    '```xml',
    '<tool_calls>',
    '  <invoke name="demo">',
    '    <parameter name="value">x</parameter>',
    '  </invoke>',
    '</tool_calls>',
    '```',
    'tail',
  ].join('\n');
  const innerClose = content.indexOf('</tool_calls>') + '</tool_calls>'.length;
  const state = createToolSieveState();
  const chunks = [
    '<tool_calls>\n  <invoke name="Write">\n    <parameter name="content"><![CDATA[',
    content.slice(0, innerClose),
    content.slice(innerClose),
    ']]></parameter>\n    <parameter name="file_path">DS2API-4.0-Release-Notes.md</parameter>\n  </invoke>\n</tool_calls>',
  ];
  const events = [];
  chunks.forEach((chunk, idx) => {
    const next = processToolSieveChunk(state, chunk, ['Write']);
    if (idx <= 1) {
      assert.deepEqual(next, []);
    }
    events.push(...next);
  });
  events.push(...flushToolSieve(state, ['Write']));

  const leakedText = collectText(events);
  const finalCalls = events.filter((evt) => evt.type === 'tool_calls').flatMap((evt) => evt.calls || []);
  assert.equal(leakedText, '');
  assert.equal(finalCalls.length, 1);
  assert.equal(finalCalls[0].name, 'Write');
  assert.equal(finalCalls[0].input.content, content);
});

test('parseToolCalls keeps XML-looking CDATA content intact', () => {
  const content = [
    '# Release notes',
    '```xml',
    '<tool_calls><invoke name="demo"><parameter name="value">x</parameter></invoke></tool_calls>',
    '```',
  ].join('\n');
  const payload = `<tool_calls><invoke name="Write"><parameter name="content"><![CDATA[${content}]]></parameter><parameter name="file_path">DS2API-4.0-Release-Notes.md</parameter></invoke></tool_calls>`;
  const calls = parseToolCalls(payload, ['Write']);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].input.content, content);
  assert.equal(calls[0].input.file_path, 'DS2API-4.0-Release-Notes.md');
});

test('sieve passes JSON tool_calls payload through as text (XML-only)', () => {
  const events = runSieve(
    ['{"tool_calls":[{"name":"read_file","input":{"path":"README.MD"}}]}'],
    ['read_file'],
  );
  const leakedText = collectText(events);
  const hasToolCall = events.some((evt) => evt.type === 'tool_calls' && evt.calls?.length > 0);
  assert.equal(hasToolCall, false);
  assert.equal(leakedText.includes('tool_calls'), true);
});

test('sieve keeps embedded invalid tool-like json as normal text to avoid stream stalls', () => {
  const events = runSieve(
    [
      '前置正文D。',
      "{'tool_calls':[{'name':'read_file','input':{'path':'README.MD'}}]}",
      '后置正文E。',
    ],
    ['read_file'],
  );
  const leakedText = collectText(events);
  const hasToolCall = events.some((evt) => evt.type === 'tool_calls');
  assert.equal(hasToolCall, false);
  assert.equal(leakedText.includes('前置正文D。'), true);
  assert.equal(leakedText.includes('后置正文E。'), true);
  assert.equal(leakedText.toLowerCase().includes('tool_calls'), true);
});

test('sieve releases malformed executable-looking XML wrappers as text', () => {
  const chunk = '<tool_calls><invoke name="read_file"><param>{"path":"README.MD"}</param></invoke></tool_calls>';
  const events = runSieve([chunk], ['read_file']);
  const leakedText = collectText(events);
  const hasToolCalls = events.some((evt) => evt.type === 'tool_calls' && evt.calls?.length > 0);
  assert.equal(hasToolCalls, false);
  assert.equal(leakedText, chunk);
});

test('sieve keeps bare tool_call XML as plain text without wrapper', () => {
  const chunk = '<invoke name="read_file"><parameter name="path">README.MD</parameter></invoke>';
  const events = runSieve([chunk], ['read_file']);
  const leakedText = collectText(events);
  const hasToolCalls = events.some((evt) => evt.type === 'tool_calls' && evt.calls?.length > 0);
  assert.equal(hasToolCalls, false);
  assert.equal(leakedText, chunk);
});

test('sieve flushes incomplete captured XML tool blocks by falling back to raw text', () => {
  const events = runSieve(
    [
      '前置正文G。',
      '<tool_calls>\n',
      '  <invoke name="read_file">\n',
    ],
    ['read_file'],
  );
  const leakedText = collectText(events);
  const expected = ['前置正文G。', '<tool_calls>\n', '  <invoke name="read_file">\n'].join('');
  const hasToolCalls = events.some((evt) => evt.type === 'tool_calls' && evt.calls?.length > 0);
  assert.equal(hasToolCalls, false);
  assert.equal(leakedText, expected);
});

test('sieve captures XML wrapper tags with attributes without leaking wrapper text', () => {
  const events = runSieve(
    [
      '前置正文H。',
      '<tool_calls id="x"><invoke name="read_file"><parameter name="path">README.MD</parameter></invoke></tool_calls>',
      '后置正文I。',
    ],
    ['read_file'],
  );
  const leakedText = collectText(events);
  const hasToolCall = events.some((evt) => evt.type === 'tool_calls' && evt.calls?.length > 0);
  assert.equal(hasToolCall, true);
  assert.equal(leakedText.includes('前置正文H。'), true);
  assert.equal(leakedText.includes('后置正文I。'), true);
  assert.equal(leakedText.includes('<tool_calls id=\"x\">'), false);
  assert.equal(leakedText.includes('</tool_calls>'), false);
});

test('sieve keeps plain text intact in tool mode when no tool call appears', () => {
  const events = runSieve(
    ['你好，', '这是普通文本回复。', '请继续。'],
    ['read_file'],
  );
  const leakedText = collectText(events);
  const hasToolCall = events.some((evt) => evt.type === 'tool_calls');
  assert.equal(hasToolCall, false);
  assert.equal(leakedText, '你好，这是普通文本回复。请继续。');
});

test('sieve keeps plain "tool_calls" prose as text when no valid payload follows', () => {
  const events = runSieve(
    ['前置。', '这里提到 tool_calls 只是解释，不是调用。', '后置。'],
    ['read_file'],
  );
  const leakedText = collectText(events);
  const hasToolCall = events.some((evt) => evt.type === 'tool_calls' && evt.calls?.length > 0);
  assert.equal(hasToolCall, false);
  assert.equal(leakedText.includes('tool_calls'), true);
  assert.equal(leakedText, '前置。这里提到 tool_calls 只是解释，不是调用。后置。');
});

test('sieve keeps numbered planning prose when no tool payload follows', () => {
  const events = runSieve(
    ['好的，我会依次测试每个工具。\n\n1. 获取当前时间'],
    ['get_current_time'],
  );
  const leakedText = collectText(events);
  const hasToolCall = events.some((evt) => evt.type === 'tool_calls' && evt.calls?.length > 0);
  assert.equal(hasToolCall, false);
  assert.equal(leakedText, '好的，我会依次测试每个工具。\n\n1. 获取当前时间');
});

test('sieve does not trigger tool calls for long fenced examples beyond legacy tail window', () => {
  const longPadding = 'x'.repeat(700);
  const events = runSieve(
    [
      `前置说明\n\`\`\`json\n${longPadding}\n`,
      '{"tool_calls":[{"name":"read_file","input":{"path":"README.MD"}}]}\n',
      '```',
      '\n后置说明',
    ],
    ['read_file'],
  );
  const hasTool = events.some((evt) => evt.type === 'tool_calls' && evt.calls?.length > 0);
  const leakedText = collectText(events);
  assert.equal(hasTool, false);
  assert.equal(leakedText.includes('后置说明'), true);
  assert.equal(leakedText.toLowerCase().includes('tool_calls'), true);
});

test('sieve keeps fence state when triple-backticks are split across chunks', () => {
  const events = runSieve(
    [
      '示例开始\n``',
      '`json\n{"tool_calls":[{"name":"read_file","input":{"path":"README.MD"}}]}\n',
      '```',
      '\n示例结束',
    ],
    ['read_file'],
  );
  const hasTool = events.some((evt) => evt.type === 'tool_calls' && evt.calls?.length > 0);
  const leakedText = collectText(events);
  assert.equal(hasTool, false);
  assert.equal(leakedText.includes('示例结束'), true);
  assert.equal(leakedText.toLowerCase().includes('tool_calls'), true);
});

test('formatOpenAIStreamToolCalls reuses ids with the same idStore', () => {
  const idStore = new Map();
  const calls = [{ name: 'read_file', input: { path: 'README.MD' } }];
  const first = formatOpenAIStreamToolCalls(calls, idStore);
  const second = formatOpenAIStreamToolCalls(calls, idStore);
  assert.equal(first.length, 1);
  assert.equal(second.length, 1);
  assert.equal(first[0].id, second[0].id);
});

test('parseToolCalls rejects mismatched markup tags', () => {
  const payload = '<tool_calls><invoke name="read_file"><parameter name="path">README.md</function></invoke></tool_calls>';
  const calls = parseToolCalls(payload, ['read_file']);
  assert.equal(calls.length, 0);
});

test('sieve hides DSML wrapper from visible text', () => {
  const input = 'before <DSML calls><DSML invoke name="read"><DSML parameter name="path">/tmp/a.txt</DSML parameter></DSML invoke></DSML calls> after';
  const chunks = [
    'before <DSML calls><DSML invoke name="read">',
    '<DSML parameter name="path">',
    '/tmp/a.txt',
    '</DSML parameter>',
    '</DSML invoke>',
    '</DSML calls> after',
  ];
  const state = createToolSieveState();
  const events = [];
  chunks.forEach((chunk) => {
    const next = processToolSieveChunk(state, chunk, ['read']);
    assert.equal(
      next.some((evt) => evt.type === 'text' && evt.text.includes('<DSML')),
      false,
    );
    events.push(...next);
  });
  events.push(...flushToolSieve(state, ['read']));

  const hasToolCall = events.some((evt) => evt.type === 'tool_calls' && evt.calls?.length > 0);
  assert.equal(hasToolCall, false);
  const text = collectText(events);
  assert.equal(text.includes('<DSML'), false);
  assert.equal(text.includes('before'), true);
  assert.equal(text.includes('after'), true);
});

test('sieve hides unclosed DSML wrapper at flush', () => {
  const state = createToolSieveState();
  const events = [];
  for (const chunk of ['before <DSML calls><DSML invoke name="read">']) {
    const next = processToolSieveChunk(state, chunk, ['read']);
    assert.equal(
      next.some((evt) => evt.type === 'text' && evt.text.includes('<DSML')),
      false,
    );
    events.push(...next);
  }
  events.push(...flushToolSieve(state, ['read']));

  const hasToolCall = events.some((evt) => evt.type === 'tool_calls' && evt.calls?.length > 0);
  assert.equal(hasToolCall, false);
  assert.equal(collectText(events), 'before ');
});
