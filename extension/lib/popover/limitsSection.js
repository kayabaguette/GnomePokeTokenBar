/* Official limits (ui.md §2.1 F): auth/stale/error states, plan and account,
 * one quota row per window (label, reset countdown, percent, Cairo bar with
 * pace marker), the 5 h forecast and the current 5 h block.
 */

import St from 'gi://St';

import {_, fmt} from '../i18n.js';
import {
    compact, displayPercent, limitPercentText, parseTs, relativeAgo, resetText,
    clock,
} from '../format.js';
import {LimitBar} from '../limitBar.js';
import * as V from '../viewModel.js';
import {hbox, label, spacer, textButton, vbox, wrapLabel} from './widgets.js';

const TIER_CLASSES = V.PACE_TIERS.map(t => `ptb-tier-${t}`);
const TEXT_TIER_CLASSES = V.PACE_TIERS.map(t => `ptb-tier-text-${t}`);

/** Swap the tier class only when it changes: each class change restyles the actor. */
function setTierClass(actor, classes, prefix, tier) {
    const wanted = `${prefix}${V.normalizeTier(tier)}`;
    if (actor.has_style_class_name(wanted))
        return;
    for (const c of classes)
        actor.remove_style_class_name(c);
    actor.add_style_class_name(wanted);
}

class QuotaRow {
    constructor(tooltip) {
        this.actor = vbox('ptb-quota-row', {x_expand: true, reactive: true});
        const top = hbox('ptb-quota-top', {x_expand: true});
        this._name = label('', 'ptb-quota-name');
        this._reset = label('', 'ptb-tertiary ptb-small ptb-quota-reset');
        this._percent = label('', 'ptb-quota-percent');
        top.add_child(this._name);
        top.add_child(spacer());
        top.add_child(this._reset);
        top.add_child(this._percent);
        this.actor.add_child(top);
        this._bar = new LimitBar();
        this.actor.add_child(this._bar);
        this._help = null;
        tooltip.attach(this.actor, () => this._help);
    }

    update(window, mode, nowMs) {
        this._name.text = V.windowLabel(window);
        const resetMs = parseTs(window.resetsAt);
        if (resetMs !== null)
            this._reset.text = resetText(resetMs, nowMs);
        else
            this._reset.text = window.notStarted ? _('Starts with your next message') : '';
        this._percent.text = limitPercentText(window.utilization, mode);
        setTierClass(this._percent, TEXT_TIER_CLASSES, 'ptb-tier-text-', window.gaugeTier);
        setTierClass(this._bar, TIER_CLASSES, 'ptb-tier-', window.gaugeTier);
        const pace = typeof window.pace === 'number' ? window.pace : null;
        const marker = pace === null ? null : (mode === 'remaining' ? 1 - pace : pace);
        this._bar.setValue(displayPercent(window.utilization, mode) / 100, marker);
        this._help = V.paceHelp(window, mode);
    }
}

