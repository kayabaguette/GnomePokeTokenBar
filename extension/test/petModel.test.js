// gjs -m extension/test/petModel.test.js
// Ported from Tests/PokeTokenBarTests/FloatingPetAgentTests.swift, FloatingPetPettingTests.swift and
// PetEventLineTests.swift (cited per test). Geometry is mirrored to y-down (floating-pet.md §5.2 B).
import {assert, run, test} from './harness.js';
import * as M from '../lib/pet/petModel.js';
import {fitRect, manifestVisibleTop, visibleTopInset} from '../lib/spriteFit.js';

/* ---- moves and runs ---- */

test('only home or a drag persists the origin', () => {
    // FloatingPetAgentTests.swift:12-22
    assert.deepEqual(M.resolveMove('home', false), {phase: 'home', persist: true});
    assert.deepEqual(M.resolveMove('home', true), {phase: 'home', persist: true});
    assert.deepEqual(M.resolveMove('away', false), {phase: 'away', persist: false});
    assert.deepEqual(M.resolveMove('returning', false), {phase: 'returning', persist: false});
    assert.deepEqual(M.resolveMove('away', true), {phase: 'home', persist: true});
    assert.deepEqual(M.resolveMove('returning', true), {phase: 'home', persist: true});
});

// Swift screen (0, 25, 1440, 875) y-up is (0, 0, 1440, 875) y-down with H = 900.
const screen = {x: 0, y: 0, width: 1440, height: 875};
const panel = {width: 180, height: 168};
const inside = (o, wa, p = panel) => o.x >= wa.x && o.y >= wa.y &&
    o.x + p.width <= wa.x + wa.width && o.y + p.height <= wa.y + wa.height;
const covers = (o, c, p = panel) => c.x >= o.x && c.x <= o.x + p.width && c.y >= o.y && c.y <= o.y + p.height;

test('the run lands below-right of the cursor (mirrored)', () => {
    // FloatingPetAgentTests.swift:34-39: cursor (600, 500) y-up → (600, 400); (616, 316) y-up → (616, 416)
    const o = M.runPanelOrigin({x: 600, y: 400}, panel, screen, 16);
    assert.deepEqual(o, {x: 616, y: 416});
    assert.ok(!covers(o, {x: 600, y: 400}));
});

test('the run flips at screen edges instead of covering the pointer (mirrored)', () => {
    // FloatingPetAgentTests.swift:41-58
    for (const c of [{x: 1430, y: 400}, {x: 600, y: 860}, {x: 1435, y: 870}, {x: 5, y: 5}]) {
        const o = M.runPanelOrigin(c, panel, screen, 16);
        assert.ok(inside(o, screen), `inside at ${JSON.stringify(c)}`);
        assert.ok(!covers(o, c), `covers the pointer at ${JSON.stringify(c)}`);
    }
    assert.deepEqual(M.runPanelOrigin({x: 1430, y: 400}, panel, screen, 16), {x: 1234, y: 416});
    assert.deepEqual(M.runPanelOrigin({x: 600, y: 860}, panel, screen, 16), {x: 616, y: 676});
});

test('the run stays on the cursor screen (mirrored)', () => {
    // FloatingPetAgentTests.swift:60-64: cursor (-10, 1075) y-up → (-10, 5), H = 1080
    const secondary = {x: -1920, y: 0, width: 1920, height: 1080};
    assert.ok(inside(M.runPanelOrigin({x: -10, y: 5}, panel, secondary), secondary));
});

test('an oversized panel keeps the pet (bottom-left) visible (mirrored)', () => {
    // FloatingPetAgentTests.swift:66-71
    const small = {x: 0, y: 0, width: 300, height: 200};
    const o = M.runPanelOrigin({x: 150, y: 100}, {width: 384, height: 456}, small);
    assert.deepEqual(o, {x: 0, y: -256});
    assert.equal(o.y + 456, small.y + small.height, 'panel bottom on the work-area bottom');
});

test('a second done does not run again while away', () => {
    // FloatingPetAgentTests.swift:76-83
    assert.equal(M.runsToCursor('home', true, true), true);
    assert.equal(M.runsToCursor('returning', true, true), true);
    assert.equal(M.runsToCursor('away', true, true), false);
    assert.equal(M.runsToCursor('home', false, true), false);
    assert.equal(M.runsToCursor('home', true, false), false);
});

