// gjs -m extension/test/capFrameRate.test.js — the Spike A cases (spikes/sprite-anim/test/).
import {assert, run, test} from './harness.js';
import {capFrameRate} from '../lib/capFrameRate.js';

const seq = (n, delaySec) => Array.from({length: n}, (_, i) => ({frame: i, delaySec}));

test('55 x 0.05 s at floor 0.4 keeps the loop length', () => {
    const out = capFrameRate(seq(55, 0.05), 0.4);
    assert.deepEqual(out.map(f => f.frame), [0, 8, 16, 24, 32, 40]);
    const delays = out.map(f => f.delaySec);
    delays.slice(0, 5).forEach(d => assert.close(d, 0.4));
    assert.close(delays[5], 0.75, 1e-9, 'tail delay');
    assert.close(delays.reduce((a, b) => a + b, 0), 2.75);
});

test('0.05 x 4 hits a 0.2 floor despite float accumulation', () => {
    assert.deepEqual(capFrameRate(seq(8, 0.05), 0.2).map(f => f.frame), [0, 4]);
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
    assert.close(out[0].delaySec, 0.3);
});

test('Pikachu manifest timing (0.2 s then 0.1 s frames) at 0.4 s', () => {
    const frames = [{frame: 0, delaySec: 0.2}, ...seq(7, 0.1).map(f => ({...f, frame: f.frame + 1}))];
    const out = capFrameRate(frames, 0.4);
    assert.deepEqual(out.map(f => f.frame), [0, 3]);
    assert.close(out[0].delaySec, 0.4);
    assert.close(out[1].delaySec, 0.5);
});

test('balanced and smooth floors keep the loop length', () => {
    const frames = seq(20, 0.05);
    for (const floor of [0.2, 0.1]) {
        const out = capFrameRate(frames, floor);
        assert.close(out.reduce((a, f) => a + f.delaySec, 0), 1.0, 1e-9, `floor ${floor}`);
        out.forEach(f => assert.ok(f.delaySec + 1e-9 >= floor, `frame below floor ${floor}`));
    }
});

run('capFrameRate.test.js');
