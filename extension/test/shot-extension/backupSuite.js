/* Backup & Transfer suite for the sandbox driver.
 *
 * PTB_SHOT_SUITE=backup  exercises the `Save1` relay (protocol §15.1) on the
 *   sandbox's PRIVATE session bus (dbus-run-session inside
 *   scripts/headless-shell.sh), against the stub helper:
 *   - the six methods on the happy path;
 *   - every error the stub injects by file name, and the relay's own
 *     argument checks (badArgs before anything reaches the helper);
 *   - a method outside the whitelist (UnknownMethod);
 *   - the same calls through `busctl --user`, spawned with this process's
 *     environment, so it reaches the sandbox bus too;
 *   - a call left pending in the helper at disable is answered helperExited;
 *   - disable → the name has no owner (NameHasNoOwner); enable → served again.
 *   It refuses to run unless XDG_RUNTIME_DIR is the sandbox's own.
 *   Each check logs `ptb-shot: backup: PASS …` or a console.error FAIL.
 *
 * PTB_SHOT_SUITE=backup-prefs with --prefs poketokenbar@local screenshots the
 *   preferences window's Backup & Transfer page: the page, the expanded
 *   snapshot list, the restore confirmation, the import confirmation (through
 *   the file chooser when one opens) and the banner while the extension is
 *   off. Clicks are window-relative coordinates read from the first shot:
 *   PTB_SHOT_BACKUP_TAB, PTB_SHOT_BACKUP_EXPAND, PTB_SHOT_BACKUP_RESTORE,
 *   PTB_SHOT_BACKUP_CANCEL, PTB_SHOT_BACKUP_CONFIRM, PTB_SHOT_BACKUP_IMPORT,
 *   PTB_SHOT_BACKUP_CREATE, PTB_SHOT_BACKUP_EXPORT
 *   ("x,y" each), plus PTB_SHOT_BACKUP_FILE and PTB_SHOT_BACKUP_OPEN in screen
 *   coordinates for the file chooser. Values used on 2026-11-19 (640x576
 *   window at 320,128): TAB 410,22 EXPAND 320,391 RESTORE 540,447
 *   CANCEL 235,371 CONFIRM 403,371 IMPORT 522,211 FILE 475,258 OPEN 1021,656
 *   CREATE 530,336 EXPORT 522,143. The chooser is the sandbox's own portal
 *   (Nautilus) and lists the real $HOME, so it is never screenshotted.
 */

import Clutter from 'gi://Clutter';
import Gio from 'gi://Gio';
import GLib from 'gi://GLib';

import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import {Extension} from 'resource:///org/gnome/shell/extensions/extension.js';

Gio._promisify(Gio.DBusConnection.prototype, 'call');
Gio._promisify(Gio.Subprocess.prototype, 'communicate_utf8_async');

const TARGET = 'poketokenbar@local';
const BUS = 'io.github.poketokenbar.Agent';
const PATH = '/io/github/poketokenbar/Save';
const IFACE = 'io.github.poketokenbar.Save1';
const ERR = `${IFACE}.Error.`;
const FIXTURE_DIGEST = '9f2c4a7e1b6d3085a4c91e7d02f8b3a65c1d7e94f0a2b8c6d3e5174a9b0c8f21';

let failures = 0;
/* Disabling the target makes the extension manager disable and re-enable every
 * extension enabled after it, this driver included: the run in progress is
 * cancelled and a new one starts. Module state survives that, so the new run
 * resumes after the disable instead of starting over. */
let stage = 'start';
/** A Save1 call left pending in the helper across the disable (resumeBackup awaits it). */
let pendingAtDisable = null;

function log(msg) {
    console.log(`ptb-shot: backup: ${msg}`);
}

function check(ok, label, detail = '') {
    if (ok) {
        log(`PASS ${label}${detail ? ` (${detail})` : ''}`);
    } else {
        failures++;
        console.error(`ptb-shot: backup: FAIL ${label}${detail ? ` (${detail})` : ''}`);
    }
}

