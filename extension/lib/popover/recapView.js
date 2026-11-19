/* Usage recap screen (recap-heatmap.md §1): it replaces the whole popover
 * body, like Settings upstream, and always opens on this week.
 *
 * The card is a small red handheld with a dark LCD; it keeps its own
 * colors in both themes (UsageRecapView.swift:150 forces light). Numbers
 * come from `getRecap` (protocol §7.5); recapModel.js turns them into text.
 * A reply that is not for the latest request is dropped, the previous
 * reply stays visible while the next one loads, and every reply checks
 * that the view still exists (pending commands fail on disable).
 */

import Cairo from 'cairo';
import Clutter from 'gi://Clutter';
import GObject from 'gi://GObject';
import Pango from 'gi://Pango';
import PangoCairo from 'gi://PangoCairo';
import St from 'gi://St';

import {_} from '../i18n.js';
import {compact} from '../format.js';
import * as R from '../recapModel.js';
import {Segmented, StaticSprite, setSensitive} from '../dex/dexWidgets.js';
import {hbox, label, spacer, vbox, wrapLabel} from './widgets.js';

/* LCD colors (UsageRecapView.swift:124-126); the card ignores the theme. */
const LCD = [0.51, 0.98, 0.62];
const BLOCK_HEIGHT = 4;
const BLOCK_GAP = 2;
const METER_HEIGHT = R.METER_BLOCKS * BLOCK_HEIGHT + (R.METER_BLOCKS - 1) * BLOCK_GAP;
const LABEL_GAP = 3;
const LABEL_HEIGHT = 10;
const SPRITE = 18;

function lcd(cr, alpha) {
    cr.setSourceRGBA(LCD[0], LCD[1], LCD[2], alpha);
}

/** Meter columns: 8 blocks lit from the bottom, a label row, a tooltip per column. */
const RecapMeters = GObject.registerClass(
class PtbRecapMeters extends St.DrawingArea {
    _init(tooltip) {
        super._init({
            style_class: 'ptb-recap-meters',
            x_expand: true,
            height: METER_HEIGHT + LABEL_GAP + LABEL_HEIGHT,
            reactive: true,
        });
        this._tooltip = tooltip;
        this._buckets = [];
        this._labels = [];
        this._scope = 'week';
        this._hovered = -1;
        const motion = new Clutter.MotionController();
        motion.connect('motion', (_c, _s, x) => this._setHovered(this._columnAt(x)));
        motion.connect('leave', () => this._setHovered(-1));
        this.add_action(motion);
        this.connect('notify::mapped', () => {
            if (!this.mapped)
                this._setHovered(-1);
        });
    }

    setData(buckets, scope) {
        this._buckets = buckets ?? [];
        this._scope = scope;
        this._labels = R.bucketLabels(this._buckets, scope);
        this._peak = R.meterPeak(this._buckets);
        this._hovered = -1;
        this._tooltip.hide(this);
        this.queue_repaint();
    }

    _geometry(width) {
        const n = this._buckets.length;
        const spacing = R.meterSpacing(this._scope);
        const w = n > 0 ? Math.max(0, (width - spacing * (n - 1)) / n) : 0;
        return {n, spacing, w};
    }

    _columnAt(x) {
        const {n, spacing, w} = this._geometry(this.width);
        if (n === 0 || x < 0 || x > this.width)
            return -1;
        return Math.min(n - 1, Math.floor(x / (w + spacing)));
    }

    _setHovered(i) {
        if (i === this._hovered)
            return;
        this._hovered = i;
        const bucket = this._buckets[i];
        if (!bucket || !this.mapped) {
            this._tooltip.hide(this);
            return;
        }
        const {spacing, w} = this._geometry(this.width);
        const [sx, sy] = this.get_transformed_position();
        const scale = this.get_transformed_size()[0] / Math.max(1, this.width);
        const centerX = sx + (i * (w + spacing) + w / 2) * scale;
        this._tooltip.showAt(this, R.bucketDescription(bucket, this._scope), centerX, sy - 4, true);
    }

    vfunc_repaint() {
        const cr = this.get_context();
        try {
            this._draw(cr);
        } finally {
            cr.$dispose();
        }
    }

    _draw(cr) {
        const [width] = this.get_surface_size();
        const {n, spacing, w} = this._geometry(width);
        if (n === 0)
            return;
        const layout = PangoCairo.create_layout(cr);
        const font = this.get_theme_node().get_font().copy();
        font.set_absolute_size((this._scope === 'week' ? 8 : 7) * Pango.SCALE);

        this._buckets.forEach((b, i) => {
            const x = i * (w + spacing);
            const lit = R.meterLit(b.tokens, this._peak);
            for (let block = 0; block < R.METER_BLOCKS; block++) {
                const on = block >= R.METER_BLOCKS - lit;
                if (on)
                    lcd(cr, b.isCurrent ? 1 : 0.55);
                else
                    lcd(cr, b.hasData ? 0.09 : 0.035);
                cr.rectangle(x, block * (BLOCK_HEIGHT + BLOCK_GAP), w, BLOCK_HEIGHT);
                cr.fill();
            }
            const text = this._labels[i];
            if (!text)
                return;
            // A zero-width frame: a wide label ("15") never widens its column.
            font.set_weight(b.isCurrent ? Pango.Weight.BOLD : Pango.Weight.NORMAL);
            layout.set_font_description(font);
            layout.set_text(text, -1);
            const [, logical] = layout.get_pixel_extents();
            lcd(cr, b.isCurrent ? 1 : 0.45);
            cr.moveTo(x + w / 2 - logical.width / 2, METER_HEIGHT + LABEL_GAP);
            PangoCairo.show_layout(cr, layout);
        });
    }
});

