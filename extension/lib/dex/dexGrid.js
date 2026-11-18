/* Pokédex grid page (ui.md §3.3): region progress strip, rarity chips, a
 * 4 × 4 page of species cells and the pager. Wheel and trackpad scrolling
 * over the grid turn pages (DexScrollPager).
 *
 * The 16 cells are built once and refilled for each page, so paging never
 * creates or destroys actors.
 */

import Clutter from 'gi://Clutter';
import Graphene from 'gi://Graphene';
import St from 'gi://St';

import {_} from '../i18n.js';
import * as V from '../viewModel.js';
import {hbox, label, vbox} from '../popover/widgets.js';
import * as D from './dexModel.js';
import {Pager, RarityChips, StaticSprite, setChecked, setRarityClass, setSensitive} from './dexWidgets.js';

const CELL_SPRITE = 56; // DexSpeciesCell.thumb
const HOVER_SCALE = 1.04;
const HOVER_MS = 120;

const DIRECTIONS = {
    [Clutter.ScrollDirection.UP]: 'up',
    [Clutter.ScrollDirection.DOWN]: 'down',
    [Clutter.ScrollDirection.LEFT]: 'left',
    [Clutter.ScrollDirection.RIGHT]: 'right',
    [Clutter.ScrollDirection.SMOOTH]: 'smooth',
};
const SOURCES = {
    [Clutter.ScrollSource.WHEEL]: 'wheel',
    [Clutter.ScrollSource.FINGER]: 'finger',
    [Clutter.ScrollSource.CONTINUOUS]: 'continuous',
};

/** Clutter scroll event → the plain object ScrollPager.step() takes. */
export function pagerEvent(event) {
    const direction = DIRECTIONS[event.get_scroll_direction()] ?? 'unknown';
    let dx = 0;
    let dy = 0;
    if (direction === 'smooth')
        [dx, dy] = event.get_scroll_delta();
    return {
        direction,
        dx,
        dy,
        source: SOURCES[event.get_scroll_source()] ?? 'unknown',
        emulated: event.is_pointer_emulated(),
        finished: (event.get_scroll_finish_flags() & Clutter.ScrollFinishFlags.VERTICAL) !== 0,
        timeMs: event.get_time(),
    };
}

class DexCell {
    constructor(deps, onActivate) {
        this._deps = deps;
        this._cell = null;
        this._tooltip = '';

        this.actor = new St.Button({
            style_class: 'ptb-dex-cell',
            x_expand: true,
            y_expand: true,
            can_focus: true,
            pivot_point: new Graphene.Point({x: 0.5, y: 0.5}),
        });
        const layers = new St.Widget({layout_manager: new Clutter.BinLayout(), x_expand: true, y_expand: true});
        this.actor.set_child(layers);

        const body = vbox('ptb-dex-cell-body', {x_expand: true, y_align: Clutter.ActorAlign.CENTER});
        const spriteBox = new St.Widget({
            layout_manager: new Clutter.BinLayout(),
            x_align: Clutter.ActorAlign.CENTER,
            width: CELL_SPRITE,
            height: CELL_SPRITE,
        });
        this._sprite = new StaticSprite(deps.sprites, CELL_SPRITE);
        spriteBox.add_child(this._sprite.actor);
        // BinLayout honours a child's alignment only when it expands.
        this._raising = label(_('RAISING'), 'ptb-dex-raising', {
            x_expand: true, y_expand: true, x_align: Clutter.ActorAlign.CENTER, y_align: Clutter.ActorAlign.END,
        });
        spriteBox.add_child(this._raising);
        body.add_child(spriteBox);

        const nameRow = hbox('ptb-dex-cell-name-row', {x_align: Clutter.ActorAlign.CENTER});
        this._name = label('', 'ptb-dex-cell-name');
        this._forms = label('', 'ptb-dex-cell-forms');
        nameRow.add_child(this._name);
        nameRow.add_child(this._forms);
        body.add_child(nameRow);
        layers.add_child(body);

        this._number = hbox('ptb-dex-number', {
            x_expand: true, y_expand: true, x_align: Clutter.ActorAlign.START, y_align: Clutter.ActorAlign.START,
        });
        this._numberText = label('', 'ptb-dex-number-text');
        this._star = new St.Icon({icon_name: 'starred-symbolic', style_class: 'ptb-dex-star'});
        this._number.add_child(this._numberText);
        this._number.add_child(this._star);
        layers.add_child(this._number);

        this._shiny = label('✨', 'ptb-dex-shiny', {
            x_expand: true, y_expand: true, x_align: Clutter.ActorAlign.END, y_align: Clutter.ActorAlign.START,
        });
        layers.add_child(this._shiny);

        this.actor.connect('clicked', () => {
            if (this._cell)
                onActivate(this._cell);
        });
        this.actor.connect('notify::hover', () => this._ease(this.actor.hover && this._cell !== null));
        deps.tooltip.attach(this.actor, () => (this._cell ? this._tooltip : null));
        this.set(null);
    }

