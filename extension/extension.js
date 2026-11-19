/* PokeTokenBar for GNOME Shell 51.
 *
 * The extension renders and sends commands; the Go helper (bin/ptb-helper)
 * does all data work and speaks NDJSON over stdio (docs/protocol.md).
 *
 * Lifecycle: the shell disables extensions on every screen lock. Everything
 * created in enable() is destroyed in disable(), every GLib source is removed,
 * and signals are connected with connectObject() so one disconnectObject()
 * releases them. The only thing allowed to outlive disable() is the helper
 * client's 3 s force_exit reaper (protocol §2.5).
 */

import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import Shell from 'gi://Shell';

import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import {getLoginManager} from 'resource:///org/gnome/shell/misc/loginManager.js';
import {Extension, gettext, ngettext} from 'resource:///org/gnome/shell/extensions/extension.js';

import {HelperClient, HelperError} from './lib/helperClient.js';
import {setTranslations} from './lib/i18n.js';
import {Indicator} from './lib/indicator.js';
import {Notifier} from './lib/notifications.js';
import {
    FORWARDED_KEYS, forwardedSettings, intlFirstDay, languageFromNames, resolveWeekStart,
} from './lib/settingsForward.js';
import {SpriteResolver} from './lib/sprites.js';
import {ThemeVariant} from './lib/theme.js';
import {Tooltip} from './lib/tooltip.js';

const SETTINGS_DEBOUNCE_MS = 300; // protocol §2.2
const DESKTOP_CALENDAR_SCHEMA = 'org.gnome.desktop.calendar';
const LOG = 'PokeTokenBar';

export default class PokeTokenBarExtension extends Extension {
    enable() {
        setTranslations(gettext, ngettext);

        this._settings = this.getSettings();
        this._state = null;
        this._helperStatus = 'starting';
        this._debounceId = 0;
        this._lastConnectivity = null;

        this._theme = new ThemeVariant();
        this._tooltip = new Tooltip();
        this._theme.track(this._tooltip.actor);

        this._client = new HelperClient(this.path, {
            onHello: () => this._sendSettings(),
            onState: msg => this._onState(msg),
            onEvent: event => this._notifier?.handle(event),
            onStatus: status => this._onStatus(status),
        });
        this._sprites = new SpriteResolver(this._client);
        this._notifier = new Notifier({settings: this._settings, sprites: this._sprites, title: 'PokeTokenBar'});

        this._indicator = new Indicator(this);
        Main.panel.addToStatusArea(this.uuid, this._indicator);

        this._settings.connectObject('changed', (_s, key) => this._onSettingChanged(key), this);
        this._desktopCalendar = this._lookupDesktopCalendar();
        this._desktopCalendar?.connectObject('changed::week-start-day', () => this._scheduleSettings(), this);
        this._watchSystem();

        this._indicator.render();
        this._client.start();
    }

    disable() {
        this._client?.stop();
        this._client = null;

        if (this._debounceId) {
            GLib.source_remove(this._debounceId);
            this._debounceId = 0;
        }
        this._settings?.disconnectObject(this);
        this._desktopCalendar?.disconnectObject(this);
        getLoginManager().disconnectObject(this);
        this._networkMonitor?.disconnectObject(this);

        this._notifier?.destroy();
        this._indicator?.destroy();
        this._tooltip?.destroy();
        this._theme?.destroy();
        this._sprites?.clear();

        this._notifier = null;
        this._indicator = null;
        this._tooltip = null;
        this._theme = null;
        this._sprites = null;
        this._networkMonitor = null;
        this._desktopCalendar = null;
        this._settings = null;
        this._state = null;
    }

    /* ---- controller API used by the indicator and the popover ---------- */

    get settings() {
        return this._settings;
    }

    get tooltip() {
        return this._tooltip;
    }

    get sprites() {
        return this._sprites;
    }

    get theme() {
        return this._theme;
    }

    /** Everything a render needs, read at call time. */
    context() {
        return {
            state: this._state,
            settings: {
                limitMode: this._settings.get_string('limit-display-mode'),
                trendWindow: this._settings.get_string('daily-trend-window'),
                activityView: this._settings.get_string('activity-view'),
            },
            nowMs: Date.now(),
            helperStatus: this._helperStatus,
        };
    }