/** A Save1 call on the sandbox bus: {ok, data} or {ok: false, name, message}. */
async function call(method, args = [], {path = PATH, iface = IFACE} = {}) {
    try {
        const reply = await Gio.DBus.session.call(BUS, path, iface, method,
            args.length ? new GLib.Variant(`(${'s'.repeat(args.length)})`, args) : null,
            null, Gio.DBusCallFlags.NO_AUTO_START, 5000, null);
        const [text] = reply.deepUnpack();
        return {ok: true, data: JSON.parse(text)};
    } catch (e) {
        const name = Gio.DBusError.is_remote_error(e) ? Gio.DBusError.get_remote_error(e) : null;
        return {ok: false, name, message: e.message};
    }
}

async function expectError(method, args, expected, label) {
    const r = await call(method, args);
    check(!r.ok && r.name === expected, label, r.ok ? `got ok ${JSON.stringify(r.data)}` : r.name);
}

async function busctl(...args) {
    const proc = Gio.Subprocess.new(['busctl', '--user', ...args],
        Gio.SubprocessFlags.STDOUT_PIPE | Gio.SubprocessFlags.STDERR_PIPE);
    const [out, err] = await proc.communicate_utf8_async(null, null);
    return {status: proc.get_exit_status(), out: (out ?? '').trim(), err: (err ?? '').trim()};
}

function sandboxed() {
    const runtime = GLib.getenv('XDG_RUNTIME_DIR') ?? '';
    const bus = GLib.getenv('DBUS_SESSION_BUS_ADDRESS') ?? '';
    log(`XDG_RUNTIME_DIR=${runtime} DBUS_SESSION_BUS_ADDRESS=${bus}`);
    return runtime.endsWith('/.sandbox/run') && !runtime.startsWith('/run/user/');
}

