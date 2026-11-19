/* Backup & Transfer: the pure model of the preferences page (save-transfer.md §5.2).
 *
 * Pure module: no gi:// imports, so it is unit-tested under plain `gjs -m`.
 * It holds the D-Bus names of the `Save1` relay (protocol §15.1), the export
 * file name, the error-code → message map, the confirmation texts and the
 * snapshot row text. prefs.js only wires these into widgets.
 *
 * English source strings come from upstream Localization.swift (LOC) and
 * LocalizationErrors.swift (LE), with "Mac" → "computer" and "app" →
 * "extension" (save-transfer.md §7 Q3).
 */

import {compact} from './format.js';
import {_, fmt} from './i18n.js';

/* ---- the relay (protocol §15.1) ---------------------------------------- */

/** The extension's well-known name, shared with Agent1 (agentBus.js BUS_NAME). */
export const SAVE_BUS_NAME = 'io.github.poketokenbar.Agent';
export const SAVE_OBJECT_PATH = '/io/github/poketokenbar/Save';
export const SAVE_INTERFACE = 'io.github.poketokenbar.Save1';
/** A helper error `<code>` travels as the D-Bus error `${SAVE_ERROR_PREFIX}<code>`. */
export const SAVE_ERROR_PREFIX = `${SAVE_INTERFACE}.Error.`;

/** Remote D-Bus errors that mean "nothing serves Save1 right now". */
const NO_OWNER_ERRORS = new Set([
    'org.freedesktop.DBus.Error.ServiceUnknown',
    'org.freedesktop.DBus.Error.NameHasNoOwner',
]);
/** The name is owned but the object or method is missing: an extension older than this window (pre-M3). */
const OUTDATED_ERRORS = new Set([
    'org.freedesktop.DBus.Error.UnknownObject',
    'org.freedesktop.DBus.Error.UnknownInterface',
    'org.freedesktop.DBus.Error.UnknownMethod',
]);
const TIMEOUT_ERRORS = new Set([
    'org.freedesktop.DBus.Error.NoReply',
    'org.freedesktop.DBus.Error.Timeout',
    'org.freedesktop.DBus.Error.TimedOut',
]);

/**
 * The page's error code for a D-Bus failure.
 *
 * @param {string|null} remoteName  `Gio.DBusError.get_remote_error(e)`, or null for a local error
 * @returns {string} a protocol §11.2 code, `timeout`, `helperExited`, `unavailable`, `outdated` or `internal`
 */
export function codeFromRemoteError(remoteName) {
    if (typeof remoteName !== 'string' || remoteName === '')
        return 'internal';
    if (remoteName.startsWith(SAVE_ERROR_PREFIX)) {
        const code = remoteName.slice(SAVE_ERROR_PREFIX.length);
        return /^[A-Za-z][A-Za-z0-9_]*$/.test(code) ? code : 'internal';
    }
    if (NO_OWNER_ERRORS.has(remoteName))
        return 'unavailable';
    if (OUTDATED_ERRORS.has(remoteName))
        return 'outdated';
    if (TIMEOUT_ERRORS.has(remoteName))
        return 'timeout';
    return 'internal';
}

/* ---- texts ------------------------------------------------------------- */

export const TEXT = {
    pageTitle: () => _('Backup & Transfer'),
    banner: () => _('Turn on PokeTokenBar to back up or transfer your save.'),
    transferGroup: () => _('Transfer'),
    exportTitle: () => _('Export save'),
    exportHint: () => _('Saves your Pokédex, lifetime tokens, Bag, and current Pokémon as one file'),
    exportButton: () => _('Export…'),
    importTitle: () => _('Import save'),
    importHint: () => _('Pick a file exported from another computer (macOS or GNOME) and continue here'),
    importButton: () => _('Import…'),
    snapshotsGroup: () => _('Snapshots'),
    createTitle: () => _('Create snapshot'),
    createHint: () => _('Saves an instant local restore point of your progress (keeps up to 10)'),
    createButton: () => _('Create'),
    snapshotsTitle: () => _('Automatic Backups (Snapshots)'),
    noSnapshots: () => _('No snapshots saved yet'),
    restoreButton: () => _('Restore'),
    snapshotCreated: () => _('Snapshot created'),
    fileFilter: () => _('PokeTokenBar saves'),
    showFile: () => _('Show in Files'),
    cancel: () => _('Cancel'),
    replace: () => _('Replace'),
    importFailed: () => _('Import failed'),
    restoreFailed: () => _('Restore failed'),
    ok: () => _('OK'),
};