/** The blue camera lens of the card header (UsageRecapView.swift:175-186). */
const Lens = GObject.registerClass(
class PtbRecapLens extends St.DrawingArea {
    _init() {
        super._init({style_class: 'ptb-recap-lens', width: 16, height: 16, y_align: Clutter.ActorAlign.CENTER});
    }

    vfunc_repaint() {
        const cr = this.get_context();
        try {
            const r = 7.25;
            const c = 8;
            const g = new Cairo.RadialGradient(16 * 0.35, 16 * 0.3, 0, 16 * 0.35, 16 * 0.3, 11);
            g.addColorStopRGB(0, 0.72, 0.88, 1.0);
            g.addColorStopRGB(1, 0.15, 0.45, 0.85);
            cr.arc(c, c, r, 0, 2 * Math.PI);
            cr.setSource(g);
            cr.fillPreserve();
            cr.setSourceRGBA(1, 1, 1, 0.9);
            cr.setLineWidth(1.5);
            cr.stroke();
            cr.arc(c - 3, c - 3, 2, 0, 2 * Math.PI);
            cr.setSourceRGBA(1, 1, 1, 0.75);
            cr.fill();
        } finally {
            cr.$dispose();
        }
    }
});

function readoutCell(title) {
    const row = hbox('ptb-recap-readout', {x_expand: true});
    row.add_child(label(title.toLocaleUpperCase(), 'ptb-recap-readout-label', {y_align: Clutter.ActorAlign.END}));
    row.add_child(spacer());
    const value = label('', 'ptb-recap-readout-value', {y_align: Clutter.ActorAlign.END});
    row.add_child(value);
    return {row, value};
}

function navButton(text, iconName, iconFirst) {
    const box = hbox('ptb-recap-nav-box');
    const icon = new St.Icon({icon_name: iconName, style_class: 'ptb-recap-nav-icon'});
    const l = label(text, 'ptb-recap-nav-label');
    if (iconFirst) {
        box.add_child(icon);
        box.add_child(l);
    } else {
        box.add_child(l);
        box.add_child(icon);
    }
    return new St.Button({style_class: 'ptb-recap-nav', child: box, can_focus: true, accessible_name: text});
}