test('motion follows animations and power saver', () => {
    // FloatingPetAgentTests.swift:85-93
    assert.equal(M.allowsMotion(true, false), true);
    assert.equal(M.allowsMotion(false, false), false);
    assert.equal(M.allowsMotion(true, true), false);
    assert.equal(M.shouldBob(true, true), true);
    assert.equal(M.shouldBob(true, false), false);
    assert.equal(M.shouldBob(false, true), false);
    assert.ok(M.BOB_INTERVAL_MS > 0);
});

test('isClick uses the 16 px² threshold', () => {
    // FPP:325-329 (derived, no Swift oracle)
    assert.equal(M.isClick({x: 0, y: 0}, {x: 3, y: 2}), true);
    assert.equal(M.isClick({x: 0, y: 0}, {x: 4, y: 0}), false);
});

/* ---- panel geometry and home ---- */

test('panel size and the pet/panel origin conversions (mirrored)', () => {
    // FPP:516-532; FloatingPetAgentTests.swift:339-358 mirrored with H = 900:
    // the panel dropped at y-up (900, 100) has its top at 900 - 268 = 632.
    assert.deepEqual(M.panelSize(96, false), {width: 96, height: 96});
    assert.deepEqual(M.panelSize(96, true), {width: 180, height: 168});
    assert.deepEqual(M.panelSize(384, true), {width: 384, height: 456});
    const p = M.panelSize(96, true);
    const pet = M.petOrigin({x: 900, y: 632}, 96, p);
    assert.deepEqual(pet, {x: 942, y: 800});
    assert.deepEqual(M.panelOrigin(pet, 96, p), {x: 900, y: 632});
});

test('default home, clamping and off-screen recovery', () => {
    // FPP:786-796 (derived, y-down)
    const wa = {x: 0, y: 32, width: 1280, height: 768};
    assert.deepEqual(M.defaultHome(wa, 96), {x: 1280 - 96 - 24, y: 800 - 24});
    assert.deepEqual(M.resolveHome(null, [wa], 0, 96), {x: 1160, y: 776});
    // Under the top bar: pushed down into the work area.
    assert.deepEqual(M.resolveHome({x: 100, y: 50}, [wa], 0, 96), {x: 100, y: 128});
    // Half off the right edge: clamped.
    assert.deepEqual(M.resolveHome({x: 1250, y: 500}, [wa], 0, 96), {x: 1184, y: 500});
    // Entirely off every monitor (unplugged): default home on the primary.
    const second = {x: 1280, y: 0, width: 1920, height: 1080};
    assert.deepEqual(M.resolveHome({x: 5000, y: 500}, [wa, second], 0, 96), {x: 1160, y: 776});
    // On the second monitor: kept there.
    assert.deepEqual(M.resolveHome({x: 2000, y: 600}, [wa, second], 0, 96), {x: 2000, y: 600});
    // Too big for the work area: left and bottom win.
    assert.deepEqual(M.clampHome({x: 50, y: 50}, {x: 0, y: 0, width: 300, height: 200}, 384), {x: 0, y: 200});
});

test('size snaps to 48–384 in steps of 8', () => {
    // SV:377 (derived)
    assert.equal(M.snapSize(96), 96);
    assert.equal(M.snapSize(99), 96);
    assert.equal(M.snapSize(101), 104);
    assert.equal(M.snapSize(10), 48);
    assert.equal(M.snapSize(1000), 384);
    assert.equal(M.snapSize('x'), 96);
});

/* ---- bubbles ---- */

const alert = {kind: 'limitAlert', windowKind: 'sevenDay', tier: 'crit', utilization: 96};
const S = (o = {}) => ({done: null, waiting: null, otherWaitingCount: 0, working: false, ...o});

test('a limit alert outranks agent bubbles', () => {
    // FloatingPetAgentTests.swift:97-109
    const waiting = S({waiting: {label: 'a', message: 'x'}, otherWaitingCount: 2});
    const both = S({done: {label: 'b', message: null}, waiting: {label: 'a', message: 'x'}});
    assert.deepEqual(M.bubble(alert, waiting), {kind: 'limit', alert});
    assert.deepEqual(M.bubble(alert, S()), {kind: 'limit', alert});
    assert.deepEqual(M.bubble(null, waiting), {kind: 'agentWaiting', label: 'a', message: 'x', others: 2});
    assert.deepEqual(M.bubble(null, both), {kind: 'agentDone', label: 'b', message: null});
    assert.equal(M.bubble(null, S({working: true})), null);
    assert.equal(M.bubble(null, S()), null);
});

