// gjs -m extension/test/heatmapModel.test.js
// Ported from Tests/PokeTokenBarTests/ActivityVisualizationTests.swift (AVT); the
// Swift assertions are the oracle. Values marked (derived) follow recap-heatmap.md §5.4.
import Gio from 'gi://Gio';

import {assert, run, test} from './harness.js';
import * as H from '../lib/heatmapModel.js';

const testDir = Gio.File.new_for_uri(import.meta.url).get_parent();
function read(file) {
    const [, bytes] = file.load_contents(null);
    return new TextDecoder().decode(bytes);
}
const active = JSON.parse(read(testDir.get_child('fixtures').get_child('state-active.json')));
const heatmap = active.usage.combined.heatmap;
const today = active.usage.calendar.today; // 2026-11-18, Wednesday; Monday-first
const monthDaily = active.usage.combined.monthDaily;

// AVT:19-39 (ActivityVisualization.allCases / next)
test('activity views: bars first, cycle wraps after one lap', () => {
    assert.deepEqual(H.ACTIVITY_VIEWS, ['bars', 'heatmap']);
    assert.equal(H.nextActivityView('bars'), 'heatmap');
    assert.equal(H.nextActivityView('heatmap'), 'bars');
    let v = 'bars';
    for (let i = 0; i < H.ACTIVITY_VIEWS.length; i++)
        v = H.nextActivityView(v);
    assert.equal(v, 'bars', 'one lap');
});

// AVT:53-68 (unknown stored value falls back to the default)
test('normalizeActivityView: default bars, unknown values give bars', () => {
    assert.equal(H.normalizeActivityView(undefined), 'bars');
    assert.equal(H.normalizeActivityView(''), 'bars');
    assert.equal(H.normalizeActivityView('recentHeatmap'), 'bars');
    assert.equal(H.normalizeActivityView('heatmap'), 'heatmap');
    assert.equal(H.nextActivityView('garbage'), 'heatmap', 'an unknown value cycles from the default');
});

// AVT:305-310 (the title names the window)
test('heatmap title contains 16', () => {
    assert.ok(H.heatmapTitle().includes('16'));
});

// AVT:194-206 (+ derived 50_000_001 and 75_000_001)
test('level: quartiles of the window peak, boundaries inclusive', () => {
    const peak = 100_000_000;
    const cases = [[0, 0], [1, 1], [25_000_000, 1], [25_000_001, 2], [50_000_000, 2], [50_000_001, 3],
        [75_000_000, 3], [75_000_001, 4], [100_000_000, 4]];
    for (const [tokens, want] of cases)
        assert.equal(H.level(tokens, peak), want, `${tokens}`);
    assert.equal(H.level(-5, peak), 0, 'negative');
    assert.equal(H.level(10, 0), 0, 'zero peak is guarded');
});

// AVT:208-218: five distinct colors per scheme, level -1 is clear.
test('palette: 5 distinct colors per scheme in the stylesheet, level -1 clear', () => {
    const css = read(testDir.get_parent().get_child('stylesheet.css'));
    const block = selector => {
        const start = css.indexOf(`${selector} {`);
        assert.ok(start >= 0, `${selector} block present`);
        return css.slice(start, css.indexOf('}', start));
    };
    const colors = text => [0, 1, 2, 3, 4].map(l => {
        const m = new RegExp(`-ptb-heat-${l}:\\s*(#[0-9A-Fa-f]{6})`).exec(text);
        assert.ok(m, `-ptb-heat-${l} defined`);
        return m[1].toUpperCase();
    });
    const dark = colors(block('.ptb-heatmap'));
    const light = colors(block('.ptb-light .ptb-heatmap'));
    assert.deepEqual(dark, ['#161B22', '#0E4429', '#006D32', '#26A641', '#39D353']);
    assert.deepEqual(light, ['#EBEDF0', '#9BE9A8', '#40C463', '#30A14E', '#216E39']);
    assert.equal(new Set(dark).size, H.LEVEL_COUNT);
    assert.equal(new Set(light).size, H.LEVEL_COUNT);
    assert.equal(H.levelColorProperty(-1), null);
    assert.equal(H.levelColorProperty(0), '-ptb-heat-0');
    assert.equal(H.levelColorProperty(4), '-ptb-heat-4');
    assert.equal(H.levelColorProperty(5), null);
});

// AVT:180-188 (weekday letters follow the week start)
test('weekday labels: en Monday-first, ko Sunday-first', () => {
    assert.deepEqual(H.weekdayLabels('2026-09-14', 'en-US'), ['M', 'T', 'W', 'T', 'F', 'S', 'S']);
    const ko = H.weekdayLabels('2026-09-13', 'ko');
    assert.equal(ko.length, 7);
    assert.equal(ko[0], '일');
    assert.equal(ko[6], '토');
    assert.deepEqual(H.weekdayLabels('bad', 'en-US'), []);
});