export async function runBackup(d) {
    if (!sandboxed()) {
        console.error('ptb-shot: backup: not in the sandbox runtime dir; refusing to call D-Bus');
        return;
    }
    if (stage === 'disabled')
        return resumeBackup(d);
    await d._sleep(5000); // helper hello and first state

    // 1. The six methods.
    let r = await call('ListSnapshots');
    check(r.ok && r.data.snapshots?.length === 3, 'ListSnapshots: the 3 fixture snapshots',
        r.ok ? r.data.snapshots.map(s => s.id).join(' ') : r.name);
    r = await call('CreateSnapshot');
    check(r.ok && /^companion-snapshot-[0-9-]+\.json$/.test(r.data.snapshot?.id ?? ''), 'CreateSnapshot',
        r.ok ? r.data.snapshot.id : r.name);
    r = await call('ListSnapshots');
    check(r.ok && r.data.snapshots.length === 4, 'ListSnapshots after CreateSnapshot: 4', r.ok ? '' : r.name);
    const oldest = r.ok ? r.data.snapshots.at(-1) : null;
    r = await call('ExportSave', ['/tmp/ptb-backup/PokeTokenBar-Save-2026-11-19.json']);
    check(r.ok && r.data.path === '/tmp/ptb-backup/PokeTokenBar-Save-2026-11-19.json' && r.data.summary,
        'ExportSave echoes the path with a summary', JSON.stringify(r.ok ? r.data : r.name));
    r = await call('InspectSave', ['/tmp/ptb-backup/PokeTokenBar-Save-2026-11-17.json']);
    check(r.ok && r.data.digest === FIXTURE_DIGEST && r.data.incoming?.dexCount === 41 && r.data.current,
        'InspectSave: digest, incoming and current', r.ok ? r.data.sourceDevice : r.name);
    r = await call('ImportSave', ['/tmp/ptb-backup/PokeTokenBar-Save-2026-11-17.json', FIXTURE_DIGEST]);
    check(r.ok && r.data.summary?.dexCount === 41, 'ImportSave with the digest', JSON.stringify(r.ok ? r.data : r.name));
    if (oldest) {
        r = await call('RestoreSnapshot', [oldest.id]);
        check(r.ok && r.data.summary?.dexCount === oldest.dexCount, `RestoreSnapshot ${oldest.id}`,
            JSON.stringify(r.ok ? r.data : r.name));
    }

    // 2. Errors from the helper, passed through as Save1.Error.<code>.
    for (const [file, code] of [['notasave', 'notASaveFile'], ['newer', 'newerSchema'], ['toolarge', 'fileTooLarge'],
        ['missing', 'fileNotFound'], ['denied', 'permissionDenied'], ['nospace', 'noSpace']])
        await expectError('InspectSave', [`/tmp/ptb-backup/${file}.json`], `${ERR}${code}`, `InspectSave ${file} → ${code}`);
    await expectError('ExportSave', ['/tmp/ptb-backup/nospace.json'], `${ERR}noSpace`, 'ExportSave nospace → noSpace');
    await expectError('ImportSave', ['/tmp/ptb-backup/a.json', 'f'.repeat(64)], `${ERR}fileChanged`,
        'ImportSave with another digest → fileChanged');
    await expectError('ImportSave', ['/tmp/ptb-backup/changed.json', FIXTURE_DIGEST], `${ERR}fileChanged`,
        'ImportSave changed file → fileChanged');
    await expectError('RestoreSnapshot', ['companion-snapshot-1999-01-01-000000.json'], `${ERR}notFound`,
        'RestoreSnapshot unknown id → notFound');

    // 3. The relay's own checks: badArgs before the helper.
    await expectError('ExportSave', ['relative.json'], `${ERR}badArgs`, 'ExportSave relative path → badArgs');
    await expectError('InspectSave', [''], `${ERR}badArgs`, 'InspectSave empty path → badArgs');
    await expectError('ImportSave', ['/tmp/a.json', 'abc'], `${ERR}badArgs`, 'ImportSave short digest → badArgs');
    await expectError('RestoreSnapshot', ['../companion-state.json'], `${ERR}badArgs`, 'RestoreSnapshot ../ → badArgs');

    // 4. Not on the whitelist.
    r = await call('BuyItem', ['rareCandy']);
    check(!r.ok && r.name === 'org.freedesktop.DBus.Error.UnknownMethod', 'BuyItem on Save1 → UnknownMethod', r.name);
    r = await call('ListSnapshots', [], {path: '/io/github/poketokenbar/Agent'});
    check(!r.ok && /Unknown(Method|Interface|Object)/.test(r.name ?? ''), 'Save1 on the Agent path → refused', r.name);
    r = await call('ExportSave', ['/tmp/x.json', 'extra']);
    check(!r.ok && r.name === 'org.freedesktop.DBus.Error.InvalidArgs', 'ExportSave with a wrong signature → InvalidArgs',
        r.name);

    // 5. busctl on the sandbox bus.
    let b = await busctl('call', BUS, PATH, IFACE, 'ListSnapshots');
    check(b.status === 0 && b.out.startsWith('s "{\\"snapshots\\":'), 'busctl ListSnapshots', b.out.slice(0, 90) || b.err);
    b = await busctl('call', BUS, PATH, IFACE, 'InspectSave', 's', '/tmp/ptb-backup/notasave.json');
    check(b.status !== 0 && b.err.includes('not a PokeTokenBar save file'), 'busctl InspectSave notasave fails', b.err);
    b = await busctl('call', BUS, PATH, IFACE, 'RestoreSnapshot', 's', 'nope');
    check(b.status !== 0, 'busctl RestoreSnapshot bad id fails', b.err);
    b = await busctl('introspect', BUS, PATH, IFACE);
    const methods = [...b.out.matchAll(/^\.(\w+)\s+method/gm)].map(m => m[1]).sort();
    check(methods.join(',') === 'CreateSnapshot,ExportSave,ImportSave,InspectSave,ListSnapshots,RestoreSnapshot',
        'busctl introspect: exactly six methods', methods.join(','));

    // 6. A call still pending in the helper at disable: the stub's `stubSilent` never answers,
    //    so ListSnapshots is sent as that, and the disable must still answer the caller.
    const client = Extension.lookupByUUID(TARGET)?._client;
    if (client) {
        const request = client.request.bind(client);
        client.request = (cmd, args, options) => (cmd === 'listSnapshots'
            ? request('stubSilent', undefined, {timeoutMs: 60_000})
            : request(cmd, args, options));
        pendingAtDisable = call('ListSnapshots');
        await d._sleep(800);
    } else {
        check(false, 'target helper client found');
    }

    // 7. Disabled: no owner; enabled again: served (resumeBackup, in the restarted run).
    stage = 'disabled';
    Main.extensionManager.disableExtension(TARGET);
}

