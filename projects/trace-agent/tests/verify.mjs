import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {readFileSync, openSync, closeSync} from 'node:fs';
import {DatabaseSync} from 'node:sqlite';
import {codex, claude, claudeOtel} from './replay.mjs';

const base = new URL('../', import.meta.url);
const read = path => readFileSync(new URL(path, base), 'utf8');
const expected = JSON.parse(read('fixtures/stage-1/expected.json'));
const schema = read('schema.sql');
assert.equal(createHash('sha256').update(schema).digest('hex'),
  'b758bc78610d591c6098f471f603da3f8c06a4996da531bc7e3abc1bd8a1d42f');

for (const [client, replay] of Object.entries({codex, claude, 'claude-otel': claudeOtel})) {
  const rows = read('fixtures/stage-1/' + client + '.jsonl').trimEnd().split('\n').map(line => JSON.parse(line));
  const facts = replay(rows);
  // Compare complete scoped identities, assignment and support positions, including missing links.
  for (const deliveries of [rows, rows.toReversed(), [...rows, ...rows], rows.flatMap(r => [r, r])]) {
    assert.deepEqual(replay(deliveries), expected[client]);
  }
  if (client === 'claude-otel') {
    const withBody = rows.map(r => ({...r, record: {...r.record, body: 'synthetic private body'}}));
    assert.deepEqual(replay(withBody), facts);
    const withoutRequest = rows.filter(r => r.record['event.name'] !== 'api_request_body');
    assert.equal(replay(withoutRequest).unlinked.length, 2);
    console.log(client, {sourceModelAttempts: facts.sourceModelAttempts.length,
      unlinked: facts.unlinked.length, tasks: facts.tasks.length, attempts: facts.attempts.length});
    continue;
  }
  const resultPosition = client === 'codex' ? 'roll-root:6' : 'cl-root:5#0';
  const withoutCall = rows.filter(r => client === 'codex'
    ? !(r.stream === 'roll-root' && r.seq === 5) : !(r.stream === 'cl-root' && r.seq === 4));
  assert.ok(replay(withoutCall).unlinked.includes(resultPosition));

  // Also put the result first in source order, independently of transport order.
  const early = structuredClone(rows);
  const scope = client === 'codex' ? 'roll-root' : 'cl-root';
  const laterCall = client === 'codex' ? 5 : 4;
  for (const r of early.filter(r => r.stream === scope)) {
    if (r.seq === laterCall) r.seq = laterCall + 1;
    else if (r.seq === laterCall + 1) r.seq = laterCall;
  }
  const earlyFacts = replay(early);
  const earlyAttempt = earlyFacts.attempts.find(a => a.tool === (client === 'codex' ? 'mcp__south__lookup' : 'Agent'));
  assert.equal(earlyAttempt.results[0], scope + ':' + laterCall + (client === 'claude' ? '#0' : ''));
  assert.equal(earlyAttempt.task, facts.tasks[0]);
  assert.equal(earlyFacts.attempts.length, facts.attempts.length);

  const altered = structuredClone(rows);
  const marker = 'synthetic private body';
  if (client === 'codex') {
    const result = altered.find(r => r.record.payload?.call_id === 'cx-2' && r.record.payload.type === 'function_call_output');
    result.record.payload.output = marker;
    const plan = altered.find(r => r.record.method);
    plan.record.params.plan[0].step = marker;
    const wait = structuredClone(rows);
    const response = wait.find(r => r.record.payload?.call_id === 'cx-wait' && r.record.payload.type === 'function_call_output');
    response.record.payload.output = JSON.stringify({timed_out: false, status: {'cx-child': {completed: 'synthetic'}}});
    assert.equal(replay(wait).children[0].state, 'ended');
  } else {
    altered.find(r => r.record.promptId).record.message.content = marker;
    for (const r of altered) {
      for (const block of Array.isArray(r.record.message.content) ? r.record.message.content : []) {
        if (block.type === 'tool_use') block.input = {text: marker};
        if (block.type === 'tool_result') block.content = marker;
      }
    }
    const withoutChild = rows.filter(r => !r.record.agentId);
    assert.deepEqual(replay(withoutChild).children, []);
  }
  assert.deepEqual(replay(altered), facts);
  assert.ok(!JSON.stringify(facts).includes(marker));
  const assigned = facts.children.filter(c => c.task !== null).length;
  const ended = facts.children.filter(c => c.state === 'ended').length;
  console.log(client, {tasks: facts.tasks.length, attempts: facts.attempts.length,
    lineage: facts.children.length, assigned, ended, unlinked: facts.unlinked.length});
}

// A supplied path must be new, so a verification run cannot replace an existing DB.
const [dbPath, ...extra] = process.argv.slice(2);
assert.equal(extra.length, 0, 'usage: node verify.mjs [new-db-path]');
if (dbPath) closeSync(openSync(dbPath, 'wx', 0o600));
const db = new DatabaseSync(dbPath ?? ':memory:');
try {
  db.exec(schema);
  const values = sql => db.prepare(sql).all().map(row => Object.values(row));
  assert.deepEqual(values('PRAGMA user_version'), [[13]]);
  assert.deepEqual(values('SELECT rev, committed_ms FROM revision'), [[0, 0]]);
  assert.deepEqual(values('SELECT singleton, epoch, replay_floor FROM store_state'), [[1, 1, 0]]);
  assert.deepEqual(values('PRAGMA foreign_keys'), [[1]]);
  assert.deepEqual(values('PRAGMA foreign_key_check'), []);
  assert.deepEqual(values('PRAGMA integrity_check'), [['ok']]);
  console.log('SQLite', values('SELECT sqlite_version()')[0][0], 'version 13, revision 0, epoch 1, FK on, 0 violations, integrity ok');
} finally {
  db.close();
}
