// gjs -m extension/test/petController.test.js
// Controller wiring, ported from Tests/PokeTokenBarTests/FloatingPetAgentTests.swift:239-492
// (FloatingPetAgentWiringTests) against fake timers; geometry mirrored to y-down.
import {assert, run, test} from './harness.js';
import {eventFromUrl} from '../lib/pet/agentActivity.js';
import {PetController} from '../lib/pet/petController.js';
import * as M from '../lib/pet/petModel.js';

/** Swift fixtures keep the display asleep (FloatingPetAgentWiringTests.swift:267): not visible. */
function fixture({petEnabled = true, agentActivity = true, bubbleAlerts = true, visible = false,
    motion = true, home = {x: 300, y: 700}} = {}) {
    const f = {
        t: 1_000_000,
        timers: new Map(),
        nextId: 1,
        opened: 0,
        saved: home,
        persisted: [],
        animations: [],
        stops: 0,
        petEnabled, agentActivity, bubbleAlerts, visible, motion,
        cursor: {x: 600, y: 400},
        prays: 0,
        eggPrayers: 0,
        emojis: 0,
        refreshes: 0,
        changes: 0,
    };
    f.ctl = new PetController({
        now: () => f.t,
        setTimer: (fn, ms) => {
            const id = f.nextId++;
            f.timers.set(id, {fn, at: f.t + ms / 1000});
            return id;
        },
        clearTimer: id => f.timers.delete(id),
        openPopover: () => f.opened++,
        // The helper counts (state first, then the reply); `notEgg` never calls back.
        pray: onCount => {
            f.prays++;
            if (f.eggPrayers === null)
                return;
            f.eggPrayers++;
            onCount(f.eggPrayers);
        },
        spawnPrayer: () => f.emojis++,
        refreshUsage: () => f.refreshes++,
        persistHome: o => {
            f.saved = o;
            f.persisted.push(o);
        },
        home: () => f.saved,
        petSize: () => 96,
        motionAllowed: () => f.motion,
        agentEnabled: () => f.petEnabled && f.agentActivity,
        bubbleAlertsEnabled: () => f.petEnabled && f.bubbleAlerts,
        visible: () => f.visible,
        cursor: () => f.cursor,
        workAreaAt: () => ({x: 0, y: 0, width: 1440, height: 900}),
        animate: (target, done) => f.animations.push({target, done}),
        stopAnimation: () => f.stops++,
        changed: () => f.changes++,
    });
    /** Advance fake time, firing due timers in order. */
    f.advance = seconds => {
        const end = f.t + seconds;
        for (;;) {
            let next = null;
            for (const [id, tm] of f.timers) {
                if (tm.at <= end && (next === null || tm.at < f.timers.get(next).at))
                    next = id;
            }
            if (next === null)
                break;
            const tm = f.timers.get(next);
            f.timers.delete(next);
            f.t = tm.at;
            tm.fn();
        }
        f.t = end;
    };
    f.send = (state, label = null, message = null) => {
        let url = `poketokenbar://agent?state=${state}`;
        if (label)
            url += `&label=${label}`;
        if (message)
            url += `&message=${message}`;
        f.ctl.handleAgentEvent(eventFromUrl(url));
    };
    return f;
}

const screen = {x: 0, y: 0, width: 1440, height: 900};
const act = f => f.ctl.tracker.activity;

test('events are dropped while the pet or the setting is off', () => {
    // FloatingPetAgentTests.swift:284-296
    const off = fixture({petEnabled: false});
    off.send('working');
    assert.equal(act(off).state, 'idle');
    const f = fixture({agentActivity: false});
    f.send('waiting');
    assert.equal(act(f).state, 'idle');
    assert.equal(f.ctl.agentExpiryFireTime, null);
});