class ProviderLimits {
    constructor(deps, providerId) {
        this._deps = deps;
        this._providerId = providerId;
        this._rows = new Map();
        this._busy = false;

        this.actor = vbox('ptb-limits-provider', {x_expand: true});

        const header = hbox('ptb-limits-header', {x_expand: true});
        header.add_child(label(_('Limits (official)'), 'ptb-caption'));
        header.add_child(spacer());
        this._plan = label('', 'ptb-tertiary ptb-small');
        header.add_child(this._plan);
        this.actor.add_child(header);

        this._account = label('', 'ptb-tertiary ptb-small');
        this.actor.add_child(this._account);

        // Expired banner (orange 12 %, radius 8).
        this._banner = vbox('ptb-banner', {x_expand: true});
        const bannerTop = hbox('ptb-banner-top', {x_expand: true});
        bannerTop.add_child(new St.Icon({icon_name: 'dialog-warning-symbolic', style_class: 'ptb-banner-icon'}));
        this._bannerTitle = wrapLabel(_('Claude session expired — limits can’t refresh'), 'ptb-banner-title');
        bannerTop.add_child(this._bannerTitle);
        this._retry = textButton(_('Retry'));
        this._retry.connect('clicked', () => this._refresh());
        bannerTop.add_child(this._retry);
        this._banner.add_child(bannerTop);
        this._bannerHint = wrapLabel(
            _('Values shown are from before expiry. Retry, or run Claude Code once to refresh automatically.'),
            'ptb-tertiary ptb-tiny');
        this._banner.add_child(this._bannerHint);
        this.actor.add_child(this._banner);

        // Load / stale / error row.
        this._loadRow = vbox('ptb-load-row', {x_expand: true});
        const loadTop = hbox('ptb-load-top', {x_expand: true});
        this._loadText = label('', 'ptb-secondary ptb-small');
        loadTop.add_child(this._loadText);
        loadTop.add_child(spacer());
        this._loadButton = textButton(_('Refresh'));
        this._loadButton.connect('clicked', () => this._refresh());
        loadTop.add_child(this._loadButton);
        this._loadRow.add_child(loadTop);
        this._loadError = wrapLabel('', 'ptb-orange-text ptb-tiny');
        this._loadRow.add_child(this._loadError);
        this.actor.add_child(this._loadRow);

        // Missing credentials hint.
        this._missing = wrapLabel('', 'ptb-secondary ptb-small ptb-hint');
        this.actor.add_child(this._missing);

        this._rowsBox = vbox('ptb-quota-rows', {x_expand: true});
        this.actor.add_child(this._rowsBox);

        this._forecast = hbox('ptb-forecast');
        this._forecastIcon = new St.Icon({style_class: 'ptb-forecast-icon'});
        this._forecastText = label('', 'ptb-small');
        this._forecast.add_child(this._forecastIcon);
        this._forecast.add_child(this._forecastText);

        this._block = hbox('ptb-block-row', {x_expand: true});
        this._blockLabel = label(_('Claude current 5h block'), 'ptb-secondary ptb-small');
        this._blockTokens = label('', 'ptb-small ptb-block-tokens');
        this._blockReset = label('', 'ptb-tertiary ptb-small');
        this._block.add_child(this._blockLabel);
        this._block.add_child(this._blockTokens);
        this._block.add_child(spacer());
        this._block.add_child(this._blockReset);

        this._destroyed = false;
        this.actor.connect('destroy', () => {
            this._destroyed = true;
            // The forecast and block rows are detached while unused; free them too.
            for (const extra of [this._forecast, this._block]) {
                if (!extra.get_parent())
                    extra.destroy();
            }
        });
    }

    _refresh() {
        if (this._busy)
            return;
        this._busy = true;
        this._syncBusy();
        this._deps.refresh().finally(() => {
            this._busy = false;
            if (!this._destroyed)
                this._syncBusy();
        });
    }

    _syncBusy() {
        for (const b of [this._retry, this._loadButton]) {
            b.reactive = !this._busy;
            b.label = this._busy ? _('Refreshing…') : (b === this._retry ? _('Retry') : _('Refresh'));
        }
    }

    update(ctx, limits) {
        const nowMs = ctx.nowMs;
        const mode = ctx.settings.limitMode;
        const auth = ['ok', 'expired', 'missing', 'error'].includes(limits.authState) ? limits.authState : 'error';

        this._plan.text = limits.plan ? fmt(_('Plan %s'), limits.plan) : '';
        this._account.text = limits.account ? fmt(_('Account %s'), limits.account) : '';
        this._account.visible = Boolean(limits.account);

        // Exactly one status element (ui.md §2.1 F priority order).
        this._banner.visible = auth === 'expired';
        this._missing.visible = auth === 'missing';
        this._missing.text = auth === 'missing' ? (V.limitsErrorText(limits) ?? '') : '';

        const updatedMs = parseTs(limits.updatedAt);
        let loadText = null;
        let loadError = null;
        if (auth === 'error') {
            loadText = limits.available && updatedMs !== null
                ? fmt(_('Stale · %s'), relativeAgo(updatedMs, nowMs))
                : _('Couldn’t load official limits');
            loadError = V.limitsErrorText(limits);
            const backoffMs = parseTs(limits.backoffUntil);
            if (backoffMs !== null && backoffMs > nowMs)
                loadError = `${loadError ?? ''} ${fmt(_('Next try at %s.'), clock(backoffMs))}`.trim();
        } else if (auth === 'ok' && !limits.available) {
            loadText = _('Load official limits');
        } else if (auth === 'ok' && limits.isStale && updatedMs !== null) {
            loadText = fmt(_('Stale · %s'), relativeAgo(updatedMs, nowMs));
        }
        this._loadRow.visible = loadText !== null;
        this._loadText.text = loadText ?? '';
        const stale = loadText !== null && (auth === 'error' || limits.isStale) && limits.available;
        if (stale)
            this._loadText.add_style_class_name('ptb-orange-text');
        else
            this._loadText.remove_style_class_name('ptb-orange-text');
        this._loadError.visible = Boolean(loadError);
        this._loadError.text = loadError ?? '';

        this._updateRows(ctx, limits, mode, nowMs);
        this._rowsBox.opacity = auth === 'expired' ? 128 : 255;
    }

