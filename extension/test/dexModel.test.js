// gjs -m extension/test/dexModel.test.js
import Gio from 'gi://Gio';

import {assert, run, test} from './harness.js';
import * as D from '../lib/dex/dexModel.js';

const fixtureDir = Gio.File.new_for_uri(import.meta.url).get_parent().get_child('fixtures');
function fixture(name) {
    const [, bytes] = fixtureDir.get_child(name).load_contents(null);
    return JSON.parse(new TextDecoder().decode(bytes));
}
const clone = v => JSON.parse(JSON.stringify(v));
const species = fixture('species.json').data; // Gastrodon: west (uncollected), east (1 individual)
const dexPage = fixture('dex-page.json').data;
const catchLog = fixture('catch-log.json').data;
const state = fixture('state-active.json');

/* ---- args ---- */

test('dex args: defaults match protocol §7.1', () => {
    assert.deepEqual(D.dexPageArgs(D.defaultFilters(), 1), {
        page: 1, pageSize: 16, query: '', rarity: null, region: null, shinyOnly: false,
        sort: 'numberAsc', anchorSpeciesId: null,
    });
});

test('dex args: filters, trimmed query, anchor', () => {
    const f = {...D.defaultFilters(), query: '  #42 ', rarity: 'rare', region: 'johto', shinyOnly: true,
        dexSort: 'nameDesc'};
    const a = D.dexPageArgs(f, 3, 423);
    assert.equal(a.query, '#42');
    assert.equal(a.rarity, 'rare');
    assert.equal(a.region, 'johto');
    assert.equal(a.shinyOnly, true);
    assert.equal(a.sort, 'nameDesc');
    assert.equal(a.page, 3);
    assert.equal(a.anchorSpeciesId, 423);
});

test('dex args: unknown enum values fall back instead of sending badArgs', () => {
    const f = {...D.defaultFilters(), rarity: 'mythic', region: 'galar', dexSort: 'recentFirst'};
    const a = D.dexPageArgs(f, 0);
    assert.equal(a.rarity, null);
    assert.equal(a.region, null);
    assert.equal(a.sort, 'numberAsc');
    assert.equal(a.page, 1);
});

test('log args: offset per page, no region, log sorts', () => {
    const f = {...D.defaultFilters(), region: 'kanto', logSort: 'oldestFirst', dexSort: 'nameAsc'};
    const a = D.catchLogArgs(f, 3);
    assert.deepEqual(a, {offset: 20, limit: 10, query: '', rarity: null, shinyOnly: false, sort: 'oldestFirst'});
    assert.equal(D.catchLogArgs({...f, logSort: 'numberAsc'}, 1).sort, 'numberAsc');
    assert.equal(D.catchLogArgs({...f, logSort: 'bogus'}, 1).sort, 'recentFirst');
});

test('query is capped', () => {
    assert.equal(D.normalizeQuery('x'.repeat(200)).length, D.MAX_QUERY_LENGTH);
    assert.equal(D.normalizeQuery(null), '');
});

test('filters: default detection and custom sort per segment', () => {
    assert.ok(D.isDefaultFilters(D.defaultFilters()));
    assert.ok(!D.isDefaultFilters({...D.defaultFilters(), shinyOnly: true}));
    assert.ok(D.isDefaultFilters({...D.defaultFilters(), query: '   '}));
    assert.ok(D.isSortCustom({...D.defaultFilters(), dexSort: 'nameAsc'}, 'grid'));
    assert.ok(!D.isSortCustom({...D.defaultFilters(), dexSort: 'nameAsc'}, 'log'));
    assert.equal(D.toggleValue('rare', 'rare'), null);
    assert.equal(D.toggleValue(null, 'rare'), 'rare');
    assert.equal(D.toggleValue('common', 'rare'), 'rare');
});

/* ---- paging ---- */

test('page after step clamps at both ends', () => {
    // Swift: DexScrollPager.pageAfterScroll (CompanionView.swift:1414-1416), 1-based here
    assert.equal(D.pageAfterStep(1, 3, -1), 1);
    assert.equal(D.pageAfterStep(1, 3, 1), 2);
    assert.equal(D.pageAfterStep(3, 3, 1), 3);
    assert.equal(D.pageAfterStep(2, 3, 0), 2);
    assert.equal(D.pageAfterStep(5, 0, 1), 1);
});