test('turning the setting off resets agent state', () => {
    // FloatingPetAgentTests.swift:298-314
    const f = fixture();
    f.send('working');
    f.ctl.beginRun({x: 600, y: 400}, screen);
    assert.equal(act(f).state, 'working');
    assert.ok(f.ctl.agentExpiryFireTime !== null);
    f.agentActivity = false;
    f.ctl.sync();
    assert.equal(act(f).state, 'idle');
    assert.equal(f.ctl.runPhase, 'home');
    assert.equal(f.ctl.agentExpiryFireTime, null);
    assert.equal(f.timers.size, 0, 'no timer left behind');
});

test('the run never overwrites the saved home', () => {
    // FloatingPetAgentTests.swift:317-337
    const f = fixture({home: {x: 300, y: 200}});
    f.send('done');
    const run = f.ctl.beginRun({x: 800, y: 300}, screen);
    f.ctl.petMoved({x: 500, y: 400}, false);
    f.ctl.petMoved(run.petOrigin, false);
    assert.deepEqual(f.saved, {x: 300, y: 200});
    assert.equal(f.persisted.length, 0);
    f.ctl.expireAgentActivity(f.ctl.tracker.deadline);
    assert.equal(act(f).state, 'idle');
    assert.equal(f.ctl.runPhase, 'home');
    assert.deepEqual(f.saved, {x: 300, y: 200});
});

test('a drag during the run becomes the new home (mirrored)', () => {
    // FloatingPetAgentTests.swift:339-358: the panel dropped at y-up (900, 100) is y-down (900, 632);
    // the saved pet bottom-left is (942, 800).
    const f = fixture({home: {x: 300, y: 200}});
    f.send('done');
    f.ctl.beginRun({x: 800, y: 300}, screen);
    const pet = M.petOrigin({x: 900, y: 632}, 96, M.panelSize(96, true));
    f.ctl.petMoved(pet, true);
    assert.equal(f.ctl.runPhase, 'home', 'the return is cancelled');
    assert.deepEqual(f.saved, {x: 942, y: 800});
});

test('a click dismisses the bubble and still opens the popover', () => {
    // FloatingPetAgentTests.swift:360-374
    const f = fixture();
    f.send('waiting');
    f.ctl.petClicked(false);
    assert.equal(act(f).state, 'idle');
    assert.equal(f.opened, 1);
    f.send('working');
    f.ctl.petClicked(false);
    assert.equal(act(f).state, 'working', 'a click does not stop work');
    assert.equal(f.opened, 2);
});

test('an egg click acknowledges agents and prays; menu Open opens without praying', () => {
    // FloatingPetAgentTests.swift:378-402 (M3 decision 39)
    const f = fixture();
    f.send('waiting');
    f.ctl.petClicked(true);
    assert.equal(act(f).state, 'idle');
    assert.equal(f.eggPrayers, 1);
    assert.equal(f.opened, 0);
    f.send('working');
    f.ctl.petClicked(true);
    assert.equal(act(f).state, 'working');
    assert.equal(f.eggPrayers, 2);
    assert.equal(f.opened, 0);
    f.send('done');
    f.ctl.openFromMenu();
    assert.equal(act(f).state, 'idle', 'the done replaced the unlabeled work and is acknowledged');
    assert.equal(f.opened, 1, 'context-menu Open still opens an egg');
    assert.equal(f.eggPrayers, 2);
});

test('a hatched click acknowledges agents and opens without praying', () => {
    // FloatingPetAgentTests.swift:404-415
    const f = fixture();
    f.send('waiting');
    f.ctl.petClicked(false);
    assert.equal(act(f).state, 'idle');
    assert.equal(f.opened, 1);
    assert.equal(f.prays, 0);
});

/* ---- prayer refresh throttle (EggPrayerTests.swift:317-402) ---- */

const prayN = (f, n) => {
    for (let i = 0; i < n; i++)
        f.ctl.petClicked(true);
};

