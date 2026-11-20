/* St pieces shared by the Shop and Bag tabs: the card frame, the item icon
 * tile, the ×N stepper and the button styles of the inline confirmations
 * (bag-shop.md §3.1: no modal dialogs, the control row morphs in place).
 */

import Clutter from 'gi://Clutter';
import Gio from 'gi://Gio';
import St from 'gi://St';

import {_} from '../i18n.js';
import {hbox, label, vbox} from '../popover/widgets.js';
import {staticPngPath} from '../sprites.js';
import {itemSpriteName, timesText} from './shopModel.js';

export const ITEM_ICON_SIZE = 30;

/** A rounded card (radius 10, padding 10, a faint ground). */
export function card(extraClass = '') {
    return vbox(`ptb-shop-card ${extraClass}`.trim(), {x_expand: true});
}

/**
 * The 30 px item icon on a small tinted patch, like the companion tile's
 * ground. `tint` is an item id, an egg tier class or `egg`.
 */
export function iconTile(emoji, tint) {
    const tile = new St.Bin({
        style_class: `ptb-item-tile ptb-item-tint-${tint}`,
        y_align: Clutter.ActorAlign.START,
        child: new St.Label({text: emoji, style_class: 'ptb-item-emoji',
            x_align: Clutter.ActorAlign.CENTER, y_align: Clutter.ActorAlign.CENTER}),
    });
    return tile;
}

/**
 * Swap an item tile's emoji for its sprite once the helper has it. The emoji
 * stays for Mint (no sprite) and when the sprite is unavailable; the resolver
 * forgets the failure, so the next popover open retries. The sprite is a 30 px
 * St.Icon over a native ~30 px PNG (1:1), the same actor as the egg icon.
 */
export function loadItemSprite(tile, sprites, item, isAlive = () => true) {
    const name = itemSpriteName(item);
    if (name === null || !sprites)
        return;
    sprites.resolve({item: name}).then(data => {
        const png = staticPngPath(data.manifestPath);
        if (!png || !isAlive())
            return;
        tile.set_child(new St.Icon({
            gicon: Gio.FileIcon.new(Gio.File.new_for_path(png)),
            icon_size: ITEM_ICON_SIZE,
            style_class: 'ptb-item-sprite',
        }));
    }).catch(() => {});
}

/** A button in one of the control-row roles: `bordered`, `prominent`, `precious` or `flat`. */
export function controlButton(text, role = 'bordered') {
    const b = new St.Button({
        style_class: `ptb-control-button ptb-control-${role}`,
        label: text,
        can_focus: true,
        y_align: Clutter.ActorAlign.CENTER,
    });
    return b;
}

/** Disable a button visibly (St has no `sensitive`: reactive plus the pseudo class). */
export function setEnabled(button, enabled) {
    button.reactive = enabled;
    button.can_focus = enabled;
    if (enabled)
        button.remove_style_pseudo_class('insensitive');
    else
        button.add_style_pseudo_class('insensitive');
}

/**
 * A compact "− ×N +" stepper over 1…max. `onChange(n)` fires on each step.
 * The accessible name is the item's name (SV:81-88).
 */
export class Stepper {
    constructor(accessibleName, onChange) {
        this._value = 1;
        this._max = 1;
        this._onChange = onChange;
        this.actor = hbox('ptb-stepper', {y_align: Clutter.ActorAlign.CENTER, accessible_name: accessibleName});
        this._minus = this._button('list-remove-symbolic', _('Fewer'), -1);
        this._label = label('', 'ptb-stepper-value', {x_align: Clutter.ActorAlign.CENTER});
        this._plus = this._button('list-add-symbolic', _('More'), +1);
        this.actor.add_child(this._minus);
        this.actor.add_child(this._label);
        this.actor.add_child(this._plus);
        this._sync();
    }

    _button(icon, name, step) {
        const b = new St.Button({
            style_class: 'ptb-stepper-button',
            child: new St.Icon({icon_name: icon, style_class: 'ptb-stepper-icon'}),
            can_focus: true,
            accessible_name: name,
        });
        b.connect('clicked', () => this._step(step));
        return b;
    }

    _step(delta) {
        const next = Math.min(this._max, Math.max(1, this._value + delta));
        if (next === this._value)
            return;
        this._value = next;
        this._sync();
        this._onChange?.(next);
    }

    /** Set the value and range without firing onChange. */
    set(value, max) {
        this._max = Math.max(1, max);
        this._value = Math.min(this._max, Math.max(1, value));
        this._sync();
    }

    get value() {
        return this._value;
    }

    _sync() {
        this._label.text = timesText(this._value);
        setEnabled(this._minus, this._value > 1);
        setEnabled(this._plus, this._value < this._max);
    }
}
