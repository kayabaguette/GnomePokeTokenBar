/* Species detail (ui.md §3.4, PokemonDetailView): Back and #id, then a
 * scrolling column with the form picker, the Normal/Shiny switch, the
 * identity header (animated sprite, types, representative button), the
 * individual picker, the evolution line, the individual card with stats and
 * moves, the species data and the complete move list.
 *
 * Data comes from one getSpecies; every selection (form, look, individual)
 * is local and re-renders the column. The animated sprite actor is kept
 * across re-renders.
 */

import Clutter from 'gi://Clutter';
import St from 'gi://St';

import {_, fmt} from '../i18n.js';
import {LimitBar} from '../limitBar.js';
import {SpriteAnimator} from '../spriteAnimator.js';
import * as V from '../viewModel.js';
import {hbox, label, spacer, vbox, wrapLabel} from '../popover/widgets.js';
import * as D from './dexModel.js';
import {Segmented, StaticSprite, StatusBox, evoStrip, setChecked, setSensitive} from './dexWidgets.js';

const HEADER_SPRITE = 96;
const FORM_SPRITE = 36;
const MOVES_FIRST = 20; // the rest of the learnset is built on request

/* Type chart colors (ui.md §6, Badge.swift:7-42), shared with the companion card. */
const TYPE_COLORS = {
    normal: '#AAAA99', fire: '#FF4422', water: '#3399FF', electric: '#FFCC33', grass: '#77CC55',
    ice: '#66CCFF', fighting: '#BB5544', poison: '#AA5599', ground: '#DDBB55', flying: '#8899FF',
    psychic: '#FF5599', bug: '#AABB22', rock: '#BBAA66', ghost: '#6666BB', dragon: '#7766EE',
    dark: '#775544', steel: '#AAAABB', fairy: '#EE99EE',
};

export class SpeciesDetail {
    /**
     * @param {object} deps  {tooltip, sprites, request(cmd, args), getState(), chains: Map,
     *                        onBack(), onOpenSpecies(speciesId, opts)}
     * @param {object} opts  {speciesId, form?, individualId?}
     */
    constructor(deps, opts) {
        this._deps = deps;
        this._speciesId = opts.speciesId;
        this._form = opts.form ?? null;
        this._individualId = opts.individualId ?? null;
        this._shiny = null; // requested look; null = the rule's default
        this._data = null;
        this._seq = 0;
        this._chainsRequested = false;
        this._repBusy = false;
        this._repError = '';
        this._showAllMoves = false;
        this._destroyed = false;

        this.actor = vbox('ptb-dex-detail', {x_expand: true, y_expand: true});

        const head = hbox('ptb-detail-head', {x_expand: true});
        const back = new St.Button({style_class: 'ptb-back-button', can_focus: true});
        const backBox = hbox('ptb-back-box');
        backBox.add_child(new St.Icon({icon_name: 'go-previous-symbolic', style_class: 'ptb-back-icon'}));
        backBox.add_child(label(_('Back')));
        back.set_child(backBox);
        back.connect('clicked', () => deps.onBack());
        head.add_child(back);
        head.add_child(spacer());
        head.add_child(label(`#${this._speciesId}`, 'ptb-secondary ptb-detail-number'));
        this.actor.add_child(head);

        this._status = new StatusBox();
        this.actor.add_child(this._status.actor);

        this._content = vbox('ptb-detail-content', {x_expand: true});
        this._scroll = new St.ScrollView({
            style_class: 'ptb-detail-scroll',
            hscrollbar_policy: St.PolicyType.NEVER,
            vscrollbar_policy: St.PolicyType.AUTOMATIC,
            overlay_scrollbars: true,
            x_expand: true,
            y_expand: true,
            child: this._content,
            visible: false,
        });
        this.actor.add_child(this._scroll);

        this._sprite = new SpriteAnimator({
            boxHeight: HEADER_SPRITE,
            maxWidth: HEADER_SPRITE,
            floorMs: 0,
            fixedWidth: true,
            allowUpscale: true,
        });
        this._spriteReq = null;
        this._spriteRetried = false;
        this._sprite.onLoadFailed = () => this._onSpriteFailed();

        this.actor.connect('destroy', () => {
            this._destroyed = true;
            // Detached between renders: free it here.
            if (this._sprite && !this._sprite.get_parent())
                this._sprite.destroy();
            this._sprite = null;
        });
        this.load();
    }

    /* ---- data ----------------------------------------------------------- */

