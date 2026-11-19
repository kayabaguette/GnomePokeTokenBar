/* Helper process client: spawn, NDJSON over stdio, lifecycle (protocol §1–§4).
 *
 * - Reads stdout with Gio.DataInputStream.read_line_async.
 * - Writes stdin through a FIFO drained by write_all_async, one write in
 *   flight. Nothing here blocks the compositor thread: a stuck helper with a
 *   full pipe only grows the queue.
 * - Nothing is sent before `hello`; `hello.protocol` is checked (§2.6).
 * - Commands get positive integer ids per spawned process, a result route and
 *   a timeout (§4). When the process exits every pending command fails with
 *   `helperExited`.
 * - Unexpected exits restart after 1, 2, 4 … 60 s; the backoff resets once a
 *   helper has stayed up 60 s (§2.6). A protocol mismatch never restarts.
 * - stop() closes stdin and leaves only the 3 s force_exit reaper, which
 *   references nothing but the subprocess (§2.5).
 */

import Gio from 'gi://Gio';
import GLib from 'gi://GLib';

export const PROTOCOL_VERSION = 1;

const BACKOFF_STEPS_S = [1, 2, 4, 8, 16, 32, 60];
const STABLE_UPTIME_S = 60;
const HELLO_TIMEOUT_S = 10;
const KILL_AFTER_S = 3;
const MAX_LINE_CHARS = 1_048_576;

/** Extension-side command timeouts, protocol §4. */
export const COMMAND_TIMEOUTS_MS = {
    ping: 5_000,
    setSettings: 10_000,
    refresh: 120_000,
    wake: 5_000,
    networkUp: 5_000,
    setRepresentative: 10_000,
    buyItem: 10_000, // Shop and Bag (M2)
    buyEgg: 10_000,
    planCandies: 5_000,
    useCandy: 10_000,
    useMint: 10_000,
    getDexPage: 10_000,
    getSpecies: 30_000,
    getCatchLog: 10_000,
    getSprite: 30_000,
    exportSave: 10_000, // Save1 relay (M3, protocol §4)
    inspectSave: 10_000,
    importSave: 10_000,
    listSnapshots: 5_000,
    createSnapshot: 10_000,
    restoreSnapshot: 10_000,
};
const DEFAULT_TIMEOUT_MS = 30_000;

const LOG = 'PokeTokenBar';

/** A failed command: `code` is a protocol error code, `timeout` or `helperExited`. */
export class HelperError extends Error {
    constructor(code, message = code) {
        super(message);
        this.name = 'HelperError';
        this.code = code;
    }
}

/**
 * Status values reported through `onStatus`:
 *   starting — spawned, waiting for hello;
 *   running  — hello accepted;
 *   down     — exited unexpectedly, a restart is scheduled;
 *   mismatch — wrong protocol version, never restarted;
 *   stopped  — stop() was called.
 */
export class HelperClient {
    /**
     * @param {string} extensionPath
     * @param {object} handlers  {onHello, onState, onEvent, onStatus}; each optional
     */
    constructor(extensionPath, handlers = {}) {
        this._path = extensionPath;
        this._handlers = handlers;
        this._stopped = true;
        this._session = null;
        this._restartId = 0;
        this._backoffIndex = 0;
        this._status = 'stopped';
        this._lastFatal = null;
    }

    get status() {
        return this._status;
    }

    /** The last `fatal` message from the helper, for diagnostics. */
    get lastFatal() {
        return this._lastFatal;
    }

    start() {
        if (!this._stopped)
            return;
        this._stopped = false;
        this._backoffIndex = 0;
        this._spawn();
    }

    stop() {
        this._stopped = true;
        if (this._restartId) {
            GLib.source_remove(this._restartId);
            this._restartId = 0;
        }
        const s = this._session;
        this._session = null;
        if (s)
            this._closeSession(s);
        this._setStatus('stopped');
        this._handlers = {};
    }

    /**
     * Send a command; resolves with the reply `data`, rejects with HelperError.
     * Callers must handle the rejection: every pending command fails on each
     * helper exit, which happens on every screen lock.
     *
     * @param {string} cmd
     * @param {object} [args]
     * @param {{timeoutMs?: number}} [options]
     */
    request(cmd, args = undefined, options = {}) {
        const s = this._session;
        if (!s || s.dead)
            return Promise.reject(new HelperError('helperExited', 'helper is not running'));
        return new Promise((resolve, reject) => {
            const id = s.nextId++;
            const timeoutMs = options.timeoutMs ?? COMMAND_TIMEOUTS_MS[cmd] ?? DEFAULT_TIMEOUT_MS;
            const entry = {cmd, resolve, reject, timerId: 0};
            entry.timerId = GLib.timeout_add(GLib.PRIORITY_DEFAULT, timeoutMs, () => {
                entry.timerId = 0;
                if (s.pending.delete(id))
                    reject(new HelperError('timeout', `${cmd} timed out after ${timeoutMs} ms`));
                return GLib.SOURCE_REMOVE;
            });
            s.pending.set(id, entry);
            const msg = args === undefined ? {id, cmd} : {id, cmd, args};
            if (s.ready)
                this._enqueue(s, msg);
            else
                s.preHello.push(msg); // protocol §2.2: send nothing before hello
        });
    }

