// gjs -m extension/test/recapModel.test.js
// Ported from Tests/PokeTokenBarTests/UsageRecapTests.swift (URT:332-437) and the
// rendering rules of UI/UsageRecapView.swift (RV). English only, locale pinned.
import Gio from 'gi://Gio';

import {assert, run, test} from './harness.js';
import * as R from '../lib/recapModel.js';

const fixtureDir = Gio.File.new_for_uri(import.meta.url).get_parent().get_child('fixtures');
function fixture(name) {
    const [, bytes] = fixtureDir.get_child(name).load_contents(null);
    return JSON.parse(new TextDecoder().decode(bytes)).data;
}
const EN = 'en-US';

// URT:384-413 fixture: now 2026-09-18 (Friday), Monday-first, ledger since
// 2026-08-01 with 12_345 on 09-17. Buckets as the helper sends them.
function weekBuckets() {
    const out = [];
    for (let d = 14; d <= 20; d++) {
        const key = `2026-09-${d}`;
        out.push({key, tokens: d === 17 ? 12345 : 0, isCurrent: d === 18, hasData: d <= 18});
    }
    return out;
}
function monthBuckets() {
    const out = [];
    for (let d = 1; d <= 30; d++) {
        const key = `2026-09-${String(d).padStart(2, '0')}`;
        out.push({key, tokens: d === 17 ? 12345 : 0, isCurrent: d === 18, hasData: d <= 18});
    }
    return out;
}
function yearBuckets() {
    const out = [];
    for (let m = 1; m <= 12; m++) {
        const key = `2026-${String(m).padStart(2, '0')}-01`;
        out.push({key, tokens: m === 9 ? 12345 : 0, isCurrent: m === 9, hasData: m === 8 || m === 9});
    }
    return out;
}

// URT:369-382
test('current period names', () => {
    assert.equal(R.currentPeriodName('week'), 'This week');
    assert.equal(R.currentPeriodName('month'), 'This month');
    assert.equal(R.currentPeriodName('year'), 'This year');
    assert.deepEqual(R.SCOPES.map(R.scopeName), ['Week', 'Month', 'Year']);
    assert.equal(R.normalizeScope('decade'), 'week');
});

// URT:384-413 (testBucketDescriptions…)
test('bucket descriptions: grouped tokens, a known zero, no data', () => {
    for (const [scope, buckets] of [['week', weekBuckets()], ['month', monthBuckets()]]) {
        const used = buckets.find(b => b.tokens > 0);
        const usedText = R.bucketDescription(used, scope, EN);
        assert.ok(usedText.endsWith(', 12,345'), `${scope}: ${usedText}`);
        assert.equal(usedText, 'Thursday, Sep 17, 12,345', scope);
        const zero = buckets.find(b => b.hasData && b.tokens === 0);
        assert.ok(R.bucketDescription(zero, scope, EN).endsWith(', 0'), `${scope} known zero`);
        const none = buckets.filter(b => !b.hasData).at(-1);
        assert.ok(R.bucketDescription(none, scope, EN).endsWith(', no data'), `${scope} no data`);
    }
    const year = yearBuckets();
    assert.equal(R.bucketDescription(year[8], 'year', EN), 'September 2026, 12,345');
    assert.ok(R.bucketDescription(year[8], 'year', EN).startsWith('September 2026, '));
    assert.equal(R.bucketDescription(year[0], 'year', EN), 'January 2026, no data');
    assert.equal(R.bucketDescription(year[7], 'year', EN), 'August 2026, 0');
});

// URT:343-367
test('bucket labels: one per bucket, week 2 letters, month 1/8/15/22/29, year initials', () => {
    const week = R.bucketLabels(weekBuckets(), 'week', EN);
    assert.deepEqual(week, ['MO', 'TU', 'WE', 'TH', 'FR', 'SA', 'SU']);
    const month = R.bucketLabels(monthBuckets(), 'month', EN);
    assert.equal(month.length, 30);
    month.forEach((l, i) => {
        const day = i + 1;
        assert.equal(l, [1, 8, 15, 22, 29].includes(day) ? String(day) : '', `day ${day}`);
    });
    const year = R.bucketLabels(yearBuckets(), 'year', EN);
    assert.deepEqual(year, ['J', 'F', 'M', 'A', 'M', 'J', 'J', 'A', 'S', 'O', 'N', 'D']);
    const recap = fixture('recap-year.json');
    assert.equal(R.bucketLabels(recap.buckets, 'year', EN).length, recap.buckets.length);
});

// RV:281-282 (derived values)
test('meterLit: 0 stays dark, usage lights at least one block', () => {
    assert.equal(R.meterLit(0, 100), 0);
    assert.equal(R.meterLit(100, 100), 8);
    assert.equal(R.meterLit(1, 100), 1);
    assert.equal(R.meterLit(50, 100), 4);
    assert.equal(R.meterLit(6, 100), 1, '0.48 rounds to 0, minimum 1');
    assert.equal(R.meterLit(7, 100), 1, '0.56 rounds to 1');
    assert.equal(R.meterLit(57, 100), 5, '4.56 rounds to 5');
    assert.equal(R.meterLit(1, 0), 8, 'peak floor of 1');
    for (let t = 1; t <= 100; t++)
        assert.ok(R.meterLit(t, 100) >= 1, `tokens ${t}`);
    assert.equal(R.meterPeak([]), 1);
    assert.equal(R.meterPeak(weekBuckets()), 12345);
    assert.deepEqual(['week', 'month', 'year'].map(R.meterSpacing), [6, 1.5, 4]);
});