/** The generic message: LE:21. */
function genericError() {
    return _('The operation could not be completed. Please try again.');
}

/**
 * The message shown for an error code. Never the raw code and never the
 * helper's English log message (STT:740-755).
 *
 * @param {string} code
 */
export function errorMessage(code) {
    switch (code) {
    case 'notASaveFile':
        return _("That isn't a PokeTokenBar save file.");
    case 'newerSchema':
        return _('This save was made by a newer version — update the extension and try again.');
    case 'fileTooLarge':
        return _('That file is too large to be a save — it looks like the wrong file.');
    case 'backupFailed':
        return _("Import stopped because the current state couldn't be backed up — your progress is untouched. Check free disk space.");
    case 'permissionDenied':
        return _('File access was denied. Check permissions or choose another location.');
    case 'noSpace':
        return _('There is not enough disk space. Free up space and try again.');
    case 'fileNotFound':
        return _('The file could not be found. Check its location and select it again.');
    case 'fileChanged':
        return _('The file changed after it was checked. Select it again.');
    case 'notFound':
        return _('That snapshot is no longer available. Choose another one.');
    case 'notLocal':
        return _('Choose a local file.');
    case 'unavailable':
        return _('Turn on PokeTokenBar to back up or transfer your save.');
    case 'outdated':
        return _('PokeTokenBar was updated. Log out and back in to finish the update, then try again.');
    default: // badArgs, ioError, timeout, helperExited, internal, anything new
        return genericError();
    }
}

/** `{dex} in Pokédex · {tokens} lifetime`. */
export function summaryText(summary) {
    return fmt(_('%d in Pokédex · %s lifetime'), summary?.dexCount ?? 0, compact(summary?.lifetimeTokens ?? 0));
}

/**
 * A timestamp as a medium date and a short time (SV:1192-1198). An
 * unparsable value is shown as it came, never as "Invalid Date".
 *
 * @param {string} ts  protocol §1.1 timestamp
 * @param {string} [locale]
 * @param {string} [timeZone]  for tests; the session's zone otherwise
 */
export function dateText(ts, locale = undefined, timeZone = undefined) {
    const ms = Date.parse(ts);
    if (!Number.isFinite(ms))
        return String(ts ?? '');
    try {
        return new Intl.DateTimeFormat(locale, {dateStyle: 'medium', timeStyle: 'short', timeZone}).format(ms);
    } catch {
        return new Date(ms).toISOString();
    }
}

/**
 * `PokeTokenBar-Save-yyyy-MM-dd.json` on the local calendar day (ST:76-79).
 *
 * @param {number} nowMs
 * @param {string} [timeZone]  for tests; the session's zone otherwise
 */
export function exportFileName(nowMs, timeZone = undefined) {
    const parts = new Intl.DateTimeFormat('en-US', {year: 'numeric', month: '2-digit', day: '2-digit', timeZone})
        .formatToParts(nowMs);
    const get = type => parts.find(p => p.type === type)?.value ?? '';
    return `PokeTokenBar-Save-${get('year')}-${get('month')}-${get('day')}.json`;
}

/** Last path component, for the export toast. */
export function baseName(path) {
    const s = String(path ?? '');
    return s.slice(s.lastIndexOf('/') + 1) || s;
}

/** The toast after an export. */
export function exportDoneText(reply, path) {
    return fmt(_('Exported to %s'), baseName(reply?.path ?? path));
}