    _ease(hovered) {
        const scale = hovered ? HOVER_SCALE : 1;
        this.actor.ease({scale_x: scale, scale_y: scale, duration: HOVER_MS, mode: Clutter.AnimationMode.EASE_OUT_QUAD});
    }

    /** @param {object|null} cell  protocol §7.1 Cell, or null for an empty slot */
    set(cell) {
        this._cell = cell;
        if (!cell) {
            // Keep the slot (the grid rows stay homogeneous); make it inert.
            this.actor.opacity = 0;
            setSensitive(this.actor, false);
            this.actor.remove_style_pseudo_class('insensitive');
            this._sprite.set(null);
            return;
        }
        this.actor.opacity = 255;
        setSensitive(this.actor, true);
        const rarity = cell.rarity;
        setRarityClass(this.actor, 'ptb-rarity-', rarity);
        setRarityClass(this._number, 'ptb-rarity-', rarity);
        if (cell.isRepresentative)
            this.actor.add_style_class_name('ptb-dex-representative');
        else
            this.actor.remove_style_class_name('ptb-dex-representative');
        // Not owned at all (defensive: the helper serves owned species only): the locked look.
        if (D.cellOwned(cell))
            this.actor.remove_style_class_name('ptb-dex-locked');
        else
            this.actor.add_style_class_name('ptb-dex-locked');

        this._sprite.set(cell.sprite ?? null);
        this._sprite.actor.opacity = D.cellOwned(cell) ? 255 : 70;
        this._numberText.text = `#${cell.speciesId}`;
        this._star.visible = Boolean(cell.isRepresentative);
        this._shiny.visible = Boolean(cell.isShiny);
        this._raising.visible = Boolean(cell.isRaising);
        this._name.text = cell.displayName ?? `#${cell.speciesId}`;
        const forms = D.formCountText(cell);
        this._forms.text = forms ?? '';
        this._forms.visible = forms !== null;
        this._tooltip = D.cellTooltip(cell, V.rarityName(rarity));
        this.actor.accessible_name = this._tooltip;
    }
}