    load() {
        const seq = ++this._seq;
        this._scroll.hide();
        this._status.loading(_('Loading Pokémon details…'));
        this._deps.request('getSpecies', {speciesId: this._speciesId}).then(data => {
            if (this._destroyed || seq !== this._seq)
                return;
            this._data = data;
            this._form = D.initialForm(data, this._form);
            this._status.hide();
            this._scroll.show();
            this._render();
        }).catch(e => {
            if (this._destroyed || seq !== this._seq)
                return;
            this._scroll.hide();
            this._status.message({
                icon: 'dialog-warning-symbolic',
                title: _('Pokémon details could not be loaded.'),
                body: D.dexErrorText(e?.code),
                action: {label: _('Retry'), run: () => this.load()},
            });
        });
    }

    /** A new state arrived with a different dex signature (pin, raising, catches). */
    stateChanged() {
        if (this._data && !this._destroyed)
            this._render();
    }

    _ensureChains() {
        // The evolution line of a caught individual lives in its catch-log
        // entry (protocol §7.3); fetch this species' entries once.
        if (this._chainsRequested)
            return;
        this._chainsRequested = true;
        this._deps.request('getCatchLog', {
            offset: 0, limit: 100, query: `#${this._speciesId}`, rarity: null, shinyOnly: false, sort: 'recentFirst',
        }).then(data => {
            if (this._destroyed)
                return;
            for (const e of data.entries ?? [])
                this._deps.chains.set(e.id, e.chain ?? []);
            this._render();
        }).catch(() => {});
    }

    /* ---- render --------------------------------------------------------- */

    _render() {
        const s = this._data;
        if (!s || this._destroyed)
            return;
        const scrollValue = this._scroll.vadjustment.value;
        if (this._sprite.get_parent())
            this._sprite.get_parent().remove_child(this._sprite);
        this._content.destroy_all_children();

        const shiny = D.displayedShiny(s, this._form, this._shiny);
        const individuals = D.visibleIndividuals(s, this._form, shiny);
        const individual = D.pickIndividual(individuals, this._individualId);
        const state = this._deps.getState();

        if ((s.forms ?? []).length > 0)
            this._content.add_child(this._formPicker(s));
        const look = D.appearanceOf(s, this._form);
        if (look.hasNormal && look.isShiny)
            this._content.add_child(this._appearancePicker(shiny));
        this._content.add_child(this._identity(s, shiny, individual, state));
        if (individuals.length > 1)
            this._content.add_child(this._individualPicker(individuals, individual));
        if (individuals.length === 0) {
            this._content.add_child(wrapLabel(s.owned
                ? _('Collected appearance · species reference')
                : _('Not caught yet · species reference'), 'ptb-secondary ptb-small'));
        }

        const line = D.evoLineFor(individual, state?.companion?.active, this._deps.chains);
        if (line && line.length > 1) {
            this._content.add_child(this._evoLine(line, shiny));
        } else if (individual && !individual.isRaising && !line) {
            this._ensureChains();
        }

        if (s.detailsStatus === 'ready' && s.details) {
            if (individual)
                this._content.add_child(this._individualCard(individual, s.details));
            else
                this._content.add_child(this._baseStatsCard(s.details));
            this._content.add_child(this._speciesCard(s.details));
            this._content.add_child(this._movesCard(s.details));
        } else {
            if (individual)
                this._content.add_child(this._individualCard(individual, null));
            const box = vbox('ptb-detail-unavailable', {x_expand: true});
            box.add_child(wrapLabel(_('Pokémon details could not be loaded.'), 'ptb-secondary'));
            const retry = new St.Button({style_class: 'ptb-text-button', label: _('Retry'), can_focus: true,
                x_align: Clutter.ActorAlign.START});
            retry.connect('clicked', () => this.load());
            box.add_child(retry);
            this._content.add_child(box);
        }
        this._scroll.vadjustment.value = scrollValue;
    }

