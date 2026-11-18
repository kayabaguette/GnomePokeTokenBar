// gjs -m extension/test/chartModel.test.js
import Gio from 'gi://Gio';

import {assert, run, test} from './harness.js';
import * as C from '../lib/chartModel.js';

const fixtureDir = Gio.File.new_for_uri(import.meta.url).get_parent().get_child('fixtures');
function fixture(name) {
    const [, bytes] = fixtureDir.get_child(name).load_contents(null);
    return JSON.parse(new TextDecoder().decode(bytes));
}
const active = fixture('state-active.json');
const combined = active.usage.combined;
const today = active.usage.calendar.today;

// MonthDailyTrendTests.testAxisLabelsCoverTheFirstDayEverySeventhAndAlwaysToday
function monthLabels(t) {
    return C.columns({monthDaily: {start: `${t.slice(0, 7)}-01`, tokens: [], costUsd: [], coverage: []}}, t, 'month')
        .map(c => C.axisLabel(c.date, t, 'month'))
        .filter(l => l !== null);
}

test('month axis: 1st, multiples of 7, always today', () => {
    const t = '2026-08-24';
    const label = d => C.axisLabel(`2026-08-${String(d).padStart(2, '0')}`, t, 'month');
    for (const [d, want] of [[1, '1'], [7, '7'], [14, '14'], [21, '21'], [28, '28'], [24, '24']])
        assert.equal(label(d), want, `day ${d}`);
    for (const d of [2, 3, 9, 15, 20, 23, 25, 31])
        assert.equal(label(d), null, `day ${d}`);
});

test('month axis: label counts and collision drops (MonthDailyTrendTests)', () => {
    assert.deepEqual(monthLabels('2026-08-31'), ['1', '7', '14', '21', '28', '31']);
    assert.deepEqual(monthLabels('2026-08-24'), ['1', '7', '14', '21', '24', '28']);
    assert.deepEqual(monthLabels('2026-08-22'), ['1', '7', '14', '22', '28']);
    assert.deepEqual(monthLabels('2026-08-29'), ['1', '7', '14', '21', '29']);
    assert.deepEqual(monthLabels('2026-08-30'), ['1', '7', '14', '21', '30']);
    assert.deepEqual(monthLabels('2026-08-21'), ['1', '7', '14', '21', '28']);
    assert.deepEqual(monthLabels('2026-08-02'), ['2', '7', '14', '21', '28']);
    for (let d = 1; d <= 31; d++) {
        const t = `2026-08-${String(d).padStart(2, '0')}`;
        const all = monthLabels(t);
        assert.ok(all.includes(String(d)), `today ${t} kept`);
        const nums = all.map(Number).sort((a, b) => a - b);
        for (let i = 1; i < nums.length; i++)
            assert.ok(nums[i] - nums[i - 1] >= 3, `${t}: ${nums[i - 1]} and ${nums[i]} collide`);
    }
});

test('rolling axis labels by window (testAxisLabelsByWindow)', () => {
    const t = '2026-10-15';
    for (const w of C.WINDOWS)
        assert.equal(C.axisLabel(t, t, w), '15', w);
    assert.equal(C.axisLabel('2026-10-12', t, '7d'), '12');
    assert.equal(C.axisLabel('2026-10-14', t, '7d'), null);
    assert.equal(C.axisLabel('2026-10-08', t, '14d'), '8');
    assert.equal(C.axisLabel('2026-10-12', t, '14d'), null);
    assert.equal(C.axisLabel('2026-10-08', t, '21d'), '8');
    assert.equal(C.axisLabel('2026-10-01', t, '21d'), '1');
    assert.equal(C.axisLabel('2026-10-05', t, '21d'), null);
    assert.equal(C.axisLabel('2026-10-01', '2026-10-10', '14d'), '1');
    assert.equal(C.axisLabel('2026-10-01', '2026-10-02', '14d'), null);
});

test('rolling axis collisions near the month boundary', () => {
    assert.equal(C.axisLabel('2026-10-02', '2026-10-02', '7d'), '2');
    assert.equal(C.axisLabel('2026-10-01', '2026-10-02', '7d'), null);
    assert.equal(C.axisLabel('2026-10-01', '2026-10-09', '14d'), '1');
    assert.equal(C.axisLabel('2026-10-02', '2026-10-09', '14d'), null);
    assert.equal(C.axisLabel('2026-10-01', '2026-10-04', '7d'), '1');
    // A regular mark right before the next month's 1st yields to it.
    assert.equal(C.axisLabel('2026-10-30', '2026-11-13', '21d'), null);
});