// RV:232-245, protocol §7.5 (the extension computes delta)
test('delta chip: up/down arrows, percent, none without a comparison', () => {
    assert.close(R.delta({total: 100, previousTotal: 40}), 1.5, 0.0001);
    assert.close(R.delta({total: 300, previousTotal: 100}), 2.0, 0.0001);
    assert.equal(R.chipText(1.5), '▲150%');
    assert.equal(R.chipText(-0.609), '▼60.9%');
    assert.equal(R.chipText(0), '▲0%');
    assert.equal(R.delta({total: 5, previousTotal: 0}), null);
    assert.equal(R.delta({total: 5, previousTotal: null}), null);
    assert.equal(R.chipText(R.delta({total: 5, previousTotal: null})), null);
    const week = fixture('recap-week.json');
    assert.equal(R.chipText(R.delta(week)), '▲28.8%', '700.3M vs 543.9M');
});

// RV:377,384,396
test('navigation: Previous iff canGoBack, Next iff offset < 0, scope change resets', () => {
    assert.ok(!R.canGoBack({canGoBack: false}));
    assert.ok(R.canGoBack({canGoBack: true}));
    assert.ok(!R.canGoBack(null));
    assert.equal(R.step(0, -1, {canGoBack: true}), -1);
    assert.equal(R.step(0, -1, {canGoBack: false}), 0, 'disabled Previous');
    assert.equal(R.step(0, 1, {canGoBack: true}), 0, 'Next is disabled at 0');
    assert.equal(R.step(-2, 1, {canGoBack: true}), -1);
    assert.equal(R.step(R.MIN_OFFSET, -1, {canGoBack: true}), R.MIN_OFFSET, 'the helper bound');
    assert.ok(!R.canGoForward(0));
    assert.ok(R.canGoForward(-1));
    assert.ok(!R.canGoToCurrent(0));
    assert.ok(R.canGoToCurrent(-3));
});

// RV:54-69, protocol §14 item 22 (formatRange only if supported)
test('period labels: week range (native and fallback), month, year', () => {
    const week = {scope: 'week', start: '2026-10-11', end: '2026-10-17'};
    assert.equal(R.periodLabel(week, EN, false), 'Oct 11, 2026 – Oct 17, 2026', 'hand-built fallback');
    if (R.HAS_FORMAT_RANGE) {
        const native = R.periodLabel(week, EN, true);
        assert.ok(/Oct 11.*17, 2026/.test(native), native);
    }
    assert.equal(R.periodLabel({scope: 'month', start: '2026-09-01', end: '2026-09-30'}, EN), 'September 2026');
    assert.equal(R.periodLabel(fixture('recap-year.json'), EN), '2026');
    assert.equal(R.periodLabel(null, EN), '');
});

// RV:401-409
test('caption lines: best day sentence, compare only while running with a delta', () => {
    const week = fixture('recap-week.json');
    assert.equal(R.bestDayLine(week, EN), 'Your best day was Monday, Nov 16 — 263,904,712 tokens.');
    assert.equal(R.compareLine(week), 'Compared with the same days last week');
    assert.equal(R.compareLine({...week, isInProgress: false}), null, 'a finished period');
    const year = fixture('recap-year.json');
    assert.equal(R.compareLine(year), null, 'no previous total');
    assert.equal(R.bestDayLine({bestDay: null}, EN), null);
    assert.equal(R.compareSoFarText('month'), 'Compared with the same days last month');
    assert.equal(R.compareSoFarText('year'), 'Compared with the same days last year');
});

test('readouts: active/counted, streak days, strip of at most 4', () => {
    const year = fixture('recap-year.json');
    assert.equal(R.activeDaysText(year), '54/66');
    assert.equal(R.daysText(12), '12d');
    assert.equal(year.graduated, 5);
    assert.equal(R.stripGraduates(year).length, 4);
    assert.equal(R.stripGraduates(year)[0].displayName, 'Vaporeon', 'newest first, as sent');
    assert.deepEqual(R.stripGraduates(fixture('recap-week.json')), []);
});

test('sprites: companion or egg, graduates static', () => {
    assert.deepEqual(R.companionSpriteRequest({phase: 'egg', active: null}), {egg: true});
    assert.deepEqual(R.companionSpriteRequest(null), {egg: true});
    assert.deepEqual(R.companionSpriteRequest({phase: 'active', active: {speciesId: 17, form: null, isShiny: true}}),
        {speciesId: 17, form: null, shiny: true, back: false, animated: false});
    assert.deepEqual(R.graduateSpriteRequest({speciesId: 423, form: 'east', isShiny: false}),
        {speciesId: 423, form: 'east', shiny: false, back: false, animated: false});
});

test('refreshKey changes with the day or today\'s tokens only', () => {
    const s = d => ({usage: {calendar: {today: d.day}, combined: {today: {tokens: d.tokens}}}});
    assert.equal(R.refreshKey(s({day: '2026-11-18', tokens: 5})), R.refreshKey(s({day: '2026-11-18', tokens: 5})));
    assert.ok(R.refreshKey(s({day: '2026-11-18', tokens: 5})) !== R.refreshKey(s({day: '2026-11-18', tokens: 6})));
    assert.ok(R.refreshKey(s({day: '2026-11-18', tokens: 5})) !== R.refreshKey(s({day: '2026-11-19', tokens: 5})));
});

run('recapModel.test.js');
