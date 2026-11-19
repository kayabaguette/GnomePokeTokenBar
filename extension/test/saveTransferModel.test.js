// gjs -m extension/test/saveTransferModel.test.js
import {assert, run, test} from './harness.js';
import * as M from '../lib/saveTransferModel.js';

const UTC = 'UTC';
const INSPECT = {
    digest: '9f2c4a7e1b6d3085a4c91e7d02f8b3a65c1d7e94f0a2b8c6d3e5174a9b0c8f21',
    schema: 2,
    appVersion: '2.5.7',
    exportedAt: '2026-11-17T21:04:12Z',
    sourceDevice: "Jane's MacBook Pro",
    incoming: {dexCount: 41, lifetimeTokens: 52_800_000_000},
    current: {dexCount: 5, lifetimeTokens: 8_640_000_000},
};

/* ---- export file name (STT:757-763) ---- */

test('export name: PokeTokenBar-Save-<local day>.json', () => {
    const name = M.exportFileName(1_785_758_400_000, UTC); // 2026-08-03T12:00:00Z
    assert.ok(name.startsWith('PokeTokenBar-Save-'), name);
    assert.ok(name.endsWith('.json'), name);
    assert.ok(name.includes('2026-08-03'), name);
    assert.equal(name, 'PokeTokenBar-Save-2026-08-03.json');
});

test('export name: the local day, not the UTC one, across midnight', () => {
    const late = Date.parse('2026-08-03T23:30:00Z');
    assert.equal(M.exportFileName(late, 'UTC'), 'PokeTokenBar-Save-2026-08-03.json');
    assert.equal(M.exportFileName(late, 'Asia/Tokyo'), 'PokeTokenBar-Save-2026-08-04.json');
    assert.equal(M.exportFileName(Date.parse('2026-01-01T03:00:00Z'), 'America/New_York'),
        'PokeTokenBar-Save-2025-12-31.json');
});

/* ---- error codes → messages (STT:740-755) ---- */

const CODES = ['notASaveFile', 'newerSchema', 'fileTooLarge', 'fileChanged', 'backupFailed', 'notFound',
    'fileNotFound', 'permissionDenied', 'noSpace', 'ioError', 'badArgs', 'internal', 'timeout', 'helperExited',
    'unavailable', 'outdated', 'notLocal', 'somethingNew', undefined];

test('errors: every code maps to a sentence, never the raw code or Cocoa text', () => {
    for (const code of CODES) {
        const msg = M.errorMessage(code);
        assert.ok(typeof msg === 'string' && msg.length > 10, `${code}`);
        if (code)
            assert.ok(!msg.includes(code), `${code} leaks into "${msg}"`);
        assert.ok(!/couldn.t be completed/i.test(msg), `${code}: "${msg}"`);
    }
});

test('errors: the exact upstream English, with the port wording', () => {
    assert.equal(M.errorMessage('notASaveFile'), "That isn't a PokeTokenBar save file.");
    assert.equal(M.errorMessage('newerSchema'),
        'This save was made by a newer version — update the extension and try again.');
    assert.equal(M.errorMessage('fileTooLarge'), 'That file is too large to be a save — it looks like the wrong file.');
    assert.equal(M.errorMessage('backupFailed'),
        "Import stopped because the current state couldn't be backed up — your progress is untouched. Check free disk space.");
    assert.equal(M.errorMessage('permissionDenied'), 'File access was denied. Check permissions or choose another location.');
    assert.equal(M.errorMessage('noSpace'), 'There is not enough disk space. Free up space and try again.');
    assert.equal(M.errorMessage('fileNotFound'), 'The file could not be found. Check its location and select it again.');
    assert.equal(M.errorMessage('notLocal'), 'Choose a local file.');
    assert.equal(M.errorMessage('unavailable'), 'Turn on PokeTokenBar to back up or transfer your save.');
});

