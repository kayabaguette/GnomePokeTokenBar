/* Shop tab (bag-shop.md §3.2): the wallet header, then one card per
 * `companion.shop.entries` item or egg, in the helper's order.
 *
 * Every confirmation morphs the card's control row in place (§3.1); the
 * panel menu never opens a dialog. Cards keep their own state (selected
 * count, confirmation step, pending command, last error) across the state
 * updates that re-render the tab, and are rebuilt only when the list of
 * entries changes.
 */

import Clutter from 'gi://Clutter';
import Gio from 'gi://Gio';
import St from 'gi://St';

import {_} from '../i18n.js';
import {compact} from '../format.js';
import {rarityName} from '../viewModel.js';
import {staticPngPath} from '../sprites.js';
import {hbox, label, spacer, vbox, wrapLabel} from '../popover/widgets.js';
import * as M from './shopModel.js';
import {Stepper, card, controlButton, iconTile, setEnabled} from './shopWidgets.js';

const EGG_ICON_SIZE = 26;

class ShopCard {
    /**
     * @param {object} entry  a shopModel.shopEntries() item
     * @param {object} deps   {request, sprites, onEggBought()}
     */
    constructor(entry, deps) {
        this._deps = deps;
        this._key = entry.key;
        this._count = 1;
        this._step = 'idle'; // idle | confirm | precious
        this._busy = false;
        this._error = null;
        this._controlsSignature = null;
        this._destroyed = false;

        this.actor = card(entry.kind === 'egg' ? 'ptb-shop-egg' : 'ptb-shop-item');
        this.actor.connect('destroy', () => {
            this._destroyed = true;
        });

        const header = hbox('ptb-shop-card-header', {x_expand: true});
        const tint = entry.kind === 'egg' ? `egg-${entry.tier ?? 'plain'}` : entry.item;
        this._icon = iconTile(entry.emoji, tint);
        header.add_child(this._icon);

        const info = vbox('ptb-shop-card-info', {x_expand: true});
        const nameRow = hbox('ptb-shop-name-row');
        this._name = label('', 'ptb-shop-name');
        nameRow.add_child(this._name);
        this._badge = label('', 'ptb-badge ptb-shop-tier-badge', {visible: false});
        nameRow.add_child(this._badge);
        this._owned = label('', 'ptb-shop-owned', {visible: false});
        nameRow.add_child(this._owned);
        info.add_child(nameRow);
        this._description = wrapLabel('', 'ptb-secondary ptb-small ptb-shop-description');
        info.add_child(this._description);
        this._releaseNote = wrapLabel('', 'ptb-tertiary ptb-tiny ptb-shop-note', {visible: false});
        info.add_child(this._releaseNote);
        header.add_child(info);

        this._stepper = new Stepper(entry.name, n => {
            this._count = n;
            this._error = null;
            this._render();
        });
        this._stepper.actor.y_align = Clutter.ActorAlign.START;
        this._stepper.actor.visible = false;
        header.add_child(this._stepper.actor);
        this.actor.add_child(header);

        this._controls = hbox('ptb-shop-controls', {x_expand: true});
        this.actor.add_child(this._controls);
        this._hint = wrapLabel('', 'ptb-tertiary ptb-tiny', {visible: false});
        this.actor.add_child(this._hint);
        this._errorLabel = wrapLabel('', 'ptb-warning-text ptb-tiny', {visible: false});
        this.actor.add_child(this._errorLabel);

        if (entry.kind === 'egg')
            this._loadEggIcon();
    }

    get key() {
        return this._key;
    }

    _loadEggIcon() {
        this._deps.sprites?.resolve({egg: true}).then(data => {
            const png = staticPngPath(data.manifestPath);
            if (!png || this._destroyed)
                return;
            this._icon.set_child(new St.Icon({
                gicon: Gio.FileIcon.new(Gio.File.new_for_path(png)),
                icon_size: EGG_ICON_SIZE,
                style_class: 'ptb-item-sprite',
            }));
        }).catch(() => {}); // the emoji stays
    }

    /** Back to idle, error cleared (the popover reopened). */
    reset() {
        if (this._busy)
            return;
        this._step = 'idle';
        this._error = null;
        this._render();
    }

