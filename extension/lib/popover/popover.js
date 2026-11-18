/* Popover frame (ui.md §2.0): tab bar, scrolling content, footer.
 * Home holds the companion card, usage totals with the daily chart, and the
 * official limits. The Pokédex tab is a placeholder until Phase 7.
 */

import St from 'gi://St';

import * as Main from 'resource:///org/gnome/shell/ui/main.js';

import {_, fmt} from '../i18n.js';
import {CompanionCard} from './companionCard.js';
import {Footer} from './footer.js';
import {LimitsSection} from './limitsSection.js';
import {UsageSection} from './usageSection.js';
import {label, vbox, hbox, wrapLabel} from './widgets.js';

/* ui.md §2: Home scrolls inside a fixed 520 px area; clamp on small screens. */
const SCROLL_MAX_HEIGHT = 520;
const SCREEN_MARGIN = 160;

function separator() {
    return new St.Widget({style_class: 'ptb-separator', x_expand: true});
}

export class Popover {
    /**
     * @param {object} deps  {tooltip, sprites, refresh(), openPrefs(), setTrendWindow(w)}
     */
    constructor(deps) {
        this.actor = vbox('ptb-popover');

        const tabs = hbox('ptb-tabs', {x_expand: true});
        this._tabButtons = {};
        for (const [name, title] of [['home', _('Home')], ['dex', _('Pokédex')]]) {
            const b = new St.Button({
                style_class: 'ptb-tab',
                label: title,
                x_expand: true,
                can_focus: true,
            });
            b.connect('clicked', () => this.showTab(name));
            tabs.add_child(b);
            this._tabButtons[name] = b;
        }
        this.actor.add_child(tabs);

        this._scroll = new St.ScrollView({
            style_class: 'ptb-scroll',
            hscrollbar_policy: St.PolicyType.NEVER,
            vscrollbar_policy: St.PolicyType.AUTOMATIC,
            overlay_scrollbars: true,
            x_expand: true,
        });
        this.actor.add_child(this._scroll);

        this._home = vbox('ptb-home', {x_expand: true});
        this._companion = new CompanionCard(deps);
        this._usage = new UsageSection(deps);
        this._limits = new LimitsSection(deps);
        this._limitsSeparator = separator();
        this._home.add_child(this._companion.actor);
        this._home.add_child(separator());
        this._home.add_child(this._usage.actor);
        this._home.add_child(this._limitsSeparator);
        this._home.add_child(this._limits.actor);

        this._dex = vbox('ptb-dex-placeholder', {x_expand: true});
        this._dexTitle = label(_('Pokédex'), 'ptb-dex-title');
        this._dexCount = label('', 'ptb-secondary');
        this._dexNote = wrapLabel('', 'ptb-tertiary ptb-small');
        this._dex.add_child(this._dexTitle);
        this._dex.add_child(this._dexCount);
        this._dex.add_child(this._dexNote);

        this.actor.add_child(separator());
        this._footer = new Footer(deps);
        this.actor.add_child(this._footer.actor);

        // The pages swap in and out of the scroll view; the detached one is freed here.
        this.actor.connect('destroy', () => {
            for (const page of [this._home, this._dex]) {
                if (!page.get_parent())
                    page.destroy();
            }
        });

        this._tab = null;
        this.showTab('home');
    }

    showTab(name) {
        if (this._tab === name)
            return;
        this._tab = name;
        for (const [n, b] of Object.entries(this._tabButtons)) {
            if (n === name)
                b.add_style_pseudo_class('checked');
            else
                b.remove_style_pseudo_class('checked');
        }
        const page = name === 'dex' ? this._dex : this._home;
        if (this._scroll.get_child() !== page)
            this._scroll.set_child(page);
        this._footer.setHomeVisible(name === 'home');
    }

    /** Called when the menu opens: back to Home, top of the page, clamped height. */
    onOpen(tab = 'home') {
        this.showTab(tab);
        this._scroll.vadjustment.value = 0;
        const monitor = Main.layoutManager.primaryMonitor;
        const scale = St.ThemeContext.get_for_stage(global.stage).scale_factor;
        if (monitor) {
            const available = Math.floor(monitor.height / scale) - SCREEN_MARGIN;
            this._scroll.style = `max-height: ${Math.max(240, Math.min(SCROLL_MAX_HEIGHT, available))}px;`;
        }
    }

    /** @param {object} ctx  {state, settings, nowMs, helperStatus} */
    update(ctx) {
        this._companion.update(ctx);
        this._usage.update(ctx);
        this._limits.update(ctx);
        this._limitsSeparator.visible = this._limits.hasContent;
        this._footer.update(ctx);

        const dex = ctx.state?.companion?.dexSummary;
        if (dex && dex.entries > 0) {
            this._dexCount.text = fmt(_('%d of %d species · %d shiny'), dex.ownedSpecies, dex.nationalTotal, dex.shinySpecies);
            this._dexNote.text = _('Browsing, search and filters arrive in the next update.');
        } else {
            this._dexCount.text = _('No Pokémon yet');
            this._dexNote.text = _('Your first hatch starts the Pokédex.');
        }
    }
}
