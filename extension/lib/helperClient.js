import Gio from 'gi://Gio';
import GLib from 'gi://GLib';

const BACKOFF_MIN_S = 1;
const BACKOFF_MAX_S = 60;
const KILL_AFTER_S = 3;

/**
 * Spawns `bin/ptb-helper serve` and exchanges newline-delimited JSON with it.
 * Restarts the helper with exponential backoff if it dies; never after stop().
 */
export class HelperClient {
    constructor(extensionPath) {
        this._path = extensionPath;
        this.onMessage = null;
        this._stopped = true;
        this._proc = null;
        this._stdin = null;
        this._cancellable = null;
        this._backoff = BACKOFF_MIN_S;
        this._restartId = 0;
        this._killId = 0;
    }

    start() {
        if (!this._stopped)
            return;
        this._stopped = false;
        this._spawn();
    }

    send(obj) {
        if (!this._stdin)
            return false;
        try {
            const bytes = new TextEncoder().encode(`${JSON.stringify(obj)}\n`);
            this._stdin.write_all(bytes, null);
            this._stdin.flush(null);
            return true;
        } catch (e) {
            console.error(`PokeTokenBar: send failed: ${e.message}`);
            return false;
        }
    }

    stop() {
        this._stopped = true;
        if (this._restartId) {
            GLib.source_remove(this._restartId);
            this._restartId = 0;
        }
        this._cancellable?.cancel();
        this._cancellable = null;

        const proc = this._proc;
        this._proc = null;
        try {
            this._stdin?.close(null);
        } catch (e) {
            console.error(`PokeTokenBar: closing stdin: ${e.message}`);
        }
        this._stdin = null;

        if (proc) {
            // Helper exits on stdin EOF; force it if it lingers.
            this._killId = GLib.timeout_add_seconds(GLib.PRIORITY_DEFAULT, KILL_AFTER_S, () => {
                this._killId = 0;
                proc.force_exit();
                return GLib.SOURCE_REMOVE;
            });
            proc.wait_async(null, () => {
                if (this._killId) {
                    GLib.source_remove(this._killId);
                    this._killId = 0;
                }
            });
        }
        this.onMessage = null;
    }

    _spawn() {
        let proc;
        try {
            proc = Gio.Subprocess.new(
                [`${this._path}/bin/ptb-helper`, 'serve'],
                Gio.SubprocessFlags.STDIN_PIPE | Gio.SubprocessFlags.STDOUT_PIPE);
        } catch (e) {
            console.error(`PokeTokenBar: spawn failed: ${e.message}`);
            this._scheduleRestart();
            return;
        }
        this._proc = proc;
        this._stdin = proc.get_stdin_pipe();
        this._cancellable = new Gio.Cancellable();
        const reader = new Gio.DataInputStream({
            base_stream: proc.get_stdout_pipe(),
            close_base_stream: true,
        });
        this._readLine(reader, this._cancellable);

        proc.wait_async(null, (p, res) => {
            try {
                p.wait_finish(res);
            } catch (e) {
                // ignore
            }
            if (this._stopped || this._proc !== proc)
                return;
            console.error('PokeTokenBar: helper exited unexpectedly');
            this._cancellable?.cancel();
            this._proc = null;
            this._stdin = null;
            this._scheduleRestart();
        });
    }

    _readLine(reader, cancellable) {
        reader.read_line_async(GLib.PRIORITY_DEFAULT, cancellable, (src, res) => {
            let line;
            try {
                [line] = src.read_line_finish_utf8(res);
            } catch (e) {
                if (!e.matches?.(Gio.IOErrorEnum, Gio.IOErrorEnum.CANCELLED))
                    console.error(`PokeTokenBar: read failed: ${e.message}`);
                return;
            }
            if (line === null)
                return; // EOF; the wait_async handler deals with restarts
            if (line.length > 0) {
                let msg = null;
                try {
                    msg = JSON.parse(line);
                } catch (e) {
                    console.error(`PokeTokenBar: bad JSON from helper: ${e.message}`);
                }
                if (msg !== null && typeof msg === 'object') {
                    this._backoff = BACKOFF_MIN_S;
                    try {
                        this.onMessage?.(msg);
                    } catch (e) {
                        console.error(`PokeTokenBar: onMessage threw: ${e.message}`);
                    }
                }
            }
            if (!this._stopped)
                this._readLine(src, cancellable);
        });
    }

    _scheduleRestart() {
        if (this._stopped || this._restartId)
            return;
        const delay = this._backoff;
        this._backoff = Math.min(this._backoff * 2, BACKOFF_MAX_S);
        this._restartId = GLib.timeout_add_seconds(GLib.PRIORITY_DEFAULT, delay, () => {
            this._restartId = 0;
            if (!this._stopped)
                this._spawn();
            return GLib.SOURCE_REMOVE;
        });
    }
}
