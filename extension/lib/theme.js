/* Light/dark tracking for the extension's own colors.
 *
 * The shell picks a light or dark stylesheet at runtime. Our colors live in a
 * single stylesheet.css, with light overrides scoped under `.ptb-light`; this
 * class is toggled on each tracked root actor (indicator, popover, tooltip)
 * whenever the shell's style variant changes.
 */

import St from 'gi://St';

import * as Main from 'resource:///org/gnome/shell/ui/main.js';

export const LIGHT_CLASS = 'ptb-light';

export class ThemeVariant {
    constructor() {
        this._actors = new Set();
        this._light = Main.getStyleVariant() === 'light';
        St.Settings.get().connectObject('notify::color-scheme', () => this._sync(), this);
    }

    get isLight() {
        return this._light;
    }

    track(actor) {
        this._actors.add(actor);
        this._apply(actor);
        actor.connect('destroy', () => this._actors.delete(actor));
    }

    _apply(actor) {
        if (this._light)
            actor.add_style_class_name(LIGHT_CLASS);
        else
            actor.remove_style_class_name(LIGHT_CLASS);
    }

    _sync() {
        this._light = Main.getStyleVariant() === 'light';
        for (const actor of this._actors)
            this._apply(actor);
    }

    destroy() {
        St.Settings.get().disconnectObject(this);
        this._actors.clear();
    }
}
