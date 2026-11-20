// Input is test-only stage-two output with synthetic ID tokens mapped back to labels.
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {codex, claude, claudeOtel} from './replay.mjs';

const expected = JSON.parse(readFileSync(new URL('../fixtures/stage-1/expected.json', import.meta.url)));
const projections = JSON.parse(readFileSync(0, 'utf8'));
for (const [name, replay] of Object.entries({codex, claude, 'claude-otel': claudeOtel})) {
  assert.deepEqual(replay(projections[name]), expected[name], name);
}
