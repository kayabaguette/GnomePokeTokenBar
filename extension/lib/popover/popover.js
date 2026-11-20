/* Popover frame (ui.md §2.0): tab bar, content, footer.
 * Home scrolls and holds the companion card, usage totals with the daily
 * chart, and the official limits. The Pokédex tab (lib/dex/) has a fixed
 * height of its own: its grid pages instead of scrolling.
 */

import St from 'gi://St';

import * as Main from 'resource:///org/gnome/shell/ui/main.js';

import {_} from '../i18n.js';
import {DexView} from '../dex/dexView.js';
import {BagView} from '../shop/bagView.js';
import {ShopView} from '../shop/shopView.js';
import {CompanionCard} from './companionCard.js';
import {Footer} from './footer.js';
import {LimitsSection} from './limitsSection.js';
import {RecapView} from './recapView.js';
import {UsageSection} from './usageSection.js';
import {vbox, hbox} from './widgets.js';

/* ui.md §2: Home scrolls inside a fixed 520 px area and the Pokédex is
 * 520 px tall (Collection); clamp both on small screens. */
const SCROLL_MAX_HEIGHT = 520;
const SCREEN_MARGIN = 160;

function separator() {
    return new St.Widget({style_class: 'ptb-separator', x_expand: true});
}

export class Popover {
    /**
     * @param {object} deps  {tooltip, sprites, refresh(), openPrefs(), setTrendWindow(w),
     *                        setActivityView(v), request(cmd, args)}
     */
    constructor(deps) {
        this.actor = vbox('ptb-popover');

        const tabs = hbox('ptb-tabs', {x_expand: true});
        this._tabButtons = {};
        for (const [name, title] of [['home', _('Home')], ['shop', _('Shop')], ['bag', _('Bag')],
            ['dex', _('Pokédex')]]) {
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
        this._companion = new CompanionCard({
            ...deps,
            openDexEntry: (speciesId, form) => this.openDexEntry(speciesId, form),
        });
        this._usage = new UsageSection({...deps, openRecap: () => this.openRecap()});
        this._limits = new LimitsSection(deps);
        this._limitsSeparator = separator();
        this._home.add_child(this._companion.actor);
        this._home.add_child(separator());
        this._home.add_child(this._usage.actor);
        this._home.add_child(this._limitsSeparator);
        this._home.add_child(this._limits.actor);

        this._scroll.set_child(this._home);

        this._dex = new DexView({tooltip: deps.tooltip, sprites: deps.sprites, request: deps.request});
        this.actor.add_child(this._dex.actor);

        // Shop and Bag (bag-shop.md §3): a bought egg and every Bag use return to Home.
        this._shop = new ShopView({...deps, onEggBought: () => this.showTab('home')});
        this._bag = new BagView({
            ...deps,
            openShop: () => this.showTab('shop'),
            onUsed: feedback => this._playFeedback(feedback),
        });
        this.actor.insert_child_above(this._shop.actor, this._scroll);
        this.actor.insert_child_above(this._bag.actor, this._shop.actor);

        this.actor.add_child(separator());
        this._footer = new Footer(deps);
        this.actor.add_child(this._footer.actor);

        // The usage recap replaces the whole body (recap-heatmap.md §1.1).
        this._recap = new RecapView({...deps, onBack: () => this.closeRecap()});
        this.actor.add_child(this._recap.actor);
        this._hiddenForRecap = null;

        this._tab = null;
        this.showTab('home');
    }

    openRecap() {
        if (this._hiddenForRecap)
            return;
        this._hiddenForRecap = this.actor.get_children().filter(c => c.visible && c !== this._recap.actor);
        for (const c of this._hiddenForRecap)
            c.hide();
        this._recap.open();
    }

    /** Back to the tab underneath (Back button, reopen, tab switches). */
    closeRecap() {
        if (!this._hiddenForRecap)
            return;
        this._recap.close();
        for (const c of this._hiddenForRecap)
            c.show();
        this._hiddenForRecap = null;
    }

    showTab(name) {
        this.closeRecap();
        if (this._tab === name)
            return;
        this._tab = name;
        for (const [n, b] of Object.entries(this._tabButtons)) {
            if (n === name)
                b.add_style_pseudo_class('checked');
            else
                b.remove_style_pseudo_class('checked');
        }
        this._scroll.visible = name === 'home';
        for (const [tab, view] of [['shop', this._shop], ['bag', this._bag]]) {
            if (name === tab)
                view.show();
            else
                view.hide();
        }
        if (name === 'dex')
            this._dex.show();
        else
            this._dex.hide();
        this._footer.setHomeVisible(name === 'home');
    }

    /**
     * Called when the menu opens: Home (or the tab asked for), top of the
     * page, clamped height. 'dex' comes from the right-click "Pokédex" item,
     * which opens the grid segment (openRepresentativeDex).
     */
    onOpen(tab = 'home') {
        const monitor = Main.layoutManager.primaryMonitor;
        const scale = St.ThemeContext.get_for_stage(global.stage).scale_factor;
        let height = SCROLL_MAX_HEIGHT;
        if (monitor) {
            const available = Math.floor(monitor.height / scale) - SCREEN_MARGIN;
            height = Math.max(240, Math.min(SCROLL_MAX_HEIGHT, available));
        }
        this._scroll.style = `max-height: ${height}px;`;
        this._dex.setHeight(height);
        this.closeRecap(); // every reopen starts on Home (PopoverNavigation.reset)
        this._shop.setHeight(height);
        this._bag.setHeight(height);
        this._shop.reset();
        this._bag.reset();
        this._tab = null; // the menu was closed: re-run the tab switch
        if (tab === 'dex')
            this._dex.openGrid();
        this.showTab(tab);
        this._scroll.vadjustment.value = 0;
    }

    /** Open a species' Pokédex detail (Home sprite and evolution line, ui.md §3.1). */
    openDexEntry(speciesId, form = null) {
        this._dex.openSpecies(speciesId, {form}); // first, so showing the tab loads no grid page
        this.showTab('dex');
    }

    /** Home, top of the page, then the "+XP" capsule or the Mint sparkle on the sprite tile. */
    _playFeedback(feedback) {
        this.showTab('home');
        this._scroll.vadjustment.value = 0;
        if (feedback?.kind === 'xp')
            this._companion.playXp(feedback.xp);
        else if (feedback?.kind === 'sparkle')
            this._companion.playSparkle();
    }

    /** @param {object} ctx  {state, settings, nowMs, helperStatus} */
    /** The hatch/evolve flash on the companion card (M2 decision 32). */
    setCelebrating(on) {
        this._companion.setCelebrating(on);
    }

    /** The Ditto reveal's 🎭 on the companion card, over the flash (M3 decision 43). */
    playRevealMask() {
        this._companion.playRevealMask();
    }

    update(ctx) {
        this._recap.update(ctx);
        this._companion.update(ctx);
        this._usage.update(ctx);
        this._limits.update(ctx);
        this._limitsSeparator.visible = this._limits.hasContent;
        this._footer.update(ctx);
        this._dex.update(ctx);
        this._shop.update(ctx);
        this._bag.update(ctx);
    }
}