    /**
     * @param {object} entry      the current display entry
     * @param {object|null} active state.companion.active
     */
    update(entry, active) {
        this._entry = entry;
        this._active = active;
        this._name.text = entry.name;
        this._description.text = entry.description;
        if (entry.kind === 'egg' && entry.tier) {
            this._badge.text = rarityName(entry.tier).toUpperCase();
            this._badge.style_class = `ptb-badge ptb-shop-tier-badge ptb-rarity-${entry.tier}`;
            this._badge.show();
        }
        this._owned.text = entry.showOwned ? `${_('Owned')} ${M.timesText(entry.owned)}` : '';
        this._owned.visible = entry.showOwned;
        this._releaseNote.text = _('A released Pokémon stays in your Pokédex and can hatch again at the same odds — only the growth progress is lost.');
        this._releaseNote.visible = entry.kind === 'egg' && Boolean(active);

        // The wallet moved: keep the selection within what can be bought (SV:102-104).
        this._count = entry.showStepper ? M.clampCount(this._count, entry.maxCount) : 1;
        this._stepper.set(this._count, entry.maxCount);
        this._stepper.actor.visible = entry.showStepper && this._step === 'idle';
        // A confirmation the entry no longer allows falls back to idle.
        if (this._step !== 'idle' && !this._busy && M.idleControl(entry) !== 'buy')
            this._step = 'idle';
        this._render();
    }

    _render() {
        const entry = this._entry;
        if (!entry || this._destroyed)
            return;
        this._stepper.actor.visible = entry.showStepper && this._step === 'idle';
        const control = M.idleControl(entry);
        const highValue = M.isHighValueCompanion(this._active);
        const signature = JSON.stringify([this._step, control, this._count, entry.price, this._busy,
            highValue, this._active?.displayName ?? null, this._active?.rarity ?? null]);
        if (signature !== this._controlsSignature) {
            this._controlsSignature = signature;
            this._controls.destroy_all_children();
            if (this._step === 'idle')
                this._buildIdle(entry, control);
            else
                this._buildConfirm(entry);
        }
        this._hint.text = _('Available once your current egg hatches.');
        this._hint.visible = control === 'locked';
        this._errorLabel.text = this._error ?? '';
        this._errorLabel.visible = Boolean(this._error);
    }

    _buildIdle(entry, control) {
        if (control === 'owned') {
            this._controls.add_child(new St.Icon({icon_name: 'object-select-symbolic', style_class: 'ptb-owned-seal'}));
            this._controls.add_child(label(_('Owned'), 'ptb-owned-text'));
            this._controls.add_child(spacer());
            return;
        }
        this._controls.add_child(label(_('Price'), 'ptb-tertiary ptb-tiny ptb-price-label'));
        this._controls.add_child(label(compact(M.totalPrice(entry.price, this._count)), 'ptb-price-value'));
        this._controls.add_child(spacer());
        if (control === 'poor') {
            this._controls.add_child(label(_('Not enough tokens'), 'ptb-tertiary ptb-tiny'));
            return;
        }
        const buy = controlButton(_('Buy'), 'bordered');
        if (control === 'locked') {
            setEnabled(buy, false);
        } else {
            buy.connect('clicked', () => {
                this._step = 'confirm';
                this._error = null;
                this._render();
            });
        }
        this._controls.add_child(buy);
    }

    _buildConfirm(entry) {
        let prompt;
        let commitLabel;
        let role = 'prominent';
        if (entry.kind === 'item') {
            prompt = M.buyConfirmText(entry.name, this._count);
            commitLabel = M.buyButtonLabel(this._count);
        } else if (this._step === 'precious') {
            const p = M.preciousConfirm(this._active);
            prompt = p.warning;
            commitLabel = p.button;
            role = 'precious';
        } else {
            prompt = M.eggConfirmText(this._active, entry.tier);
            commitLabel = _('Buy');
        }
        const text = entry.kind === 'item'
            ? label(prompt, 'ptb-secondary ptb-tiny ptb-confirm-text', {x_expand: true})
            : wrapLabel(prompt, `ptb-tiny ptb-confirm-text ${role === 'precious' ? 'ptb-precious-text' : 'ptb-secondary'}`);
        this._controls.add_child(text);
        const commit = controlButton(commitLabel, role);
        const cancel = controlButton(_('Cancel'), 'flat');
        if (this._busy) {
            setEnabled(commit, false);
            setEnabled(cancel, false);
        }
        commit.connect('clicked', () => this._onCommit());
        cancel.connect('clicked', () => {
            this._step = 'idle';
            this._render();
        });
        this._controls.add_child(commit);
        this._controls.add_child(cancel);
    }

