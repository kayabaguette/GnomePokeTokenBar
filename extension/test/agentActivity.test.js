// gjs -m extension/test/agentActivity.test.js
// Ported from Tests/PokeTokenBarTests/AgentActivityTests.swift (cited per test).
import {assert, run, test} from './harness.js';
import * as A from '../lib/pet/agentActivity.js';

const act = url => A.eventFromUrl(url)?.activity ?? null;
const working = {state: 'working', message: null};
const idle = {state: 'idle', message: null};
const waiting = (message = null) => ({state: 'waiting', message});
const done = (message = null) => ({state: 'done', message});
const graphemes = s => [...new Intl.Segmenter(undefined, {granularity: 'grapheme'}).segment(s)].length;

test('every state parses; scheme, host and state are case-insensitive', () => {
    // AgentActivityTests.swift:11-17
    assert.deepEqual(act('poketokenbar://agent?state=working'), working);
    assert.deepEqual(act('poketokenbar://agent?state=waiting'), waiting());
    assert.deepEqual(act('poketokenbar://agent?state=done'), done());
    assert.deepEqual(act('poketokenbar://agent?state=idle'), idle);
    assert.deepEqual(act('POKETOKENBAR://Agent?state=DONE'), done());
});

test('unknown input is ignored', () => {
    // AgentActivityTests.swift:19-26
    assert.equal(A.eventFromUrl('poketokenbar://agent?state=sleeping'), null, 'unknown state');
    assert.equal(A.eventFromUrl('poketokenbar://agent'), null, 'missing state');
    assert.equal(A.eventFromUrl('poketokenbar://agent?message=hi'), null, 'message without state');
    assert.equal(A.eventFromUrl('poketokenbar://settings?state=done'), null, 'unknown host');
    assert.equal(A.eventFromUrl('poketokenbar:agent?state=done'), null, 'no host');
    assert.equal(A.eventFromUrl('https://agent?state=done'), null, 'other scheme');
});

test('malformed input never throws (D-Bus handler safety, derived)', () => {
    assert.equal(A.eventFromUrl('poketokenbar://agent?state=done&message=%E2%80'), null);
    assert.equal(A.eventFromUrl('poketokenbar://agent?state=%ZZ'), null);
    assert.equal(A.eventFromUrl(null), null);
    assert.equal(A.eventFromUrl(''), null);
    assert.equal(A.eventFromReport('sleeping', 'x', 'y'), null);
    assert.equal(A.eventFromReport(undefined, undefined, undefined), null);
});

test('message is kept only for bubble states; the first state wins', () => {
    // AgentActivityTests.swift:28-35
    assert.deepEqual(act('poketokenbar://agent?state=waiting&message=Pick%20a%20branch'), waiting('Pick a branch'));
    assert.deepEqual(act('poketokenbar://agent?state=done&message=Tests%20pass'), done('Tests pass'));
    assert.deepEqual(act('poketokenbar://agent?state=working&message=ignored'), working);
    assert.deepEqual(act('poketokenbar://agent?message=first&state=done&state=working'), done('first'));
    // '+' stays literal, as URLComponents keeps it (floating-pet.md §5.2 A).
    assert.deepEqual(act('poketokenbar://agent?state=done&message=a+b'), done('a+b'));
});

test('message is collapsed to one plain line', () => {
    // AgentActivityTests.swift:37-47
    assert.deepEqual(act('poketokenbar://agent?state=done&message=%20%20line%201%0A%0D%09line%202%20%20'),
        done('line 1 line 2'));
    assert.equal(A.sanitizedMessage('ab‮cd​ef\u0007g'), 'abcdef g');
    assert.equal(A.sanitizedMessage('family 👨‍👩‍👧'), 'family 👨‍👩‍👧', 'ZWJ emoji stay joined');
    assert.equal(A.sanitizedMessage(' \n\t '), null);
    assert.equal(A.sanitizedMessage(''), null);
    assert.equal(A.sanitizedMessage(null), null);
    assert.deepEqual(act('poketokenbar://agent?state=waiting&message='), waiting());
});

test('an oversized message is capped with an ellipsis', () => {
    // AgentActivityTests.swift:49-65
    const exact = 'a'.repeat(A.MAX_MESSAGE_LENGTH);
    assert.equal(A.sanitizedMessage(exact), exact);
    const long = 'word '.repeat(100);
    const capped = A.sanitizedMessage(long);
    assert.ok(graphemes(capped) <= A.MAX_MESSAGE_LENGTH, 'length');
    assert.ok(capped.endsWith('…'), 'ellipsis');
    assert.ok(!capped.slice(0, -1).endsWith(' '), 'no dangling space before the ellipsis');
    const encoded = encodeURIComponent(long);
    assert.deepEqual(act(`poketokenbar://agent?state=done&message=${encoded}`), done(capped));
});