test('log pages', () => {
    assert.equal(D.logPageCount(0), 1);
    assert.equal(D.logPageCount(10), 1);
    assert.equal(D.logPageCount(11), 2);
    assert.equal(D.logPageOf(0), 1);
    assert.equal(D.logPageOf(19), 2);
    assert.equal(D.logPageOf(20), 3);
});

/* ---- scroll pager ---- */

const notch = (direction, extra = {}) => ({direction, dx: 0, dy: 0, source: 'wheel', emulated: false,
    finished: false, timeMs: 0, ...extra});
const smooth = (dy, timeMs, extra = {}) => ({direction: 'smooth', dx: 0, dy, source: 'finger', emulated: false,
    finished: false, timeMs, ...extra});

test('scroll: one page per wheel notch', () => {
    const p = new D.ScrollPager();
    assert.equal(p.step(notch('down')), 1);
    assert.equal(p.step(notch('down')), 1);
    assert.equal(p.step(notch('up')), -1);
    assert.equal(p.step(notch('left')), 0);
});

test('scroll: emulated copies never page', () => {
    // A notch arrives as a real discrete event plus an emulated smooth copy (or the reverse).
    const p = new D.ScrollPager();
    assert.equal(p.step(notch('down')), 1);
    assert.equal(p.step(smooth(1, 0, {source: 'wheel', emulated: true})), 0);
    assert.equal(p.step(notch('down', {emulated: true})), 0);
});

test('scroll: smooth gesture accumulates to the threshold, then pages once', () => {
    const p = new D.ScrollPager({threshold: 2, idleMs: 250});
    assert.equal(p.step(smooth(0.8, 0)), 0);
    assert.equal(p.step(smooth(0.8, 16)), 0);
    assert.equal(p.step(smooth(0.8, 32)), 1);
    // still the same gesture: latched
    assert.equal(p.step(smooth(5, 48)), 0);
    assert.equal(p.step(smooth(5, 64)), 0);
});

test('scroll: the end event (zero delta) re-arms the gesture', () => {
    const p = new D.ScrollPager({threshold: 2, idleMs: 250});
    assert.equal(p.step(smooth(3, 0)), 1);
    assert.equal(p.step(smooth(3, 16)), 0);
    assert.equal(p.step(smooth(0, 32)), 0); // finger lifted (swipeTracker.js convention)
    assert.equal(p.step(smooth(-3, 48)), -1);
});

test('scroll: the finish flag re-arms the gesture', () => {
    const p = new D.ScrollPager({threshold: 2, idleMs: 250});
    assert.equal(p.step(smooth(3, 0)), 1);
    assert.equal(p.step(smooth(0.5, 10, {finished: true})), 0);
    assert.equal(p.step(smooth(3, 20)), 1);
});

test('scroll: silence re-arms a gesture without an end event (time latch)', () => {
    const p = new D.ScrollPager({threshold: 2, idleMs: 250});
    assert.equal(p.step(smooth(3, 0)), 1);
    assert.equal(p.step(smooth(3, 200)), 0); // within the latch
    assert.equal(p.step(smooth(3, 451)), 1); // > 250 ms since the last event
});

test('scroll: partial accumulation is dropped after silence', () => {
    const p = new D.ScrollPager({threshold: 2, idleMs: 250});
    assert.equal(p.step(smooth(1.5, 0)), 0);
    assert.equal(p.step(smooth(1.5, 1000)), 0); // the old 1.5 expired
    assert.equal(p.step(smooth(1.5, 1010)), 1);
});

test('scroll: direction reversal inside one gesture cancels out', () => {
    const p = new D.ScrollPager({threshold: 2, idleMs: 250});
    assert.equal(p.step(smooth(1.5, 0)), 0);
    assert.equal(p.step(smooth(-1.5, 10)), 0);
    assert.equal(p.step(smooth(-2, 20)), -1);
});

