// gjs -m extension/test/helperClient.test.js
//
// Drives lib/helperClient.js against the protocol stub (test/stub-helper.py)
// through a real Gio.Subprocess and a GLib main loop: handshake, ids and
// result routing, queued writes before hello, timeouts, crash restart,
// protocol mismatch, and stop() closing stdin.

import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import System from 'system';

import {HelperClient} from '../lib/helperClient.js';

const testDir = Gio.File.new_for_uri(import.meta.url).get_parent();
const stub = testDir.get_child('stub-helper.py').get_path();
const tmp = GLib.dir_make_tmp('ptb-client-XXXXXX');
GLib.mkdir_with_parents(`${tmp}/bin`, 0o755);
Gio.File.new_for_path(`${tmp}/bin/ptb-helper`).make_symbolic_link(stub, null);
GLib.setenv('PTB_STATE_DIR', `${tmp}/state`, true);
GLib.setenv('PTB_STUB_EVENTS_AFTER', '0.2', true);

const loop = new GLib.MainLoop(null, false);
let failed = 0;
let passed = 0;

function sleep(ms) {
    return new Promise(resolve => GLib.timeout_add(GLib.PRIORITY_DEFAULT, ms, () => {
        resolve();
        return GLib.SOURCE_REMOVE;
    }));
}

async function waitFor(pred, what, timeoutMs = 8000) {
    const t0 = Date.now();
    while (!pred()) {
        if (Date.now() - t0 > timeoutMs)
            throw new Error(`timed out waiting for ${what}`);
        await sleep(20);
    }
}

function check(cond, message) {
    if (!cond)
        throw new Error(message);
}

async function rejects(promise, code, message) {
    try {
        await promise;
    } catch (e) {
        check(e.code === code, `${message}: expected ${code}, got ${e.code}`);
        return;
    }
    throw new Error(`${message}: expected a rejection`);
}

function makeClient(log) {
    return new HelperClient(tmp, {
        onHello: msg => log.push(['hello', msg]),
        onState: msg => log.push(['state', msg]),
        onEvent: msg => log.push(['event', msg]),
        onStatus: s => log.push(['status', s]),
    });
}

const tests = [
    ['handshake, results, unknown command, events', async () => {
        const log = [];
        const c = makeClient(log);
        c.start();
        // Requested before hello: queued, then sent after it.
        const early = c.request('ping');
        await waitFor(() => c.status === 'running', 'running');
        check((await early) !== undefined, 'early ping resolves');
        const states = log.filter(([k]) => k === 'state');
        check(states.length >= 1 && states[0][1].meta.fromCache === true, 'first state is fromCache');
        check(log.findIndex(([k]) => k === 'hello') < log.findIndex(([k]) => k === 'state'), 'hello first');
        const applied = await c.request('setSettings', {refreshInterval: 60});
        check(applied.refreshInterval === 60, 'setSettings echoes the applied set');
        await rejects(c.request('frobnicate'), 'unknownCommand', 'unknown command');
        await c.request('refresh', {manual: true});
        await waitFor(() => log.some(([k, m]) => k === 'event' && m.kind === 'hatch'), 'hatch event');
        c.stop();
        check(c.status === 'stopped', 'stopped');
        await rejects(c.request('ping'), 'helperExited', 'request after stop');
    }],
    ['timeout fails one command, others still route', async () => {
        const c = makeClient([]);
        c.start();
        await waitFor(() => c.status === 'running', 'running');
        const silent = c.request('stubSilent', undefined, {timeoutMs: 300});
        const ping = c.request('ping');
        await rejects(silent, 'timeout', 'silent command');
        await ping;
        c.stop();
    }],
    ['crash: pending commands fail, restart after 1 s', async () => {
        const log = [];
        const c = makeClient(log);
        c.start();
        await waitFor(() => c.status === 'running', 'running');
        const pending = c.request('stubSilent');
        c.send('stubExit');
        await rejects(pending, 'helperExited', 'pending at crash');
        await waitFor(() => log.some(([k, s]) => k === 'status' && s === 'down'), 'down');
        await waitFor(() => c.status === 'running', 'running again', 5000);
        await c.request('ping');
        c.stop();
    }],
    ['protocol mismatch: never restarted', async () => {
        GLib.setenv('PTB_STUB_PROTOCOL', '2', true);
        const log = [];
        const c = makeClient(log);
        c.start();
        await waitFor(() => c.status === 'mismatch', 'mismatch');
        await rejects(c.request('ping'), 'helperExited', 'no commands after a mismatch');
        await sleep(2500);
        check(c.status === 'mismatch', 'still mismatch after 2.5 s');
        check(!log.some(([k]) => k === 'state'), 'no state accepted');
        check(log.filter(([k, s]) => k === 'status' && s === 'starting').length === 1, 'spawned once');
        c.stop();
        GLib.unsetenv('PTB_STUB_PROTOCOL');
    }],
];

(async () => {
    for (const [name, fn] of tests) {
        try {
            await fn();
            passed++;
            print(`  ok   ${name}`);
        } catch (e) {
            failed++;
            print(`  FAIL ${name}\n       ${e.message}`);
        }
    }
    await sleep(500); // let the stopped helpers exit
})().finally(() => loop.quit());

loop.run();
GLib.spawn_command_line_sync(`rm -rf ${GLib.shell_quote(tmp)}`);
print(`helperClient.test.js: ${passed}/${tests.length} passed`);
if (failed > 0)
    System.exit(1);