    _formPicker(s) {
        const box = vbox('ptb-detail-section', {x_expand: true});
        const owned = s.forms.filter(D.formCollected).length;
        box.add_child(label(fmt(_('Forms %d/%d'), owned, s.forms.length), 'ptb-detail-title'));
        const columns = D.formPickerColumns(s.forms.length);
        const grid = new Clutter.GridLayout({column_homogeneous: true, row_spacing: 4, column_spacing: 3});
        const gridActor = new St.Widget({layout_manager: grid, x_expand: true});
        s.forms.forEach((f, i) => {
            const collected = D.formCollected(f);
            const b = new St.Button({style_class: 'ptb-form-cell', can_focus: collected, x_expand: true});
            const col = vbox('ptb-form-box', {x_expand: true});
            const sprite = new StaticSprite(this._deps.sprites, FORM_SPRITE);
            // Owned shiny only: show it shiny, as the cell rule does.
            sprite.set({speciesId: s.speciesId, form: f.form, shiny: f.isShiny && !f.hasNormal,
                back: false, animated: false});
            col.add_child(sprite.actor);
            const name = label(f.displayName ?? f.form, 'ptb-form-name', {x_align: Clutter.ActorAlign.CENTER});
            col.add_child(name);
            b.set_child(col);
            if (!collected) {
                b.opacity = 64;
                setSensitive(b, false);
                this._deps.tooltip.attach(b, fmt(_('%s · not collected yet'), f.displayName ?? f.form));
            } else {
                this._deps.tooltip.attach(b, f.isShiny ? `${f.displayName ?? f.form} · ${_('Shiny')}` : f.displayName ?? f.form);
            }
            if (f.isShiny)
                name.text = `${name.text} ✨`;
            if (f.form === this._form)
                setChecked(b, true);
            if (f.isRepresentative)
                b.add_style_class_name('ptb-form-pinned');
            b.connect('clicked', () => {
                // tooltip.attach() makes every cell reactive; uncollected forms stay inert.
                if (!collected || f.form === this._form)
                    return;
                this._form = f.form;
                this._individualId = null;
                this._shiny = null;
                this._render();
            });
            grid.attach(b, i % columns, Math.floor(i / columns), 1, 1);
        });
        box.add_child(gridActor);
        return box;
    }

    _appearancePicker(shiny) {
        const seg = new Segmented([['normal', _('Normal')], ['shiny', `✨ ${_('Shiny')}`]], key => {
            this._shiny = key === 'shiny';
            this._individualId = null;
            this._render();
        }, 'ptb-segmented ptb-appearance');
        seg.set(shiny ? 'shiny' : 'normal');
        return seg.actor;
    }

    _identity(s, shiny, individual, state) {
        const row = hbox('ptb-detail-identity', {x_expand: true});
        const tile = new St.Bin({style_class: 'ptb-detail-tile', y_align: Clutter.ActorAlign.START});
        tile.set_child(this._sprite);
        const req = {speciesId: s.speciesId, form: this._form, shiny, back: false, animated: true};
        this._setSprite(req);
        row.add_child(tile);

        const info = vbox('ptb-detail-info', {x_expand: true});
        const nameRow = hbox('ptb-detail-name-row', {x_expand: true});
        nameRow.add_child(wrapLabel(D.formTitle(s, this._form), 'ptb-detail-name'));
        if (s.rarity) {
            nameRow.add_child(label(V.rarityName(s.rarity), 'ptb-secondary ptb-detail-rarity',
                {y_align: Clutter.ActorAlign.START}));
        }
        info.add_child(nameRow);

        const types = s.details?.types ?? [];
        if (types.length > 0) {
            const typeRow = hbox('ptb-type-row');
            for (const t of types) {
                const badge = label((t.name ?? t.key).toUpperCase(), 'ptb-badge ptb-type-badge');
                badge.style = `background-color: ${TYPE_COLORS[t.key] ?? '#999999'};`;
                typeRow.add_child(badge);
            }
            info.add_child(typeRow);
            if (!D.isDefaultForm(s, this._form))
                info.add_child(label(_('Data uses the default form'), 'ptb-tertiary ptb-tiny'));
        }
        if (shiny)
            info.add_child(label(`✨ ${_('Shiny')}`, 'ptb-small'));
        if (individual?.isRaising)
            info.add_child(label(_('Raising'), 'ptb-accent-text ptb-small'));

        info.add_child(this._representativeButton(s, state));
        if (this._repError)
            info.add_child(wrapLabel(this._repError, 'ptb-warning-text ptb-tiny'));
        row.add_child(info);
        return row;
    }

    _representativeButton(s, state) {
        const rep = state?.companion?.representative ?? null;
        const pinned = D.isPinned(s, this._form, rep);
        const owned = (s.forms ?? []).length > 0
            ? D.formCollected(s.forms.find(f => f.form === this._form))
            : Boolean(s.owned);
        const b = new St.Button({style_class: 'ptb-rep-button', can_focus: true, x_align: Clutter.ActorAlign.START});
        const box = hbox('ptb-rep-box');
        box.add_child(new St.Icon({icon_name: pinned ? 'starred-symbolic' : 'non-starred-symbolic',
            style_class: 'ptb-rep-icon'}));
        box.add_child(label(pinned ? _('Follow the companion') : _('Set as representative')));
        b.set_child(box);
        if (pinned)
            b.add_style_class_name('ptb-rep-pinned');
        this._deps.tooltip.attach(b, pinned
            ? _('This Pokémon is shown in the top bar. Click to show the companion you are raising instead.')
            : _('Show this Pokémon in the top bar instead of the companion you are raising.'));
        setSensitive(b, (owned || pinned) && !this._repBusy);
        b.connect('clicked', () => this._toggleRepresentative(pinned));
        return b;
    }

