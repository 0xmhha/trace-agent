// Optional local capability check. Supply a directory with saved Codex history.
import {spawn} from 'node:child_process';
import {createInterface} from 'node:readline';
import {setTimeout as delay} from 'node:timers/promises';

const child = spawn('codex', ['app-server', '--stdio'], {stdio: ['pipe', 'pipe', 'ignore']});
const lines = createInterface({input: child.stdout});
const pending = new Map();
const observed = new Set();
let nextId = 0;
let active = true;
function fail(error) {
  active = false;
  for (const {reject} of pending.values()) reject(error);
  pending.clear();
}
child.on('error', fail);
child.on('exit', () => fail(new Error('app-server exited')));
child.stdin.on('error', fail);
lines.on('line', line => {
  const msg = JSON.parse(line);
  if (['turn/started', 'turn/completed', 'item/started', 'item/completed', 'turn/plan/updated'].includes(msg.method)) {
    observed.add(msg.method);
  }
  const call = pending.get(msg.id);
  if (!call) return;
  pending.delete(msg.id);
  if (msg.error) call.reject(new Error('RPC failed with code ' + msg.error.code));
  else call.resolve(msg.result);
});
function rpc(method, params) {
  if (!active) throw new Error('app-server closed');
  const id = ++nextId;
  return new Promise((resolve, reject) => {
    pending.set(id, {resolve, reject});
    child.stdin.write(JSON.stringify({id, method, params}) + '\n');
  });
}
const deadline = setTimeout(() => child.kill(), 30000);
try {
  await rpc('initialize', {clientInfo: {name: 'stage1_read_probe', version: '1'}, capabilities: {experimentalApi: true}});
  child.stdin.write('{"method":"initialized"}\n');
  const threads = await rpc('thread/list', {limit: 1, cwd: process.argv[2] ?? process.cwd()});
  console.log('thread/list', threads.data.length);
  if (threads.data.length) {
    const threadId = threads.data[0].id;
    const turns = await rpc('thread/turns/list', {threadId, limit: 1, itemsView: 'notLoaded'});
    console.log('thread/turns/list', turns.data.length);
    if (turns.data.length) {
      const items = await rpc('thread/items/list', {threadId, turnId: turns.data[0].id, limit: 2});
      console.log('thread/items/list', items.data.length,
        'with item.id', items.data.filter(r => typeof r.item?.id === 'string').length);
    }
  }
  await delay(12000);
  if (!active) throw new Error('app-server closed during observation');
  console.log('observed turn/item notification kinds', [...observed]);
} finally {
  clearTimeout(deadline);
  lines.close();
  child.kill();
}