test('prayer refresh follows the milestones', () => {
    // EggPrayerTests.swift:337-359
    const f = fixture();
    const start = f.t;
    prayN(f, 29);
    assert.equal(f.refreshes, 0, '29 prayers: no refresh');
    prayN(f, 1);
    assert.equal(f.refreshes, 1, 'the 30th prayer refreshes');
    f.t = start + M.PRAYER_REFRESH_INTERVAL_S;
    prayN(f, 29);
    assert.equal(f.refreshes, 1, '59 prayers: no new refresh');
    prayN(f, 1);
    assert.equal(f.refreshes, 2, 'the 60th prayer refreshes');
    f.t = start + 2 * M.PRAYER_REFRESH_INTERVAL_S;
    prayN(f, 29);
    assert.equal(f.refreshes, 2);
    prayN(f, 1);
    assert.equal(f.refreshes, 3, 'the 90th prayer refreshes');
    assert.equal(f.emojis, f.prays, 'every click still spawns its emoji');
    assert.ok(f.changes >= f.prays, 'every reply rebuilds the hover text');
});

test('a milestone inside the floor is refreshed by the first click after it', () => {
    // EggPrayerTests.swift:361-375
    const f = fixture();
    const start = f.t;
    prayN(f, 30);
    assert.equal(f.refreshes, 1);
    f.t = start + 5;
    prayN(f, 30);
    assert.equal(f.refreshes, 1, 'the floor holds the 60th-prayer refresh back');
    f.t = start + M.PRAYER_REFRESH_INTERVAL_S;
    prayN(f, 1);
    assert.equal(f.refreshes, 2, 'the first click after the floor does not lose the milestone');
    prayN(f, 1);
    assert.equal(f.refreshes, 2, 'and it is not refreshed twice');
});

test('a new egg starts the milestones over', () => {
    // EggPrayerTests.swift:377-389
    const f = fixture();
    const start = f.t;
    prayN(f, 60);
    assert.equal(f.refreshes, 1);
    f.t = start + 2 * M.PRAYER_REFRESH_INTERVAL_S;
    f.eggPrayers = 0;
    prayN(f, 29);
    assert.equal(f.refreshes, 1);
    prayN(f, 1);
    assert.equal(f.refreshes, 2, 'the new egg\'s 30th prayer refreshes again');
});

test('hatched clicks open the popover without praying or refreshing', () => {
    // EggPrayerTests.swift:391-402
    const f = fixture();
    prayN(f, 29);
    for (let i = 0; i < 50; i++)
        f.ctl.petClicked(false);
    assert.equal(f.opened, 50);
    assert.equal(f.prays, 29);
    assert.equal(f.refreshes, 0);
    prayN(f, 1);
    assert.equal(f.refreshes, 1, 'the 30th real prayer still refreshes');
});

test('with motion off a prayer still counts and refreshes, with no particle or wobble', () => {
    // M3 decision 40 (deviation from FPP:1057, which ignores reduced motion)
    const f = fixture({motion: false});
    prayN(f, 30);
    assert.equal(f.eggPrayers, 30);
    assert.equal(f.emojis, 0);
    assert.equal(f.refreshes, 1);
    assert.ok(f.changes >= 30, 'the hover text still rebuilds');
});

test('a prayer the helper refuses (notEgg) changes nothing', () => {
    // protocol §7.6: the extension ignores notEgg (a click racing a hatch)
    const f = fixture();
    f.eggPrayers = null;
    const changes = f.changes;
    prayN(f, 40);
    assert.equal(f.prays, 40);
    assert.equal(f.emojis, 40, 'the click effect already played');
    assert.equal(f.refreshes, 0);
    assert.equal(f.changes, changes);
    assert.equal(f.opened, 0);
});

