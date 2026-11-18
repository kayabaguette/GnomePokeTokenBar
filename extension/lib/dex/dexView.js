/* Pokédex tab (ui.md §3): Pokédex / Catch log segments sharing one search
 * and filter toolbar, the species detail, and the empty state.
 *
 * Data is fetched page by page (getDexPage, getCatchLog), only while the tab
 * is visible, and cached for a few seconds. A new `state` only invalidates
 * the cache when its dex signature changes (summary, pin, raising), so the
 * 60 s clock states never refetch or rebuild anything here.
 *
 * Every helper reply is checked against a request sequence number and the
 * destroyed flag before it touches an actor: on disable every pending command
 * fails with helperExited after the actors are gone.
 */

import Clutter from 'gi://Clutter';
import GLib from 'gi://GLib';
import St from 'gi://St';

import {Spinner} from 'resource:///org/gnome/shell/ui/animation.js';

import {_, fmt, ngettext} from '../i18n.js';
import {hbox, label, spacer, vbox} from '../popover/widgets.js';
import {CatchLog} from './catchLog.js';
import {DexGrid} from './dexGrid.js';
import * as D from './dexModel.js';
import {Segmented, StaticSprite, StatusBox, setChecked} from './dexWidgets.js';
import {SpeciesDetail} from './speciesDetail.js';

const SEARCH_DEBOUNCE_MS = 250;
const EMPTY_ART_SIZE = 96;

export class DexView {
    /**
     * @param {object} deps  {tooltip, sprites, request(cmd, args): Promise}
     */
    constructor(deps) {
        this._deps = deps;
        this._state = null;
        this._signature = null;
        this._filters = D.defaultFilters();
        this._segment = 'grid';
        this._gridPage = 1;
        this._logPage = 1;
        this._gridData = null;
        this._logData = null;
        this._anchor = null; // where Back returns: {segment, speciesId?, entryId?}
        this._detail = null;
        this._visible = false;
        this._needsReload = false;
        this._seq = 0;
        this._searchId = 0;
        this._destroyed = false;
        this._cache = new D.ResultCache();
        this._chains = new Map(); // catch-log entry id → chain, for the detail's evolution line

        this.actor = vbox('ptb-dex', {x_expand: true, visible: false});

        /* List mode: toolbar, title row, body. */
        this._list = vbox('ptb-dex-list', {x_expand: true, y_expand: true});
        this.actor.add_child(this._list);

        const toolbar = hbox('ptb-dex-toolbar', {x_expand: true});
        this._segmented = new Segmented([['grid', _('Pokédex')], ['log', _('Catch log')]],
            key => this._setSegment(key));
        this._segmented.set('grid');
        toolbar.add_child(this._segmented.actor);
        this._search = this._buildSearch();
        toolbar.add_child(this._search);
        this._list.add_child(toolbar);

        const titleRow = hbox('ptb-dex-title-row', {x_expand: true});
        this._title = label('', 'ptb-dex-title');
        this._count = label('', 'ptb-secondary ptb-small');
        this._medals = hbox('ptb-dex-medals');
        // Shown while a page loads over the previous one.
        this._spinner = new Spinner(14, {hideOnStop: true});
        this._spinner.y_align = Clutter.ActorAlign.CENTER;
        titleRow.add_child(this._title);
        titleRow.add_child(this._count);
        titleRow.add_child(this._medals);
        titleRow.add_child(this._spinner);
        titleRow.add_child(spacer());
        this._shinyButton = new St.Button({
            style_class: 'ptb-dex-tool ptb-shiny-toggle',
            label: '✨',
            can_focus: true,
            accessible_name: _('Shiny only'),
        });
        this._shinyButton.connect('clicked', () => {
            this._filters.shinyOnly = !this._filters.shinyOnly;
            this._filtersChanged();
        });
        deps.tooltip.attach(this._shinyButton, _('Shiny only'));
        titleRow.add_child(this._shinyButton);
        this._sortButton = new St.Button({
            style_class: 'ptb-dex-tool ptb-sort-button',
            child: new St.Icon({icon_name: 'view-sort-descending-symbolic', style_class: 'ptb-dex-tool-icon'}),
            can_focus: true,
            accessible_name: _('Sort'),
        });
        this._sortButton.connect('clicked', () => this._toggleSort());
        deps.tooltip.attach(this._sortButton, () => fmt(_('Sort: %s'), D.sortLabel(this._currentSort())));
        titleRow.add_child(this._sortButton);
        this._list.add_child(titleRow);

        // Body: the page, the status box, and the sort card over them.
        const body = new St.Widget({layout_manager: new Clutter.BinLayout(), x_expand: true, y_expand: true});
        this._grid = new DexGrid(deps, {
            onOpen: cell => this._openFromGrid(cell),
            onStep: step => this._stepPage(step),
            onRegion: key => {
                this._filters.region = D.toggleValue(this._filters.region, key);
                this._filtersChanged();
            },
            onRarity: key => {
                this._filters.rarity = D.toggleValue(this._filters.rarity, key);
                this._filtersChanged();
            },
            onReset: () => this._resetFilters(),
        });
        this._log = new CatchLog(deps, {
            onOpen: (entry, node) => this._openFromLog(entry, node),
            onStep: step => this._stepPage(step),
            onRarity: key => {
                this._filters.rarity = D.toggleValue(this._filters.rarity, key);
                this._filtersChanged();
            },
            onReset: () => this._resetFilters(),
        });
        this._status = new StatusBox();
        body.add_child(this._grid.actor);
        body.add_child(this._log.actor);
        body.add_child(this._status.actor);
        this._scrim = new St.Widget({reactive: true, x_expand: true, y_expand: true, visible: false});
        this._scrim.add_action(clickAction(() => this._closeSort()));
        body.add_child(this._scrim);
        // BinLayout honours a child's alignment only when it expands.
        this._sortCard = vbox('ptb-sort-card', {
            x_expand: true, y_expand: true,
            x_align: Clutter.ActorAlign.END, y_align: Clutter.ActorAlign.START, visible: false,
        });
        body.add_child(this._sortCard);
        this._list.add_child(body);

        /* Empty dex. */
        this._empty = new StatusBox();
        this.actor.add_child(this._empty.actor);

        this.actor.connect('destroy', () => this._onDestroy());
        this._syncSegment();
    }

