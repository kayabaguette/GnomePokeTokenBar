/* The "Backup & Transfer" preferences page (save-transfer.md §5.2).
 *
 * Preferences process only (Gtk/Adw): never import it from the shell. The
 * page reaches the helper through the extension's `Save1` relay on the
 * session bus (protocol §15.1): paths and ids go out, JSON replies come
 * back. While the relay's name has no owner (extension off, or the screen
 * locked) the page shows a banner and its rows are insensitive.
 *
 * Every text and rule lives in the pure lib/saveTransferModel.js.
 */

import Adw from 'gi://Adw';
import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import Gtk from 'gi://Gtk';

import * as M from './saveTransferModel.js';

Gio._promisify(Gio.DBusConnection.prototype, 'call');
Gio._promisify(Gtk.FileDialog.prototype, 'open');
Gio._promisify(Gtk.FileDialog.prototype, 'save');
Gio._promisify(Adw.AlertDialog.prototype, 'choose');
Gio._promisify(Gtk.FileLauncher.prototype, 'open_containing_folder');

/** Longer than the extension's own 10 s command timeout, so its `timeout` code arrives first. */
const CALL_TIMEOUT_MS = 20_000;
const LOG = 'PokeTokenBar';

class SaveError extends Error {
    constructor(code, message) {
        super(message);
        this.code = code;
    }
}

function isDismissed(e) {
    return e?.matches?.(Gtk.DialogError, Gtk.DialogError.DISMISSED) ||
        e?.matches?.(Gtk.DialogError, Gtk.DialogError.CANCELLED);
}

function centered(widget) {
    widget.valign = Gtk.Align.CENTER;
    return widget;
}

export class BackupPage {
    /** @param {Adw.PreferencesWindow} window */
    constructor(window) {
        this._window = window;
        this._conn = Gio.DBus.session;
        this._available = null; // null until the name watcher has answered
        this._busy = 0;
        this._closed = false;
        this._listGeneration = 0;
        this._snapRows = [];

        this.page = new Adw.PreferencesPage({title: M.TEXT.pageTitle(), icon_name: 'document-save-symbolic'});
        this._banner = new Adw.Banner({title: M.TEXT.banner(), revealed: false});
        this.page.banner = this._banner;
        this.page.add(this._buildTransfer());
        this.page.add(this._buildSnapshots());

        // Collapsed and refetched every time the page is shown (SV:89-91).
        this.page.connect('map', () => {
            this._expander.expanded = false;
            this._refreshSnapshots();
        });

        this._watchId = Gio.bus_watch_name_on_connection(this._conn, M.SAVE_BUS_NAME,
            Gio.BusNameWatcherFlags.NONE, () => this._setAvailable(true), () => this._setAvailable(false));
        window.connect('close-request', () => {
            this.destroy();
            return false;
        });
        this._sync();
    }

    destroy() {
        this._closed = true;
        if (this._watchId) {
            Gio.bus_unwatch_name(this._watchId);
            this._watchId = 0;
        }
    }

    /* ---- layout -------------------------------------------------------- */

    _buildTransfer() {
        this._transfer = new Adw.PreferencesGroup({title: M.TEXT.transferGroup()});

        const exportRow = new Adw.ActionRow({title: M.TEXT.exportTitle(), subtitle: M.TEXT.exportHint(), use_markup: false});
        const exportButton = centered(new Gtk.Button({label: M.TEXT.exportButton()}));
        exportButton.connect('clicked', () => this._run(() => this._export()));
        exportRow.add_suffix(exportButton);
        exportRow.activatable_widget = exportButton;
        this._transfer.add(exportRow);

        const importRow = new Adw.ActionRow({title: M.TEXT.importTitle(), subtitle: M.TEXT.importHint(), use_markup: false});
        const importButton = centered(new Gtk.Button({label: M.TEXT.importButton()}));
        importButton.connect('clicked', () => this._run(() => this._import()));
        importRow.add_suffix(importButton);
        importRow.activatable_widget = importButton;
        this._transfer.add(importRow);
        return this._transfer;
    }