/** The toast after an import: LOC:844-853. */
export function importDoneText(reply) {
    return fmt(_('Imported — %d in Pokédex · %s lifetime'),
        reply?.summary?.dexCount ?? 0, compact(reply?.summary?.lifetimeTokens ?? 0));
}

/** The toast after a restore: LOC:980-989. */
export function restoreDoneText(reply) {
    return fmt(_('Restored — %d in Pokédex · %s lifetime'),
        reply?.summary?.dexCount ?? 0, compact(reply?.summary?.lifetimeTokens ?? 0));
}

/* ---- confirmations (ImportConfirmPolicy, STT:687-691) -------------------- */

/** Cancel is both the default and the close response; the destructive one never is. */
export const CONFIRM_POLICY = Object.freeze({defaultResponse: 'cancel', closeResponse: 'cancel'});

/**
 * The import confirmation from an `inspectSave` reply (LOC:777-799).
 *
 * @returns {{heading: string, body: string, confirmId: string, confirmLabel: string,
 *            defaultResponse: string, closeResponse: string}}
 */
export function importConfirm(inspect, locale = undefined, timeZone = undefined) {
    const lines = [
        fmt(_('Incoming save: %s'), summaryText(inspect?.incoming)),
        fmt(_('Exported: %s · %s'), dateText(inspect?.exportedAt, locale, timeZone), inspect?.sourceDevice ?? ''),
        fmt(_('This computer now: %s'), summaryText(inspect?.current)),
        '',
        _("This computer's current progress is replaced. The previous state is kept as a backup in ~/.local/state/poketokenbar (last 5)."),
    ];
    return {
        heading: _("Replace this computer's progress?"),
        body: lines.join('\n'),
        confirmId: 'replace',
        confirmLabel: _('Replace'),
        ...CONFIRM_POLICY,
    };
}

/**
 * The restore confirmation for a snapshot row (LOC:911-936). Upstream also
 * prints the current state; no `Save1` reply carries it outside
 * `inspectSave`, so that line is left out.
 */
export function restoreConfirm(snapshot, locale = undefined, timeZone = undefined) {
    const lines = [
        fmt(_('Target snapshot: %s'), summaryText(snapshot)),
        fmt(_('Created: %s'), dateText(snapshot?.date, locale, timeZone)),
        '',
        _('Your current state will be backed up as a new snapshot before restoring.'),
    ];
    return {
        heading: _('Restore this snapshot?'),
        body: lines.join('\n'),
        confirmId: 'restore',
        confirmLabel: _('Restore'),
        ...CONFIRM_POLICY,
    };
}

/* ---- snapshot list (text-only rows, decision 48) -------------------------- */

/** One expander child row: the date over `Pokédex {dex} · {tokens} lifetime` (LOC:1000-1009). */
export function snapshotRow(snapshot, locale = undefined, timeZone = undefined) {
    return {
        id: snapshot?.id ?? '',
        title: dateText(snapshot?.date, locale, timeZone),
        subtitle: fmt(_('Pokédex %d · %s lifetime'), snapshot?.dexCount ?? 0, compact(snapshot?.lifetimeTokens ?? 0)),
    };
}

/** The expander's subtitle, so a collapsed list still tells how fresh the newest restore point is. */
export function snapshotsSubtitle(snapshots, locale = undefined, timeZone = undefined) {
    if (!Array.isArray(snapshots) || snapshots.length === 0)
        return _('No snapshots saved yet');
    return fmt(_('Latest: %s'), dateText(snapshots[0]?.date, locale, timeZone));
}

/** The snapshot list of a `listSnapshots` reply: well-formed entries only, newest first as sent, at most 10. */
export function snapshotList(reply) {
    const list = Array.isArray(reply?.snapshots) ? reply.snapshots : [];
    return list.filter(s => typeof s?.id === 'string' && s.id !== '').slice(0, 10);
}