    /* ---- public API (Popover) ------------------------------------------ */

    setHeight(px) {
        this.actor.style = `height: ${px}px;`;
    }

    /** The tab became visible. */
    show() {
        this._visible = true;
        this.actor.show();
        this._refreshMode();
    }

    /** The tab was left: drop the detail and transient UI (ui.md §3.1). */
    hide() {
        this._visible = false;
        this.actor.hide();
        this._closeSort();
        this._closeDetail(false);
    }

    /** Right-click "Pokédex": the grid segment, no detail (openRepresentativeDex). */
    openGrid() {
        this._closeSort();
        this._closeDetail(false);
        if (this._segment !== 'grid')
            this._setSegment('grid');
    }

    /** Open a species' detail from elsewhere (Home sprite, evolution line). */
    openSpecies(speciesId, opts = {}) {
        if (!Number.isInteger(speciesId))
            return;
        this._closeSort();
        this._closeDetail(false);
        this._anchor = {segment: this._segment, speciesId};
        this._openDetail(speciesId, opts);
    }

    /** @param {object} ctx  {state, nowMs} */
    update(ctx) {
        this._state = ctx.state ?? this._state;
        this._nowMs = ctx.nowMs ?? Date.now();
        const sig = D.dexSignature(this._state);
        const changed = sig !== this._signature;
        this._signature = sig;
        if (changed)
            this._cache.clear();
        if (!this._visible)
            return;
        if (changed || this._needsReload) {
            this._needsReload = false;
            if (this._detail)
                this._detail.stateChanged();
            this._refreshMode();
        }
        this._syncTitle();
    }

    /* ---- modes ---------------------------------------------------------- */

    _isEmpty() {
        const s = this._state?.companion?.dexSummary;
        return !s || !(s.entries > 0);
    }

    _refreshMode() {
        if (this._destroyed)
            return;
        if (this._detail) {
            this._list.hide();
            this._empty.hide();
            return;
        }
        if (this._isEmpty()) {
            this._list.hide();
            this._showEmpty();
            return;
        }
        this._empty.hide();
        this._list.show();
        this._syncTitle();
        this._load();
    }