export class RecapView {
    /**
     * @param {object} deps  {tooltip, sprites, request(cmd, args), onBack()}
     */
    constructor(deps) {
        this._deps = deps;
        this._alive = true;
        this._open = false;
        this._scope = 'week';
        this._offset = 0;
        this._recap = null;
        this._failed = false;
        this._seq = 0;
        this._key = null;
        this._state = null;

        this.actor = vbox('ptb-recap', {x_expand: true, visible: false});
        this.actor.connect('destroy', () => {
            this._alive = false;
        });

        // 1. Back + scope picker.
        const top = hbox('ptb-recap-top', {x_expand: true});
        const back = navButton(_('Back'), 'go-previous-symbolic', true);
        back.add_style_class_name('ptb-recap-back');
        back.connect('clicked', () => deps.onBack());
        top.add_child(back);
        top.add_child(spacer());
        this._scopes = new Segmented(R.SCOPES.map(s => [s, R.scopeName(s)]), s => this._setScope(s),
            'ptb-segmented ptb-recap-scopes');
        top.add_child(this._scopes.actor);
        this.actor.add_child(top);

        // 2. Time row: the period itself is printed on the card.
        const time = hbox('ptb-recap-time', {x_expand: true});
        this._prev = navButton(_('Previous'), 'pan-start-symbolic', true);
        this._prev.connect('clicked', () => this._step(-1));
        this._current = new St.Button({style_class: 'ptb-recap-nav ptb-recap-current', label: '', can_focus: true});
        this._current.connect('clicked', () => this._go(0));
        this._next = navButton(_('Next'), 'pan-end-symbolic', false);
        this._next.connect('clicked', () => this._step(1));
        time.add_child(this._prev);
        time.add_child(spacer());
        time.add_child(this._current);
        time.add_child(spacer());
        time.add_child(this._next);
        this.actor.add_child(time);

        // 3. The card.
        const card = hbox('ptb-recap-card', {x_expand: true});
        card.add_child(new St.Widget({style_class: 'ptb-recap-hinge', y_expand: true}));
        const body = vbox('ptb-recap-body', {x_expand: true});
        card.add_child(body);

        const header = hbox('ptb-recap-header', {x_expand: true});
        header.add_child(new Lens());
        for (const led of ['red', 'yellow', 'green']) {
            header.add_child(new St.Widget({style_class: `ptb-recap-led ptb-recap-led-${led}`,
                y_align: Clutter.ActorAlign.CENTER}));
        }
        header.add_child(spacer());
        this._period = label('', 'ptb-recap-period', {y_align: Clutter.ActorAlign.CENTER});
        header.add_child(this._period);
        body.add_child(header);

        const screen = vbox('ptb-recap-screen', {x_expand: true});
        const totalRow = hbox('ptb-recap-total-row', {x_expand: true});
        this._total = label('', 'ptb-recap-total', {y_align: Clutter.ActorAlign.END});
        totalRow.add_child(this._total);
        totalRow.add_child(label(_('tokens').toLocaleUpperCase(), 'ptb-recap-unit',
            {y_align: Clutter.ActorAlign.END}));
        totalRow.add_child(spacer());
        this._chip = label('', 'ptb-recap-chip', {y_align: Clutter.ActorAlign.CENTER});
        totalRow.add_child(this._chip);
        screen.add_child(totalRow);
        this._meters = new RecapMeters(deps.tooltip);
        screen.add_child(this._meters);
        const grid = hbox('ptb-recap-readouts', {x_expand: true});
        const left = vbox('ptb-recap-readout-col', {x_expand: true});
        const right = vbox('ptb-recap-readout-col', {x_expand: true});
        this._bestDay = readoutCell(_('Best day'));
        this._bestStreak = readoutCell(_('Best streak'));
        this._activeDays = readoutCell(_('Active days'));
        this._graduated = readoutCell(_('Graduated'));
        left.add_child(this._bestDay.row);
        left.add_child(this._bestStreak.row);
        right.add_child(this._activeDays.row);
        right.add_child(this._graduated.row);
        grid.add_child(left);
        grid.add_child(right);
        screen.add_child(grid);
        body.add_child(screen);

        const strip = hbox('ptb-recap-strip', {x_expand: true});
        this._companion = new StaticSprite(deps.sprites, SPRITE, 'ptb-recap-sprite');
        strip.add_child(this._companion.actor);
        strip.add_child(new St.Widget({style_class: 'ptb-recap-divider', y_align: Clutter.ActorAlign.CENTER}));
        this._noGraduates = wrapLabel(_('No graduation in this period.'), 'ptb-recap-empty',
            {y_align: Clutter.ActorAlign.CENTER});
        strip.add_child(this._noGraduates);
        this._chips = hbox('ptb-recap-chips', {x_expand: true});
        strip.add_child(this._chips);
        body.add_child(strip);
        this.actor.add_child(card);

        // 4. Caption lines.
        const captions = vbox('ptb-recap-captions', {x_expand: true});
        this._bestLine = wrapLabel('', 'ptb-recap-caption');
        this._compareLine = wrapLabel('', 'ptb-recap-caption');
        this._errorLine = wrapLabel(_('The helper did not answer. The recap loads again with the next update.'),
            'ptb-recap-caption ptb-warning-text');
        captions.add_child(this._bestLine);
        captions.add_child(this._compareLine);
        captions.add_child(this._errorLine);
        this.actor.add_child(captions);

        this._chipKey = null;
        this._render();
    }

    get isOpen() {
        return this._open;
    }

    /** Opens on this week (no persisted recap state, RV:355-357). */
    open() {
        this._open = true;
        this._scope = 'week';
        this._offset = 0;
        this._recap = null;
        this._failed = false;
        this.actor.show();
        this._key = R.refreshKey(this._state);
        this._request();
        this._render();
    }

    close() {
        this._open = false;
        this._seq++; // drop any reply in flight
        this._deps.tooltip.hide();
        this.actor.hide();
    }