    _toggleRepresentative(pinned) {
        if (this._repBusy)
            return;
        this._repBusy = true;
        this._repError = '';
        const args = pinned
            ? {speciesId: null, form: null}
            : {speciesId: this._speciesId, form: this._form};
        this._deps.request('setRepresentative', args).then(() => {
            if (this._destroyed)
                return;
            this._repBusy = false;
            this._render(); // the state carrying the new pin arrived before this reply (protocol §2.4)
        }).catch(e => {
            if (this._destroyed)
                return;
            this._repBusy = false;
            this._repError = e?.code === 'notOwned'
                ? _('Only Pokémon in your collection can be shown in the top bar.')
                : D.dexErrorText(e?.code);
            this._render();
        });
    }

    _individualPicker(list, current) {
        const row = hbox('ptb-detail-picker', {x_expand: true});
        row.add_child(label(_('Individual'), 'ptb-secondary ptb-small'));
        const index = Math.max(0, list.indexOf(current));
        const prev = new St.Button({style_class: 'ptb-pager-button', can_focus: true, accessible_name: _('Previous'),
            child: new St.Icon({icon_name: 'go-previous-symbolic', style_class: 'ptb-pager-icon'})});
        const next = new St.Button({style_class: 'ptb-pager-button', can_focus: true, accessible_name: _('Next'),
            child: new St.Icon({icon_name: 'go-next-symbolic', style_class: 'ptb-pager-icon'})});
        const text = label(D.individualLabel(current, index), 'ptb-detail-picker-label',
            {x_expand: true, x_align: Clutter.ActorAlign.CENTER});
        setSensitive(prev, index > 0);
        setSensitive(next, index < list.length - 1);
        const go = d => {
            this._individualId = list[Math.min(list.length - 1, Math.max(0, index + d))].id;
            this._render();
        };
        prev.connect('clicked', () => go(-1));
        next.connect('clicked', () => go(1));
        row.add_child(prev);
        row.add_child(text);
        row.add_child(next);
        return row;
    }

    _evoLine(line, shiny) {
        const box = vbox('ptb-detail-section', {x_expand: true});
        box.add_child(label(_('Evolution'), 'ptb-detail-title'));
        box.add_child(evoStrip(this._deps, line, {
            size: 40,
            shiny,
            names: true,
            onOpen: node => {
                if (node.speciesId !== this._speciesId)
                    this._deps.onOpenSpecies(node.speciesId, {form: node.form ?? null});
            },
        }));
        return box;
    }

    _card(title) {
        const card = vbox('ptb-detail-card', {x_expand: true});
        card.add_child(label(title, 'ptb-detail-title'));
        return card;
    }

    _pair(name, value) {
        const p = vbox('ptb-detail-pair');
        p.add_child(label(name, 'ptb-secondary ptb-tiny'));
        p.add_child(label(value, 'ptb-detail-value'));
        return p;
    }

    _statRows(card, values, ivs, scale) {
        const rows = vbox('ptb-stat-rows', {x_expand: true});
        for (const key of D.STAT_KEYS) {
            const v = values?.[key];
            if (v === undefined || v === null)
                continue;
            const row = hbox('ptb-stat-row', {x_expand: true});
            row.add_child(label(D.statLabel(key), 'ptb-stat-name'));
            const bar = new LimitBar({style_class: 'ptb-limit-bar ptb-stat-bar', y_align: Clutter.ActorAlign.CENTER});
            bar.setValue(v / scale);
            row.add_child(bar);
            row.add_child(label(String(v), 'ptb-stat-value'));
            if (ivs)
                row.add_child(label(fmt(_('IV %d'), ivs[key] ?? 0), 'ptb-stat-iv ptb-secondary'));
            rows.add_child(row);
        }
        card.add_child(rows);
    }