    _buildSnapshots() {
        this._snapshots = new Adw.PreferencesGroup({title: M.TEXT.snapshotsGroup()});

        const createRow = new Adw.ActionRow({title: M.TEXT.createTitle(), subtitle: M.TEXT.createHint(), use_markup: false});
        const createButton = centered(new Gtk.Button({label: M.TEXT.createButton()}));
        createButton.connect('clicked', () => this._run(() => this._createSnapshot()));
        createRow.add_suffix(createButton);
        createRow.activatable_widget = createButton;
        this._snapshots.add(createRow);

        this._expander = new Adw.ExpanderRow({title: M.TEXT.snapshotsTitle(), use_markup: false});
        this._snapshots.add(this._expander);
        this._renderSnapshots([]);
        return this._snapshots;
    }

    _renderSnapshots(list) {
        for (const row of this._snapRows)
            this._expander.remove(row);
        this._snapRows = [];
        this._expander.subtitle = M.snapshotsSubtitle(list);
        if (list.length === 0) {
            const empty = new Adw.ActionRow({title: M.TEXT.noSnapshots(), use_markup: false, css_classes: ['dim-label']});
            this._snapRows.push(empty);
            this._expander.add_row(empty);
            return;
        }
        for (const snapshot of list) {
            const text = M.snapshotRow(snapshot);
            const row = new Adw.ActionRow({title: text.title, subtitle: text.subtitle, use_markup: false});
            const restore = centered(new Gtk.Button({label: M.TEXT.restoreButton(), css_classes: ['flat']}));
            restore.update_property([Gtk.AccessibleProperty.DESCRIPTION], [`${text.title}, ${text.subtitle}`]);
            restore.connect('clicked', () => this._run(() => this._restore(snapshot)));
            row.add_suffix(restore);
            this._snapRows.push(row);
            this._expander.add_row(row);
        }
    }

    /* ---- state --------------------------------------------------------- */

    _setAvailable(available) {
        if (this._closed)
            return;
        const was = this._available;
        this._available = available;
        this._sync();
        if (available && was !== true)
            this._refreshSnapshots();
    }

    _sync() {
        this._banner.revealed = this._available === false;
        const sensitive = this._available === true && this._busy === 0;
        this._transfer.sensitive = sensitive;
        this._snapshots.sensitive = sensitive;
    }

    /** Runs `fn` with the rows insensitive, so a second action cannot start halfway through the first. */
    async _whileBusy(fn) {
        this._busy++;
        this._sync();
        try {
            await fn();
        } finally {
            this._busy--;
            if (!this._closed)
                this._sync();
        }
    }

    /* ---- the relay ----------------------------------------------------- */

    async _call(method, args = []) {
        let reply;
        try {
            reply = await this._conn.call(M.SAVE_BUS_NAME, M.SAVE_OBJECT_PATH, M.SAVE_INTERFACE, method,
                args.length ? new GLib.Variant(`(${'s'.repeat(args.length)})`, args) : null,
                new GLib.VariantType('(s)'), Gio.DBusCallFlags.NO_AUTO_START, CALL_TIMEOUT_MS, null);
        } catch (e) {
            const remote = Gio.DBusError.is_remote_error(e) ? Gio.DBusError.get_remote_error(e) : null;
            let code = M.codeFromRemoteError(remote);
            if (!remote && e.matches?.(Gio.IOErrorEnum, Gio.IOErrorEnum.TIMED_OUT))
                code = 'timeout';
            if (code !== 'unavailable')
                console.warn(`${LOG}: Save1.${method} failed: ${code}: ${e.message}`);
            throw new SaveError(code, e.message);
        }
        const [text] = reply.deepUnpack();
        try {
            return JSON.parse(text);
        } catch {
            throw new SaveError('internal', `${method}: unreadable reply`);
        }
    }

    async _refreshSnapshots() {
        if (this._available !== true || this._closed)
            return;
        const generation = ++this._listGeneration;
        try {
            const reply = await this._call('ListSnapshots');
            if (generation === this._listGeneration && !this._closed)
                this._renderSnapshots(M.snapshotList(reply));
        } catch {
            // Logged by _call; the list keeps what it showed.
        }
    }

    /* ---- feedback ------------------------------------------------------ */

