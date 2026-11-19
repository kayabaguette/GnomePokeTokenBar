/* Bag tab (bag-shop.md §3.3): one card per owned item in the fixed order
 * Rare Candy, Mint, Shiny Charm, or the empty state with a sleeping Snorlax.
 *
 * Rare Candy has a ×N stepper and a preview from `planCandies` (protocol
 * §7.4), requested when the tab opens, on each step and whenever the
 * companion's progress changes. A successful use switches to Home, where the
 * companion card plays the "+XP" capsule or the Mint sparkle (§2.5). A failed
 * use stays here and shows why (§6 item 5).
 */

import Clutter from 'gi://Clutter';
import St from 'gi://St';

import {_} from '../i18n.js';
import {SpriteAnimator} from '../spriteAnimator.js';
import {hbox, label, spacer, vbox, wrapLabel} from '../popover/widgets.js';
import * as M from './shopModel.js';
import {Stepper, card, controlButton, iconTile, setEnabled} from './shopWidgets.js';

const SNORLAX = 143;
const EMPTY_SPRITE = 96;

class BagCard {
    /**
     * @param {string} item
     * @param {object} deps  {request, onUsed(feedback), planner: BagView}
     */
    constructor(entry, deps) {
        this._deps = deps;
        this._item = entry.item;
        this._count = 1;
        this._confirming = false;
        this._busy = false;
        this._error = null;
        this._controlsSignature = null;
        this._destroyed = false;

        this.actor = card(`ptb-bag-${entry.item}`);
        this.actor.connect('destroy', () => {
            this._destroyed = true;
        });

        const header = hbox('ptb-shop-card-header', {x_expand: true});
        header.add_child(iconTile(entry.emoji, entry.item));
        const info = vbox('ptb-shop-card-info', {x_expand: true});
        const nameRow = hbox('ptb-shop-name-row');
        nameRow.add_child(label(entry.name, 'ptb-shop-name'));
        this._countLabel = label('', 'ptb-shop-owned', {visible: !entry.passive});
        nameRow.add_child(this._countLabel);
        info.add_child(nameRow);
        info.add_child(wrapLabel(entry.description, 'ptb-secondary ptb-small ptb-shop-description'));
        header.add_child(info);

        this._stepper = new Stepper(entry.name, n => {
            this._count = n;
            this._error = null;
            this._render();
            this._deps.planner.requestPlan();
        });
        this._stepper.actor.y_align = Clutter.ActorAlign.START;
        this._stepper.actor.visible = false;
        header.add_child(this._stepper.actor);
        this.actor.add_child(header);

        this._preview = vbox('ptb-candy-preview', {visible: false});
        this.actor.add_child(this._preview);
        this._controls = hbox('ptb-shop-controls', {x_expand: true});
        this.actor.add_child(this._controls);
        this._errorLabel = wrapLabel('', 'ptb-warning-text ptb-tiny', {visible: false});
        this.actor.add_child(this._errorLabel);
    }

    get item() {
        return this._item;
    }

    /** The count the preview and the Use button refer to. */
    get count() {
        return this._count;
    }

    reset() {
        if (this._busy)
            return;
        this._confirming = false;
        this._error = null;
        this._render();
    }

    update(entry, active) {
        this._entry = entry;
        this._active = active;
        this._countLabel.text = M.timesText(entry.count);
        this._count = entry.showStepper ? M.clampCount(this._count, entry.maxUse) : 1;
        this._stepper.set(this._count, entry.maxUse);
        if (!entry.usable && !this._busy)
            this._confirming = false;
        this._render();
    }

    /** @param {object|null} plan  CandyPlan, or null for no preview */
    setPlan(plan) {
        this._preview.destroy_all_children();
        const lines = M.candyPreviewLines(plan);
        for (const l of lines)
            this._preview.add_child(wrapLabel(l.text, `ptb-tiny ${l.warning ? 'ptb-warning-text' : 'ptb-secondary'}`));
        this._preview.visible = lines.length > 0;
    }

