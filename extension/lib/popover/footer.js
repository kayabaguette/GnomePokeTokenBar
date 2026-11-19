/* Footer (ui.md §2.2): refresh button and "Updated …" on the Home tab, an
 * orange triangle whose tooltip lists ongoing errors, the floating-pet eye
 * toggle and settings on the right (PopoverView.swift:1133-1141).
 */

import St from 'gi://St';

import {_, fmt} from '../i18n.js';
import {parseTs, relativeAgo} from '../format.js';
import * as V from '../viewModel.js';
import {hbox, iconButton, label, spacer} from './widgets.js';

export class Footer {
    /** @param {object} deps  {tooltip, refresh(): Promise, openPrefs(), petEnabled(), setPetEnabled(on)} */
    constructor(deps) {
        this._deps = deps;
        this._busy = false;
        this._destroyed = false;
        this.actor = hbox('ptb-footer', {x_expand: true});

        this._homeOnly = hbox('ptb-footer-home');
        this._refresh = iconButton('view-refresh-symbolic', 'ptb-icon-button', _('Refresh now'));
        deps.tooltip.attach(this._refresh, _('Refresh now'));
        this._refresh.connect('clicked', () => this._onRefresh());
        this._homeOnly.add_child(this._refresh);
        this._updated = label('', 'ptb-tertiary ptb-small');
        this._homeOnly.add_child(this._updated);
        this._error = new St.Icon({
            icon_name: 'dialog-warning-symbolic',
            style_class: 'ptb-footer-error',
            reactive: true,
        });
        this._errorText = null;
        deps.tooltip.attach(this._error, () => this._errorText);
        this._homeOnly.add_child(this._error);
        this.actor.add_child(this._homeOnly);

        this.actor.add_child(spacer());

        // The eye shows the pet's current state; a click flips it.
        this._eye = iconButton('view-conceal-symbolic', 'ptb-icon-button ptb-pet-eye', _('Show floating pet'));
        deps.tooltip.attach(this._eye, () => this._eyeText());
        this._eye.connect('clicked', () => {
            deps.setPetEnabled(!deps.petEnabled());
            this._syncEye();
        });
        this.actor.add_child(this._eye);
        this._syncEye();

        const settings = iconButton('emblem-system-symbolic', 'ptb-icon-button', _('Settings'));
        deps.tooltip.attach(settings, _('Settings'));
        settings.connect('clicked', () => deps.openPrefs());
        this.actor.add_child(settings);
        this.actor.connect('destroy', () => {
            this._destroyed = true;
        });
    }

    _onRefresh() {
        if (this._busy)
            return;
        this._busy = true;
        this._refresh.reactive = false;
        this._refresh.add_style_pseudo_class('insensitive');
        this._deps.refresh().finally(() => {
            this._busy = false;
            if (this._destroyed)
                return;
            this._refresh.reactive = true;
            this._refresh.remove_style_pseudo_class('insensitive');
        });
    }

    _eyeText() {
        return this._deps.petEnabled() ? _('Hide floating pet') : _('Show floating pet');
    }

    _syncEye() {
        const on = this._deps.petEnabled();
        this._eye.child.icon_name = on ? 'view-reveal-symbolic' : 'view-conceal-symbolic';
        this._eye.accessible_name = this._eyeText();
        if (on)
            this._eye.add_style_pseudo_class('checked');
        else
            this._eye.remove_style_pseudo_class('checked');
        this._deps.tooltip.setText(this._eye, () => this._eyeText());
    }

    setHomeVisible(visible) {
        this._homeOnly.visible = visible;
    }

    /** @param {object} ctx  {state, nowMs, helperStatus} */
    update(ctx) {
        const meta = ctx.state?.meta;
        const lastMs = parseTs(meta?.lastUpdated);
        if (lastMs !== null) {
            this._updated.text = fmt(_('Updated %s'), relativeAgo(lastMs, ctx.nowMs));
        } else if (ctx.state) {
            this._updated.text = _('Reading usage…');
        } else {
            this._updated.text = _('Starting…');
        }
        if (meta?.isStale && lastMs !== null)
            this._updated.add_style_class_name('ptb-orange-text');
        else
            this._updated.remove_style_class_name('ptb-orange-text');

        const lines = [];
        const helper = V.helperStatusText(ctx.helperStatus);
        if (helper)
            lines.push(helper);
        for (const e of meta?.errors ?? [])
            lines.push(V.metaErrorText(e));
        this._syncEye();
        this._errorText = lines.join('\n');
        this._error.visible = lines.length > 0;
        this._deps.tooltip.setText(this._error, () => this._errorText);
    }
}
