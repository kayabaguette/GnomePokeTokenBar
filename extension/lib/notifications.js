/* Notifications for helper events (ui.md §5, protocol §6).
 *
 * One MessageTray.Source for the extension, created on first use. Companion
 * events are gated by `companion-notifications`, limit events by
 * `limit-notifications`. A critical limit alert uses Urgency.HIGH and plays a
 * sound; warnings are silent. The expired-sign-in event and candy grants are
 * LOW (listed, no banner): the popover banner already covers the sign-in, and
 * candies cannot be spent before the Bag ships. Pokémon events use the
 * species' static sprite as icon when the helper has it at hand.
 */

import Gio from 'gi://Gio';

import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import * as MessageTray from 'resource:///org/gnome/shell/ui/messageTray.js';

import {staticPngPath} from './sprites.js';
import * as V from './viewModel.js';

const ICON_WAIT_MS = 3000;
const SOURCE_ICON = 'applications-games-symbolic';
const CRIT_SOUND = 'message-new-instant';
const LOG = 'PokeTokenBar';

function urgencyFor(event) {
    switch (event.kind) {
    case 'limitAlert':
        return event.tier === 'crit' ? MessageTray.Urgency.HIGH : MessageTray.Urgency.NORMAL;
    case 'authExpired':
    case 'candyGranted':
        return MessageTray.Urgency.LOW;
    default:
        return MessageTray.Urgency.NORMAL;
    }
}

function iconRequest(event) {
    switch (event.kind) {
    case 'hatch':
    case 'dittoReveal':
    case 'evolve':
    case 'graduate':
        if (!Number.isInteger(event.speciesId))
            return null;
        return {speciesId: event.speciesId, form: event.form ?? null, shiny: Boolean(event.isShiny),
            back: false, animated: false};
    default:
        return null;
    }
}

export class Notifier {
    /**
     * @param {object} deps  {settings: Gio.Settings, sprites: SpriteResolver, title: string}
     */
    constructor(deps) {
        this._deps = deps;
        this._source = null;
        this._closed = false;
    }

    /** Handle one protocol event. */
    handle(event) {
        if (this._closed)
            return;
        if (event?.kind === 'error') {
            console.warn(`${LOG}: helper reported ${event.code}: ${event.message}`);
            return;
        }
        const text = V.notificationText(event);
        if (!text)
            return;
        const gate = V.notificationGate(event.kind);
        if (gate && !this._deps.settings.get_boolean(gate))
            return;

        const post = gicon => this._post(text, urgencyFor(event), gicon,
            event.kind === 'limitAlert' && event.tier === 'crit');
        const req = iconRequest(event);
        if (!req) {
            post(null);
            return;
        }
        this._deps.sprites.resolve(req, {timeoutMs: ICON_WAIT_MS}).then(data => {
            const png = staticPngPath(data.manifestPath);
            post(png ? Gio.FileIcon.new(Gio.File.new_for_path(png)) : null);
        }).catch(() => post(null));
    }

    _ensureSource() {
        if (!this._source) {
            this._source = new MessageTray.Source({
                title: this._deps.title,
                iconName: SOURCE_ICON,
            });
            this._source.connect('destroy', () => {
                this._source = null;
            });
            Main.messageTray.add(this._source);
        }
        return this._source;
    }

    _post({title, body}, urgency, gicon, withSound) {
        if (this._closed)
            return; // also an icon lookup that settles after close()
        try {
            const source = this._ensureSource();
            const params = {source, title, body, urgency};
            if (gicon)
                params.gicon = gicon;
            const notification = new MessageTray.Notification(params);
            if (withSound)
                notification.sound = new MessageTray.Sound(null, CRIT_SOUND);
            source.addNotification(notification);
        } catch (e) {
            console.error(`${LOG}: cannot post a notification: ${e}`);
        }
    }

    /**
     * Refuse every post from now on and leave the message tray alone: at shell exit the tray and
     * the date menu are already destroyed, and destroying our source would reach them too.
     */
    close() {
        this._closed = true;
    }

    destroy() {
        this.close();
        this._source?.destroy();
        this._source = null;
    }
}