    _render() {
        const entry = this._entry;
        if (!entry || this._destroyed)
            return;
        this._stepper.actor.visible = entry.showStepper && !this._confirming;
        const signature = JSON.stringify([entry.passive, entry.usable, entry.blockedText, this._confirming,
            this._count, this._busy, this._active?.displayName ?? null]);
        if (signature !== this._controlsSignature) {
            this._controlsSignature = signature;
            this._controls.destroy_all_children();
            this._buildControls(entry);
        }
        this._errorLabel.text = this._error ?? '';
        this._errorLabel.visible = Boolean(this._error);
    }

    _buildControls(entry) {
        if (entry.passive) {
            this._controls.add_child(new St.Icon({icon_name: 'object-select-symbolic', style_class: 'ptb-owned-seal'}));
            this._controls.add_child(label(M.effectHint(entry.item), 'ptb-owned-text'));
            this._controls.add_child(spacer());
            return;
        }
        if (!entry.usable) {
            this._controls.add_child(label(entry.blockedText ?? '', 'ptb-tertiary ptb-tiny'));
            return;
        }
        if (!this._confirming) {
            this._controls.add_child(label(M.effectHint(entry.item, this._count), 'ptb-tertiary ptb-tiny ptb-effect-hint'));
            this._controls.add_child(spacer());
            const use = controlButton(_('Use'), 'bordered');
            use.connect('clicked', () => {
                this._confirming = true;
                this._error = null;
                this._render();
            });
            this._controls.add_child(use);
            return;
        }
        this._controls.add_child(label(M.useConfirmText(this._active), 'ptb-secondary ptb-tiny ptb-confirm-text',
            {x_expand: true}));
        const commit = controlButton(M.useButtonLabel(entry.item, this._count), 'prominent');
        const cancel = controlButton(_('Cancel'), 'flat');
        if (this._busy) {
            setEnabled(commit, false);
            setEnabled(cancel, false);
        }
        commit.connect('clicked', () => this._onUse());
        cancel.connect('clicked', () => {
            this._confirming = false;
            this._render();
        });
        this._controls.add_child(commit);
        this._controls.add_child(cancel);
    }

    _onUse() {
        if (this._busy || !this._entry)
            return;
        const [cmd, args] = this._item === 'rareCandy' ? ['useCandy', {count: this._count}] : ['useMint', undefined];
        this._busy = true;
        this._error = null;
        this._render();
        this._deps.request(cmd, args).then(data => {
            // Using the last one removes this card (the state comes before the
            // reply), but Home must still play the feedback.
            if (!this._destroyed) {
                this._busy = false;
                this._confirming = false;
                this._count = 1;
                this._stepper.set(1, this._entry.maxUse);
                this._render();
                this._deps.planner.requestPlan();
            }
            if (!this._deps.planner.destroyed)
                this._deps.onUsed?.(M.feedbackFor(cmd, data));
        }, e => {
            if (this._destroyed)
                return;
            this._busy = false;
            this._confirming = false;
            this._error = M.errorText(e?.code);
            this._render();
            if (e?.code !== 'helperExited')
                console.warn(`PokeTokenBar: ${cmd} failed: ${e?.code ?? ''} ${e?.message ?? e}`);
        });
    }
}

export class BagView {
    /** @param {object} deps  {request, sprites, openShop(), onUsed(feedback)} */
    constructor(deps) {
        this._deps = deps;
        this._cards = new Map();
        this._listSignature = null;
        this._planSignature = null;
        this._planSeq = 0;
        this._visible = false;
        this._destroyed = false;

        this.actor = new St.ScrollView({
            style_class: 'ptb-scroll ptb-shop-scroll',
            hscrollbar_policy: St.PolicyType.NEVER,
            vscrollbar_policy: St.PolicyType.AUTOMATIC,
            overlay_scrollbars: true,
            x_expand: true,
        });
        this._content = vbox('ptb-bag', {x_expand: true});
        this.actor.set_child(this._content);

        this._list = vbox('ptb-bag-list', {x_expand: true});
        this._content.add_child(this._list);

        this._empty = vbox('ptb-bag-empty', {x_expand: true, visible: false});
        this._snorlax = new SpriteAnimator({boxHeight: EMPTY_SPRITE, maxWidth: EMPTY_SPRITE, fixedWidth: true,
            allowUpscale: true});
        this._empty.add_child(this._snorlax);
        this._empty.add_child(label(_('Your bag is empty!'), 'ptb-bag-empty-title',
            {x_align: Clutter.ActorAlign.CENTER}));
        const shop = controlButton(_('Shop'), 'bordered');
        shop.x_align = Clutter.ActorAlign.CENTER;
        shop.connect('clicked', () => deps.openShop?.());
        this._empty.add_child(shop);
        this._content.add_child(this._empty);
        this._snorlaxRequested = false;

        this.actor.connect('destroy', () => {
            this._destroyed = true;
            this._snorlax = null;
        });
    }

