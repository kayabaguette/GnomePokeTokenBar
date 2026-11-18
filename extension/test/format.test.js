// gjs -m extension/test/format.test.js
import {assert, run, test} from './harness.js';
import * as F from '../lib/format.js';
import {fmt} from '../lib/i18n.js';

// TokenFormatterTests (upstream) cases.
test('compact: units, trimming, promotion', () => {
    assert.equal(F.compact(0), '0');
    assert.equal(F.compact(987), '987');
    assert.equal(F.compact(1000), '1K');
    assert.equal(F.compact(12_345), '12.3K');
    assert.equal(F.compact(999_949), '999.9K');
    assert.equal(F.compact(999_950), '1M', 'rounds to 1000.0K → promoted');
    assert.equal(F.compact(1_150_000), '1.1M');
    assert.equal(F.compact(190_612_940), '190.6M');
    assert.equal(F.compact(187_291_640), '187.3M');
    assert.equal(F.compact(999_950_000), '1B');
    assert.equal(F.compact(1_240_000_000), '1.24B');
    assert.equal(F.compact(2_895_345_439), '2.9B');
    assert.equal(F.compact(-12_345), '-12.3K');
});

test('costCompact: bands judged on the rounded string', () => {
    assert.equal(F.costCompact(9.54), '$9.5');
    assert.equal(F.costCompact(0), '$0.0');
    assert.equal(F.costCompact(99.96), '$100', '99.96 rounds to 100.0 → whole dollars');
    assert.equal(F.costCompact(311.2), '$311');
    assert.equal(F.costCompact(9999.4), '$9999');
    assert.equal(F.costCompact(9999.6), '$10.0K');
    assert.equal(F.costCompact(12_345), '$12.3K');
});

test('costText: coverage', () => {
    assert.equal(F.costText({usd: 124.3487, coverage: 'estimated'}), '$124.35');
    assert.equal(F.costText({usd: 124.3487, coverage: 'estimated'}, true), '$124');
    assert.equal(F.costText({usd: 0, coverage: 'unavailable'}, true), '$—');
    assert.equal(F.costText({usd: 0, coverage: 'unavailable'}), 'Unavailable');
    assert.equal(F.costText({usd: 0, coverage: 'bogus'}, true), '$—', 'unknown coverage falls back to unavailable');
    assert.equal(F.costText({usd: 0, coverage: 'empty'}, true), '$0.0');
    assert.equal(F.costText({usd: 3.5, coverage: 'partial'}), '$3.50');
    assert.equal(F.costText(null, true), '$—');
});

test('percent drops .0', () => {
    assert.equal(F.percent(79.96), '80%');
    assert.equal(F.percent(88.35 + 1e-9), '88.4%');
    assert.equal(F.percent(88.3), '88.3%');
    assert.equal(F.percent(47), '47%');
    assert.equal(F.percent(0), '0%');
});

test('displayPercent and limitPercentText', () => {
    assert.equal(F.displayPercent(83, 'used'), 83);
    assert.equal(F.displayPercent(83, 'remaining'), 17);
    assert.equal(F.displayPercent(120, 'remaining'), 0, 'never negative');
    assert.equal(F.limitPercentText(47, 'used'), '47%');
    assert.equal(F.limitPercentText(15, 'remaining'), '85% left');
});

test('growthText', () => {
    assert.equal(F.growthText(42, true), 'Hatch 42%');
    assert.equal(F.growthText(64, false), 'Growth 64%');
});

test('countdown: units and clamping', () => {
    assert.equal(F.countdown(4 * 86400 + 2 * 3600 + 59), '4d 2h');
    assert.equal(F.countdown(3600 + 10 * 60 + 30), '1h 10m');
    assert.equal(F.countdown(12 * 60), '12m');
    assert.equal(F.countdown(30), '<1m');
    assert.equal(F.countdown(-500), '<1m', 'a past reset clamps to zero');
    assert.equal(F.countdown(NaN), '<1m');
});

test('resetClock: HH:MM nearby, weekday further out', () => {
    const now = new Date(2026, 10, 18, 14, 20).getTime();
    const later = new Date(2026, 10, 18, 16, 30).getTime();
    assert.equal(F.resetClock(later, now), '16:30');
    // Tomorrow 01:00 is within 6 h of 21:00 → still a bare clock.
    const night = new Date(2026, 10, 18, 21, 0).getTime();
    const tomorrow = new Date(2026, 10, 19, 1, 0).getTime();
    assert.equal(F.resetClock(tomorrow, night), '01:00');
    const friday = new Date(2026, 10, 20, 20, 20).getTime();
    const text = F.resetClock(friday, now, 'en-US');
    assert.ok(text.includes('20:20') && text.includes('Fri') && text.includes('20'), `got ${text}`);
    assert.equal(F.resetText(later, now), '2h 10m (16:30)');
});

test('relativeAgo, clamps the future', () => {
    const now = 1_000_000_000;
    assert.equal(F.relativeAgo(now - 5_000, now), 'just now');
    assert.equal(F.relativeAgo(now + 60_000, now), 'just now', 'clock skew never goes negative');
    assert.equal(F.relativeAgo(now - 5 * 60_000, now), '5 min ago');
    assert.equal(F.relativeAgo(now - 3 * 3_600_000, now), '3 h ago');
    assert.equal(F.relativeAgo(now - 2 * 86_400_000, now), '2 d ago');
});

test('parseTs and dayStamp', () => {
    assert.equal(F.parseTs('2026-11-18T13:20:00Z'), Date.UTC(2026, 10, 18, 13, 20));
    assert.equal(F.parseTs(null), null);
    assert.equal(F.parseTs('nope'), null);
    assert.equal(F.dayStamp('2026-11-18', 'en-US'), 'Wed, 11/18');
    assert.equal(F.dayStamp('bad'), '');
});

test('grouped uses the locale separators', () => {
    assert.equal(F.grouped(187_291_640, 'en-US'), '187,291,640');
    assert.equal(F.grouped(187_291_640, 'de-DE'), '187.291.640');
});

test('fmt: sequential, positional and literal percent', () => {
    assert.equal(fmt('%s at %s', 'Weekly', '83%'), 'Weekly at 83%');
    assert.equal(fmt('Hatch %d%%', 42.9), 'Hatch 42%');
    assert.equal(fmt('%2$s / %1$s', 'a', 'b'), 'b / a');
});

run('format.test.js');