test('scroll: high-resolution wheel pages once per full notch, without a latch', () => {
    const p = new D.ScrollPager({threshold: 2, idleMs: 250});
    const hires = (dy, t) => smooth(dy, t, {source: 'wheel'});
    assert.equal(p.step(hires(0.5, 0)), 0);
    assert.equal(p.step(hires(0.5, 10)), 1);
    assert.equal(p.step(hires(1, 20)), 1);
    assert.equal(p.step(hires(-1, 30)), -1);
});

test('scroll: horizontal smooth motion never pages', () => {
    const p = new D.ScrollPager({threshold: 2, idleMs: 250});
    assert.equal(p.step({...smooth(0, 0), dx: 5}), 0);
    assert.equal(p.step({...smooth(0, 10), dx: 5}), 0);
});

/* ---- cache ---- */

test('cache: hit within the TTL, miss after', () => {
    let now = 1000;
    const c = new D.ResultCache({ttlMs: 100, now: () => now});
    c.set('a', {x: 1});
    assert.deepEqual(c.get('a'), {x: 1});
    now = 1100;
    assert.deepEqual(c.get('a'), {x: 1});
    now = 1101;
    assert.equal(c.get('a'), null);
    assert.equal(c.size, 0);
});

test('cache: bounded, oldest evicted first', () => {
    const c = new D.ResultCache({ttlMs: 1e9, max: 2, now: () => 0});
    c.set('a', 1);
    c.set('b', 2);
    c.set('c', 3);
    assert.equal(c.get('a'), null);
    assert.equal(c.get('b'), 2);
    assert.equal(c.get('c'), 3);
    c.clear();
    assert.equal(c.get('c'), null);
});

test('cache keys separate commands and args', () => {
    assert.ok(D.cacheKey('getDexPage', {page: 1}) !== D.cacheKey('getDexPage', {page: 2}));
    assert.ok(D.cacheKey('getDexPage', {page: 1}) !== D.cacheKey('getCatchLog', {page: 1}));
});

test('dex signature: clock-only states keep it, dex changes move it', () => {
    const a = clone(state);
    const b = clone(state);
    b.meta.generatedAt = '2030-01-01T00:00:00Z';
    b.usage.combined.today.tokens += 5;
    assert.equal(D.dexSignature(a), D.dexSignature(b));
    b.companion.representative = null;
    assert.ok(D.dexSignature(a) !== D.dexSignature(b));
    const c = clone(state);
    c.companion.dexSummary.entries += 1;
    assert.ok(D.dexSignature(a) !== D.dexSignature(c));
    const d = clone(state);
    d.companion.active.stageIndex = 2;
    assert.ok(D.dexSignature(a) !== D.dexSignature(d));
    assert.equal(D.dexSignature(null), null);
});

/* ---- species detail: forms and individuals ---- */

test('initial form: first collected form, not the uncollected default', () => {
    // species.json: west is uncollected, east holds the only individual.
    assert.equal(D.initialForm(species), 'east');
});

test('initial form: a requested collected form wins; an uncollected one is ignored', () => {
    const s = clone(species);
    s.forms[0].hasNormal = true;
    assert.equal(D.initialForm(s), 'west');
    assert.equal(D.initialForm(s, 'east'), 'east');
    assert.equal(D.initialForm(species, 'west'), 'east');
});

test('initial form: the pinned form wins over the first collected', () => {
    const s = clone(species);
    s.forms[0].hasNormal = true;
    s.forms[1].isRepresentative = true;
    assert.equal(D.initialForm(s), 'east');
});

test('initial form: species without forms → null; nothing collected → the first', () => {
    assert.equal(D.initialForm({forms: []}), null);
    assert.equal(D.initialForm({forms: [{form: 'a'}, {form: 'b'}]}), 'a');
});

test('individuals are filtered by form client-side', () => {
    assert.equal(D.individualsForForm(species, 'east').length, 1);
    assert.equal(D.individualsForForm(species, 'west').length, 0);
    const noForms = {forms: [], individuals: [{id: 'x', form: null}, {id: 'y', form: null}]};
    assert.equal(D.individualsForForm(noForms, null).length, 2);
});