test('stacked combining marks are bounded by scalars', () => {
    // AgentActivityTests.swift:69-76
    const raw = `a${'́'.repeat(2000)}`.repeat(79);
    assert.equal(graphemes(raw), 79, 'precondition');
    const message = A.sanitizedMessage(raw);
    assert.ok([...message].length <= A.MAX_MESSAGE_SCALARS, 'scalars');
    assert.ok(graphemes(message) <= A.MAX_MESSAGE_LENGTH, 'graphemes');
    assert.ok(message.endsWith('…'));
});

test('Report(sss): empty label and message mean none', () => {
    // protocol §15 (derived, no Swift oracle)
    assert.deepEqual(A.eventFromReport('WAITING', '', ''), {activity: waiting(), label: null});
    assert.deepEqual(A.eventFromReport('done', ' my\nproject ', 'ok'), {activity: done('ok'), label: 'my project'});
    assert.deepEqual(A.eventFromReport('working', 'web', 'dropped'), {activity: working, label: 'web'});
    // busctl passes quotes literally: they are text, not syntax.
    assert.equal(A.eventFromReport('done', "'x'", '').label, "'x'");
});

/* ---- tracker ---- */

const t0 = 1_000_000;
function apply(tracker, activity, at, label = null) {
    let url = `poketokenbar://agent?state=${activity.state}`;
    if (activity.message)
        url += `&message=${activity.message}`;
    if (label)
        url += `&label=${label}`;
    tracker.apply(A.eventFromUrl(url), at);
}
const keys = tracker => [...tracker.agents.keys()];

test('working expires after ten minutes without news', () => {
    // AgentActivityTests.swift:96-106
    const t = new A.AgentActivityTracker();
    apply(t, working, t0);
    assert.equal(t.deadline, t0 + 600);
    assert.equal(t.expire(t0 + 599), false);
    assert.deepEqual(t.activity, working);
    assert.equal(t.expire(t0 + 600), true);
    assert.deepEqual(t.activity, idle);
    assert.equal(t.deadline, null);
    assert.equal(t.expire(t0 + 10_000), false, 'idle has nothing to expire');
});

test('another working pushes the timeout back', () => {
    // AgentActivityTests.swift:108-114
    const t = new A.AgentActivityTracker();
    apply(t, working, t0);
    apply(t, working, t0 + 500);
    assert.equal(t.expire(t0 + 700), false);
    assert.deepEqual(t.activity, working);
});

test('done shows for six seconds', () => {
    // AgentActivityTests.swift:116-123
    const t = new A.AgentActivityTracker();
    apply(t, done('ok'), t0);
    assert.equal(t.expire(t0 + 5.9), false);
    assert.deepEqual(t.activity, done('ok'));
    assert.equal(t.expire(t0 + 6), true);
    assert.deepEqual(t.activity, idle);
});

test('waiting stays until the next event or a click', () => {
    // AgentActivityTests.swift:125-139
    const t = new A.AgentActivityTracker();
    apply(t, working, t0);
    apply(t, waiting(), t0 + 1);
    assert.equal(t.deadline, null, 'no inherited timeout');
    assert.equal(t.expire(t0 + 86_400), false);
    assert.deepEqual(t.activity, waiting());
    assert.equal(t.dismissBubble(), true);
    assert.deepEqual(t.activity, idle);
    apply(t, waiting(), t0);
    apply(t, working, t0 + 2);
    assert.deepEqual(t.activity, working);
});

test('a click dismisses only bubbles', () => {
    // AgentActivityTests.swift:141-153
    const t = new A.AgentActivityTracker();
    apply(t, working, t0);
    assert.equal(t.dismissBubble(), false);
    assert.deepEqual(t.activity, working);
    assert.ok(t.deadline !== null);
    apply(t, done(), t0);
    assert.equal(t.dismissBubble(), true);
    assert.deepEqual(t.activity, idle);
    assert.equal(t.deadline, null);
    assert.equal(t.dismissBubble(), false);
});

test('idle clears everything', () => {
    // AgentActivityTests.swift:155-162
    const t = new A.AgentActivityTracker();
    apply(t, working, t0);
    apply(t, idle, t0);
    assert.equal(t.agents.size, 0);
    assert.equal(t.deadline, null);
    assert.deepEqual(t.activity, idle);
});

test('the label is parsed and cleaned like a message', () => {
    // AgentActivityTests.swift:166-181
    assert.equal(A.eventFromUrl('poketokenbar://agent?state=done&label=%20my%0A%20project%E2%80%AE%20').label,
        'my project');
    assert.equal(A.eventFromUrl('poketokenbar://agent?state=done').label, null);
    assert.equal(A.eventFromUrl('poketokenbar://agent?state=done&label=%20').label, null);
    const long = A.sanitizedLabel('x'.repeat(100));
    assert.equal(graphemes(long), A.MAX_LABEL_LENGTH);
    assert.ok(long.endsWith('…'));
    const stacked = A.sanitizedLabel(`a${'́'.repeat(500)}`.repeat(20));
    assert.ok([...stacked].length <= A.MAX_LABEL_LENGTH * 4);
});