test('errors: distinct causes get distinct messages; the rest share the generic one', () => {
    const specific = ['notASaveFile', 'newerSchema', 'fileTooLarge', 'fileChanged', 'backupFailed', 'notFound',
        'fileNotFound', 'permissionDenied', 'noSpace', 'unavailable', 'outdated', 'notLocal'];
    assert.equal(new Set(specific.map(M.errorMessage)).size, specific.length);
    const generic = M.errorMessage('internal');
    assert.equal(generic, 'The operation could not be completed. Please try again.');
    for (const code of ['ioError', 'badArgs', 'timeout', 'helperExited', 'somethingNew'])
        assert.equal(M.errorMessage(code), generic, code);
});

/* ---- D-Bus error names → codes (protocol §15.1) ---- */

test('remote errors: Save1.Error.<code> gives the code', () => {
    assert.equal(M.codeFromRemoteError('io.github.poketokenbar.Save1.Error.notASaveFile'), 'notASaveFile');
    assert.equal(M.codeFromRemoteError('io.github.poketokenbar.Save1.Error.timeout'), 'timeout');
    assert.equal(M.codeFromRemoteError('io.github.poketokenbar.Save1.Error.helperExited'), 'helperExited');
    assert.equal(M.codeFromRemoteError('io.github.poketokenbar.Save1.Error.'), 'internal');
    assert.equal(M.codeFromRemoteError('io.github.poketokenbar.Save1.Error.a.b'), 'internal');
});

test('remote errors: no owner, an older extension, timeouts, the rest', () => {
    assert.equal(M.codeFromRemoteError('org.freedesktop.DBus.Error.ServiceUnknown'), 'unavailable');
    assert.equal(M.codeFromRemoteError('org.freedesktop.DBus.Error.NameHasNoOwner'), 'unavailable');
    assert.equal(M.codeFromRemoteError('org.freedesktop.DBus.Error.UnknownObject'), 'outdated');
    assert.equal(M.codeFromRemoteError('org.freedesktop.DBus.Error.UnknownMethod'), 'outdated');
    assert.equal(M.codeFromRemoteError('org.freedesktop.DBus.Error.UnknownInterface'), 'outdated');
    assert.equal(M.codeFromRemoteError('org.freedesktop.DBus.Error.NoReply'), 'timeout');
    assert.equal(M.codeFromRemoteError('org.freedesktop.DBus.Error.AccessDenied'), 'internal');
    assert.equal(M.codeFromRemoteError(null), 'internal');
    assert.equal(M.codeFromRemoteError(''), 'internal');
});

/* ---- confirmations (STT:687-691, LOC:777-799, LOC:911-936) ---- */

test('import confirm: both summaries, date and device, computer wording', () => {
    const c = M.importConfirm(INSPECT, 'en-US', UTC);
    assert.equal(c.heading, "Replace this computer's progress?");
    assert.equal(c.body, [
        'Incoming save: 41 in Pokédex · 52.8B lifetime',
        `Exported: ${M.dateText('2026-11-17T21:04:12Z', 'en-US', UTC)} · Jane's MacBook Pro`,
        'This computer now: 5 in Pokédex · 8.64B lifetime',
        '',
        "This computer's current progress is replaced. The previous state is kept as a backup in " +
            '~/.local/state/poketokenbar (last 5).',
    ].join('\n'));
    assert.ok(!c.body.includes('Mac now') && !c.body.includes('state folder'));
    assert.equal(c.confirmId, 'replace');
    assert.equal(c.confirmLabel, 'Replace');
});

test('confirm policy: Cancel is the default and the close response, never the destructive one', () => {
    for (const c of [M.importConfirm(INSPECT), M.restoreConfirm({id: 'x', date: '2026-11-18T05:20:15Z'})]) {
        assert.equal(c.defaultResponse, 'cancel');
        assert.equal(c.closeResponse, 'cancel');
        assert.ok(c.confirmId !== c.defaultResponse && c.confirmId !== c.closeResponse);
    }
});

test('restore confirm: the snapshot summary and date, then the safety-snapshot sentence', () => {
    const snap = {id: 'companion-snapshot-2026-11-17-201533.json', date: '2026-11-17T19:15:33Z', dexCount: 5,
        lifetimeTokens: 8_402_000_000, speciesId: 423, shiny: true, form: 'east'};
    const c = M.restoreConfirm(snap, 'en-US', UTC);
    assert.equal(c.heading, 'Restore this snapshot?');
    assert.equal(c.body, [
        'Target snapshot: 5 in Pokédex · 8.4B lifetime',
        `Created: ${M.dateText(snap.date, 'en-US', UTC)}`,
        '',
        'Your current state will be backed up as a new snapshot before restoring.',
    ].join('\n'));
    assert.equal(c.confirmId, 'restore');
});