test('individual form filter: the trigger branch (two forms, individuals of both)', () => {
    const s = clone(species);
    s.forms[0].hasNormal = true;
    s.individuals.push({...s.individuals[0], id: 'w1', form: 'west'});
    s.individuals.push({...s.individuals[0], id: 'w2', form: 'west', isShiny: true});
    assert.deepEqual(D.individualsForForm(s, 'west').map(i => i.id), ['w1', 'w2']);
    assert.deepEqual(D.individualsForForm(s, 'east').map(i => i.id), [species.individuals[0].id]);
    assert.deepEqual(D.visibleIndividuals(s, 'west', true).map(i => i.id), ['w2']);
    assert.deepEqual(D.visibleIndividuals(s, 'west', false).map(i => i.id), ['w1']);
});

test('displayed look: requested when available, else the first individual', () => {
    const s = clone(species);
    s.forms[1].isShiny = true;
    s.individuals.unshift({...s.individuals[0], id: 's1', isShiny: true});
    assert.equal(D.displayedShiny(s, 'east'), true); // first individual is shiny
    assert.equal(D.displayedShiny(s, 'east', false), false);
    assert.equal(D.displayedShiny(species, 'east', true), false); // east has no shiny
    assert.equal(D.displayedShiny({forms: [], isShiny: true, hasNormal: false, individuals: []}, null), true);
});

test('pick individual: requested id, else the first', () => {
    const list = [{id: 'a'}, {id: 'b'}];
    assert.equal(D.pickIndividual(list, 'b').id, 'b');
    assert.equal(D.pickIndividual(list, 'zzz').id, 'a');
    assert.equal(D.pickIndividual([], 'a'), null);
});

test('form picker columns', () => {
    // Swift: CompanionView.swift:1815-1817
    assert.equal(D.formPickerColumns(2), 2);
    assert.equal(D.formPickerColumns(6), 4);
    assert.equal(D.formPickerColumns(8), 4);
    assert.equal(D.formPickerColumns(9), 6);
    assert.equal(D.formPickerColumns(20), 6);
    assert.equal(D.formPickerColumns(28), 7);
});

test('stat scale and totals', () => {
    // Swift: PokemonProfile.swift:219-223
    assert.equal(D.statScaleMaximum([20, 11]), 300);
    assert.equal(D.statScaleMaximum([356, 160]), 400);
    assert.equal(D.statScaleMaximum([400]), 400);
    assert.equal(D.statScaleMaximum([]), 300);
    assert.equal(D.baseStatTotal(species.details.baseStats), 475);
});

test('form title and default-form note', () => {
    assert.equal(D.formTitle(species, 'east'), 'Gastrodon [East Sea]');
    assert.equal(D.formTitle(species, 'west'), 'Gastrodon');
    assert.ok(D.isDefaultForm(species, 'west'));
    assert.ok(!D.isDefaultForm(species, 'east'));
});

test('pinned: per form for form species, per species otherwise', () => {
    assert.ok(D.isPinned(species, 'east', {speciesId: 423, form: 'east'}));
    assert.ok(!D.isPinned(species, 'west', {speciesId: 423, form: 'east'}));
    assert.ok(D.isPinned({speciesId: 12, forms: []}, null, {speciesId: 12, form: null, shiny: true}));
    assert.ok(!D.isPinned({speciesId: 12, forms: []}, null, null));
});

test('move methods: level-up, TM, de-duplicated', () => {
    const hp = species.details.moves.find(m => m.name === 'hidden-power');
    assert.equal(D.moveMethodsText(hp.methods), 'Lv. 16 · TM');
    assert.equal(D.moveMethodsText([{method: 'machine'}, {method: 'machine'}]), 'TM');
    assert.equal(D.moveMethodLabel({method: 'level-up', level: 0}), 'Start');
    assert.equal(D.moveMethodLabel({method: 'egg', level: null}), 'Egg');
});

test('individual label', () => {
    const ind = species.individuals[0];
    const label = D.individualLabel(ind, 0, 'en-US');
    assert.ok(label.startsWith('#1 · Normal · Lv. 100 · '), label);
    assert.ok(label.includes('2026'), label);
    assert.equal(D.individualLabel({isRaising: true, level: 41, caughtAt: null}, 1), '#2 · Normal · Lv. 41 · Raising');
});

test('caught date for the individual card', () => {
    const text = D.caughtDateText(species.individuals[0], 'en-US');
    assert.ok(text.includes('2026') && text.includes('Oct'), text);
    assert.equal(D.caughtDateText({isRaising: true, caughtAt: null}), 'Raising');
    assert.equal(D.caughtDateText({caughtAt: null}), '—');
});