export class DexGrid {
    /**
     * @param {object} deps      {tooltip, sprites}
     * @param {object} handlers  {onOpen(cell), onStep(step), onRegion(key), onRarity(key), onReset()}
     */
    constructor(deps, handlers) {
        this._deps = deps;
        this._handlers = handlers;
        this._pager = new D.ScrollPager();

        this.actor = vbox('ptb-dex-grid-page', {x_expand: true, y_expand: true});

        this._regions = hbox('ptb-region-strip', {x_expand: true});
        this._regionTiles = new Map();
        for (const key of D.REGIONS)
            this._regions.add_child(this._regionTile(key));
        this.actor.add_child(this._regions);

        this._rarity = new RarityChips(deps.tooltip, r => handlers.onRarity(r));
        this.actor.add_child(this._rarity.actor);

        const gridLayout = new Clutter.GridLayout({
            row_homogeneous: true,
            column_homogeneous: true,
            row_spacing: 4,
            column_spacing: 4,
        });
        this._grid = new St.Widget({
            style_class: 'ptb-dex-grid',
            layout_manager: gridLayout,
            x_expand: true,
            y_expand: true,
            reactive: true,
        });
        this._cells = [];
        for (let i = 0; i < D.PAGE_SIZE; i++) {
            const cell = new DexCell(deps, c => handlers.onOpen(c));
            gridLayout.attach(cell.actor, i % D.COLUMNS, Math.floor(i / D.COLUMNS), 1, 1);
            this._cells.push(cell);
        }
        // Wheel/trackpad paging. Handled here (bubbling from the cells) and
        // stopped, so the popover never scrolls under the grid.
        this._grid.connect('scroll-event', (_a, event) => {
            const step = this._pager.step(pagerEvent(event));
            if (step !== 0)
                handlers.onStep(step);
            return Clutter.EVENT_STOP;
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
        body.add_child(this._grid);
        body.add_child(this._noResults);
        this.actor.add_child(body);

        this._pagerWidget = new Pager(step => handlers.onStep(step));
        this.actor.add_child(this._pagerWidget.actor);
    }

    _regionTile(key) {
        const b = new St.Button({style_class: 'ptb-region-tile', can_focus: true, x_expand: true});
        const box = vbox('ptb-region-box', {x_expand: true});
        const top = hbox('ptb-region-top', {x_align: Clutter.ActorAlign.CENTER});
        const name = label(V.regionName(key), 'ptb-region-name');
        const medal = label('', 'ptb-region-medal');
        top.add_child(name);
        top.add_child(medal);
        const count = label('', 'ptb-region-count', {x_align: Clutter.ActorAlign.CENTER});
        // A 3 px progress line: the fill is scaled, never re-laid-out.
        const track = new St.Widget({style_class: 'ptb-region-track', layout_manager: new Clutter.BinLayout(),
            x_expand: true});
        const fill = new St.Widget({style_class: 'ptb-region-fill', x_expand: true,
            pivot_point: new Graphene.Point({x: 0, y: 0.5})});
        track.add_child(fill);
        box.add_child(top);
        box.add_child(count);
        box.add_child(track);
        b.set_child(box);
        b.connect('clicked', () => this._handlers.onRegion(key));
        this._regionTiles.set(key, {button: b, medal, count, fill, region: null});
        this._deps.tooltip.attach(b, () => {
            const r = this._regionTiles.get(key).region;
            if (!r)
                return null;
            const text = D.regionProgressText(r, V.regionName(key));
            const shiny = r.shinyOwned > 0 ? ` · ✨ ${r.shinyOwned}` : '';
            return `${text}${shiny}\n${_('Click to show only this region; click again to show all')}`;
        });
        return b;
    }

    /**
     * @param {object} data     getDexPage reply data
     * @param {object} filters  dexModel filters
     */
    setData(data, filters) {
        for (const r of data.regions ?? []) {
            const tile = this._regionTiles.get(r.key);
            if (!tile)
                continue;
            tile.region = r;
            tile.count.text = `${r.owned}/${r.total}`;
            const medal = D.regionMedal(r);
            tile.medal.text = medal ?? '';
            tile.medal.visible = medal !== null;
            tile.fill.scale_x = D.regionFraction(r);
            if (r.complete)
                tile.button.add_style_class_name('ptb-region-complete');
            else
                tile.button.remove_style_class_name('ptb-region-complete');
            setChecked(tile.button, filters.region === r.key);
            setSensitive(tile.button, r.owned > 0 || filters.region === r.key);
        }
        this._rarity.set(data.rarityCounts, filters.rarity);

        const cells = data.cells ?? [];
        this._cells.forEach((c, i) => c.set(cells[i] ?? null));
        const empty = (data.total ?? cells.length) === 0;
        this._grid.visible = !empty;
        this._noResults.visible = empty;

        this._pagerWidget.set(data.page ?? 1, data.pageCount ?? 1);
    }

    /** Forget a half-done trackpad gesture (page changed by other means). */
    resetGesture() {
        this._pager.reset();
    }
}