    /** Fire-and-forget request; failures are logged, never thrown. */
    send(cmd, args = undefined, options = {}) {
        this.request(cmd, args, options).catch(e => {
            if (e?.code !== 'helperExited')
                console.warn(`${LOG}: ${cmd} failed: ${e?.code ?? ''} ${e?.message ?? e}`);
        });
    }

    _setStatus(status) {
        if (this._status === status)
            return;
        this._status = status;
        try {
            this._handlers.onStatus?.(status);
        } catch (e) {
            console.error(`${LOG}: onStatus threw: ${e}`);
        }
    }

    _spawn() {
        let proc;
        try {
            proc = Gio.Subprocess.new(
                [GLib.build_filenamev([this._path, 'bin', 'ptb-helper']), 'serve'],
                Gio.SubprocessFlags.STDIN_PIPE | Gio.SubprocessFlags.STDOUT_PIPE);
        } catch (e) {
            console.error(`${LOG}: cannot start the helper: ${e.message}`);
            this._setStatus('down');
            this._scheduleRestart();
            return;
        }

        const s = {
            proc,
            stdin: proc.get_stdin_pipe(),
            cancellable: new Gio.Cancellable(),
            spawnedAt: GLib.get_monotonic_time(),
            nextId: 1,
            pending: new Map(),
            queue: [],
            writing: false,
            closeAfterWrite: false,
            ready: false,
            preHello: [],
            helloTimerId: 0,
            dead: false,
        };
        this._session = s;
        this._setStatus('starting');

        s.helloTimerId = GLib.timeout_add_seconds(GLib.PRIORITY_DEFAULT, HELLO_TIMEOUT_S, () => {
            s.helloTimerId = 0;
            if (!s.ready && !s.dead) {
                console.error(`${LOG}: no hello from the helper within ${HELLO_TIMEOUT_S} s; killing it`);
                s.proc.force_exit();
            }
            return GLib.SOURCE_REMOVE;
        });

        const reader = new Gio.DataInputStream({
            base_stream: proc.get_stdout_pipe(),
            close_base_stream: true,
        });
        this._readLine(s, reader);

        proc.wait_async(null, (p, res) => {
            try {
                p.wait_finish(res);
            } catch {
                // the exit status below is what matters
            }
            if (this._session !== s)
                return; // stopped or replaced; the session was already closed
            const code = p.get_if_exited() ? p.get_exit_status() : -1;
            console.warn(`${LOG}: helper exited (status ${code})`);
            this._session = null;
            this._closeSession(s);
            if (this._stopped)
                return;
            const uptimeS = (GLib.get_monotonic_time() - s.spawnedAt) / 1e6;
            if (uptimeS >= STABLE_UPTIME_S)
                this._backoffIndex = 0;
            this._setStatus('down');
            this._scheduleRestart();
        });
    }

    /** Fail pending commands, cancel IO, close stdin, arm the reaper. */
    _closeSession(s) {
        if (s.dead)
            return;
        s.dead = true;
        if (s.helloTimerId) {
            GLib.source_remove(s.helloTimerId);
            s.helloTimerId = 0;
        }
        for (const entry of s.pending.values()) {
            if (entry.timerId)
                GLib.source_remove(entry.timerId);
            entry.reject(new HelperError('helperExited', `${entry.cmd}: helper exited`));
        }
        s.pending.clear();
        s.queue = [];
        s.preHello = [];
        s.cancellable.cancel();

        // A stream with an operation in flight refuses close(); the cancelled
        // write's callback closes it instead.
        if (s.writing)
            s.closeAfterWrite = true;
        else
            closeQuietly(s.stdin);

        const proc = s.proc;
        let killId = GLib.timeout_add_seconds(GLib.PRIORITY_DEFAULT, KILL_AFTER_S, () => {
            killId = 0;
            proc.force_exit();
            return GLib.SOURCE_REMOVE;
        });
        proc.wait_async(null, () => {
            if (killId) {
                GLib.source_remove(killId);
                killId = 0;
            }
        });
    }

    _scheduleRestart() {
        if (this._stopped || this._restartId)
            return;
        const delay = BACKOFF_STEPS_S[Math.min(this._backoffIndex, BACKOFF_STEPS_S.length - 1)];
        this._backoffIndex++;
        console.log(`${LOG}: restarting the helper in ${delay} s`);
        this._restartId = GLib.timeout_add_seconds(GLib.PRIORITY_DEFAULT, delay, () => {
            this._restartId = 0;
            if (!this._stopped)
                this._spawn();
            return GLib.SOURCE_REMOVE;
        });
    }