/* ---- grid and log ---- */

test('cell helpers on the dex-page fixture', () => {
    const shellos = dexPage.cells.find(c => c.speciesId === 422);
    assert.equal(D.formCountText(shellos), '1/2');
    assert.equal(D.formCountText(dexPage.cells[0]), null);
    const butterfree = dexPage.cells.find(c => c.speciesId === 12);
    assert.equal(D.cellTooltip(butterfree, 'Common'), '#12 Butterfree · Common · Shiny · Representative');
    const pidgeotto = dexPage.cells.find(c => c.speciesId === 17);
    assert.equal(D.cellTooltip(pidgeotto, 'Common'), '#17 Pidgeotto · Common · Raising');
    assert.ok(D.cellOwned(butterfree));
    assert.ok(!D.cellOwned({hasNormal: false, isShiny: false}));
});

test('regions: text, medal, fraction', () => {
    const kanto = dexPage.regions[0];
    assert.equal(D.regionProgressText(kanto, 'Kanto'), 'Kanto 11/151');
    assert.equal(D.regionMedal(kanto), null);
    assert.equal(D.regionMedal({complete: true, shinyComplete: false}), '🏅');
    assert.equal(D.regionMedal({complete: true, shinyComplete: true}), '✨');
    assert.close(D.regionFraction(kanto), 11 / 151);
    assert.equal(D.regionFraction({owned: 1, total: 0}), 0);
});

test('header medals: national and Unown tiers only', () => {
    const medals = D.headerMedals(['region.kanto', 'regionShiny.kanto', 'unown', 'national', 'nationalShiny', 'x']);
    assert.deepEqual(medals.map(m => m.key), ['unown', 'national', 'nationalShiny']);
});

test('caught text', () => {
    const now = Date.parse('2026-11-18T13:20:00Z');
    assert.equal(D.caughtText(now - 2 * 3600e3, now), '2 h ago');
    assert.equal(D.caughtText(Date.parse(catchLog.entries[1].caughtAt), now), '5 d ago');
    assert.equal(D.caughtText(now - 90 * 86400e3, now), '2 mo ago');
    assert.equal(D.caughtText(now - 800 * 86400e3, now), '2 yr ago');
    assert.equal(D.caughtText(NaN, now), '');
});

test('error codes', () => {
    assert.ok(D.retriesOnState('notReady'));
    assert.ok(D.retriesOnState('helperExited'));
    assert.ok(!D.retriesOnState('timeout'));
    for (const code of ['notReady', 'helperExited', 'timeout', 'unavailable', 'internal', undefined])
        assert.ok(D.dexErrorText(code).length > 0);
});

test('evolution line: raising uses active.evoLine, others the log chain', () => {
    const active = state.companion.active;
    const raising = {id: 'active-16-17', isRaising: true};
    assert.equal(D.evoLineFor(raising, active, new Map()).length, 3);
    const chains = new Map(catchLog.entries.map(e => [e.id, e.chain]));
    const gastrodon = species.individuals[0];
    const line = D.evoLineFor(gastrodon, active, chains);
    assert.deepEqual(line.map(n => [n.speciesId, n.form, n.current]), [[422, 'east', false], [423, 'east', true]]);
    assert.equal(D.evoLineFor({id: 'unknown'}, active, chains), null);
    assert.equal(D.evoLineFor(null, active, chains), null);
});

test('the individual card prayer lines', () => {
    // EggPrayerTests.swift:263-272; CV:1947-1953 (shown only when > 0)
    const ind = fixture('species.json').data.individuals.find(i => i.prayers > 0);
    assert.deepEqual(D.prayerLines(ind),
        {born: 'Born after 37 prayers', caption: 'Your prayers reached the heavens. Probably.'});
    assert.equal(D.prayerLines({prayers: 1}).born, 'Born after 1 prayer', 'ngettext (M3 decision 40)');
    assert.equal(D.prayerLines({prayers: 0}), null);
    assert.equal(D.prayerLines({}), null, 'an older helper without the field');
    assert.equal(D.prayerLines(null), null);
});

run('dexModel.test.js');