// AVT:170-178 + protocol §5.2 (16 x 7, outside before first and after today)
test('grid: 16 columns of 7, index col*7+row, outside flags', () => {
    const grid = H.buildGrid(heatmap, today);
    assert.equal(grid.length, 16);
    assert.ok(grid.every(col => col.length === 7));
    assert.equal(grid[0][0].date, '2026-08-03');
    assert.equal(grid[15][6].date, '2026-11-22');
    assert.equal(grid[15][2].date, today, "today is in the last column, row of Wednesday");
    const flat = grid.flat();
    assert.equal(new Set(flat.map(c => c.date)).size, 112, 'unique dates');
    assert.equal(H.cellFor(grid, '2026-09-13'), null, 'the day before first is outside');
    assert.equal(H.cellFor(grid, '2026-09-14').tokens, 282986562, 'first is in range');
    assert.ok(flat.find(c => c.date === '2026-11-19').outside, 'tomorrow is outside');
    assert.ok(!flat.find(c => c.date === today).outside);
    assert.equal(flat.filter(c => !c.outside).length, 66, 'Sep 14 .. Nov 18');
});

test('grid: a bad start gives [], a missing first means start', () => {
    assert.deepEqual(H.buildGrid(null, today), []);
    assert.deepEqual(H.buildGrid({start: 'nope', first: today, tokens: []}, today), []);
    const g = H.buildGrid({start: '2026-08-03', tokens: []}, today);
    assert.ok(!g[0][0].outside, 'first defaults to start');
    assert.equal(g[0][0].tokens, 0, 'missing tokens are 0');
});

// AH:201-206: the peak is over in-range used cells only; showsPeak needs more than one.
test('peak and showsPeak over in-range cells only', () => {
    const grid = H.buildGrid(heatmap, today);
    assert.equal(H.peakOf(grid), 284667456, 'Oct 16');
    assert.equal(H.usedCount(grid), 54);
    assert.ok(H.showsPeak(grid));

    const tokens = new Array(112).fill(0);
    tokens[0] = 999; // before first: never data
    tokens[50] = 7;
    const one = H.buildGrid({start: '2026-08-03', first: '2026-08-10', tokens}, today);
    assert.equal(H.peakOf(one), 7);
    assert.equal(H.usedCount(one), 1);
    assert.ok(!H.showsPeak(one), 'a single used day hides the peak');

    const empty = H.buildGrid({start: '2026-08-03', first: '2026-08-03', tokens: new Array(112).fill(0)}, today);
    assert.equal(H.peakOf(empty), 0, 'the empty grid has no peak (the panel keeps its caption)');
});

test('geometry: cell rects and hit testing agree, no dead zone', () => {
    const width = 352;
    const r = H.cellRect(0, 0, width);
    assert.equal(r.x, H.GRID_X);
    assert.close(r.width, (352 - 14 - 30) / 16);
    assert.equal(H.HEIGHT, 82);
    const last = H.cellRect(15, 6, width);
    assert.close(last.x + last.width, width, 1e-9, 'the last column ends at the edge');
    assert.equal(last.y + last.height, H.HEIGHT);
    for (let col = 0; col < 16; col++) {
        for (let row = 0; row < 7; row++) {
            const c = H.cellRect(col, row, width);
            assert.deepEqual(H.cellAt(c.x + c.width / 2, c.y + c.height / 2, width), {col, row});
            // the gap after a cell still belongs to it (the last row and column end at the edge)
            const gx = col < 15 ? c.x + c.width + 0.5 : c.x + c.width;
            const gy = row < 6 ? c.y + c.height + 0.5 : c.y + c.height;
            assert.deepEqual(H.cellAt(gx, gy, width), {col, row}, `gap of ${col},${row}`);
        }
    }
    assert.equal(H.cellAt(H.GRID_X - 1, 5, width), null, 'label column');
    assert.equal(H.cellAt(20, -1, width), null);
    assert.equal(H.cellAt(20, H.HEIGHT + 1, width), null);
    assert.equal(H.cellAt(width + 1, 5, width), null);
});

// AH:245-251, PV:1305-1311: live-series days read like the bars, others read the grid,
// tokens only; outside days read nothing; the caller falls back to today.
test('readout: bars format for this month, grid tokens before, nothing outside', () => {
    const grid = H.buildGrid(heatmap, today);
    assert.equal(H.readout(grid, today, monthDaily, 'en-US'), 'Wed, 11/18 187.3M');
    assert.equal(H.readout(grid, '2026-10-16', monthDaily, 'en-US'), 'Fri, 10/16 284.7M');
    assert.equal(H.readout(grid, '2026-11-11', monthDaily, 'en-US'), 'Wed, 11/11 0', 'a zero day reads 0');
    assert.equal(H.readout(grid, '2026-09-01', monthDaily, 'en-US'), '', 'before first');
    assert.equal(H.readout(grid, '2026-11-20', monthDaily, 'en-US'), '', 'future');
    // A live day wins over a trailing grid value (ledger written once per refresh).
    const trailing = {...heatmap, tokens: heatmap.tokens.map((t, i) => (i === 107 ? 5 : t))};
    assert.equal(H.readout(H.buildGrid(trailing, today), today, monthDaily, 'en-US'), 'Wed, 11/18 187.3M');
    assert.equal(H.readout(H.buildGrid(trailing, today), today, null, 'en-US'), 'Wed, 11/18 5');
});

run('heatmapModel.test.js');