    _onCommit() {
        const entry = this._entry;
        if (this._busy || !entry)
            return;
        if (entry.kind === 'egg') {
            const next = M.nextEggStep(this._step, this._active);
            if (next === 'precious') {
                this._step = 'precious';
                this._render();
                return;
            }
        }
        this._busy = true;
        this._error = null;
        this._render();
        const [cmd, args] = entry.kind === 'egg'
            ? ['buyEgg', {tier: entry.tier}]
            : ['buyItem', {item: entry.item, count: this._count}];
        this._deps.request(cmd, args).then(() => {
            if (this._destroyed)
                return;
            this._busy = false;
            this._step = 'idle';
            this._count = 1; // SV:148-151
            this._stepper.set(1, entry.maxCount);
            this._render();
            if (entry.kind === 'egg')
                this._deps.onEggBought?.();
        }, e => {
            if (this._destroyed)
                return;
            this._busy = false;
            this._step = 'idle';
            this._error = M.errorText(e?.code);
            this._render();
            if (e?.code !== 'helperExited')
                console.warn(`PokeTokenBar: ${cmd} failed: ${e?.code ?? ''} ${e?.message ?? e}`);
        });
    }
}

export class ShopView {
    /** @param {object} deps  {request, sprites, onEggBought()} */
    constructor(deps) {
        this._deps = deps;
        this._cards = new Map();
        this._listSignature = null;

        this.actor = new St.ScrollView({
            style_class: 'ptb-scroll ptb-shop-scroll',
            hscrollbar_policy: St.PolicyType.NEVER,
            vscrollbar_policy: St.PolicyType.AUTOMATIC,
            overlay_scrollbars: true,
            x_expand: true,
        });
        this._content = vbox('ptb-shop', {x_expand: true});
        this.actor.set_child(this._content);

        const wallet = vbox('ptb-wallet', {x_expand: true});
        wallet.add_child(label(_('Spendable tokens'), 'ptb-caption ptb-wallet-caption'));
        this._wallet = label('—', 'ptb-wallet-number');
        wallet.add_child(this._wallet);
        wallet.add_child(wrapLabel(_('Spend the tokens you’ve used on items.'), 'ptb-tertiary ptb-tiny'));
        this._content.add_child(wallet);

        this._list = vbox('ptb-shop-list', {x_expand: true});
        this._content.add_child(this._list);
    }

    setHeight(px) {
        this.actor.style = `height: ${px}px;`;
    }

    show() {
        this.actor.show();
    }

    hide() {
        this.actor.hide();
    }

    /** The popover reopened: top of the list, confirmations and errors cleared. */
    reset() {
        this.actor.vadjustment.value = 0;
        for (const c of this._cards.values())
            c.reset();
    }

    /** @param {object} ctx  {state} */
    update(ctx) {
        const companion = ctx.state?.companion ?? null;
        this._wallet.text = typeof companion?.wallet === 'number' ? compact(companion.wallet) : '—';
        const entries = M.shopEntries(companion);
        const signature = entries.map(e => e.key).join(',');
        if (signature !== this._listSignature) {
            this._listSignature = signature;
            // Keep the cards that stay (their count and step), in the new order.
            const next = new Map();
            for (const e of entries)
                next.set(e.key, this._cards.get(e.key) ?? new ShopCard(e, this._deps));
            for (const [key, c] of this._cards) {
                if (!next.has(key))
                    c.actor.destroy();
            }
            for (const c of next.values()) {
                if (c.actor.get_parent())
                    this._list.remove_child(c.actor);
            }
            for (const c of next.values())
                this._list.add_child(c.actor);
            this._cards = next;
        }
        const active = companion?.phase === 'active' ? companion.active ?? null : null;
        for (const e of entries)
            this._cards.get(e.key).update(e, active);
    }

    destroy() {
        this.actor.destroy();
    }
}
