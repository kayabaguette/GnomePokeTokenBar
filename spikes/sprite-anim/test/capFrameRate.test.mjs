// Headless check of the capFrameRate port: node test/capFrameRate.test.mjs
import assert from 'node:assert/strict';
import {test} from 'node:test';

import {capFrameRate} from '../extension/capFrameRate.js';

const seq = (n, delaySec) => Array.from({length: n}, (_, i) => ({frame: i, delaySec}));
const close = (a, b) => Math.abs(a - b) < 1e-9;

test('55 x 0.05 s at floor 0.4 keeps the loop length', () => {
    const out = capFrameRate(seq(55, 0.05), 0.4);
    assert.deepEqual(out.map(f => f.frame), [0, 8, 16, 24, 32, 40]);
    const delays = out.map(f => f.delaySec);
    delays.slice(0, 5).forEach(d => assert.ok(close(d, 0.4), `delay ${d}`));
    assert.ok(close(delays[5], 0.75), `tail delay ${delays[5]}`);
    assert.ok(close(delays.reduce((a, b) => a + b, 0), 2.75));
});

test('0.05 x 4 hits a 0.2 floor despite float accumulation', () => {
    const out = capFrameRate(seq(8, 0.05), 0.2);
    assert.deepEqual(out.map(f => f.frame), [0, 4]);
});

test('floor 0 is native timing (identity)', () => {
    const frames = seq(5, 0.1);
    assert.equal(capFrameRate(frames, 0), frames);
});

test('a single frame passes through', () => {
    const frames = seq(1, 0.1);
    assert.equal(capFrameRate(frames, 0.4), frames);
});

test('a loop shorter than the floor becomes one frame with the summed delay', () => {
    const out = capFrameRate(seq(3, 0.1), 0.4);
    assert.equal(out.length, 1);
    assert.equal(out[0].frame, 0);
    assert.ok(close(out[0].delaySec, 0.3));
});

test('Pikachu manifest timing (0.2 s then 0.1 s frames) at 0.4 s', () => {
    // First frame 200 ms, then 100 ms frames, as in out/25/manifest.json.
    const frames = [{frame: 0, delaySec: 0.2}, ...seq(7, 0.1).map(f => ({...f, frame: f.frame + 1}))];
    const out = capFrameRate(frames, 0.4);
    // 0.2+0.1+0.1 -> frame 0 at 0.4; frames 3..6 -> frame 3 at 0.4;
    // frame 7's 0.1 s tail joins frame 3 (0.5 s), total stays 0.9 s.
    assert.deepEqual(out.map(f => f.frame), [0, 3]);
    assert.ok(close(out[0].delaySec, 0.4));
    assert.ok(close(out[1].delaySec, 0.5));
});