    _showEmpty() {
        const art = new StaticSprite(this._deps.sprites, EMPTY_ART_SIZE);
        art.set({speciesId: 25, form: null, shiny: false, back: false, animated: false});
        this._empty.message({
            art: art.actor,
            title: this._state ? _('No Pokémon caught yet!') : _('Starting…'),
            body: this._state ? _('Spend tokens to hatch your first Pokémon.') : '',
        });
    }

    _setSegment(key) {
        this._segment = key;
        this._segmented.set(key);
        this._closeSort();
        this._closeDetail(false);
        this._syncSegment();
        if (this._visible)
            this._refreshMode();
    }

    _syncSegment() {
        const grid = this._segment === 'grid';
        this._grid.actor.visible = grid && this._gridData !== null;
        this._log.actor.visible = !grid && this._logData !== null;
        this._syncTools();
    }

    _syncTools() {
        setChecked(this._shinyButton, this._filters.shinyOnly);
        if (D.isSortCustom(this._filters, this._segment))
            this._sortButton.add_style_class_name('ptb-dex-tool-custom');
        else
            this._sortButton.remove_style_class_name('ptb-dex-tool-custom');
    }

    _syncTitle() {
        const s = this._state?.companion?.dexSummary;
        this._medals.destroy_all_children();
        if (this._segment === 'grid') {
            this._title.text = _('Pokédex');
            const owned = s?.ownedSpecies ?? 0;
            const total = this._gridData?.total ?? owned;
            this._count.text = this._filtered()
                ? fmt(_('%d of %d species'), total, owned)
                : fmt(ngettext('%d species', '%d species', owned), owned);
            for (const m of D.headerMedals(s?.badges)) {
                const medal = label(m.emoji, 'ptb-dex-medal');
                this._deps.tooltip.attach(medal, m.label);
                this._medals.add_child(medal);
            }
        } else {
            this._title.text = _('Catch log');
            const entries = s?.entries ?? 0;
            const total = this._logData?.total ?? entries;
            this._count.text = this._filtered()
                ? fmt(_('%d of %d caught'), total, entries)
                : fmt(ngettext('%d caught', '%d caught', entries), entries);
        }
        this._medals.visible = this._medals.get_n_children() > 0;
    }

    /** A filter or search narrows the list (the sort does not). */
    _filtered() {
        const f = this._filters;
        return D.normalizeQuery(f.query) !== '' || f.rarity !== null || f.shinyOnly ||
            (this._segment === 'grid' && f.region !== null);
    }

    /* ---- loading -------------------------------------------------------- */

    _currentRequest(anchorSpeciesId = null) {
        if (this._segment === 'grid')
            return ['getDexPage', D.dexPageArgs(this._filters, this._gridPage, anchorSpeciesId)];
        return ['getCatchLog', D.catchLogArgs(this._filters, this._logPage)];
    }

    /**
     * Fetch (or take from the cache) the current page and show it.
     * @param {object} [opts]  {anchorSpeciesId, revealEntryId}
     */
    _load(opts = {}) {
        if (this._destroyed || !this._visible || this._detail || this._isEmpty())
            return;
        const segment = this._segment;
        const [cmd, args] = this._currentRequest(opts.anchorSpeciesId ?? null);
        const key = D.cacheKey(cmd, args);
        const seq = ++this._seq;
        const cached = this._cache.get(key);
        if (cached) {
            this._apply(segment, cached, opts);
            return;
        }
        const hasContent = segment === 'grid' ? this._gridData !== null : this._logData !== null;
        if (hasContent) {
            this._spinner.play();
        } else {
            this._status.loading(_('Loading the Pokédex…'));
        }
        this._deps.request(cmd, args).then(data => {
            if (this._destroyed || seq !== this._seq)
                return;
            this._spinner.stop();
            this._cache.set(key, data);
            this._apply(segment, data, opts);
        }).catch(e => {
            if (this._destroyed || seq !== this._seq)
                return;
            this._spinner.stop();
            this._showError(e?.code);
        });
    }

