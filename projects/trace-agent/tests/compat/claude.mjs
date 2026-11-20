// The installed client writes a genuine transcript in a test-owned profile.
// Only the Messages endpoint is simulated. No user config or credentials enter it.
import {spawn} from 'node:child_process';
import {randomUUID} from 'node:crypto';
import {mkdir, writeFile} from 'node:fs/promises';
import {createServer} from 'node:http';
import {join, resolve} from 'node:path';
import {once} from 'node:events';
async function checkpoint(label) {
  if (!process.env.TRACE_COLLECT_HANDSHAKE) return;
  const ready = once(process.stdin, 'data');
  console.log(label);
  await ready;
}

const root = resolve(process.argv[2]);
const workspace = join(root, 'project');
const profile = join(root, 'profile');
await mkdir(workspace, {recursive: true});
await mkdir(profile, {recursive: true});
const example = join(workspace, 'sample.txt');
await writeFile(example, 'synthetic sample\n');
const session = randomUUID();
let sequence = 0;
let requestedPlan = false;
function blocks(input) {
  const main = input.tools?.some(t => t.name === 'Read');
  if (!input.tools?.some(t => t.name === 'Agent') || !main) return [{type: 'text', text: 'sample'}];
  const results = input.messages.flatMap(m => Array.isArray(m.content) ? m.content : [])
    .filter(b => b.type === 'tool_result');
  if (results.length === 0 && sequence === 0) {
    sequence++;
    return [{type: 'tool_use', id: 'toolu_sample_read', name: 'Read', input: {file_path: example}}];
  }
  if (sequence === 1) {
    sequence++;
    return [{type: 'tool_use', id: 'toolu_sample_agent', name: 'Agent',
      input: {subagent_type: 'sample', description: 'Read sample', prompt: 'Report sample completion.'}}];
  }
  if (sequence === 2) {
    const plan = input.tools.find(t => ['TodoWrite', 'TaskCreate'].includes(t.name));
    if (plan) {
      sequence++;
      requestedPlan = true;
      const data = plan.name === 'TodoWrite'
        ? {todos: [{content: 'Read sample', activeForm: 'Reading sample', status: 'completed'}]}
        : {subject: 'Read sample', description: 'Read a synthetic sample'};
      return [{type: 'tool_use', id: 'toolu_sample_plan', name: plan.name, input: data}];
    }
  }
  return [{type: 'text', text: 'sample complete'}];
}
const server = createServer(async (req, res) => {
  if (req.url.split('?')[0] !== '/v1/messages') {
    res.writeHead(404).end(); return;
  }
  let raw = '';
  for await (const piece of req) raw += piece;
  const input = JSON.parse(raw);
  if (sequence === 1 && input.tools?.some(t => t.name === 'Agent')) await checkpoint('running');
  const content = blocks(input);
  const reason = content[0].type === 'tool_use' ? 'tool_use' : 'end_turn';
  const msg = {id: 'msg_' + randomUUID(), type: 'message', role: 'assistant', model: input.model,
    content, stop_reason: reason, stop_sequence: null, usage: {input_tokens: 12, output_tokens: 9}};
  if (!input.stream) { res.writeHead(200, {'content-type': 'application/json'}).end(JSON.stringify(msg)); return; }
  res.writeHead(200, {'content-type': 'text/event-stream'});
  const event = (type, data) => res.write(`event: ${type}\ndata: ${JSON.stringify({type, ...data})}\n\n`);
  event('message_start', {message: {...msg, content: [], stop_reason: null}});
  for (const [index, b] of content.entries()) {
    const tool = b.type === 'tool_use';
    event('content_block_start', {index, content_block: tool ? {...b, input: {}} : {type: 'text', text: ''}});
    event('content_block_delta', {index, delta: tool ? {type: 'input_json_delta', partial_json: JSON.stringify(b.input)}
      : {type: 'text_delta', text: b.text}});
    event('content_block_stop', {index});
  }
  event('message_delta', {delta: {stop_reason: reason, stop_sequence: null}, usage: {output_tokens: 9}});
  event('message_stop', {});
  res.end();
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
const endpoint = 'http://127.0.0.1:' + server.address().port;
const env = {PATH: process.env.PATH, TMPDIR: root, CLAUDE_CONFIG_DIR: profile, ANTHROPIC_BASE_URL: endpoint,
  ANTHROPIC_API_KEY: 'local-fixture', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', DISABLE_AUTOUPDATER: '1', ENABLE_TOOL_SEARCH: 'false', CLAUDE_CODE_ENABLE_TASKS: 'true',
  NO_PROXY: '127.0.0.1,localhost', HTTPS_PROXY: 'http://127.0.0.1:1', HTTP_PROXY: 'http://127.0.0.1:1'};
if (process.env.TRACE_TEST_OTLP_ENDPOINT) {
  const target = new URL(process.env.TRACE_TEST_OTLP_ENDPOINT);
  if (target.protocol !== 'http:' || target.hostname !== '127.0.0.1' || !target.port ||
      target.pathname !== '/v1/logs' || target.username || target.password || target.search || target.hash) {
    throw new Error('an isolated local OTLP endpoint is required');
  }
  const token = process.env.TRACE_TEST_OTLP_TOKEN;
  if (!/^[A-Za-z0-9_-]{43}$/.test(token ?? '')) throw new Error('invalid OTLP test capability');
  const protocol = process.env.TRACE_TEST_OTLP_PROTOCOL ?? 'http/protobuf';
  if (!['http/protobuf','http/json'].includes(protocol)) throw new Error('unsupported OTLP protocol');
  Object.assign(env, {HOME: root, CLAUDE_CODE_ENABLE_TELEMETRY: '1',
    OTEL_LOGS_EXPORTER:'otlp', OTEL_METRICS_EXPORTER:'none', OTEL_TRACES_EXPORTER:'none',
    OTEL_EXPORTER_OTLP_PROTOCOL:protocol, OTEL_EXPORTER_OTLP_LOGS_PROTOCOL:protocol,
    OTEL_EXPORTER_OTLP_LOGS_ENDPOINT:target.href, OTEL_EXPORTER_OTLP_LOGS_HEADERS:'Authorization=Bearer%20'+token,
    OTEL_LOGS_EXPORT_INTERVAL:'100', OTEL_BLRP_SCHEDULE_DELAY:'100',
    OTEL_LOG_RAW_API_BODIES:process.env.TRACE_TEST_OTLP_CONSENT === '1' ? '1' : '0'});
}
let settings = {disableAllHooks: true};
if (process.env.TRACE_TEST_HOOK_ENDPOINT) {
  const url = new URL(process.env.TRACE_TEST_HOOK_ENDPOINT);
  const token = process.env.TRACE_TEST_HOOK_TOKEN;
  if (url.protocol !== 'http:' || url.hostname !== '127.0.0.1' || !url.port ||
      url.pathname !== '/v1/hooks' || url.username || url.password || url.search || url.hash ||
      !/^[A-Za-z0-9_-]{43}$/.test(token ?? '')) throw new Error('invalid test hook endpoint');
  env.TRACE_TEST_HOOK_TOKEN = token;
  env.HOME = root;
  const hooks = {};
  for (const name of ['PreToolUse', 'PostToolUse', 'PostToolUseFailure']) {
    hooks[name] = [{hooks: [{type: 'http', url: url.href, timeout: 1, onFailure: 'continue',
      headers: {Authorization: 'Bearer $TRACE_TEST_HOOK_TOKEN'}, allowedEnvVars: ['TRACE_TEST_HOOK_TOKEN']}]}];
  }
  settings = {hooks};
}
async function turn(option, prompt) {
  const args = ['-p', '--settings', JSON.stringify(settings), '--model', 'claude-sonnet-4-6', '--strict-mcp-config', '--setting-sources', '',
    '--mcp-config', '{"mcpServers":{}}', '--agents', JSON.stringify({sample: {description: 'Inspect the synthetic sample', prompt: 'Report only completion', tools: ['Read']}}), '--permission-mode', 'dontAsk', '--tools', 'Read,TaskCreate,TodoWrite,EnterPlanMode,Agent',
    '--allowedTools', 'Read,TaskCreate,TodoWrite,EnterPlanMode,Agent', option, session, '--', prompt];
  const child = spawn('claude', args, {cwd: workspace, env, stdio: ['ignore','pipe','pipe'], timeout: 60000});
  // This profile contains only test-owned records and explicitly selected hooks.
  let diagnostic = '';
  child.stdout.on('data', chunk => { diagnostic += chunk; });
  child.stderr.on('data', chunk => { diagnostic += chunk; });
  const code = await new Promise((resolve, reject) => { child.once('error', reject); child.once('close', resolve); });
  if (code !== 0) throw new Error('isolated client failed: ' + diagnostic.slice(0, 500));
}
try {
  await turn('--session-id', 'Read the sample and track the work.');
  await checkpoint('first');
  await turn('--resume', 'Confirm the sample in a new turn.');
  if (!requestedPlan) throw new Error('client did not request the configured planning tool');
  await checkpoint('resumed');
} finally { server.closeAllConnections(); server.close(); process.stdin.pause(); }
