// Contract oracle for the two metadata projections in fixtures/stage-1.
import assert from 'node:assert/strict';

function sourceOrder(rows) {
  const seen = new Map();
  for (const row of rows) {
    const key = JSON.stringify([row.stream, row.seq]);
    if (seen.has(key)) assert.deepEqual(row, seen.get(key), 'conflicting data at one source position');
    else seen.set(key, row);
  }
  return [...seen.values()].sort((a, b) => a.stream.localeCompare(b.stream) || a.seq - b.seq);
}

function observations() {
  return {tasks: new Set(), calls: new Map(), results: [], children: new Map(),
    plans: new Map(), timeouts: new Set(), terminal: new Set()};
}

function addCall(state, key, tool, task, at) {
  const existing = state.calls.get(key);
  if (existing) {
    assert.equal(existing.tool, tool);
    assert.equal(existing.task, task);
    return;
  }
  state.calls.set(key, {key, tool, task, call: at, results: [], outcome: 'unknown'});
}

function finish(state) {
  const unlinked = [];
  for (const result of state.results) {
    const call = state.calls.get(result.key);
    if (!call) {
      unlinked.push(result.at);
      continue;
    }
    call.results.push(result.at);
    call.outcome = result.outcome;
  }
  const children = [...state.children.values()].map(child => ({
    ...child, state: state.terminal.has(child.id) ? 'ended' : 'unknown'
  }));
  return {
    tasks: [...state.tasks], attempts: [...state.calls.values()], children,
    unlinked, waitTimeouts: [...state.timeouts], plans: [...state.plans.values()]
  };
}

export function codex(input) {
  const state = observations();
  const sessions = new Map();
  const turns = new Map();
  for (const {stream, seq, record} of sourceOrder(input)) {
    const at = stream + ':' + seq;
    if (record.method === 'turn/plan/updated') {
      const {threadId, turnId, plan} = record.params;
      const task = threadId + '/' + turnId;
      state.plans.set(task, {task, at, steps: plan.map(step => step.status)});
      continue;
    }
    const p = record.payload;
    if (record.type === 'session_meta') {
      sessions.set(stream, p.id);
      turns.delete(stream);
      if (p.parent_thread_id) state.children.set(p.id, {id: p.id, parent: p.parent_thread_id, task: null, at});
      continue;
    }
    if (record.type === 'event_msg' && p.type === 'task_started') {
      const task = sessions.get(stream) + '/' + p.turn_id;
      turns.set(stream, task);
      state.tasks.add(task);
      continue;
    }
    assert.equal(record.type, 'response_item');
    const task = turns.get(stream);
    assert.ok(task, 'fixture requires an unambiguous task_started boundary');
    const key = p.call_id ? [stream, task, 'function', p.call_id].join('/') : null;
    if (p.type === 'function_call') {
      assert.ok(key);
      addCall(state, key, p.name, task, at);
    } else {
      assert.equal(p.type, 'function_call_output');
      state.results.push({key, at, outcome: 'unknown', output: p.output});
    }
  }
  // wait result describes the wait operation. Only an explicit per-child terminal
  // status could end a child. A timeout has no such implication.
  for (const {key, output, at} of state.results) {
    if (state.calls.get(key)?.tool !== 'wait_agent') continue;
    const report = JSON.parse(output);
    if (report.timed_out === true) state.timeouts.add(at);
    for (const [id, status] of Object.entries(report.status ?? {})) {
      if (status && typeof status === 'object' && ('completed' in status || 'errored' in status)) {
        state.terminal.add(id);
      }
    }
  }
  return finish(state);
}

export function claude(input) {
  const state = observations();
  const ancestry = new Map();
  const childRecords = new Map();
  const delegations = [];
  for (const {stream, seq, record} of sourceOrder(input)) {
    const {sessionId, uuid, message, agentId} = record;
    const scope = sessionId + '/' + stream;
    const at = stream + ':' + seq;
    let task = ancestry.get(scope + '/' + record.parentUuid) ?? null;
    if (agentId) childRecords.set(sessionId + '/' + agentId, at);
    // The root fixture has a single human-entered prompt with explicit provenance.
    if (record.type === 'user' && record.userType === 'external' && record.isMeta === false &&
        !record.isSidechain && record.promptId && typeof message.content === 'string') {
      task = sessionId + '/' + record.promptId;
      state.tasks.add(task);
    }
    ancestry.set(scope + '/' + uuid, task);
    if (!Array.isArray(message.content)) continue;
    for (const [index, block] of message.content.entries()) {
      const position = at + '#' + index;
      if (block.type === 'tool_use' && record.type === 'assistant') {
        addCall(state, scope + '/' + block.id, block.name, task, position);
      } else if (block.type === 'tool_result' && record.type === 'user') {
        const key = block.tool_use_id ? scope + '/' + block.tool_use_id : null;
        const outcome = new Map([[true, 'failure'], [false, 'success']]).get(block.is_error) ?? 'unknown';
        state.results.push({key, at: position, outcome});
        if (record.toolUseResult?.agentId && message.content.filter(b => b.type === 'tool_result').length === 1) {
          delegations.push({key, sessionId, id: record.toolUseResult.agentId, at: position, stream});
        }
      }
    }
  }
  for (const {key, sessionId, id, at, stream} of delegations) {
    const agent = state.calls.get(key);
    const childAt = childRecords.get(sessionId + '/' + id);
    if (agent?.tool === 'Agent' && childAt) {
      state.children.set(sessionId + '/' + id, {id, parent: stream, task: agent.task, at, childAt});
    }
  }
  return finish(state);
}

export function claudeOtel(input) {
  const requests = new Map();
  const responses = [];
  const unlinked = [];
  for (const {stream, seq, record} of sourceOrder(input)) {
    const at = stream + ':' + seq;
    const key = record.request_body_id ? stream + '/' + record.request_body_id : null;
    if (record['event.name'] === 'api_request_body') {
      assert.ok(key);
      requests.set(key, {key, request: at, responses: []});
    } else {
      assert.equal(record['event.name'], 'api_response_body');
      responses.push({key, at});
    }
  }
  for (const {key, at} of responses) {
    const request = requests.get(key);
    if (request) request.responses.push(at);
    else unlinked.push(at);
  }
  return {...finish(observations()), unlinked, sourceModelAttempts: [...requests.values()]};
}