    _individualCard(ind, details) {
        const card = this._card(_('Individual'));
        const pairs = hbox('ptb-detail-pairs');
        pairs.add_child(this._pair(_('Level'), String(ind.level ?? '—')));
        pairs.add_child(this._pair(_('Gender'), D.genderLabel(ind.gender)));
        pairs.add_child(this._pair(_('Nature'), V.natureName(ind.nature) ?? '—'));
        pairs.add_child(this._pair(_('Caught'), D.caughtDateText(ind)));
        card.add_child(pairs);

        const ability = ind.ability
            ? (ind.ability.displayName ?? ind.ability.name) + (ind.ability.isHidden ? ` · ${_('Hidden')}` : '')
            : '—';
        card.add_child(this._pair(_('Ability'), ability));

        if (ind.stats) {
            card.add_child(label(_('Actual stats'), 'ptb-detail-subtitle'));
            const scale = D.statScaleMaximum(D.STAT_KEYS.map(k => ind.stats[k]));
            this._statRows(card, ind.stats, ind.ivs, scale);
        } else if (details?.baseStats) {
            card.add_child(label(_('Base stats'), 'ptb-detail-subtitle'));
            this._statRows(card, details.baseStats, ind.ivs, 300);
        } else if (ind.ivs) {
            card.add_child(label(_('Individual values'), 'ptb-detail-subtitle'));
            const ivRow = hbox('ptb-iv-row');
            for (const key of D.STAT_KEYS)
                ivRow.add_child(this._pair(D.statLabel(key), String(ind.ivs[key] ?? 0)));
            card.add_child(ivRow);
        }

        card.add_child(label(_('Known moves'), 'ptb-detail-subtitle'));
        if ((ind.moves ?? []).length === 0) {
            card.add_child(wrapLabel(_('No level-up moves learned at this level.'), 'ptb-secondary ptb-small'));
        } else {
            for (const m of ind.moves) {
                const row = hbox('ptb-move-row', {x_expand: true});
                row.add_child(label(m.displayName ?? m.name, '', {x_expand: true}));
                row.add_child(label(fmt(_('Lv. %d'), m.level ?? 1), 'ptb-secondary'));
                card.add_child(row);
            }
        }
        return card;
    }

    _baseStatsCard(details) {
        const card = this._card(_('Base stats'));
        this._statRows(card, details.baseStats, null, 300);
        return card;
    }

    _speciesCard(details) {
        const card = this._card(_('Species data'));
        const pairs = hbox('ptb-detail-pairs');
        pairs.add_child(this._pair(_('Height'), D.heightText(details.heightM)));
        pairs.add_child(this._pair(_('Weight'), D.weightText(details.weightKg)));
        pairs.add_child(this._pair(_('Base stat total'), String(D.baseStatTotal(details.baseStats))));
        card.add_child(pairs);
        card.add_child(label(_('Possible abilities'), 'ptb-detail-subtitle'));
        const abilities = (details.abilities ?? []).map(a =>
            (a.displayName ?? a.name) + (a.isHidden ? ` (${_('hidden')})` : '')).join(' · ');
        card.add_child(wrapLabel(abilities || '—', 'ptb-secondary ptb-small'));
        return card;
    }

    _movesCard(details) {
        const moves = details.moves ?? [];
        const card = this._card(fmt(_('Complete move list · %d'), moves.length));
        const shown = this._showAllMoves ? moves : moves.slice(0, MOVES_FIRST);
        for (const m of shown) {
            const row = hbox('ptb-move-row', {x_expand: true});
            row.add_child(label(m.displayName ?? m.name, '', {x_expand: true}));
            row.add_child(label(D.moveMethodsText(m.methods), 'ptb-secondary ptb-small'));
            card.add_child(row);
        }
        if (shown.length < moves.length) {
            const more = new St.Button({style_class: 'ptb-text-button', can_focus: true,
                label: fmt(_('Show all %d moves'), moves.length), x_align: Clutter.ActorAlign.START});
            more.connect('clicked', () => {
                this._showAllMoves = true;
                this._render();
            });
            card.add_child(more);
        }
        return card;
    }

    /* ---- sprite --------------------------------------------------------- */

    _setSprite(req) {
        const key = JSON.stringify(req);
        if (key === JSON.stringify(this._spriteReq))
            return;
        this._spriteReq = req;
        this._spriteRetried = false;
        this._resolveSprite(req, key);
    }

    _resolveSprite(req, key) {
        this._deps.sprites.resolve(req).then(data => {
            if (!this._destroyed && this._sprite && JSON.stringify(this._spriteReq) === key)
                this._sprite.setSource({path: data.manifestPath});
        }).catch(() => {
            if (!this._destroyed && this._sprite && JSON.stringify(this._spriteReq) === key)
                this._sprite.setSource(null);
        });
    }

    _onSpriteFailed() {
        // The cache was cleared under us: ask the helper once more (protocol §8.2).
        if (this._destroyed || this._spriteRetried || !this._spriteReq)
            return;
        this._spriteRetried = true;
        this._deps.sprites.forget(this._spriteReq);
        this._resolveSprite(this._spriteReq, JSON.stringify(this._spriteReq));
    }

    destroy() {
        this.actor.destroy();
    }
}