test('agent speech reserves headroom and outranks hearts and event callouts', () => {
    // FloatingPetAgentTests.swift:111-127
    for (const agent of [S({waiting: {label: 'web', message: null}}), S({done: {label: 'api', message: null}})]) {
        const showing = M.isShowingBubble(false, false, agent);
        assert.equal(showing, true);
        assert.equal(M.heartsShowing(false, agent), false);
        assert.equal(M.shouldPopEvent(2, 1, true, showing), false);
    }
    const working = S({working: true});
    assert.equal(M.heartsShowing(false, working), true);
    assert.equal(M.isShowingBubble(false, true, working), true);
    assert.equal(M.isShowingBubble(false, false, working), false);
    assert.equal(M.heartsShowing(true, working), false);
});

test('hearts show unless a limit alert is active; they reserve headroom', () => {
    // FloatingPetPettingTests.swift:90-104
    assert.equal(M.heartsShowing(false), true);
    assert.equal(M.heartsShowing(true), false);
    assert.equal(M.HEART_DURATION_MS, 1800);
    assert.equal(M.isShowingBubble(false, true), true);
    assert.equal(M.isShowingBubble(true, false), true);
    assert.equal(M.isShowingBubble(false, false), false);
});

test('an event pops the callout once, only while visible and quiet', () => {
    // PetEventLineTests.swift:46-57
    assert.equal(M.shouldPopEvent(1, null, true, false), true);
    assert.equal(M.shouldPopEvent(2, 1, true, false), true);
    assert.equal(M.shouldPopEvent(1, 1, true, false), false);
    assert.equal(M.shouldPopEvent(null, null, true, false), false);
    assert.equal(M.shouldPopEvent(2, 1, false, false), false);
    assert.equal(M.shouldPopEvent(2, 1, true, true), false);
    assert.equal(M.EVENT_POP_MS, 6000);
});

test('a click opens the popover, for an egg too (prayers deferred)', () => {
    // FPP:260-262 adapted (M2 decision 24)
    assert.equal(M.clickAction(false), 'openPopover');
    assert.equal(M.clickAction(true), 'openPopover');
});

test('pickBubbleAlert: critical first, then the higher utilisation (derived, no Swift oracle)', () => {
    // US:2153-2158
    const warn90 = {tier: 'warn', utilization: 90};
    const warn85 = {tier: 'warn', utilization: 85};
    const crit95 = {tier: 'crit', utilization: 95};
    const crit97 = {tier: 'crit', utilization: 97};
    assert.equal(M.pickBubbleAlert([warn90, crit95, warn85]), crit95);
    assert.equal(M.pickBubbleAlert([crit95, crit97]), crit97);
    assert.equal(M.pickBubbleAlert([warn85, warn90]), warn90);
    const tie = {tier: 'warn', utilization: 90};
    assert.equal(M.pickBubbleAlert([warn90, tie]), warn90, 'the first wins a tie');
    assert.equal(M.pickBubbleAlert([]), null);
});

test('shouldDismissBubble: 6 s TTL (derived, no Swift oracle)', () => {
    // US:2161-2163
    assert.equal(M.shouldDismissBubble(1000, 6999), false);
    assert.equal(M.shouldDismissBubble(1000, 7000), true);
});

/* ---- hover text ---- */

test('hover text: tokens, limit and the event line', () => {
    // PetEventLineTests.swift:59-67 (event-only half; prayers deferred) and FPP:1086-1101
    const plain = M.hoverText({todayTokens: 1234, limitUtilization: null, mode: 'used'});
    assert.equal(plain, `Today: ${new Intl.NumberFormat().format(1234)} tokens`);
    assert.equal(M.hoverText({todayTokens: 1234, limitUtilization: null, mode: 'used', eventLine: 'EVENT'}),
        `${plain}\nEVENT`);
    assert.equal(M.hoverText({todayTokens: 5, limitUtilization: 83, mode: 'used'}), 'Today: 5 tokens (limit 83%)');
    assert.equal(M.hoverText({todayTokens: 5, limitUtilization: 83, mode: 'remaining'}),
        'Today: 5 tokens (limit 17% left)');
});