    /** Manual refresh (footer, limits row, right-click). Never rejects. */
    refresh() {
        if (!this._client)
            return Promise.resolve();
        return this._client.request('refresh', {manual: true}).then(() => undefined, e => {
            if (e?.code !== 'helperExited')
                console.warn(`${LOG}: refresh failed: ${e?.code ?? ''} ${e?.message ?? e}`);
        });
    }

    /**
     * A helper command for the views (Pokédex pages, species, representative).
     * Rejects with HelperError; callers must catch (every pending command
     * fails with helperExited on each helper exit and on disable).
     */
    request(cmd, args) {
        if (!this._client)
            return Promise.reject(new HelperError('helperExited', 'helper is not running'));
        return this._client.request(cmd, args);
    }

    openPrefs() {
        try {
            this.openPreferences();
        } catch (e) {
            console.error(`${LOG}: cannot open preferences: ${e}`);
        }
    }

    setTrendWindow(window) {
        this._settings.set_string('daily-trend-window', window);
    }

    setActivityView(view) {
        this._settings.set_string('activity-view', view);
    }

    /* ---- helper messages ---------------------------------------------- */

    _onState(msg) {
        this._state = msg;
        this._indicator?.render();
    }

    _onStatus(status) {
        this._helperStatus = status;
        this._indicator?.render();
    }

    /* ---- settings ------------------------------------------------------ */

    _onSettingChanged(key) {
        if (FORWARDED_KEYS.includes(key)) {
            this._scheduleSettings(); // the helper answers with a new state
            return;
        }
        if (key === 'animation-quality')
            this._indicator?.setAnimationQuality(this._settings.get_string(key));
        this._indicator?.render();
    }

    _scheduleSettings() {
        if (this._debounceId)
            GLib.source_remove(this._debounceId);
        this._debounceId = GLib.timeout_add(GLib.PRIORITY_DEFAULT, SETTINGS_DEBOUNCE_MS, () => {
            this._debounceId = 0;
            this._sendSettings();
            return GLib.SOURCE_REMOVE;
        });
    }

    _sendSettings() {
        if (!this._client || !this._settings)
            return;
        const language = languageFromNames(GLib.get_language_names());
        const weekStart = resolveWeekStart(this._settings.get_string('week-start'), {
            intlDay: intlFirstDay(language),
            desktopDay: this._desktopWeekStart(),
            shellWeekStart: Shell.util_get_week_start(),
        });
        const args = forwardedSettings(key => this._settings.get_value(key).recursiveUnpack(), language, weekStart);
        this._client.send('setSettings', args);
    }

    _lookupDesktopCalendar() {
        const schema = Gio.SettingsSchemaSource.get_default()?.lookup(DESKTOP_CALENDAR_SCHEMA, true);
        if (!schema?.has_key('week-start-day'))
            return null;
        return new Gio.Settings({schema_id: DESKTOP_CALENDAR_SCHEMA});
    }

    /** GNOME's own week start (1 = Monday … 7 = Sunday), or null for 'default'. */
    _desktopWeekStart() {
        const value = this._desktopCalendar?.get_enum('week-start-day') ?? 0;
        return value >= 1 && value <= 7 ? value : null;
    }

    /* ---- system events the helper cannot see (protocol §4) ------------- */

    _watchSystem() {
        getLoginManager().connectObject('prepare-for-sleep', (_lm, aboutToSuspend) => {
            if (!aboutToSuspend)
                this._client?.send('wake');
        }, this);

        this._networkMonitor = Gio.NetworkMonitor.get_default();
        this._lastConnectivity = this._networkMonitor.connectivity;
        this._networkMonitor.connectObject('notify::connectivity', () => {
            const now = this._networkMonitor.connectivity;
            if (now === Gio.NetworkConnectivity.FULL && this._lastConnectivity !== Gio.NetworkConnectivity.FULL)
                this._client?.send('networkUp');
            this._lastConnectivity = now;
        }, this);
    }
}