test('expiry re-arms when the timer fires early', () => {
    // FloatingPetAgentTests.swift:417-434
    const f = fixture();
    f.send('done');
    const deadline = f.ctl.tracker.deadline;
    assert.equal(f.ctl.agentExpiryFireTime, deadline);
    f.ctl.expireAgentActivity(deadline - 0.5);
    assert.deepEqual(act(f), {state: 'done', message: null});
    assert.equal(f.ctl.agentExpiryFireTime, deadline, 'an early fire must arm again');
    f.ctl.expireAgentActivity(deadline);
    assert.equal(act(f).state, 'idle');
    assert.equal(f.ctl.agentExpiryFireTime, null);
    f.send('waiting');
    assert.equal(f.ctl.agentExpiryFireTime, null, 'waiting arms no timer');
    assert.equal(f.timers.size, 0);
});

test('the armed timer fires at the deadline (fake clock)', () => {
    // derived: the timer delay matches the tracker deadline
    const f = fixture();
    f.send('done');
    f.advance(5.9);
    assert.equal(act(f).state, 'done');
    f.advance(0.1);
    assert.equal(act(f).state, 'idle');
    assert.equal(f.timers.size, 0);
});

test('a done while another agent works keeps the bob', () => {
    // FloatingPetAgentTests.swift:443-460
    const f = fixture();
    f.send('working', 'web');
    assert.equal(f.ctl.isBobbing, true);
    f.send('done', 'api');
    f.ctl.beginRun({x: 600, y: 400}, screen);
    assert.deepEqual(f.ctl.bubble, {kind: 'agentDone', label: 'api', message: null});
    assert.equal(f.ctl.isBobbing, true);
    f.ctl.expireAgentActivity(f.ctl.tracker.agents.get('api').deadline);
    assert.equal(f.ctl.runPhase, 'home', 'the pet walks home after the done');
    assert.equal(f.ctl.bubble, null);
    assert.equal(f.ctl.isBobbing, true, 'and keeps bobbing for web');
});

test('a done while another agent waits brings the question back', () => {
    // FloatingPetAgentTests.swift:463-476
    const f = fixture();
    f.send('waiting', 'web');
    f.send('working', 'api');
    assert.deepEqual(f.ctl.bubble, {kind: 'agentWaiting', label: 'web', message: null, others: 0});
    f.send('done', 'api');
    assert.deepEqual(f.ctl.bubble, {kind: 'agentDone', label: 'api', message: null});
    f.ctl.expireAgentActivity(f.ctl.tracker.agents.get('api').deadline);
    assert.deepEqual(f.ctl.bubble, {kind: 'agentWaiting', label: 'web', message: null, others: 0});
});

test('a click clears every waiting agent but not work', () => {
    // FloatingPetAgentTests.swift:478-492
    const f = fixture();
    f.send('waiting', 'web');
    f.send('waiting', 'api');
    f.send('working', 'cli');
    assert.deepEqual(f.ctl.bubble, {kind: 'agentWaiting', label: 'api', message: null, others: 1});
    f.ctl.petClicked(false);
    assert.equal(f.ctl.bubble, null);
    assert.equal(f.ctl.summary.working, true);
    assert.equal(f.ctl.isBobbing, true);
    assert.equal(f.opened, 1);
});

/* ---- visible runs (derived: the animate seam and the run generation) ---- */

test('a visible done runs to the cursor once, then walks home', () => {
    const f = fixture({visible: true});
    f.send('done', 'a');
    assert.equal(f.ctl.runPhase, 'away');
    assert.equal(f.animations.length, 1);
    // Panel below-right of the cursor (616, 416); the pet is centred in the 180-wide panel, bottom at 584.
    assert.deepEqual(f.animations[0].target, {x: 658, y: 584});
    f.send('done', 'b');
    assert.equal(f.animations.length, 1, 'a second done does not run again while away');
    f.advance(6);
    assert.equal(f.ctl.runPhase, 'returning');
    assert.deepEqual(f.animations[1].target, {x: 300, y: 700});
    f.animations[0].done(); // a stale completion does nothing
    assert.equal(f.ctl.runPhase, 'returning');
    f.animations[1].done();
    assert.equal(f.ctl.runPhase, 'home');
});