test('each label keeps its own state', () => {
    // AgentActivityTests.swift:184-195
    const t = new A.AgentActivityTracker();
    apply(t, working, t0, 'web');
    apply(t, done(), t0 + 1, 'api');
    assert.equal(t.summary.working, true);
    assert.deepEqual(t.summary.done, {label: 'api', message: null});
    assert.equal(t.expire(t0 + 7), true);
    assert.equal(t.summary.done, null);
    assert.equal(t.summary.working, true);
    assert.deepEqual(t.activity, working);
});

test('unlabeled events still share one agent', () => {
    // AgentActivityTests.swift:197-203
    const t = new A.AgentActivityTracker();
    apply(t, working, t0);
    apply(t, done(), t0);
    assert.equal(t.summary.working, false);
    assert.equal(t.agents.size, 1);
});

test('waiting outranks working and counts the others', () => {
    // AgentActivityTests.swift:205-219
    const t = new A.AgentActivityTracker();
    apply(t, waiting('first'), t0, 'a');
    apply(t, working, t0, 'b');
    apply(t, waiting('latest'), t0, 'c');
    const s = t.summary;
    assert.deepEqual(s.waiting, {label: 'c', message: 'latest'});
    assert.equal(s.otherWaitingCount, 1);
    assert.equal(s.working, true);
    assert.deepEqual(t.activity, waiting('latest'));
    apply(t, working, t0, 'c');
    assert.deepEqual(t.summary.waiting, {label: 'a', message: 'first'});
    assert.equal(t.summary.otherWaitingCount, 0);
});

test('done overlays a waiting agent only for its few seconds', () => {
    // AgentActivityTests.swift:221-229
    const t = new A.AgentActivityTracker();
    apply(t, waiting(), t0, 'a');
    apply(t, done(), t0, 'b');
    assert.deepEqual(t.activity, done());
    assert.equal(t.summary.waiting.label, 'a');
    t.expire(t0 + A.DONE_DURATION_S);
    assert.deepEqual(t.activity, waiting());
});

test('overlapping dones show the latest', () => {
    // AgentActivityTests.swift:232-240
    const t = new A.AgentActivityTracker();
    apply(t, done('a'), t0, 'a');
    apply(t, done('b'), t0 + 2, 'b');
    apply(t, done('c'), t0 + 1, 'c');
    assert.deepEqual(t.summary.done, {label: 'c', message: 'c'});
    t.expire(t0 + A.DONE_DURATION_S + 1);
    assert.deepEqual(t.summary.done, {label: 'b', message: 'b'});
});

test('each label expires on its own clock', () => {
    // AgentActivityTests.swift:242-250
    const t = new A.AgentActivityTracker();
    apply(t, working, t0, 'a');
    apply(t, working, t0 + 300, 'b');
    assert.equal(t.deadline, t0 + 600);
    assert.equal(t.expire(t0 + 600), true);
    assert.deepEqual(keys(t), ['b']);
    assert.equal(t.deadline, t0 + 900);
});

test('a click clears every question and result but not work', () => {
    // AgentActivityTests.swift:252-261
    const t = new A.AgentActivityTracker();
    apply(t, waiting(), t0, 'a');
    apply(t, waiting(), t0, 'b');
    apply(t, done(), t0, 'c');
    apply(t, working, t0, 'd');
    assert.equal(t.dismissBubble(), true);
    assert.deepEqual(keys(t), ['d']);
    assert.equal(t.dismissBubble(), false);
});

test('idle clears only its own label', () => {
    // AgentActivityTests.swift:263-269
    const t = new A.AgentActivityTracker();
    apply(t, working, t0, 'a');
    apply(t, working, t0, 'b');
    apply(t, idle, t0, 'a');
    assert.deepEqual(keys(t), ['b']);
});

test('the number of agents is capped', () => {
    // AgentActivityTests.swift:272-285
    const t = new A.AgentActivityTracker();
    for (let i = 0; i < 50; i++)
        apply(t, working, t0 + i, `agent${i}`);
    assert.equal(t.agents.size, A.MAX_AGENTS);
    assert.deepEqual(keys(t).sort(), Array.from({length: 8}, (_, i) => `agent${42 + i}`).sort());
    apply(t, working, t0 + 60, 'agent42');
    assert.equal(t.agents.size, A.MAX_AGENTS);
    apply(t, waiting(), t0 + 61, 'new');
    assert.equal(t.agents.has('agent43'), false);
    assert.equal(t.agents.has('agent42'), true);
});

run('agentActivity.test.js');
