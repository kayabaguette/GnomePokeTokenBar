// gjs -m extension/test/prefsModel.test.js
import {assert, run, test} from './harness.js';
import * as P from '../lib/prefsModel.js';

/* ---- refresh presets ---- */

test('refresh: presets map to themselves', () => {
    for (const p of P.REFRESH_PRESETS)
        assert.equal(P.nearestPreset(p), p);
});

test('refresh: other values take the nearest preset, ties the shorter', () => {
    assert.equal(P.nearestPreset(30), 0);
    assert.equal(P.nearestPreset(31), 60);
    assert.equal(P.nearestPreset(90), 60);
    assert.equal(P.nearestPreset(200), 120);
    assert.equal(P.nearestPreset(211), 300);
    assert.equal(P.nearestPreset(5000), 900);
    assert.equal(P.nearestPreset(NaN), 120);
});

/* ---- threshold rule (protocol §9.1) ---- */

test('thresholds: defaults pass through', () => {
    assert.deepEqual(P.applyThresholdRule(80, 95), {warn: 80, crit: 95});
});

test('thresholds: clamp each to its range', () => {
    assert.deepEqual(P.applyThresholdRule(10, 200), {warn: 50, crit: 100});
    assert.deepEqual(P.applyThresholdRule(99, 50), {warn: 75, crit: 80});
});

test('thresholds: raising warn past crit − 5 is refused (crit wins)', () => {
    assert.deepEqual(P.applyThresholdRule(95, 95), {warn: 90, crit: 95});
    assert.deepEqual(P.applyThresholdRule(91, 95), {warn: 90, crit: 95});
    assert.deepEqual(P.applyThresholdRule(90, 95), {warn: 90, crit: 95});
});

test('thresholds: lowering crit pushes warn down', () => {
    assert.deepEqual(P.applyThresholdRule(80, 80), {warn: 75, crit: 80});
    assert.deepEqual(P.applyThresholdRule(85, 85), {warn: 80, crit: 85});
});

test('thresholds: the gap always holds over the whole grid', () => {
    for (let w = 40; w <= 100; w += 5) {
        for (let c = 70; c <= 105; c += 5) {
            const r = P.applyThresholdRule(w, c);
            assert.ok(r.crit - r.warn >= P.THRESHOLD_GAP, `gap for ${w}/${c}`);
            assert.ok(r.crit >= 80 && r.crit <= 100, `crit range for ${w}/${c}`);
            assert.ok(r.warn <= 95, `warn max for ${w}/${c}`);
        }
    }
});

/* ---- difficulty slider (DifficultyTests.swift) ---- */

test('snap keeps two significant digits', () => {
    // Swift: Tests/PokeTokenBarTests/DifficultyTests.swift:287-289
    assert.close(P.snapDifficulty(1.234), 1.2, 1e-9);
    assert.close(P.snapDifficulty(15.7), 16, 1e-9);
    assert.close(P.snapDifficulty(0.0123), 0.012, 1e-9);
});

test('snap of a non-positive value is the lower bound', () => {
    assert.equal(P.snapDifficulty(0), 0.1);
    assert.equal(P.snapDifficulty(-3), 0.1);
});

test('clamp: garbage values', () => {
    // Swift: DifficultyTests.swift:221-231
    assert.close(P.clampDifficulty(0), 0.1);
    assert.close(P.clampDifficulty(-5), 0.1);
    assert.close(P.clampDifficulty(20), 2.0);
    assert.close(P.clampDifficulty(NaN), 1.0);
    assert.close(P.clampDifficulty(Infinity), 1.0);
    assert.close(P.clampDifficulty(-Infinity), 1.0);
});

test('position endpoints map to the range bounds', () => {
    // Swift: DifficultyTests.swift:265-272
    assert.close(P.difficultyAtPosition(0), 0.1, 1e-9);
    assert.close(P.difficultyAtPosition(1), 2.0, 1e-6);
    assert.close(P.difficultyPosition(0.1), 0, 1e-9);
    assert.close(P.difficultyPosition(2.0), 1, 1e-9);
});

test('position round-trips through difficulty', () => {
    // Swift: DifficultyTests.swift:258-263
    for (const value of [0.1, 0.25, 0.5, 1.0, 1.5, 2.0]) {
        const back = P.difficultyAtPosition(P.difficultyPosition(value));
        assert.close(back, value, value * 0.02, `value=${value}`);
    }
});

test('the default is reachable by dragging (±0.01 snap)', () => {
    // Swift: DifficultyTests.swift:275-281
    const exact = P.difficultyPosition(1.0);
    for (const offset of [-0.009, -0.005, 0, 0.005, 0.009])
        assert.equal(P.difficultyAtPosition(exact + offset), 1.0, `offset=${offset}`);
    assert.ok(P.difficultyAtPosition(exact + 0.02) !== 1.0, 'outside the snap band');
});

test('slider output is clean and in range everywhere on the track', () => {
    for (let i = 0; i <= 1000; i++) {
        const v = P.difficultyAtPosition(i / 1000);
        assert.ok(v >= 0.1 && v <= 2.0, `in range at ${i}`);
        assert.equal(v, Number(v.toPrecision(2)), `two significant digits at ${i}: ${v}`);
    }
    assert.equal(P.difficultyAtPosition(-1), 0.1);
    assert.close(P.difficultyAtPosition(2), 2.0);
});

test('percent text', () => {
    // Swift: DifficultyTests.swift:292-299
    assert.equal(P.difficultyValueText(1.0), '100%');
    assert.equal(P.difficultyValueText(0.5), '50%');
    assert.equal(P.difficultyValueText(20), '2000%');
    assert.equal(P.difficultyValueText(0.05), '5.0%');
    assert.equal(P.difficultyValueText(0.0001), '0.01%');
    assert.ok(P.difficultyValueText(0.0001) !== P.difficultyValueText(0.0005));
});

test('all menu items off', () => {
    assert.ok(P.allMenuItemsOff({tokens: false, cost: false, growth: false, limit: false}));
    assert.ok(!P.allMenuItemsOff({tokens: false, cost: false, growth: false, limit: true}));
});

/* ---- floating pet size (SettingsView.swift:375-381; derived) ---- */

test('pet size snaps to 48–384 in steps of 8', () => {
    assert.equal(P.snapPetSize(96), 96);
    assert.equal(P.snapPetSize(99), 96);
    assert.equal(P.snapPetSize(101), 104);
    assert.equal(P.snapPetSize(10), 48);
    assert.equal(P.snapPetSize(1000), 384);
    assert.equal(P.snapPetSize('x'), 96);
});

run('prefsModel.test.js');
