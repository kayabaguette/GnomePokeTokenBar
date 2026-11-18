// gjs -m extension/test/viewModel.test.js
import Gio from 'gi://Gio';

import {assert, run, test} from './harness.js';
import * as V from '../lib/viewModel.js';

const fixtureDir = Gio.File.new_for_uri(import.meta.url).get_parent().get_child('fixtures');
function fixture(name) {
    const [, bytes] = fixtureDir.get_child(name).load_contents(null);
    return JSON.parse(new TextDecoder().decode(bytes));
}
const active = fixture('state-active.json');
const egg = fixture('state-egg.json');
const events = new TextDecoder().decode(fixtureDir.get_child('events.ndjson').load_contents(null)[1])
    .split('\n').filter(l => l.trim()).map(l => JSON.parse(l));

const defaults = {
    showTokens: true, showCost: false, showGrowth: false, showLimit: false,
    limitMode: 'used', limitColorMode: 'gauge',
};
const clone = v => JSON.parse(JSON.stringify(v));
const text = lines => lines.map(line => line.map(r => r.text).join(''));

test('default: tokens only', () => {
    assert.deepEqual(text(V.menuLines(active, defaults)), ['187.3M']);
});

test('before the first refresh: growth text or —', () => {
    const fresh = clone(egg);
    fresh.meta.lastUpdated = null;
    assert.deepEqual(text(V.menuLines(fresh, defaults)), ['—']);
    assert.deepEqual(text(V.menuLines(fresh, {...defaults, showGrowth: true})), ['Hatch 62%']);
    assert.deepEqual(text(V.menuLines(null, defaults)), ['—']);
});

test('two usage items stack one per line (screenshot-menu-growth)', () => {
    assert.deepEqual(text(V.menuLines(active, {...defaults, showGrowth: true})), ['187.3M', 'Growth 64%']);
});

test('usage ≥ 2 plus a limit: [usage joined, limit]', () => {
    const s = {...defaults, showCost: true, showLimit: true};
    const lines = V.menuLines(active, s);
    assert.deepEqual(text(lines), ['187.3M · $124', 'Claude 47%']);
    assert.equal(lines[1][0].tier, 'onPace', 'gauge mode colors every tier');
    assert.equal(lines[0][0].tier, null, 'the usage line keeps the text color');
});

test('three usage items, no limit: [first two joined, last]', () => {
    const s = {...defaults, showCost: true, showGrowth: true};
    assert.deepEqual(text(V.menuLines(active, s)), ['187.3M · $124', 'Growth 64%']);
});

test('one usage item plus a limit: one per line (B alone, A=false)', () => {
    assert.deepEqual(text(V.menuLines(active, {...defaults, showLimit: true})), ['187.3M', 'Claude 47%']);
    // Limit alone: usage off.
    assert.deepEqual(text(V.menuLines(active, {...defaults, showTokens: false, showLimit: true})), ['Claude 47%']);
});

test('all off → sprite only', () => {
    assert.deepEqual(V.menuLines(active, {...defaults, showTokens: false}), []);
});

test('remaining mode flips the number, not the tier', () => {
    const lines = V.menuLines(active, {...defaults, showLimit: true, limitMode: 'remaining'});
    assert.equal(lines[1][0].text, 'Claude 53%');
    assert.equal(lines[1][0].tier, 'onPace');
});

test('color modes: attention drops calm tiers, off drops all', () => {
    const s = {...defaults, showLimit: true};
    assert.equal(V.menuLines(active, {...s, limitColorMode: 'attention'})[1][0].tier, null, 'onPace is calm');
    assert.equal(V.menuLines(active, {...s, limitColorMode: 'off'})[1][0].tier, null);
    const fast = clone(active);
    fast.limits.providers.claude_code.windows[0].gaugeTier = 'slightlyOver';
    assert.equal(V.menuLines(fast, {...s, limitColorMode: 'attention'})[1][0].tier, 'slightlyOver');
});

test('limit item needs usage today and a headline window', () => {
    const s = {...defaults, showLimit: true};
    const idle = clone(active);
    idle.usage.providers.claude_code.today.tokens = 0;
    assert.deepEqual(text(V.menuLines(idle, s)), ['187.3M'], 'no Claude usage today → no limit item');
    assert.deepEqual(text(V.menuLines(egg, s)), ['4.1M'], 'no headline window (missing credentials)');
    const weekly = clone(active);
    weekly.limits.providers.claude_code.headlineKey = 'claude.sevenDay';
    const lines = V.menuLines(weekly, s);
    assert.equal(lines[1][0].text, 'Claude 83%');
    assert.equal(lines[1][0].tier, 'over');
});

test('cost hidden when no provider reports cost', () => {
    const noCost = clone(active);
    noCost.meta.providers[0].reportsCost = false;
    assert.deepEqual(text(V.menuLines(noCost, {...defaults, showCost: true})), ['187.3M']);
    assert.equal(V.menuInfoText(noCost), 'Today’s tokens 187.3M');
    assert.equal(V.menuInfoText(active), 'Today’s tokens 187.3M · $124.35');
});