test('confirm: missing fields never print undefined', () => {
    for (const c of [M.importConfirm({}), M.restoreConfirm({}), M.importConfirm(null), M.restoreConfirm(null)])
        assert.ok(!c.body.includes('undefined') && !c.body.includes('NaN') && !c.body.includes('Invalid'), c.body);
});

/* ---- results ---- */

test('done texts: import, restore, export', () => {
    assert.equal(M.importDoneText({summary: {dexCount: 41, lifetimeTokens: 52_800_000_000}}),
        'Imported — 41 in Pokédex · 52.8B lifetime');
    assert.equal(M.restoreDoneText({summary: {dexCount: 3, lifetimeTokens: 3_120_000_000}}),
        'Restored — 3 in Pokédex · 3.12B lifetime');
    assert.equal(M.exportDoneText({path: '/home/u/Saves/PokeTokenBar-Save-2026-11-18.json'}, '/x'),
        'Exported to PokeTokenBar-Save-2026-11-18.json');
    assert.equal(M.exportDoneText({}, '/tmp/a.json'), 'Exported to a.json');
});

/* ---- snapshot rows ---- */

test('snapshot row: date title, Pokédex/lifetime subtitle', () => {
    const row = M.snapshotRow({id: 'companion-snapshot-2026-11-18-062015.json', date: '2026-11-18T05:20:15Z',
        dexCount: 5, lifetimeTokens: 8_640_000_000}, 'en-US', UTC);
    assert.equal(row.id, 'companion-snapshot-2026-11-18-062015.json');
    // ICU puts a narrow no-break space before AM/PM.
    assert.equal(row.title.replace(/\s/g, ' '), 'Nov 18, 2026, 5:20 AM');
    assert.equal(row.subtitle, 'Pokédex 5 · 8.64B lifetime');
});

test('snapshot date: an unparsable stamp is shown as sent', () => {
    assert.equal(M.dateText('not a date'), 'not a date');
    assert.equal(M.dateText(undefined), '');
});

test('snapshot list: well-formed entries, at most 10, order kept', () => {
    const ten = Array.from({length: 12}, (_, i) => ({id: `companion-snapshot-${i}.json`, date: '2026-11-18T05:20:15Z'}));
    assert.equal(M.snapshotList({snapshots: ten}).length, 10);
    assert.equal(M.snapshotList({snapshots: ten})[0].id, 'companion-snapshot-0.json');
    assert.deepEqual(M.snapshotList({snapshots: [null, {}, {id: ''}, {id: 'a'}]}).map(s => s.id), ['a']);
    assert.deepEqual(M.snapshotList(null), []);
    assert.deepEqual(M.snapshotList({snapshots: 'x'}), []);
});

test('expander subtitle: the newest date, or the empty text', () => {
    assert.equal(M.snapshotsSubtitle([]), 'No snapshots saved yet');
    assert.equal(M.snapshotsSubtitle(null), 'No snapshots saved yet');
    const s = M.snapshotsSubtitle([{date: '2026-11-18T05:20:15Z'}, {date: '2026-11-10T08:32:01Z'}], 'en-US', UTC);
    assert.equal(s, `Latest: ${M.dateText('2026-11-18T05:20:15Z', 'en-US', UTC)}`);
});

test('relay names match protocol §15.1', () => {
    assert.equal(M.SAVE_BUS_NAME, 'io.github.poketokenbar.Agent');
    assert.equal(M.SAVE_OBJECT_PATH, '/io/github/poketokenbar/Save');
    assert.equal(M.SAVE_INTERFACE, 'io.github.poketokenbar.Save1');
    assert.equal(M.SAVE_ERROR_PREFIX, 'io.github.poketokenbar.Save1.Error.');
});

run('saveTransferModel.test.js');