test('hover text: sulk suffix in hours below 48 h, then days', () => {
    // FPP:1095-1098 with companion.sulkIdleSeconds (protocol §5.4)
    const t = s => M.hoverText({todayTokens: 0, limitUtilization: null, mode: 'used', sulkIdleSeconds: s});
    assert.equal(t(0), 'Today: 0 tokens');
    assert.equal(t(5 * 3600), 'Today: 0 tokens · Sulking for 5 hours');
    assert.equal(t(47 * 3600), 'Today: 0 tokens · Sulking for 47 hours');
    assert.equal(t(48 * 3600), 'Today: 0 tokens · Sulking for 2 days');
    assert.equal(t(80 * 3600), 'Today: 0 tokens · Sulking for 3 days');
});

test('highest limit utilisation counts only providers used today', () => {
    // US:749-770 (derived)
    const state = {
        usage: {providers: {claude_code: {today: {tokens: 10}}, other: {today: {tokens: 0}}}},
        limits: {providers: {
            claude_code: {windows: [{utilization: 47}, {utilization: 83}, {utilization: null}]},
            other: {windows: [{utilization: 99}]},
        }},
    };
    assert.equal(M.highestLimitUtilization(state), 83);
    state.usage.providers.claude_code.today.tokens = 0;
    assert.equal(M.highestLimitUtilization(state), null);
    assert.equal(M.highestLimitUtilization(null), null);
});

/* ---- petting ---- */

function triggers(detector, xs, step = 0.05, stroke = 14) {
    let time = 100, count = 0;
    for (const x of xs) {
        if (detector.feed(x, time, stroke))
            count++;
        time += step;
    }
    return count;
}

test('petting: a wiggle triggers on the third and fourth reversal', () => {
    // FloatingPetPettingTests.swift:22-28
    assert.equal(triggers(new M.PettingDetector(), [10, 25, 40, 25, 10, 25, 40, 25, 10, 25, 40]), 2);
});

test('petting: a straight pass, a slow wiggle and jitter do not trigger', () => {
    // FloatingPetPettingTests.swift:30-40, :54-59
    const straight = Array.from({length: 21}, (_, i) => i * 10);
    assert.equal(triggers(new M.PettingDetector(), straight), 0);
    assert.equal(triggers(new M.PettingDetector(), [10, 40, 10, 40, 10, 40, 10, 40], 0.8), 0);
    assert.equal(triggers(new M.PettingDetector(), Array.from({length: 30}, (_, i) => (i % 2 === 0 ? 10 : 18))), 0);
});

test('petting: a long pause resets the state', () => {
    // FloatingPetPettingTests.swift:42-52
    const d = new M.PettingDetector();
    assert.equal(triggers(d, [10, 40, 10, 40]), 0);
    let time = 100 + M.PettingDetector.WINDOW_S + 1;
    assert.equal(d.feed(10, time, 14), false);
    time += 0.05;
    assert.equal(d.feed(40, time, 14), false);
    time += 0.05;
    assert.equal(d.feed(10, time, 14), false);
});

test('petting: the minimum stroke scales the threshold', () => {
    // FloatingPetPettingTests.swift:61-75
    const small = new M.PettingDetector(), big = new M.PettingDetector();
    let time = 100, s = 0, b = 0;
    for (const x of [10, 30, 10, 30, 10, 30, 10]) {
        if (small.feed(x, time, 14))
            s++;
        if (big.feed(x, time, 40))
            b++;
        time += 0.05;
    }
    assert.ok(s > 0);
    assert.equal(b, 0);
    assert.equal(M.minStroke(48), 10);
    assert.close(M.minStroke(96), 14.4);
});

test('petting: reset forgets reversals', () => {
    // FloatingPetPettingTests.swift:77-84
    const d = new M.PettingDetector();
    assert.equal(triggers(d, [10, 40, 10, 40]), 0);
    d.reset();
    assert.equal(triggers(d, [10, 40, 10]), 0);
});

/* ---- heart art and anchoring ---- */

