// gjs -m extension/test/bubbleText.test.js
// Bubble titles and bodies. Exact strings from FloatingPetAgentTests.swift:144-157; the cut points
// depend on fonts, so :158-233 are ported as property tests over a Pango measurer
// (floating-pet.md §5.2 D).
import Pango from 'gi://Pango';
import PangoCairo from 'gi://PangoCairo';

import {assert, run, test} from './harness.js';
import * as A from '../lib/pet/agentActivity.js';
import * as B from '../lib/pet/bubbleText.js';
import {BUBBLE_CONTENT_WIDTH} from '../lib/pet/petModel.js';
import {pangoMeasure} from '../lib/pet/pangoMeasure.js';

function font(spec, px) {
    const f = Pango.FontDescription.from_string(spec);
    f.set_absolute_size(px * Pango.SCALE);
    return f;
}
const context = PangoCairo.FontMap.get_default().create_context();
const measure = pangoMeasure(context, font('Sans Bold', 11), font('Sans', 10));
const content = b => B.bubbleContent(b, measure);
const waiting = (label, message, others) => ({kind: 'agentWaiting', label, message, others});
const done = (label, message) => ({kind: 'agentDone', label, message});

test('agent bubbles use defaults without a message', () => {
    // FloatingPetAgentTests.swift:144-150
    assert.deepEqual(content(waiting(null, null, 0)),
        {title: 'Your agent needs you', body: "It's waiting for your input.", isCritical: false});
    assert.deepEqual(content(done(null, 'Tests pass')), {title: 'Done!', body: 'Tests pass', isCritical: false});
    assert.equal(content(done(null, null)).body, 'Your agent finished its task.');
});

test('labeled titles name the agent and count the rest', () => {
    // FloatingPetAgentTests.swift:152-157
    assert.equal(content(waiting('web', null, 0)).title, 'web · waiting');
    assert.equal(content(waiting('web', null, 2)).title, 'web · waiting +2');
    assert.equal(content(done('api', null)).title, 'api · done');
});

test('titles are cut at a word or keep the status and count (properties)', () => {
    // FloatingPetAgentTests.swift:158-178
    const unlabeled = content(waiting(null, null, 1)).title;
    assert.ok(measure(unlabeled, '').titleWidth <= BUBBLE_CONTENT_WIDTH, unlabeled);
    assert.ok(unlabeled.endsWith(' +1'), unlabeled);
    const kept = unlabeled.slice(0, -' +1'.length);
    if (kept !== 'Your agent needs you') {
        assert.ok(kept.endsWith('…'), unlabeled);
        assert.ok('Your agent needs you'.startsWith(`${kept.slice(0, -1)} `), `cut between words: ${unlabeled}`);
    }
    const sentence = 'Your coding agent is waiting for an answer from you';
    const cut = B.agentTitle(measure, null, sentence, '', '+3');
    assert.ok(cut.endsWith('… +3'), cut);
    assert.ok(sentence.startsWith(`${cut.slice(0, -'… +3'.length)} `), `cut between words: ${cut}`);
    const wide = 'W'.repeat(A.MAX_LABEL_LENGTH);
    const long = content(waiting(wide, null, 7)).title;
    assert.ok(long !== `${wide} · waiting +7`, 'precondition: the full title does not fit');
    assert.ok(long.startsWith('WWW') && long.endsWith('… · waiting +7'), long);
});

test('agent text never truncates and titles stay on one line (properties)', () => {
    // FloatingPetAgentTests.swift:183-220, English only (no translations yet)
    const messages = [
        null,
        'Tests pass',
        A.sanitizedMessage('M'.repeat(200)),
        A.sanitizedMessage('あ'.repeat(200)),
        A.sanitizedMessage('Supercalifragilistic '.repeat(10)),
        'W'.repeat(A.MAX_MESSAGE_LENGTH),
        A.sanitizedMessage(`a${'́'.repeat(2000)}`.repeat(79)),
    ];
    const labels = [null, 'my-web-app', A.sanitizedLabel('W'.repeat(60)), A.sanitizedLabel('가나다라'.repeat(10))];
    for (const message of messages) {
        for (const label of labels) {
            for (const b of [waiting(label, message, 0), waiting(label, message, A.MAX_AGENTS - 1),
                done(label, message)]) {
                const c = content(b);
                const m = measure(c.title, c.body);
                assert.ok(m.bodyLines <= 2, `body wraps past 2 lines: ${c.body}`);
                assert.ok(m.titleWidth <= BUBBLE_CONTENT_WIDTH, `title wraps: ${c.title}`);
            }
        }
    }
});

test('fitting keeps short text and cuts long text maximally (properties)', () => {
    // FloatingPetAgentTests.swift:222-233
    assert.equal(B.fittedBubbleBody(measure, 'Tests pass', 'Done!'), 'Tests pass');
    const long = 'W'.repeat(80);
    assert.ok(B.wouldTruncate(measure, 'Done!', long), 'precondition');
    const fitted = B.fittedBubbleBody(measure, long, 'Done!');
    assert.ok(fitted.endsWith('…'));
    assert.ok(long.startsWith(fitted.slice(0, -1)));
    assert.ok(!B.wouldTruncate(measure, 'Done!', fitted));
    assert.ok(B.wouldTruncate(measure, 'Done!', `${long.slice(0, fitted.length)}…`),
        'one more character must not fit');
});

test('limit bubble reuses the notification text', () => {
    // FloatingPetAgentTests.swift:130-142 (content unchanged; one account, no suffix)
    const crit = {kind: 'limitAlert', windowKind: 'sevenDay', label: null, tier: 'crit', utilization: 96};
    assert.deepEqual(content({kind: 'limit', alert: crit}),
        {title: 'Limit imminent', body: 'Claude weekly at 96%', isCritical: true});
    const warn = {...crit, tier: 'warn', utilization: 83};
    assert.deepEqual(content({kind: 'limit', alert: warn}),
        {title: 'Limit warning', body: 'Claude weekly at 83%', isCritical: false});
});

run('bubbleText.test.js');