test('window labels, scoped from scopeKind + label', () => {
    const [five, week, opus] = active.limits.providers.claude_code.windows;
    assert.equal(V.windowLabel(five), '5-hour session');
    assert.equal(V.windowLabel(week), 'Weekly');
    assert.equal(V.windowLabel(opus), 'Weekly Opus');
    assert.equal(V.windowLabel({kind: 'scoped', scopeKind: 'weekly_scoped', label: null}), 'Weekly (scoped)');
    assert.equal(V.windowLabel({kind: 'scoped', scopeKind: 'monthly_cap', label: 'Fable'}), 'monthly cap Fable');
    assert.equal(V.windowLabel({kind: 'bogus'}), 'limit', 'unknown kind → scoped fallback');
});

test('pace tooltip', () => {
    const [five, week] = active.limits.providers.claude_code.windows;
    assert.equal(V.paceHelp(five, 'used'),
        'On pace · 10 pts less used\nPace — an even burn across this window would sit at 56.7% now.');
    assert.equal(V.paceHelp(week, 'remaining').split('\n')[1],
        'Pace — an even burn across this window would sit at 32.1% now.');
    assert.equal(V.paceHelp({...five, paceTier: null}, 'used').startsWith('Pace'), true, 'early hold: hint only');
    assert.equal(V.paceHelp({...five, pace: null}, 'used'), null);
    assert.equal(V.paceDeltaText(0), null);
});

test('tiers: fallback and calm', () => {
    assert.equal(V.normalizeTier('weird'), 'onPace');
    assert.equal(V.isCalm('wayUnder'), true);
    assert.equal(V.isCalm('slightlyOver'), false);
});

test('companion text', () => {
    assert.equal(V.stageText(active.companion.active), 'Stage 2 / 3 · Adamant');
    assert.equal(V.stageText({...active.companion.active, isFinalStage: true, nature: null}), 'Final form');
    assert.equal(V.remainingText(162_400_000, 250_000_000, 'evolve'), '87.6M to next evolution');
    assert.equal(V.remainingText(3_100_000, 5_000_000, 'egg'), '1.9M to hatch');
    assert.equal(V.progressDetail(162_400_000, 250_000_000), '162.4M / 250M · 64%');
    assert.equal(V.moodText(active.companion), 'In focus mode now.');
    assert.equal(V.moodText({mood: 'levelUp', justEvolvedTo: 'Pidgeot'}), 'Evolved into Pidgeot!');
    assert.equal(V.moodText({mood: 'nope'}), 'Keeping quiet today.');
    assert.equal(V.natureName('adamant'), 'Adamant');
    assert.equal(V.natureName('zany'), 'zany', 'unknown nature shows the id');
    assert.equal(V.rarityName('mythic'), 'Common');
});

test('limits error text by code', () => {
    assert.ok(V.limitsErrorText(egg.limits.providers.claude_code).includes('credentials'));
    assert.equal(V.limitsErrorText({errorCode: null}), null);
    assert.ok(V.limitsErrorText({errorCode: 'httpStatus', httpStatus: 503}).includes('503'));
    assert.ok(V.limitsErrorText({errorCode: 'somethingNew'}).length > 0, 'unknown code still says something');
    assert.equal(V.metaErrorText({code: 'mystery', message: 'helper says'}), 'helper says');
});

test('notification text for every fixture event', () => {
    const byKind = Object.fromEntries(events.map(e => [e.kind, V.notificationText(e)]));
    assert.deepEqual(byKind.hatch, {title: '🥚 Hatched!', body: 'Pidgey hatched from the egg!'});
    assert.equal(byKind.evolve.body, 'Pidgey evolved into Pidgeotto!');
    assert.equal(byKind.graduate.title, '🎓 Graduated!');
    assert.equal(byKind.candyGranted.title, '🍬 You got 1× Rare Candy!');
    assert.equal(byKind.badge.title, '🏅 Kanto Dex complete!');
    assert.deepEqual(byKind.limitAlert, {title: 'Limit warning', body: 'Claude weekly at 83%'},
        'legacy window labelled from windowKind (label is null)');
    assert.equal(byKind.authExpired.title, 'Claude limits paused');
    assert.equal(byKind.error, null, 'error events are log-only');
    const shiny = V.notificationText({kind: 'hatch', displayName: 'Pidgey', isShiny: true, shinyOdds: 64});
    assert.equal(shiny.body, 'A shiny Pidgey hatched! (1 in 64)');
    const crit = V.notificationText({kind: 'limitAlert', windowKind: 'fiveHour', label: null, tier: 'crit', utilization: 96.5});
    assert.deepEqual(crit, {title: 'Limit imminent', body: 'Claude 5-hour session at 96.5%'});
    const scoped = V.notificationText({kind: 'limitAlert', windowKind: 'scoped', label: 'Opus', tier: 'warn', utilization: 80});
    assert.equal(scoped.body, 'Claude weekly Opus at 80%');
});

test('notification gates', () => {
    assert.equal(V.notificationGate('limitAlert'), 'limit-notifications');
    assert.equal(V.notificationGate('hatch'), 'companion-notifications');
    assert.equal(V.notificationGate('error'), null);
});

run('viewModel.test.js');