test('rolling columns take the tail of recentDaily', () => {
    const cols = C.columns(combined, today, '7d');
    assert.equal(cols.length, 7);
    assert.equal(cols[6].date, today);
    assert.equal(cols[6].tokens, combined.today.tokens, 'last column equals today');
    assert.equal(cols[0].date, '2026-11-12');
    const sum = C.columns(combined, today, '21d').reduce((s, c) => s + c.tokens, 0);
    assert.equal(sum, combined.recentDaily.tokens.reduce((a, b) => a + b, 0));
});

test('month columns span the calendar month, future days marked', () => {
    const cols = C.columns(combined, today, 'month');
    assert.equal(cols.length, 30);
    assert.equal(cols[0].date, '2026-11-01');
    assert.equal(cols[17].date, today);
    assert.equal(cols[17].isFuture, false);
    assert.equal(cols[18].isFuture, true);
    assert.equal(cols.reduce((s, c) => s + c.tokens, 0), combined.month.tokens, 'Σ monthDaily = month');
    assert.equal(C.columns(combined, '2028-02-10', 'month').length, 29, 'leap February');
});

test('unknown window falls back to 14d', () => {
    assert.equal(C.columns(combined, today, 'bogus').length, 14);
    assert.equal(C.windowTitle('bogus'), 'Last 14 days');
    assert.equal(C.nextWindow('month'), '7d');
    assert.equal(C.nextWindow('14d'), '21d');
});

test('bar geometry', () => {
    assert.equal(C.barHeight(0, 100), C.BASELINE);
    assert.equal(C.barHeight(50, 0), C.BASELINE, 'no peak → baseline, no division by zero');
    assert.equal(C.barHeight(100, 100), C.TRACK);
    assert.equal(C.barHeight(1, 1_000_000), C.BASELINE, 'tiny days keep the baseline');
    assert.close(C.barHeight(50, 100), 13);
    const r0 = C.barRect(0, 14, 320);
    const r13 = C.barRect(13, 14, 320);
    assert.close(r13.x + r13.width, 320, 1e-9, 'last bar ends at the edge');
    assert.close(r0.width, (320 - 13 * C.SPACING) / 14);
    assert.equal(C.columnAt(0, 14, 320), 0);
    assert.equal(C.columnAt(320, 14, 320), 13);
    assert.equal(C.columnAt(-1, 14, 320), -1);
    assert.equal(C.columnAt(160, 14, 320), 6);
});

test('peak, showsPeak, weekend', () => {
    const cols = C.columns(combined, today, '14d');
    assert.equal(C.peak(cols), 284_095_164);
    assert.equal(C.showsPeak(cols), true);
    assert.equal(C.showsPeak([{tokens: 5}, {tokens: 0}]), false, 'one used day → peak equals the readout');
    assert.equal(C.isWeekend('2026-11-21'), true, 'Saturday');
    assert.equal(C.isWeekend('2026-11-22'), true, 'Sunday');
    assert.equal(C.isWeekend('2026-11-18'), false);
    assert.equal(C.showsWeekendTick({date: '2026-11-21', isFuture: true}), false, 'no ticks under future days');
});

test('day arithmetic is DST-safe calendar stepping', () => {
    assert.equal(C.addDays('2026-10-31', 1), '2026-11-01');
    assert.equal(C.addDays('2026-03-29', -1), '2026-03-28');
    assert.equal(C.addDays('2026-01-01', -1), '2025-12-31');
    assert.equal(C.dayDifference('2026-10-25', '2026-10-26'), 1, 'across the EU DST change');
    assert.equal(C.dayDifference('2026-11-01', '2026-11-18'), 17);
});

test('readout and hover text', () => {
    const cols = C.columns(combined, today, '14d');
    assert.equal(C.readout(cols, today, true, 'en-US'), 'Wed, 11/18 187.3M $124.35');
    assert.equal(C.readout(cols, today, false, 'en-US'), 'Wed, 11/18 187.3M');
    assert.equal(C.readout(cols, '1999-01-01', true), '');
    const zero = cols.find(c => c.tokens === 0);
    assert.equal(C.dayText(zero, true, 'en-US').endsWith('0 $0.00'), true);
});

run('chartModel.test.js');