    _apply(segment, data, opts) {
        if (segment !== this._segment)
            return;
        this._status.hide();
        if (segment === 'grid') {
            this._gridData = data;
            this._gridPage = data.page ?? this._gridPage;
            // The anchored reply is the page itself: remember it under the plain key too.
            if (opts.anchorSpeciesId)
                this._cache.set(D.cacheKey('getDexPage', D.dexPageArgs(this._filters, this._gridPage)), data);
            this._grid.setData(data, this._filters);
        } else {
            this._logData = data;
            for (const e of data.entries ?? [])
                this._chains.set(e.id, e.chain ?? []);
            this._log.setData(data, this._filters, this._nowMs ?? Date.now());
            if (opts.revealEntryId)
                this._log.revealEntry(opts.revealEntryId);
        }
        this._syncSegment();
        this._syncTitle();
    }

    _showError(code) {
        const hasContent = this._segment === 'grid' ? this._gridData !== null : this._logData !== null;
        if (D.retriesOnState(code))
            this._needsReload = true; // reload with the next state (helper back, data ready)
        if (hasContent && D.retriesOnState(code))
            return; // keep showing the last page meanwhile
        this._grid.actor.hide();
        this._log.actor.hide();
        this._status.message({
            icon: 'dialog-warning-symbolic',
            title: D.dexErrorText(code),
            action: D.retriesOnState(code) ? null : {label: _('Retry'), run: () => this._load()},
        });
    }

    /* ---- filters, paging ------------------------------------------------ */

    _buildSearch() {
        const entry = new St.Entry({
            style_class: 'ptb-search',
            hint_text: _('Search name or #…'),
            can_focus: true,
            x_expand: true,
            primary_icon: new St.Icon({icon_name: 'edit-find-symbolic', style_class: 'ptb-search-icon'}),
        });
        this._clearIcon = new St.Icon({icon_name: 'edit-clear-symbolic', style_class: 'ptb-search-icon'});
        entry.clutter_text.connect('text-changed', () => {
            const empty = entry.get_text().length === 0;
            entry.set_secondary_icon(empty ? null : this._clearIcon);
            this._scheduleSearch();
        });
        entry.connect('secondary-icon-clicked', () => entry.set_text(''));
        // Keys the text field does not handle bubble up to the panel menu,
        // whose Left/Right handler would jump to the neighbouring panel
        // button (panelMenu.js _onMenuKeyPress). Keep them here.
        entry.connect('key-press-event', (_a, event) => {
            const sym = event.get_key_symbol();
            if (sym === Clutter.KEY_Left || sym === Clutter.KEY_Right)
                return Clutter.EVENT_STOP;
            return Clutter.EVENT_PROPAGATE;
        });
        return entry;
    }

    _scheduleSearch() {
        if (this._searchId)
            GLib.source_remove(this._searchId);
        this._searchId = GLib.timeout_add(GLib.PRIORITY_DEFAULT, SEARCH_DEBOUNCE_MS, () => {
            this._searchId = 0;
            const q = D.normalizeQuery(this._search.get_text());
            if (q !== this._filters.query) {
                this._filters.query = q;
                this._filtersChanged();
            }
            return GLib.SOURCE_REMOVE;
        });
    }

    /** Any filter, sort or search change returns to page 1 (ui.md §3.3). */
    _filtersChanged() {
        this._gridPage = 1;
        this._logPage = 1;
        this._grid.resetGesture();
        this._syncTools();
        this._load();
    }

    _resetFilters() {
        const d = D.defaultFilters();
        this._filters = d;
        if (this._search.get_text() !== '') {
            this._search.set_text('');
            if (this._searchId) {
                GLib.source_remove(this._searchId);
                this._searchId = 0;
            }
        }
        this._filtersChanged();
    }

    _stepPage(step) {
        if (this._segment === 'grid') {
            const count = this._gridData?.pageCount ?? 1;
            const next = D.pageAfterStep(this._gridPage, count, step);
            if (next === this._gridPage)
                return;
            this._gridPage = next;
        } else {
            const count = D.logPageCount(this._logData?.total ?? 0);
            const next = D.pageAfterStep(this._logPage, count, step);
            if (next === this._logPage)
                return;
            this._logPage = next;
        }
        this._load();
    }

    /* ---- sort card ------------------------------------------------------ */