    _updateRows(ctx, limits, mode, nowMs) {
        const windows = limits.available ? (limits.windows ?? []).filter(w => typeof w.utilization === 'number') : [];
        const usage = ctx.state?.usage?.providers?.[this._providerId];
        const forecast = usage?.forecast;
        const showForecast = windows.length > 0 && parseTs(forecast?.depletionAt) !== null;
        const block = usage?.activeBlock;
        const blockEndMs = parseTs(block?.end);
        const showBlock = Boolean(block) && blockEndMs !== null && blockEndMs > nowMs;

        // Desired order: rows, the forecast after the 5 h row (else after the
        // first row), then the current block.
        const keep = new Set(windows.map(w => w.key));
        for (const [key, row] of this._rows) {
            if (!keep.has(key)) {
                row.actor.destroy();
                this._rows.delete(key);
            }
        }
        const order = [];
        for (const w of windows) {
            let row = this._rows.get(w.key);
            if (!row) {
                row = new QuotaRow(this._deps.tooltip);
                this._rows.set(w.key, row);
            }
            order.push(row.actor);
        }
        if (showForecast) {
            const i = windows.findIndex(w => w.kind === 'fiveHour');
            order.splice((i >= 0 ? i : 0) + 1, 0, this._forecast);
        }
        if (showBlock)
            order.push(this._block);

        // Re-parent and reorder before updating: widgets restyled while out
        // of the stage make St complain.
        for (const extra of [this._forecast, this._block]) {
            if (!order.includes(extra) && extra.get_parent())
                extra.get_parent().remove_child(extra);
        }
        order.forEach((child, i) => {
            if (child.get_parent() !== this._rowsBox)
                this._rowsBox.insert_child_at_index(child, i);
            else
                this._rowsBox.set_child_at_index(child, i);
        });

        windows.forEach(w => this._rows.get(w.key).update(w, mode, nowMs));
        if (showForecast)
            this._updateForecast(forecast);
        if (showBlock) {
            this._blockTokens.text = compact(block.tokens);
            this._blockReset.text = fmt(_('Reset %s'), resetText(blockEndMs, nowMs));
        }
        this._rowsBox.visible = order.length > 0;
    }

    _updateForecast(forecast) {
        const atMs = parseTs(forecast.depletionAt);
        if (forecast.beforeReset) {
            this._forecastIcon.icon_name = 'dialog-warning-symbolic';
            this._forecastText.text = fmt(_('At current rate, limit hit at %s'), clock(atMs));
            this._forecast.remove_style_class_name('ptb-tertiary');
            this._forecast.add_style_class_name('ptb-orange-text');
        } else {
            this._forecastIcon.icon_name = 'object-select-symbolic';
            this._forecastText.text = _('Won’t hit limit before reset at current rate');
            this._forecast.remove_style_class_name('ptb-orange-text');
            this._forecast.add_style_class_name('ptb-tertiary');
        }
    }

    destroy() {
        this.actor.destroy();
    }
}

export class LimitsSection {
    /** @param {object} deps  {tooltip, refresh(): Promise} */
    constructor(deps) {
        this._deps = deps;
        this._providers = new Map();
        this.actor = vbox('ptb-limits', {x_expand: true});
    }

    get hasContent() {
        return this._providers.size > 0;
    }

    update(ctx) {
        const limits = ctx.state?.limits?.providers ?? {};
        const order = (ctx.state?.meta?.providers ?? []).map(p => p.id).filter(id => limits[id]);
        for (const id of Object.keys(limits)) {
            if (!order.includes(id))
                order.push(id);
        }
        for (const [id, block] of this._providers) {
            if (!order.includes(id)) {
                block.destroy();
                this._providers.delete(id);
            }
        }
        order.forEach((id, i) => {
            let block = this._providers.get(id);
            if (!block) {
                block = new ProviderLimits(this._deps, id);
                this._providers.set(id, block);
                this.actor.add_child(block.actor);
            }
            this.actor.set_child_at_index(block.actor, i);
            block.update(ctx, limits[id]);
        });
        this.actor.visible = this._providers.size > 0;
    }
}