test('heart art: one width, symmetric, single-pixel tail at the centre, known cells', () => {
    // FloatingPetPettingTests.swift:113-136, :237-241
    const rows = M.HEART_ROWS;
    assert.ok(rows.every(r => r.length === 17));
    for (const row of rows) {
        const plain = [...row].map(c => ('RDH'.includes(c) ? 'R' : c)).join('');
        assert.equal([...plain].reverse().join(''), plain, row);
    }
    const tip = rows[rows.length - 1];
    assert.equal(tip.indexOf('O'), 8);
    assert.equal([...tip].filter(c => c === 'O').length, 1);
    const cells = new Set(rows.join(''));
    assert.deepEqual([...cells].sort(), ['.', 'D', 'H', 'O', 'R', 'W']);
    for (const c of cells)
        assert.ok(c === '.' || M.HEART_COLORS[c], `color for ${c}`);
    assert.equal(M.heartRgba().length, 17 * 16 * 4);
});

test('heart bubble fits the panel and the headroom', () => {
    // FloatingPetPettingTests.swift:138-146
    assert.ok(M.HEART_WIDTH <= M.BUBBLE_MIN_WIDTH);
    assert.equal(M.HEART_HEIGHT - M.HEART_TAIL_OVERLAP, 29);
    assert.ok(M.HEART_HEIGHT - M.HEART_TAIL_OVERLAP <= M.BUBBLE_HEADROOM);
    assert.equal(M.bubbleTopOffset(0, M.HEART_HEIGHT, M.HEART_TAIL_OVERLAP), -29);
    assert.ok(M.bubbleTopOffset(0, M.HEART_HEIGHT, M.HEART_TAIL_OVERLAP) >= -M.BUBBLE_HEADROOM);
});

test('bubble offset: tail just inside the visible top, whole pixels', () => {
    // FloatingPetPettingTests.swift:170-180
    assert.equal(M.bubbleTopOffset(24, 32, 3) + 32, 27);
    assert.equal(M.bubbleTopOffset(3.84, 32, 3), -25);
    assert.equal(M.swiftRound(-2.5), -3, 'half away from zero');
    assert.equal(M.swiftRound(2.5), 3);
});

test('visible top inset: upstream centred fractional fit', () => {
    // FloatingPetPettingTests.swift:150-168 through fitRect(fractional, center), the upstream fit
    const inset = (w, h, f) => visibleTopInset(fitRect(96, 96, w, h, {upscale: 'fractional', align: 'center'}), f);
    assert.close(inset(36, 66, 0.05), 4.8, 0.001);
    assert.close(inset(60, 30, 0.1), 28.8, 0.001);
    assert.close(inset(30, 30, 0.1), 9.6, 0.001);
});

test('visible top inset: the pet path (integer upscale, bottom-aligned) (derived)', () => {
    // M2 decision 28; SpriteAnimator bottom-aligns. 50×46 in 96: 1×, top at 50.
    let r = fitRect(96, 96, 50, 46, {upscale: 'integer', align: 'bottom'});
    assert.deepEqual(r, {x: 23, y: 50, width: 50, height: 46, scale: 1});
    assert.close(visibleTopInset(r, 0.1304), 50 + 46 * 0.1304);
    // 40×40 in 96: 2×.
    r = fitRect(96, 96, 40, 40, {upscale: 'integer', align: 'bottom'});
    assert.deepEqual(r, {x: 8, y: 16, width: 80, height: 80, scale: 2});
    // 50×46 in 192: 3× (3.84 floored).
    assert.equal(fitRect(192, 192, 50, 46, {upscale: 'integer'}).scale, 3);
    // Bigger than the box: fractional shrink.
    assert.close(fitRect(48, 48, 96, 64, {upscale: 'integer'}).scale, 0.5);
    // 'none' keeps 1× (the top bar).
    assert.equal(fitRect(96, 96, 40, 40, {upscale: 'none'}).scale, 1);
    assert.equal(visibleTopInset(r, NaN), r.y);
});

test('manifest visibleTopFraction falls back to 0 (protocol §8.2)', () => {
    assert.equal(manifestVisibleTop({visibleTopFraction: 0.1304}), 0.1304);
    assert.equal(manifestVisibleTop({}), 0);
    assert.equal(manifestVisibleTop({visibleTopFraction: 1.5}), 0);
    assert.equal(manifestVisibleTop({visibleTopFraction: '0.2'}), 0);
    assert.equal(manifestVisibleTop(null), 0);
});

run('petModel.test.js');