    /** @param {object} ctx  {state} */
    update(ctx) {
        this._state = ctx.state;
        this._companion.set(R.companionSpriteRequest(ctx.state?.companion));
        if (!this._open)
            return;
        // Re-fetch only when today's day or tokens moved (protocol §7.5), or after a failure.
        const key = R.refreshKey(ctx.state);
        if (key !== this._key || this._failed) {
            this._key = key;
            this._request();
        }
    }

    _setScope(scope) {
        this._scope = R.normalizeScope(scope);
        this._offset = 0; // a new scope starts on its running period
        this._request();
        this._render();
    }

    _step(direction) {
        this._go(R.step(this._offset, direction, this._recap));
    }

    _go(offset) {
        if (offset === this._offset)
            return;
        this._offset = offset;
        this._request();
        this._render();
    }

    _request() {
        const seq = ++this._seq;
        const args = {scope: this._scope, offset: this._offset};
        this._deps.request('getRecap', args).then(data => {
            if (!this._alive || !this._open || seq !== this._seq)
                return;
            if (data?.scope !== args.scope || data?.offset !== args.offset)
                return;
            this._recap = data;
            this._failed = false;
            this._render();
        }).catch(e => {
            if (!this._alive || !this._open || seq !== this._seq)
                return;
            if (e?.code !== 'helperExited')
                console.warn(`PokeTokenBar: getRecap failed: ${e?.code ?? ''} ${e?.message ?? e}`);
            this._failed = true;
            this._render();
        });
    }

    _render() {
        const recap = this._recap;
        this._scopes.set(this._scope);
        this._current.label = R.currentPeriodName(this._scope);
        setSensitive(this._prev, R.canGoBack(recap));
        setSensitive(this._current, R.canGoToCurrent(this._offset));
        setSensitive(this._next, R.canGoForward(this._offset));

        this._period.text = R.periodLabel(recap).toLocaleUpperCase();
        this._total.text = recap ? compact(recap.total) : '–';
        const d = R.delta(recap);
        const chip = R.chipText(d);
        this._chip.visible = chip !== null;
        this._chip.text = chip ?? '';
        if (d !== null && d < 0)
            this._chip.add_style_class_name('ptb-recap-chip-down');
        else
            this._chip.remove_style_class_name('ptb-recap-chip-down');

        this._meters.setData(recap?.buckets ?? [], recap?.scope ?? this._scope);
        this._bestDay.value.text = recap?.bestDay ? compact(recap.bestDay.tokens) : '--';
        this._bestStreak.value.text = recap ? R.daysText(recap.bestStreak) : '--';
        this._activeDays.value.text = recap ? R.activeDaysText(recap) : '--';
        this._graduated.value.text = recap ? String(Number(recap.graduated) || 0) : '--';

        this._renderGraduates(R.stripGraduates(recap));

        const best = R.bestDayLine(recap);
        this._bestLine.text = best ?? '';
        this._bestLine.visible = best !== null;
        const compare = R.compareLine(recap);
        this._compareLine.text = compare ?? '';
        this._compareLine.visible = compare !== null;
        this._errorLine.visible = this._failed;
    }

    _renderGraduates(graduates) {
        const key = JSON.stringify([this._recap !== null, graduates.map(g => [g.entryId, g.displayName])]);
        if (key === this._chipKey)
            return;
        this._chipKey = key;
        this._chips.destroy_all_children();
        this._noGraduates.visible = graduates.length === 0 && this._recap !== null;
        this._chips.visible = graduates.length > 0;
        for (const g of graduates) {
            const chip = hbox('ptb-recap-grad');
            const holder = new St.Widget({layout_manager: new Clutter.BinLayout(), style_class: 'ptb-recap-grad-sprite'});
            const sprite = new StaticSprite(this._deps.sprites, SPRITE, 'ptb-recap-sprite');
            sprite.set(R.graduateSpriteRequest(g));
            holder.add_child(sprite.actor);
            if (g.isShiny) {
                holder.add_child(new St.Label({
                    text: '✨',
                    style_class: 'ptb-recap-grad-shiny',
                    x_align: Clutter.ActorAlign.END,
                    y_align: Clutter.ActorAlign.START,
                    translation_x: 3,
                    translation_y: -2,
                }));
            }
            chip.add_child(holder);
            chip.add_child(label(g.displayName ?? `#${g.speciesId}`, 'ptb-recap-grad-name'));
            this._deps.tooltip.attach(chip, g.displayName ?? `#${g.speciesId}`);
            this._chips.add_child(chip);
        }
    }
}
