/* St building blocks shared by the Pokédex views: static sprites, chips,
 * segmented control, pager, status box and the evolution strip.
 */

import Clutter from 'gi://Clutter';
import Gio from 'gi://Gio';
import St from 'gi://St';

import {Spinner} from 'resource:///org/gnome/shell/ui/animation.js';

import {_, fmt} from '../i18n.js';
import {spriteRequestKey, staticPngPath} from '../sprites.js';
import * as V from '../viewModel.js';
import {hbox, label, vbox, wrapLabel} from '../popover/widgets.js';
import * as D from './dexModel.js';

export const RARITIES = ['common', 'uncommon', 'rare', 'legendary'];

export function normRarity(r) {
    return RARITIES.includes(r) ? r : 'common';
}

/** Swap `<prefix><rarity>` style classes on an actor. */
export function setRarityClass(actor, prefix, rarity) {
    for (const r of RARITIES)
        actor.remove_style_class_name(`${prefix}${r}`);
    actor.add_style_class_name(`${prefix}${normRarity(rarity)}`);
}

export function setChecked(actor, checked) {
    if (checked)
        actor.add_style_pseudo_class('checked');
    else
        actor.remove_style_pseudo_class('checked');
}

export function setSensitive(actor, sensitive) {
    actor.reactive = sensitive;
    actor.can_focus = sensitive;
    if (sensitive)
        actor.remove_style_pseudo_class('insensitive');
    else
        actor.add_style_pseudo_class('insensitive');
}

/**
 * A static sprite fed by getSprite. The PNG is loaded by St's texture cache
 * through a Gio.FileIcon, asynchronously and cached per size; nothing is
 * decoded here.
 */
export class StaticSprite {
    constructor(sprites, size, styleClass = 'ptb-dex-sprite') {
        this._sprites = sprites;
        this._key = null;
        this._alive = true;
        this.actor = new St.Icon({
            icon_size: size,
            style_class: styleClass,
            x_align: Clutter.ActorAlign.CENTER,
            y_align: Clutter.ActorAlign.CENTER,
        });
        this.actor.connect('destroy', () => {
            this._alive = false;
        });
    }

    /** @param {object|null} req  SpriteRequest args (protocol §8.1) */
    set(req) {
        const key = req ? spriteRequestKey(req) : null;
        if (key === this._key)
            return;
        this._key = key;
        this.actor.gicon = null;
        if (!req)
            return;
        // Replies can land after the view is gone (every pending command
        // fails with helperExited on disable): check before touching the actor.
        this._sprites.resolve(req).then(data => {
            if (!this._alive || this._key !== key)
                return;
            const png = staticPngPath(data.manifestPath);
            this.actor.gicon = png ? Gio.FileIcon.new(Gio.File.new_for_path(png)) : null;
        }).catch(() => {});
    }
}

/** Two or more mutually exclusive buttons in a pill (Home/Pokédex tab look, smaller). */
export class Segmented {
    /**
     * @param {Array<[string, string]>} items  [key, label]
     * @param {(key: string) => void} onChange
     */
    constructor(items, onChange, styleClass = 'ptb-segmented') {
        this.actor = hbox(styleClass);
        this._buttons = new Map();
        this._value = null;
        for (const [key, text] of items) {
            const b = new St.Button({style_class: 'ptb-segment', label: text, can_focus: true, x_expand: true});
            b.connect('clicked', () => {
                if (this._value !== key) {
                    this.set(key);
                    onChange(key);
                }
            });
            this.actor.add_child(b);
            this._buttons.set(key, b);
        }
    }

    set(key) {
        this._value = key;
        for (const [k, b] of this._buttons)
            setChecked(b, k === key);
    }
}

/** ‹ 2 / 4 › with 28 px hit targets (ui.md §3.3 footer). */
export class Pager {
    constructor(onStep) {
        this.actor = hbox('ptb-pager', {x_align: Clutter.ActorAlign.END});
        this._prev = pagerButton('go-previous-symbolic', _('Previous page'));
        this._next = pagerButton('go-next-symbolic', _('Next page'));
        this._label = label('', 'ptb-pager-label');
        this._prev.connect('clicked', () => onStep(-1));
        this._next.connect('clicked', () => onStep(1));
        this.actor.add_child(this._prev);
        this.actor.add_child(this._label);
        this.actor.add_child(this._next);
    }

    set(page, count) {
        this._label.text = `${page} / ${count}`;
        setSensitive(this._prev, page > 1);
        setSensitive(this._next, page < count);
        // A single page hides the pager but keeps its row, so the grid does not jump.
        this.actor.opacity = count > 1 ? 255 : 0;
    }
}

function pagerButton(iconName, accessibleName) {
    return new St.Button({
        style_class: 'ptb-pager-button',
        child: new St.Icon({icon_name: iconName, style_class: 'ptb-pager-icon'}),
        can_focus: true,
        accessible_name: accessibleName,
    });
}

/**
 * Rarity filter chips in display order (legendary → common): color dot,
 * name, bold count; disabled at zero; the selected one is checked
 * (CompanionView.swift:1572-1590).
 */
export class RarityChips {
    constructor(tooltip, onToggle) {
        this.actor = hbox('ptb-chip-row', {x_expand: true});
        this._chips = new Map();
        for (const r of D.RARITY_ORDER) {
            const b = new St.Button({style_class: `ptb-chip ptb-rarity-chip ptb-rarity-${r}`, can_focus: true,
                x_expand: true});
            const box = hbox('ptb-chip-box', {x_align: Clutter.ActorAlign.CENTER});
            box.add_child(new St.Widget({style_class: 'ptb-chip-dot', y_align: Clutter.ActorAlign.CENTER}));
            const name = label(V.rarityName(r), 'ptb-chip-label');
            const count = label('0', 'ptb-chip-count');
            box.add_child(name);
            box.add_child(count);
            b.set_child(box);
            b.connect('clicked', () => onToggle(r));
            tooltip.attach(b, _('Show only this rarity; click again to show all'));
            this.actor.add_child(b);
            this._chips.set(r, {button: b, count});
        }
    }

