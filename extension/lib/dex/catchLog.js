/* Catch log page (ui.md §3.5): rarity chips, one card per individual (rarity
 * and status badges, nature, the evolution chain with names, catch date) and
 * the pager. Pages of 10, so at most 10 cards exist at a time.
 */

import Clutter from 'gi://Clutter';
import GLib from 'gi://GLib';
import St from 'gi://St';

import {_} from '../i18n.js';
import {parseTs} from '../format.js';
import * as V from '../viewModel.js';
import {hbox, label, spacer, vbox} from '../popover/widgets.js';
import * as D from './dexModel.js';
import {Pager, RarityChips, evoStrip, setRarityClass} from './dexWidgets.js';

const CHAIN_SPRITE = 44;

export class CatchLog {
    /**
     * @param {object} deps      {tooltip, sprites}
     * @param {object} handlers  {onOpen(entry, node), onStep(step), onRarity(key), onReset()}
     */
    constructor(deps, handlers) {
        this._deps = deps;
        this._handlers = handlers;
        this._cards = new Map(); // entry id → card actor
        this._scrollId = 0;
        this._destroyed = false;

        this.actor = vbox('ptb-dex-log-page', {x_expand: true, y_expand: true});
        this._rarity = new RarityChips(deps.tooltip, r => handlers.onRarity(r));
        this.actor.add_child(this._rarity.actor);

        this._list = vbox('ptb-log-list', {x_expand: true});
        this._scroll = new St.ScrollView({
            style_class: 'ptb-log-scroll',
            hscrollbar_policy: St.PolicyType.NEVER,
            vscrollbar_policy: St.PolicyType.AUTOMATIC,
            overlay_scrollbars: true,
            x_expand: true,
            y_expand: true,
            child: this._list,
        });

        this._noResults = vbox('ptb-dex-no-results', {
            x_expand: true, y_expand: true, y_align: Clutter.ActorAlign.CENTER, visible: false,
        });
        this._noResults.add_child(new St.Icon({icon_name: 'system-search-symbolic', style_class: 'ptb-dex-status-icon',
            x_align: Clutter.ActorAlign.CENTER}));
        this._noResults.add_child(label(_('No Pokémon found'), 'ptb-dex-status-title',
            {x_align: Clutter.ActorAlign.CENTER}));
        const reset = new St.Button({style_class: 'ptb-text-button', label: _('Reset filters'), can_focus: true,
            x_align: Clutter.ActorAlign.CENTER});
        reset.connect('clicked', () => handlers.onReset());
        this._noResults.add_child(reset);

        const body = new St.Widget({layout_manager: new Clutter.BinLayout(), x_expand: true, y_expand: true});
        body.add_child(this._scroll);
        body.add_child(this._noResults);
        this.actor.add_child(body);

        this._pager = new Pager(step => handlers.onStep(step));
        this.actor.add_child(this._pager.actor);

        this.actor.connect('destroy', () => {
            this._destroyed = true;
            this._cancelScroll();
        });
    }

    /**
     * @param {object} data     getCatchLog reply data
     * @param {object} filters  dexModel filters
     * @param {number} nowMs
     */
    setData(data, filters, nowMs) {
        this._rarity.set(data.rarityCounts, filters.rarity);
        this._cancelScroll();
        this._list.destroy_all_children();
        this._cards.clear();
        for (const entry of data.entries ?? []) {
            const card = this._card(entry, nowMs);
            this._cards.set(entry.id, card);
            this._list.add_child(card);
        }
        const empty = (data.total ?? 0) === 0;
        this._scroll.visible = !empty;
        this._noResults.visible = empty;
        this._scroll.vadjustment.value = 0;
        const page = D.logPageOf(data.offset ?? 0);
        this._pager.set(page, D.logPageCount(data.total));
    }

    _card(entry, nowMs) {
        const card = vbox('ptb-log-card', {x_expand: true});
        const top = hbox('ptb-log-top', {x_expand: true});
        const rarity = label(V.rarityName(entry.rarity).toUpperCase(), 'ptb-badge');
        setRarityClass(rarity, 'ptb-rarity-', entry.rarity);
        top.add_child(rarity);
        if (entry.isRaising)
            top.add_child(label(_('RAISING'), 'ptb-badge ptb-badge-accent'));
        else if (entry.released)
            top.add_child(label(_('RELEASED'), 'ptb-badge ptb-badge-muted'));
        if (entry.isShiny) {
            const shiny = label('✨', 'ptb-log-shiny');
            this._deps.tooltip.attach(shiny, _('Shiny'));
            top.add_child(shiny);
        }
        top.add_child(spacer());
        const nature = V.natureName(entry.nature);
        if (nature)
            top.add_child(label(nature, 'ptb-secondary ptb-small'));
        card.add_child(top);

        const chain = (entry.chain ?? []).length > 0
            ? entry.chain
            : [{speciesId: entry.speciesId, form: entry.form ?? null, displayName: entry.displayName}];
        card.add_child(evoStrip(this._deps, chain.map(n => ({...n, reached: true})), {
            size: CHAIN_SPRITE,
            shiny: entry.isShiny,
            names: true,
            onOpen: node => this._handlers.onOpen(entry, node),
        }));

        const caughtMs = parseTs(entry.caughtAt);
        if (caughtMs !== null) {
            const when = label(D.caughtText(caughtMs, nowMs), 'ptb-tertiary ptb-small');
            this._deps.tooltip.attach(when, () => new Date(caughtMs).toLocaleString());
            card.add_child(when);
        } else if (entry.isRaising) {
            card.add_child(label(_('Being raised now'), 'ptb-tertiary ptb-small'));
        }
        return card;
    }

    /** Bring an entry's card into view (Back from a detail opened from this row). */
    revealEntry(id) {
        const card = this._cards.get(id);
        if (!card)
            return;
        this._cancelScroll();
        // Allocation is known only after a layout pass: check every 50 ms, a
        // few times (the Swift view also retried after 50 ms).
        let tries = 0;
        this._scrollId = GLib.timeout_add(GLib.PRIORITY_DEFAULT, 50, () => {
            if (this._destroyed || !card.get_parent()) {
                this._scrollId = 0;
                return GLib.SOURCE_REMOVE;
            }
            if (!card.has_allocation() && ++tries < 5)
                return GLib.SOURCE_CONTINUE;
            this._scrollId = 0;
            const adj = this._scroll.vadjustment;
            const box = card.get_allocation_box();
            const target = box.y1 - (adj.page_size - (box.y2 - box.y1)) / 2;
            adj.value = Math.max(adj.lower, Math.min(target, adj.upper - adj.page_size));
            return GLib.SOURCE_REMOVE;
        });
    }

    _cancelScroll() {
        if (this._scrollId) {
            GLib.source_remove(this._scrollId);
            this._scrollId = 0;
        }
    }
}