    _toast(title, file = null) {
        if (this._closed)
            return;
        const toast = new Adw.Toast({title, use_markup: false});
        if (file) {
            toast.button_label = M.TEXT.showFile();
            toast.connect('button-clicked', () => {
                new Gtk.FileLauncher({file}).open_containing_folder(this._window, null)
                    .catch(e => console.warn(`${LOG}: cannot show the export: ${e.message}`));
            });
        }
        this._window.add_toast(toast);
    }

    async _alert(heading, body) {
        if (this._closed)
            return;
        const dialog = new Adw.AlertDialog({heading, body});
        dialog.add_response('ok', M.TEXT.ok());
        dialog.default_response = 'ok';
        dialog.close_response = 'ok';
        await dialog.choose(this._window, null);
    }

    /** Asks with Cancel as the default and close response; true only for the destructive choice. */
    async _confirm(c) {
        // Wide when the window allows it, so a summary line does not wrap mid-figure.
        const dialog = new Adw.AlertDialog({heading: c.heading, body: c.body, prefer_wide_layout: true});
        dialog.add_response('cancel', M.TEXT.cancel());
        dialog.add_response(c.confirmId, c.confirmLabel);
        dialog.set_response_appearance(c.confirmId, Adw.ResponseAppearance.DESTRUCTIVE);
        dialog.default_response = c.defaultResponse;
        dialog.close_response = c.closeResponse;
        return await dialog.choose(this._window, null) === c.confirmId;
    }

    _fileDialog(title) {
        const filter = new Gtk.FileFilter({name: M.TEXT.fileFilter()});
        filter.add_pattern('*.json');
        filter.add_mime_type('application/json');
        const filters = new Gio.ListStore({item_type: Gtk.FileFilter});
        filters.append(filter);
        return new Gtk.FileDialog({title, modal: true, filters, default_filter: filter});
    }

    /** The picked file, or null when the dialog was dismissed. */
    async _pick(start) {
        try {
            return await start();
        } catch (e) {
            if (!isDismissed(e))
                console.warn(`${LOG}: file dialog failed: ${e.message}`);
            return null;
        }
    }

    /* ---- actions ------------------------------------------------------- */

    _run(action) {
        action().catch(e => console.error(`${LOG}: backup action failed: ${e}\n${e?.stack ?? ''}`));
    }

    async _export() {
        const dialog = this._fileDialog(M.TEXT.exportTitle());
        dialog.initial_name = M.exportFileName(Date.now());
        const file = await this._pick(() => dialog.save(this._window, null));
        if (!file)
            return;
        const path = file.get_path();
        if (!path) {
            this._toast(M.errorMessage('notLocal'));
            return;
        }
        await this._whileBusy(async () => {
            try {
                const reply = await this._call('ExportSave', [path]);
                this._toast(M.exportDoneText(reply, path), file);
            } catch (e) {
                this._toast(M.errorMessage(e.code));
            }
        });
    }

    async _import() {
        const file = await this._pick(() => this._fileDialog(M.TEXT.importTitle()).open(this._window, null));
        if (!file)
            return;
        const path = file.get_path();
        if (!path) {
            this._toast(M.errorMessage('notLocal'));
            return;
        }
        await this._whileBusy(async () => {
            try {
                const inspect = await this._call('InspectSave', [path]);
                if (!await this._confirm(M.importConfirm(inspect)))
                    return;
                const reply = await this._call('ImportSave', [path, String(inspect?.digest ?? '')]);
                this._toast(M.importDoneText(reply));
            } catch (e) {
                await this._alert(M.TEXT.importFailed(), M.errorMessage(e.code));
            }
        });
        this._refreshSnapshots();
    }

    async _createSnapshot() {
        await this._whileBusy(async () => {
            try {
                await this._call('CreateSnapshot');
                this._toast(M.TEXT.snapshotCreated());
            } catch (e) {
                this._toast(M.errorMessage(e.code));
            }
        });
        this._refreshSnapshots();
    }

    async _restore(snapshot) {
        await this._whileBusy(async () => {
            try {
                if (!await this._confirm(M.restoreConfirm(snapshot)))
                    return;
                const reply = await this._call('RestoreSnapshot', [snapshot.id]);
                this._toast(M.restoreDoneText(reply));
            } catch (e) {
                await this._alert(M.TEXT.restoreFailed(), M.errorMessage(e.code));
            }
        });
        this._refreshSnapshots();
    }
}
