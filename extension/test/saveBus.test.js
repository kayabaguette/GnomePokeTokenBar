// gjs -m extension/test/saveBus.test.js
// The pure parts of the Save1 relay. Importing saveBus.js loads Gio but opens
// no bus: only `new SaveBus()` touches Gio.DBus.session, and no test builds one.
import {assert, run, test} from './harness.js';
import {INTERFACE_XML, SAVE_METHODS, SaveCallError, buildCommand, errorName, replyText} from '../lib/saveBus.js';
import {codeFromRemoteError} from '../lib/saveTransferModel.js';

const DIGEST = '9f2c4a7e1b6d3085a4c91e7d02f8b3a65c1d7e94f0a2b8c6d3e5174a9b0c8f21';

function rejects(fn, code, label) {
    try {
        fn();
    } catch (e) {
        assert.ok(e instanceof SaveCallError, `${label}: ${e}`);
        assert.equal(e.code, code, label);
        return;
    }
    assert.ok(false, `${label}: expected ${code}`);
}

test('whitelist: exactly the six methods of protocol §15.1, each to its own command', () => {
    assert.deepEqual(Object.keys(SAVE_METHODS).sort(),
        ['CreateSnapshot', 'ExportSave', 'ImportSave', 'InspectSave', 'ListSnapshots', 'RestoreSnapshot']);
    assert.deepEqual(Object.values(SAVE_METHODS).map(m => m.cmd).sort(),
        ['createSnapshot', 'exportSave', 'importSave', 'inspectSave', 'listSnapshots', 'restoreSnapshot']);
    const declared = [...INTERFACE_XML.matchAll(/<method name="(\w+)">/g)].map(m => m[1]).sort();
    assert.deepEqual(declared, Object.keys(SAVE_METHODS).sort(), 'the XML declares the same set');
    assert.ok(Object.isFrozen(SAVE_METHODS));
});

test('whitelist: anything else is refused, prototype names included', () => {
    for (const m of ['BuyItem', 'buyItem', 'exportSave', 'Refresh', 'toString', 'constructor', '__proto__', 'hasOwnProperty', ''])
        rejects(() => buildCommand(m, ['/tmp/x.json']), 'unknownMethod', m);
});

test('forwarding: paths and ids only, as the helper args', () => {
    assert.deepEqual(buildCommand('ExportSave', ['/home/u/a.json']), {cmd: 'exportSave', args: {path: '/home/u/a.json'}});
    assert.deepEqual(buildCommand('InspectSave', ['/home/u/a.json']), {cmd: 'inspectSave', args: {path: '/home/u/a.json'}});
    assert.deepEqual(buildCommand('ImportSave', ['/home/u/a.json', DIGEST]),
        {cmd: 'importSave', args: {path: '/home/u/a.json', digest: DIGEST}});
    assert.deepEqual(buildCommand('RestoreSnapshot', ['companion-snapshot-2026-11-18-062015-1.json']),
        {cmd: 'restoreSnapshot', args: {id: 'companion-snapshot-2026-11-18-062015-1.json'}});
    assert.deepEqual(buildCommand('ListSnapshots', []), {cmd: 'listSnapshots', args: undefined});
    assert.deepEqual(buildCommand('CreateSnapshot', []), {cmd: 'createSnapshot', args: undefined});
});

test('paths: absolute only; relative, empty, NUL, huge or non-string are badArgs', () => {
    for (const method of ['ExportSave', 'InspectSave']) {
        for (const bad of ['relative.json', './a.json', '~/a.json', '', 'a\0b', `/${'a'.repeat(5000)}`, 42, null, undefined])
            rejects(() => buildCommand(method, [bad]), 'badArgs', `${method}(${JSON.stringify(bad)})`);
    }
    rejects(() => buildCommand('ImportSave', ['rel.json', DIGEST]), 'badArgs', 'ImportSave relative');
    rejects(() => buildCommand('ExportSave', []), 'badArgs', 'missing path');
});

test('digest: 64 lowercase hex, else badArgs', () => {
    for (const bad of ['', DIGEST.toUpperCase(), DIGEST.slice(1), `${DIGEST}0`, `${DIGEST.slice(1)}g`, null])
        rejects(() => buildCommand('ImportSave', ['/a.json', bad]), 'badArgs', `digest ${JSON.stringify(bad)}`);
});

test('snapshot id: the file-name pattern only, so no path can be smuggled in', () => {
    for (const bad of ['../companion-state.json', 'companion-snapshot-../../x.json', '/etc/passwd',
        'companion-snapshot-2026.json/..', 'companion-snapshot-.json.bak', 'companion-snapshot-1.JSON', '', 7])
        rejects(() => buildCommand('RestoreSnapshot', [bad]), 'badArgs', `id ${JSON.stringify(bad)}`);
    // '-' alone matches the helper's regex too; the helper answers notFound.
    assert.equal(buildCommand('RestoreSnapshot', ['companion-snapshot--.json']).args.id, 'companion-snapshot--.json');
});

test('error names: a valid D-Bus name for every code, internal for a malformed one', () => {
    for (const code of ['notASaveFile', 'newerSchema', 'fileTooLarge', 'fileChanged', 'backupFailed', 'notFound',
        'fileNotFound', 'permissionDenied', 'noSpace', 'ioError', 'badArgs', 'internal', 'timeout', 'helperExited',
        'unknownMethod']) {
        const name = errorName(code);
        assert.equal(name, `io.github.poketokenbar.Save1.Error.${code}`);
        assert.equal(codeFromRemoteError(name), code, 'round trip through the window');
    }
    for (const bad of ['', 'a.b', 'has space', '9lives', 'x-y', null, undefined, 42])
        assert.equal(errorName(bad), 'io.github.poketokenbar.Save1.Error.internal', JSON.stringify(bad));
});

test('reply: the data object as JSON, {} for none', () => {
    assert.equal(replyText({summary: {dexCount: 1, lifetimeTokens: 2}}), '{"summary":{"dexCount":1,"lifetimeTokens":2}}');
    assert.equal(replyText(undefined), '{}');
    assert.equal(replyText(null), '{}');
});

run('saveBus.test.js');