    _enqueue(s, msg) {
        if (s.dead)
            return;
        s.queue.push(new TextEncoder().encode(`${JSON.stringify(msg)}\n`));
        this._drain(s);
    }

    _drain(s) {
        if (s.writing || s.dead || s.queue.length === 0)
            return;
        const bytes = s.queue.shift();
        s.writing = true;
        s.stdin.write_all_async(bytes, GLib.PRIORITY_DEFAULT, s.cancellable, (stream, res) => {
            s.writing = false;
            try {
                stream.write_all_finish(res);
            } catch (e) {
                if (!e.matches?.(Gio.IOErrorEnum, Gio.IOErrorEnum.CANCELLED))
                    console.warn(`${LOG}: write to helper failed: ${e.message}`);
            }
            if (s.closeAfterWrite) {
                s.closeAfterWrite = false;
                closeQuietly(stream);
                return;
            }
            this._drain(s);
        });
    }

    _readLine(s, reader) {
        reader.read_line_async(GLib.PRIORITY_DEFAULT, s.cancellable, (src, res) => {
            let line;
            try {
                [line] = src.read_line_finish_utf8(res);
            } catch (e) {
                if (!e.matches?.(Gio.IOErrorEnum, Gio.IOErrorEnum.CANCELLED))
                    console.warn(`${LOG}: read from helper failed: ${e.message}`);
                return;
            }
            if (line === null)
                return; // EOF: wait_async handles the exit
            if (s.dead)
                return;
            if (line.length > MAX_LINE_CHARS) {
                console.warn(`${LOG}: skipped a helper line of ${line.length} characters (limit 1 MiB)`);
            } else if (line.length > 0) {
                let msg = null;
                try {
                    msg = JSON.parse(line);
                } catch (e) {
                    console.warn(`${LOG}: bad JSON from helper: ${e.message}: ${line.slice(0, 200)}`);
                }
                if (msg !== null && typeof msg === 'object' && !Array.isArray(msg))
                    this._dispatch(s, msg);
            }
            if (!s.dead)
                this._readLine(s, src);
        });
    }

    _dispatch(s, msg) {
        if (msg.type === 'fatal') {
            this._lastFatal = {code: msg.code, message: msg.message};
            console.warn(`${LOG}: helper fatal ${msg.code}: ${msg.message}`);
            return;
        }
        if (!s.ready) {
            if (msg.type === 'hello')
                this._onHello(s, msg);
            else
                console.warn(`${LOG}: ignored '${msg.type}' before hello`);
            return;
        }
        try {
            switch (msg.type) {
            case 'result':
                this._onResult(s, msg);
                break;
            case 'state':
                this._handlers.onState?.(msg);
                break;
            case 'event':
                this._handlers.onEvent?.(msg);
                break;
            default:
                break; // unknown types are ignored (§2.7)
            }
        } catch (e) {
            console.error(`${LOG}: handling '${msg.type}' threw: ${e}\n${e?.stack ?? ''}`);
        }
    }

    _onHello(s, msg) {
        if (s.helloTimerId) {
            GLib.source_remove(s.helloTimerId);
            s.helloTimerId = 0;
        }
        if (msg.protocol !== PROTOCOL_VERSION) {
            console.error(`${LOG}: helper speaks protocol ${msg.protocol}, expected ${PROTOCOL_VERSION}; ` +
                'reinstall the extension');
            this._setStatus('mismatch');
            // Detach the session first: its exit handler then neither restarts
            // nor reports 'down'. Closing stdin makes the helper exit (EOF).
            this._session = null;
            this._closeSession(s);
            return;
        }
        console.log(`${LOG}: helper ${msg.helperVersion ?? '?'} (pid ${msg.pid ?? '?'}) ready`);
        s.ready = true;
        this._setStatus('running');
        try {
            this._handlers.onHello?.(msg); // sends setSettings first (§2.2)
        } catch (e) {
            console.error(`${LOG}: onHello threw: ${e}`);
        }
        const early = s.preHello;
        s.preHello = [];
        for (const m of early)
            this._enqueue(s, m);
    }

    _onResult(s, msg) {
        if (!Number.isInteger(msg.id))
            return;
        const entry = s.pending.get(msg.id);
        if (!entry)
            return; // timed out already
        s.pending.delete(msg.id);
        if (entry.timerId)
            GLib.source_remove(entry.timerId);
        if (msg.ok)
            entry.resolve(msg.data ?? {});
        else
            entry.reject(new HelperError(msg.error?.code ?? 'internal', msg.error?.message ?? 'command failed'));
    }
}

function closeQuietly(stream) {
    try {
        stream?.close(null);
    } catch (e) {
        console.warn(`${LOG}: closing helper stdin: ${e.message}`);
    }
}