    get destroyed() {
        return this._destroyed;
    }

    setHeight(px) {
        this.actor.style = `height: ${px}px;`;
    }

    show() {
        this._visible = true;
        this.actor.show();
        this._planSignature = null; // the Bag opens: preview again
        this.requestPlan();
    }

    hide() {
        this._visible = false;
        this.actor.hide();
    }

    reset() {
        this.actor.vadjustment.value = 0;
        for (const c of this._cards.values())
            c.reset();
    }

    _loadSnorlax() {
        if (this._snorlaxRequested)
            return;
        this._snorlaxRequested = true;
        this._deps.sprites?.resolve({speciesId: SNORLAX, form: null, shiny: false, back: false, animated: true})
            .then(data => this._snorlax?.setSource({path: data.manifestPath}))
            .catch(() => {
                this._snorlaxRequested = false; // retry next time the empty state shows
            });
    }

    /** @param {object} ctx  {state} */
    update(ctx) {
        const companion = ctx.state?.companion ?? null;
        this._companion = companion;
        const entries = M.bagEntries(companion);
        const empty = entries.length === 0;
        this._empty.visible = empty;
        this._list.visible = !empty;
        if (empty)
            this._loadSnorlax();

        const signature = entries.map(e => e.item).join(',');
        if (signature !== this._listSignature) {
            this._listSignature = signature;
            const next = new Map();
            for (const e of entries)
                next.set(e.item, this._cards.get(e.item) ?? new BagCard(e, {...this._deps, planner: this}));
            for (const [item, c] of this._cards) {
                if (!next.has(item))
                    c.actor.destroy();
            }
            for (const c of next.values()) {
                if (c.actor.get_parent())
                    this._list.remove_child(c.actor);
            }
            for (const c of next.values())
                this._list.add_child(c.actor);
            this._cards = next;
            this._planSignature = null;
        }
        const active = companion?.phase === 'active' ? companion.active ?? null : null;
        for (const e of entries)
            this._cards.get(e.item).update(e, active);
        this.requestPlan();
    }

    /**
     * Ask for the candy preview when something it depends on changed: the
     * selected count, the stock and max, or the companion's progress.
     */
    requestPlan() {
        const candy = this._cards.get('rareCandy');
        if (!candy || !this._visible || this._destroyed)
            return;
        const c = this._companion;
        const a = c?.active;
        const maxUse = c?.bag?.candyMaxUse ?? 0;
        const signature = JSON.stringify([candy.count, maxUse, c?.inventory?.rareCandy, a?.speciesId,
            a?.stageIndex, a?.usedAtStage, a?.threshold]);
        if (signature === this._planSignature)
            return;
        this._planSignature = signature;
        const seq = ++this._planSeq;
        if (!(maxUse > 0)) {
            candy.setPlan(null);
            return;
        }
        this._deps.request('planCandies', {count: candy.count}).then(data => {
            if (this._destroyed || seq !== this._planSeq)
                return;
            this._cards.get('rareCandy')?.setPlan(data?.plan ?? null);
        }, e => {
            if (this._destroyed || seq !== this._planSeq)
                return;
            this._planSignature = null; // try again on the next change
            this._cards.get('rareCandy')?.setPlan(null);
            if (e?.code !== 'helperExited')
                console.warn(`PokeTokenBar: planCandies failed: ${e?.code ?? ''} ${e?.message ?? e}`);
        });
    }

    destroy() {
        this.actor.destroy();
    }
}