async function resumeBackup(d) {
    stage = 'resumed';
    log('resumed after the disable');
    let r = await pendingAtDisable;
    check(r && !r.ok && r.name === `${ERR}helperExited`, 'a call pending at disable is answered helperExited', r?.name);
    r = await call('ListSnapshots');
    check(!r.ok && r.name === 'org.freedesktop.DBus.Error.NameHasNoOwner', 'disabled → NameHasNoOwner', r.name);
    const b = await busctl('call', BUS, PATH, IFACE, 'ListSnapshots');
    check(b.status !== 0, 'disabled → busctl fails at once', b.err);
    Main.extensionManager.enableExtension(TARGET);
    await d._sleep(4000);
    r = await call('ListSnapshots');
    check(r.ok && Array.isArray(r.data.snapshots), 'enabled again → ListSnapshots served', r.ok ? `${r.data.snapshots.length}` : r.name);

    log(failures === 0 ? 'ALL PASS' : `${failures} FAILED`);
    log('done');
}

/* ---- preferences window shots --------------------------------------------- */

function coords(name) {
    const v = (GLib.getenv(name) ?? '').split(',').map(Number);
    return v.length === 2 && v.every(Number.isFinite) ? v : null;
}

async function clickAt(d, r, xy) {
    d._movePointer(r.x + xy[0] - 2, r.y + xy[1]);
    await d._sleep(200);
    d._movePointer(r.x + xy[0], r.y + xy[1]);
    await d._sleep(250);
    d._click(Clutter.BUTTON_PRIMARY);
}

/** The virtual keyboard, created early: the first events of a fresh device can be dropped. */
function keyboard(d) {
    if (!d._keyboard) {
        const seat = global.stage.context.get_backend().get_default_seat();
        d._keyboard = seat.create_virtual_device(Clutter.InputDeviceType.KEYBOARD_DEVICE);
    }
    return d._keyboard;
}

function ctrlKey(d, keyval) {
    keyboard(d);
    const t = GLib.get_monotonic_time();
    d._keyboard.notify_keyval(t, Clutter.KEY_Control_L, Clutter.KeyState.PRESSED);
    d._keyboard.notify_keyval(t + 1000, keyval, Clutter.KeyState.PRESSED);
    d._keyboard.notify_keyval(t + 2000, keyval, Clutter.KeyState.RELEASED);
    d._keyboard.notify_keyval(t + 3000, Clutter.KEY_Control_L, Clutter.KeyState.RELEASED);
}

function prefsWindow() {
    return global.get_window_actors().map(a => a.meta_window)
        .find(w => (w.get_title() ?? '').length > 0 && w.get_wm_class() !== 'gnome-shell');
}

/** The top-level window that is not the prefs window and appeared after it (a dialog or a chooser). */
function otherWindows(prefs) {
    return global.get_window_actors().map(a => a.meta_window)
        .filter(w => w !== prefs && w.get_wm_class() !== 'gnome-shell');
}