    _currentSort() {
        return this._segment === 'log' ? this._filters.logSort : this._filters.dexSort;
    }

    _toggleSort() {
        if (this._sortCard.visible) {
            this._closeSort();
            return;
        }
        this._sortCard.destroy_all_children();
        this._sortCard.add_child(label(_('Sort'), 'ptb-sort-title'));
        const options = this._segment === 'log' ? D.LOG_SORTS : D.DEX_SORTS;
        const current = this._currentSort();
        for (const sort of options) {
            const b = new St.Button({style_class: 'ptb-sort-option', can_focus: true, x_expand: true});
            const row = hbox('ptb-sort-row', {x_expand: true});
            const check = new St.Icon({icon_name: 'object-select-symbolic', style_class: 'ptb-sort-check'});
            check.opacity = sort === current ? 255 : 0;
            row.add_child(check);
            row.add_child(label(D.sortLabel(sort), '', {x_expand: true}));
            b.set_child(row);
            b.connect('clicked', () => {
                if (this._segment === 'log')
                    this._filters.logSort = sort;
                else
                    this._filters.dexSort = sort;
                this._closeSort();
                this._filtersChanged();
            });
            this._sortCard.add_child(b);
        }
        this._scrim.show();
        this._sortCard.show();
        setChecked(this._sortButton, true);
    }

    _closeSort() {
        if (this._destroyed)
            return;
        this._sortCard.hide();
        this._scrim.hide();
        setChecked(this._sortButton, false);
    }

    /* ---- navigation ----------------------------------------------------- */

    _openFromGrid(cell) {
        this._closeSort();
        this._anchor = {segment: 'grid', speciesId: cell.speciesId};
        this._openDetail(cell.speciesId, {});
    }

    _openFromLog(entry, node) {
        this._closeSort();
        this._anchor = {segment: 'log', entryId: entry.id};
        const isFinal = node.speciesId === entry.speciesId;
        this._openDetail(node.speciesId, {
            form: node.form ?? null,
            individualId: isFinal ? entry.id : null,
        });
    }

    _openDetail(speciesId, opts) {
        this._closeDetail(false);
        this._detail = new SpeciesDetail({
            tooltip: this._deps.tooltip,
            sprites: this._deps.sprites,
            request: (cmd, args) => this._deps.request(cmd, args),
            getState: () => this._state,
            chains: this._chains,
            onBack: () => this._closeDetail(true),
            onOpenSpecies: (id, o) => this._openDetail(id, o),
        }, {speciesId, form: opts.form ?? null, individualId: opts.individualId ?? null});
        this.actor.add_child(this._detail.actor);
        this._refreshMode();
    }

    /** @param {boolean} restore  Back: return to the page or row it came from */
    _closeDetail(restore) {
        if (!this._detail)
            return;
        const detail = this._detail;
        this._detail = null;
        detail.destroy();
        if (!restore || this._destroyed)
            return;
        const anchor = this._anchor;
        this._anchor = null;
        if (anchor && anchor.segment !== this._segment) {
            this._segment = anchor.segment;
            this._segmented.set(anchor.segment);
            this._syncSegment();
        }
        this._list.show();
        this._empty.hide();
        this._syncTitle();
        if (this._isEmpty()) {
            this._refreshMode();
            return;
        }
        if (anchor?.segment === 'grid' && anchor.speciesId)
            this._load({anchorSpeciesId: anchor.speciesId});
        else if (anchor?.segment === 'log' && anchor.entryId)
            this._load({revealEntryId: anchor.entryId});
        else
            this._load();
    }

    /* ---- teardown ------------------------------------------------------- */

    _onDestroy() {
        this._destroyed = true;
        this._seq++;
        if (this._searchId) {
            GLib.source_remove(this._searchId);
            this._searchId = 0;
        }
        this._detail = null; // destroyed with this.actor
        this._cache.clear();
        this._chains.clear();
        // The clear icon is a secondary icon only while text is present.
        if (!this._clearIcon.get_parent())
            this._clearIcon.destroy();
    }

    destroy() {
        this.actor.destroy();
    }
}

function clickAction(cb) {
    const gesture = new Clutter.ClickGesture();
    gesture.connect('recognize', () => cb());
    return gesture;
}