    /** @param {object} counts  {legendary, rare, uncommon, common} */
    set(counts, selected) {
        for (const [r, {button, count}] of this._chips) {
            const n = Number(counts?.[r]) || 0;
            count.text = String(n);
            setChecked(button, selected === r);
            // A selected chip stays clickable so the filter can always be cleared.
            setSensitive(button, n > 0 || selected === r);
        }
    }
}

/** Loading, error and empty messages, centered in the body. */
export class StatusBox {
    constructor() {
        this.actor = vbox('ptb-dex-status', {
            x_expand: true, y_expand: true,
            x_align: Clutter.ActorAlign.FILL, y_align: Clutter.ActorAlign.CENTER,
            visible: false,
        });
        this._spinner = new Spinner(16, {hideOnStop: true});
        this._spinner.x_align = Clutter.ActorAlign.CENTER;
        this._icon = new St.Icon({style_class: 'ptb-dex-status-icon', x_align: Clutter.ActorAlign.CENTER});
        this._art = new St.Bin({x_align: Clutter.ActorAlign.CENTER});
        this._title = wrapLabel('', 'ptb-dex-status-title');
        this._body = wrapLabel('', 'ptb-secondary ptb-small ptb-dex-status-body');
        this._button = new St.Button({style_class: 'ptb-text-button', can_focus: true,
            x_align: Clutter.ActorAlign.CENTER});
        this._action = null;
        this._button.connect('clicked', () => this._action?.());
        for (const l of [this._title, this._body])
            l.clutter_text.line_alignment = 2; // Pango.Alignment.CENTER
        for (const a of [this._spinner, this._icon, this._art, this._title, this._body, this._button])
            this.actor.add_child(a);
    }

    /** @param {string} text */
    loading(text) {
        this._show({loading: true, title: text});
    }

    /**
     * @param {object} m  {icon?, art?: Clutter.Actor, title, body?, action?: {label, run}}
     */
    message(m) {
        this._show({loading: false, ...m});
    }

    _show({loading, icon = null, art = null, title = '', body = '', action = null}) {
        this.actor.show();
        if (loading)
            this._spinner.play();
        else
            this._spinner.stop();
        this._icon.visible = Boolean(icon);
        if (icon)
            this._icon.icon_name = icon;
        const old = this._art.get_child();
        if (old !== art) {
            this._art.set_child(art);
            old?.destroy();
        }
        this._art.visible = Boolean(art);
        this._title.text = title;
        this._title.visible = Boolean(title);
        this._body.text = body;
        this._body.visible = Boolean(body);
        this._action = action?.run ?? null;
        this._button.label = action?.label ?? '';
        this._button.visible = Boolean(action);
    }

    hide() {
        this._spinner.stop();
        this.actor.hide();
    }
}

/**
 * Evolution strip: static thumbnails with arrows between; known species link
 * to their detail, the "?" node of an undecided branch does not.
 *
 * @param {object} deps  {sprites, tooltip}
 * @param {object[]} nodes  [{speciesId|null, form, displayName, reached?, current?}]
 * @param {object} opts  {size, shiny, names, onOpen(node)}
 */
export function evoStrip(deps, nodes, {size = 40, shiny = false, names = false, onOpen = null} = {}) {
    const strip = hbox('ptb-dex-evo', {x_align: Clutter.ActorAlign.START});
    nodes.forEach((node, i) => {
        if (i > 0)
            strip.add_child(label('→', 'ptb-dex-evo-arrow ptb-tertiary', {y_align: Clutter.ActorAlign.CENTER}));
        const col = vbox('ptb-dex-evo-node-box');
        const tile = new St.Button({
            style_class: 'ptb-dex-evo-node',
            can_focus: Boolean(onOpen && node.speciesId),
            reactive: Boolean(onOpen && node.speciesId),
            x_align: Clutter.ActorAlign.CENTER,
            width: size + 8,
            height: size + 8,
        });
        if (node.current)
            tile.add_style_class_name('ptb-dex-evo-current');
        if (node.reached === false)
            tile.opacity = 115;
        if (!node.speciesId) {
            tile.set_child(label('?', 'ptb-evo-mystery', {x_align: Clutter.ActorAlign.CENTER}));
            deps.tooltip.attach(tile, _('Next form not decided yet'));
        } else {
            const sprite = new StaticSprite(deps.sprites, size);
            sprite.set({speciesId: node.speciesId, form: node.form ?? null, shiny: Boolean(shiny), back: false,
                animated: false});
            tile.set_child(sprite.actor);
            const name = node.displayName ?? `#${node.speciesId}`;
            deps.tooltip.attach(tile, onOpen ? fmt(_('Open %s in the Pokédex'), name) : name);
            if (onOpen)
                tile.connect('clicked', () => onOpen(node));
        }
        col.add_child(tile);
        if (names) {
            const n = label(node.displayName ?? (node.speciesId ? `#${node.speciesId}` : '?'),
                'ptb-dex-evo-name ptb-secondary', {x_align: Clutter.ActorAlign.CENTER});
            n.clutter_text.line_alignment = 2;
            col.add_child(n);
        }
        strip.add_child(col);
    });
    return strip;
}