test('with motion off a done shows the bubble without moving', () => {
    // README.md:218 upstream: Reduce Motion / Low Power keep bubbles, nothing moves
    const f = fixture({visible: true, motion: false});
    f.send('working');
    assert.equal(f.ctl.isBobbing, false);
    f.send('done');
    assert.equal(f.ctl.runPhase, 'home');
    assert.equal(f.animations.length, 0);
    assert.deepEqual(f.ctl.bubble, {kind: 'agentDone', label: null, message: null});
});

test('hiding the pet brings it back home and cancels the run', () => {
    // FPP:679-692
    const f = fixture({visible: true});
    f.send('done');
    f.ctl.hidden();
    assert.equal(f.ctl.runPhase, 'home');
    assert.deepEqual(f.ctl.petOrigin, {x: 300, y: 700});
    f.animations[0].done();
    assert.equal(f.ctl.runPhase, 'home');
});

/* ---- limit bubble, hearts, event callout (derived from US:2263-2274, FPP:642-677) ---- */

test('limit alerts are batched for 250 ms; critical wins; 6 s TTL', () => {
    const f = fixture({visible: true});
    f.ctl.handleLimitAlert({kind: 'limitAlert', tier: 'warn', utilization: 90});
    f.ctl.handleLimitAlert({kind: 'limitAlert', tier: 'crit', utilization: 95});
    assert.equal(f.ctl.limitAlert, null, 'nothing before the batch closes');
    f.advance(0.25);
    assert.equal(f.ctl.limitAlert.tier, 'crit');
    assert.equal(f.ctl.bubble.kind, 'limit');
    f.advance(3);
    // A newer alert replaces it and restarts the TTL.
    f.ctl.handleLimitAlert({kind: 'limitAlert', tier: 'warn', utilization: 85});
    f.advance(0.25);
    assert.equal(f.ctl.limitAlert.utilization, 85);
    f.advance(5.9);
    assert.ok(f.ctl.limitAlert !== null, 'the first TTL does not clear the newer alert');
    f.advance(0.1);
    assert.equal(f.ctl.limitAlert, null);
});

test('limit alerts are dropped when bubble alerts are off, independently of notifications', () => {
    const f = fixture({bubbleAlerts: false});
    f.ctl.handleLimitAlert({kind: 'limitAlert', tier: 'crit', utilization: 99});
    f.advance(1);
    assert.equal(f.ctl.limitAlert, null);
    assert.equal(f.timers.size, 0);
});

test('hearts: a stroke shows them for 1.8 s unless speech is up', () => {
    const f = fixture({visible: true});
    f.ctl.stroked();
    assert.equal(f.ctl.heartsVisible, true);
    f.advance(1.0);
    f.ctl.stroked(); // restarts the expiry
    f.advance(1.0);
    assert.equal(f.ctl.heartsVisible, true);
    f.advance(0.8);
    assert.equal(f.ctl.heartsVisible, false);
    f.send('waiting');
    f.ctl.stroked();
    assert.equal(f.ctl.hearts, false, 'speech outranks hearts');
});

test('event callout: pops once for 6 s, dropped while hidden or behind a bubble', () => {
    const f = fixture({visible: true});
    f.ctl.handleCompanionEvent('Pidgey hatched from the egg!');
    assert.equal(f.ctl.eventText, 'Pidgey hatched from the egg!');
    f.advance(6);
    assert.equal(f.ctl.eventText, null);
    f.visible = false;
    f.ctl.handleCompanionEvent('missed');
    assert.equal(f.ctl.eventText, null);
    f.visible = true;
    f.send('waiting');
    f.ctl.handleCompanionEvent('behind a bubble');
    assert.equal(f.ctl.eventText, null, 'dropped, not queued');
});

test('destroy leaves no timer behind', () => {
    const f = fixture({visible: true});
    f.send('working');
    f.ctl.handleLimitAlert({kind: 'limitAlert', tier: 'warn', utilization: 90});
    f.ctl.stroked();
    f.ctl.destroy();
    assert.equal(f.timers.size, 0);
});

run('petController.test.js');
