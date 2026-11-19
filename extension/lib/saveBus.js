/* The save relay D-Bus object (protocol §15.1).
 *
 * Exports `io.github.poketokenbar.Save1` at `/io/github/poketokenbar/Save`
 * on the shell's session connection while the extension is enabled. The
 * well-known name `io.github.poketokenbar.Agent` is owned once, by AgentBus,
 * in the same enable(); this object rides on that ownership and is created
 * before it and destroyed with it, so both interfaces appear and vanish
 * together.
 *
 * The preferences window (a separate process) calls it; each method is
 * relayed to the helper command of the same name (protocol §7.7):
 *   - a whitelist: GDBus dispatches only the six methods of the XML below,
 *     and each maps to one fixed command, so no other command can be sent;
 *   - paths and ids only, never file contents;
 *   - asynchronous replies: the command's `data` as a JSON string, or the
 *     D-Bus error `io.github.poketokenbar.Save1.Error.<code>`.
 *
 * The pure parts (command table, argument checks, error names) are exported
 * for the unit tests; nothing here touches a bus until SaveBus is built.
 */

import Gio from 'gi://Gio';
import GLib from 'gi://GLib';

import {SAVE_ERROR_PREFIX, SAVE_OBJECT_PATH} from './saveTransferModel.js';

export const INTERFACE_XML = `
<node>
  <interface name="io.github.poketokenbar.Save1">
    <method name="ExportSave">
      <arg type="s" name="path" direction="in"/>
      <arg type="s" name="reply" direction="out"/>
    </method>
    <method name="InspectSave">
      <arg type="s" name="path" direction="in"/>
      <arg type="s" name="reply" direction="out"/>
    </method>
    <method name="ImportSave">
      <arg type="s" name="path" direction="in"/>
      <arg type="s" name="digest" direction="in"/>
      <arg type="s" name="reply" direction="out"/>
    </method>
    <method name="ListSnapshots">
      <arg type="s" name="reply" direction="out"/>
    </method>
    <method name="CreateSnapshot">
      <arg type="s" name="reply" direction="out"/>
    </method>
    <method name="RestoreSnapshot">
      <arg type="s" name="id" direction="in"/>
      <arg type="s" name="reply" direction="out"/>
    </method>
  </interface>
</node>`;

const LOG = 'PokeTokenBar';
const MAX_PATH_CHARS = 4096; // PATH_MAX
const DIGEST_RE = /^[0-9a-f]{64}$/; // lowercase hex sha256 (protocol §7.7)
const SNAPSHOT_ID_RE = /^companion-snapshot-[0-9-]+\.json$/;
const CODE_RE = /^[A-Za-z][A-Za-z0-9_]*$/;

/** A call rejected before it reaches the helper. */
export class SaveCallError extends Error {
    constructor(code, message) {
        super(message);
        this.name = 'SaveCallError';
        this.code = code;
    }
}

function absolutePath(value) {
    if (typeof value !== 'string' || !value.startsWith('/') || value.length > MAX_PATH_CHARS || value.includes('\0'))
        throw new SaveCallError('badArgs', 'path must be an absolute local file path');
    return value;
}

function digest(value) {
    if (typeof value !== 'string' || !DIGEST_RE.test(value))
        throw new SaveCallError('badArgs', 'digest must be the sha256 that InspectSave returned');
    return value;
}

function snapshotId(value) {
    if (typeof value !== 'string' || !SNAPSHOT_ID_RE.test(value))
        throw new SaveCallError('badArgs', 'id must be a snapshot file name');
    return value;
}

/** Method → helper command and its argument builder. The six entries are the whole surface. */
export const SAVE_METHODS = Object.freeze({
    ExportSave: {cmd: 'exportSave', args: p => ({path: absolutePath(p[0])})},
    InspectSave: {cmd: 'inspectSave', args: p => ({path: absolutePath(p[0])})},
    ImportSave: {cmd: 'importSave', args: p => ({path: absolutePath(p[0]), digest: digest(p[1])})},
    ListSnapshots: {cmd: 'listSnapshots', args: () => undefined},
    CreateSnapshot: {cmd: 'createSnapshot', args: () => undefined},
    RestoreSnapshot: {cmd: 'restoreSnapshot', args: p => ({id: snapshotId(p[0])})},
});

/**
 * The helper command for a D-Bus call; throws SaveCallError (`badArgs`,
 * `unknownMethod`) when it must not be forwarded.
 *
 * @param {string} method
 * @param {Array} params  the unpacked in-arguments
 * @returns {{cmd: string, args: object|undefined}}
 */
export function buildCommand(method, params) {
    if (!Object.hasOwn(SAVE_METHODS, method))
        throw new SaveCallError('unknownMethod', `${method} is not a Save1 method`);
    const entry = SAVE_METHODS[method];
    return {cmd: entry.cmd, args: entry.args(Array.isArray(params) ? params : [])};
}

/**
 * The D-Bus error name for a code. A code that is not a valid name element
 * would make return_dbus_error() fail, so it becomes `internal`.
 */
export function errorName(code) {
    return `${SAVE_ERROR_PREFIX}${typeof code === 'string' && CODE_RE.test(code) ? code : 'internal'}`;
}

/** The reply string for a command's `data`. */
export function replyText(data) {
    return JSON.stringify(data ?? {});
}

export class SaveBus {
    /**
     * @param {(cmd: string, args?: object) => Promise<object>} request
     *        the helper request (rejects with HelperError)
     */
    constructor(request) {
        this._request = request;
        const impl = {};
        for (const method of Object.keys(SAVE_METHODS))
            impl[`${method}Async`] = (params, invocation) => this._serve(method, params, invocation);
        this._exported = Gio.DBusExportedObject.wrapJSObject(INTERFACE_XML, impl);
        try {
            this._exported.export(Gio.DBus.session, SAVE_OBJECT_PATH);
        } catch (e) {
            console.warn(`${LOG}: cannot export the save D-Bus object: ${e.message}`);
            this._exported = null;
        }
    }

    /** Every invocation gets exactly one reply, even after destroy(). */
    _serve(method, params, invocation) {
        let command;
        try {
            command = buildCommand(method, params);
        } catch (e) {
            this._fail(invocation, e.code ?? 'internal', e.message);
            return;
        }
        let pending;
        try {
            pending = this._request
                ? this._request(command.cmd, command.args)
                : Promise.reject(Object.assign(new Error('helper is not running'), {code: 'helperExited'}));
        } catch (e) {
            pending = Promise.reject(e);
        }
        Promise.resolve(pending).then(data => {
            try {
                invocation.return_value(new GLib.Variant('(s)', [replyText(data)]));
            } catch (e) {
                console.warn(`${LOG}: Save1.${method}: cannot reply: ${e.message}`);
            }
        }, e => {
            if (e?.code !== 'helperExited')
                console.warn(`${LOG}: Save1.${method} failed: ${e?.code ?? ''} ${e?.message ?? e}`);
            this._fail(invocation, e?.code ?? 'internal', e?.message ?? String(e));
        });
    }

    _fail(invocation, code, message) {
        try {
            invocation.return_dbus_error(errorName(code), String(message ?? code));
        } catch (e) {
            console.warn(`${LOG}: Save1: cannot return an error: ${e.message}`);
        }
    }

    destroy() {
        this._request = null;
        try {
            this._exported?.unexport();
        } catch {
            // never exported
        }
        this._exported = null;
    }
}