export async function runBackupPrefs(d) {
    if (!sandboxed()) {
        console.error('ptb-shot: backup: not in the sandbox runtime dir');
        return;
    }
    if (stage === 'disabled')
        return resumeBackupPrefs(d);
    // A save file the chooser can pick (the stub never reads it).
    const savesDir = GLib.build_filenamev([GLib.getenv('PTB_STATE_DIR'), 'saves']);
    GLib.mkdir_with_parents(savesDir, 0o755);
    const savePath = GLib.build_filenamev([savesDir, 'PokeTokenBar-Save-2026-11-17.json']);
    GLib.file_set_contents(savePath, '{"format":"poketokenbar.save","schema":2}\n');

    keyboard(d);
    await d._sleep(9000);
    const prefs = prefsWindow();
    Main.overview.hide();
    await d._sleep(2000);
    if (!prefs) {
        console.error('ptb-shot: no prefs window');
        return;
    }
    prefs.activate(global.get_current_time());
    await d._sleep(1500);
    const r = prefs.get_frame_rect();
    const area = () => {
        const f = prefs.get_frame_rect();
        return {x: f.x, y: f.y, width: f.width, height: f.height};
    };
    d._movePointer(r.x + r.width / 2, r.y + r.height / 2);
    await d._sleep(300);
    d._movePointer(r.x + r.width / 2 + 1, r.y + r.height / 2);
    await d._sleep(300);
    Main.overview.hide();
    await d._sleep(1000);
    log(`prefs ${r.x},${r.y} ${r.width}x${r.height}`);
    await d._shot('backup-0-first-page.png', area());

    const tab = coords('PTB_SHOT_BACKUP_TAB');
    if (!tab) {
        log('no PTB_SHOT_BACKUP_TAB; done');
        return;
    }
    await clickAt(d, r, tab);
    await d._sleep(1500);
    await d._shot('backup-1-page.png', area());

    const expand = coords('PTB_SHOT_BACKUP_EXPAND');
    if (expand) {
        await clickAt(d, r, expand);
        await d._sleep(1200);
        await d._shot('backup-2-snapshots.png', area());
    }

    const restore = coords('PTB_SHOT_BACKUP_RESTORE');
    if (restore) {
        await clickAt(d, r, restore);
        await d._sleep(1500);
        await d._shot('backup-3-restore-confirm.png', area());
        const cancel = coords('PTB_SHOT_BACKUP_CANCEL');
        const confirm = coords('PTB_SHOT_BACKUP_CONFIRM');
        if (cancel) {
            await clickAt(d, r, cancel);
            await d._sleep(1000);
            await d._shot('backup-3b-after-cancel.png', area());
        }
        if (confirm) {
            await clickAt(d, r, restore);
            await d._sleep(1500);
            await clickAt(d, r, confirm);
            await d._sleep(900);
            await d._shot('backup-3c-restored-toast.png', area());
            await d._sleep(5000); // let the toast go
        }
    }

    const imp = coords('PTB_SHOT_BACKUP_IMPORT');
    if (imp) {
        await clickAt(d, r, imp);
        await d._sleep(3000);
        const others = otherWindows(prefs);
        log(`windows after Import…: ${others.map(w => `${w.get_wm_class()}:${w.get_title()}`).join(', ') || 'none'}`);
        if (others.length > 0) {
            // GTK file chooser: Ctrl+L opens the location entry; type the path; Enter picks it.
            ctrlKey(d, Clutter.KEY_l);
            await d._sleep(600);
            await d._type(savePath);
            await d._sleep(400);
            d._key(Clutter.KEY_Return);
            await d._sleep(2500);
            // The portal chooser opens the folder: pick the file, then Open (screen coordinates).
            const file = coords('PTB_SHOT_BACKUP_FILE');
            const open = coords('PTB_SHOT_BACKUP_OPEN');
            if (file && open) {
                await clickAt(d, {x: 0, y: 0}, file);
                await d._sleep(600);
                await clickAt(d, {x: 0, y: 0}, open);
                await d._sleep(2500);
                await d._shot('backup-5-import-confirm.png', area());
            }
            d._key(Clutter.KEY_Escape); // the close response: Cancel
            await d._sleep(1000);
            await d._shot('backup-5b-after-escape.png', area());
        }
    }

    // Create snapshot; then Export opens the save chooser, which is closed unanswered.
    const create = coords('PTB_SHOT_BACKUP_CREATE');
    if (create) {
        await clickAt(d, r, create);
        await d._sleep(900);
        await d._shot('backup-8-created-toast.png', area());
        await d._sleep(5000);
    }
    const exp = coords('PTB_SHOT_BACKUP_EXPORT');
    if (exp) {
        await clickAt(d, r, exp);
        await d._sleep(3000);
        const others = otherWindows(prefs);
        log(`windows after Export…: ${others.map(w => `${w.get_wm_class()}:${w.get_title()}`).join(', ') || 'none'}`);
        // Typing into the save chooser did not reach it (the keys went to the prefs window's search),
        // so the chooser is only opened, checked by its title, and dismissed with Escape.
        const chooser = others.find(w => w.get_title() === 'Export save');
        chooser?.activate(global.get_current_time());
        await d._sleep(800);
        d._key(Clutter.KEY_Escape);
        await d._sleep(1500);
        log(`windows after closing the chooser: ${otherWindows(prefs).length}`);
        await d._shot('backup-9-after-export-dismissed.png', area());
    }

    // The banner: the extension off while the window is open, then on again (resumeBackupPrefs).
    stage = 'disabled';
    Main.extensionManager.disableExtension(TARGET);
}

async function resumeBackupPrefs(d) {
    stage = 'resumed';
    const prefs = prefsWindow();
    const area = () => {
        const f = prefs.get_frame_rect();
        return {x: f.x, y: f.y, width: f.width, height: f.height};
    };
    await d._sleep(2000);
    await d._shot('backup-6-banner.png', area());
    Main.extensionManager.enableExtension(TARGET);
    await d._sleep(4000);
    await d._shot('backup-7-back-on.png', area());
    log('done');
}
