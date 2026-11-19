/* Top-bar indicator (ui.md §1): animated sprite plus 1–2 text lines, the
 * popover on left click, a secondary menu on right click.
 */

import Clutter from 'gi://Clutter';
import GLib from 'gi://GLib';
import GObject from 'gi://GObject';
import St from 'gi://St';

import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import * as PanelMenu from 'resource:///org/gnome/shell/ui/panelMenu.js';
import * as PopupMenu from 'resource:///org/gnome/shell/ui/popupMenu.js';

import {_} from './i18n.js';
import {Popover} from './popover/popover.js';
import {
    PANEL_BOX_HEIGHT, PANEL_MAX_WIDTH, SpriteAnimator, floorForQuality,
} from './spriteAnimator.js';
import {menuSubjectRequest} from './sprites.js';
import * as V from './viewModel.js';

/* While the popover is open its countdowns and "Updated …" tick this often. */
const OPEN_TICK_S = 30;

const TEXT_TIER_CLASSES = V.PACE_TIERS.map(t => `ptb-tier-text-${t}`);

export const Indicator = GObject.registerClass(
class PtbIndicator extends PanelMenu.Button {
    /**
     * @param {object} controller  the extension: settings, tooltip, sprites,
     *   theme, context(), refresh(), openPrefs(), setTrendWindow(), setActivityView(), request()
     */
    _init(controller) {
        super._init(0.5, 'PokeTokenBar', false);
        this._controller = controller;
        this._lineSignature = null;
        this._spritePath = undefined;
        this._spriteRetried = new Set();
        this._openTab = 'home';
        this._tickId = 0;
        this._destroyed = false;

        // Left click toggles the popover; the inherited gesture accepts any
        // button by default, so restrict it and give right click its own menu.
        this._clickGesture.set_required_button(Clutter.BUTTON_PRIMARY);
        this._secondaryGesture = new Clutter.ClickGesture({recognize_on_press: true});
        this._secondaryGesture.set_required_button(Clutter.BUTTON_SECONDARY);
        this._secondaryGesture.connect('recognize', () => {
            if (!this._destroyed)
                this._toggleSecondary();
        });
        this.add_action(this._secondaryGesture);

        this._buildPanel();
        this._buildPopover();
        this._buildSecondaryMenu();
        // The panel's own text color decides the limit-item palette: the panel
        // stays light-on-dark in the overview and on the lock screen even with
        // the light style, so the global variant is not enough here.
        this.connect('style-changed', () => {
            if (!this._destroyed)
                this._syncPanelContrast();
        });
        controller.theme.track(this.menu.actor);
        controller.theme.track(this._secondary.actor);

        // Disconnected first thing in _onDestroy(): the base class closes an
        // open menu while it destroys it, which would call back into a
        // half-torn-down indicator (screen lock with the popover open).
        this._menuOpenId = this.menu.connect('open-state-changed', (_m, open) => this._onMenuOpen(open));
        controller.tooltip.attach(this, () => this._tooltipText());
    }

    _buildPanel() {
        const box = new St.BoxLayout({style_class: 'panel-status-menu-box ptb-panel-box'});
        this._sprite = new SpriteAnimator({
            boxHeight: PANEL_BOX_HEIGHT,
            maxWidth: PANEL_MAX_WIDTH,
            floorMs: floorForQuality(this._controller.settings.get_string('animation-quality')),
            styleClass: 'ptb-panel-sprite',
        });
        this._sprite.onLoadFailed = () => this._onPanelSpriteFailed();
        box.add_child(this._sprite);

        this._lines = new St.BoxLayout({
            style_class: 'ptb-panel-lines',
            orientation: Clutter.Orientation.VERTICAL,
            y_align: Clutter.ActorAlign.CENTER,
        });
        box.add_child(this._lines);
        this.add_child(box);
    }

    _syncPanelContrast() {
        const c = this.get_theme_node().get_foreground_color();
        const luminance = (0.2126 * c.red + 0.7152 * c.green + 0.0722 * c.blue) / 255;
        if (luminance < 0.5)
            this._lines.add_style_class_name('ptb-panel-on-light');
        else
            this._lines.remove_style_class_name('ptb-panel-on-light');
    }

    _buildPopover() {
        const c = this._controller;
        this._popover = new Popover({
            tooltip: c.tooltip,
            sprites: c.sprites,
            refresh: () => c.refresh(),
            openPrefs: () => {
                this.menu.close();
                c.openPrefs();
            },
            setTrendWindow: w => c.setTrendWindow(w),
            setActivityView: v => c.setActivityView(v),
            request: (cmd, args) => c.request(cmd, args),
        });
        const item = new PopupMenu.PopupBaseMenuItem({
            reactive: false,
            can_focus: false,
            style_class: 'ptb-popover-item',
        });
        item.add_child(this._popover.actor);
        this.menu.addMenuItem(item);
        this.menu.actor.add_style_class_name('ptb-menu');
    }

    _buildSecondaryMenu() {
        const c = this._controller;
        const menu = new PopupMenu.PopupMenu(this, 0.5, St.Side.TOP);
        menu.actor.add_style_class_name('panel-menu');
        Main.uiGroup.add_child(menu.actor);
        menu.actor.hide();
        // A manager of its own: the panel's manager matches menus by source
        // actor, and both menus share this button.
        this._secondaryManager = new PopupMenu.PopupMenuManager(this);
        this._secondaryManager.addMenu(menu);
        this._secondary = menu;

        this._infoItem = new PopupMenu.PopupMenuItem('', {reactive: false, can_focus: false});
        this._infoItem.add_style_class_name('ptb-info-item');
        menu.addMenuItem(this._infoItem);
        menu.addMenuItem(new PopupMenu.PopupSeparatorMenuItem());

        menu.addAction(_('Refresh now'), () => {
            c.refresh();
        });
        menu.addAction(_('Pokédex'), () => {
            this._openTab = 'dex';
            this.menu.open();
        });
        menu.addAction(_('Settings…'), () => c.openPrefs());

        this._backItem = new PopupMenu.PopupSwitchMenuItem(_('Show back view'),
            c.settings.get_boolean('always-faces-back'));
        this._backItem.connect('toggled', (_i, state) => c.settings?.set_boolean('always-faces-back', state));
        menu.addMenuItem(this._backItem);
    }

    _toggleSecondary() {
        this.menu.close();
        this._infoItem.label.text = V.menuInfoText(this._controller.context().state);
        this._backItem.setToggleState(this._controller.settings.get_boolean('always-faces-back'));
        this._secondary.toggle();
    }

    _tooltipText() {
        if (!this._controller)
            return null;
        if (this.menu.isOpen || this._secondary.isOpen)
            return null;
        return V.helperStatusText(this._controller.context().helperStatus);
    }

    _onMenuOpen(open) {
        if (this._destroyed)
            return;
        // The popover card already animates the companion: rest the top bar (ui.md §1.3).
        this._sprite.setPaused(open);
        this._controller.tooltip.hide();
        if (open) {
            this._popover.onOpen(this._openTab);
            this._openTab = 'home';
            this._renderPopover();
            if (!this._tickId) {
                this._tickId = GLib.timeout_add_seconds(GLib.PRIORITY_DEFAULT, OPEN_TICK_S, () => {
                    this._renderPopover();
                    return GLib.SOURCE_CONTINUE;
                });
            }
        } else if (this._tickId) {
            GLib.source_remove(this._tickId);
            this._tickId = 0;
        }
    }

    /** Re-render everything from the controller's current context. */
    render() {
        const ctx = this._controller.context();
        this._renderPanel(ctx);
        // The popover is rebuilt on open; skip the work while it is closed.
        if (this.menu.isOpen)
            this._popover.update(ctx);
    }

    _renderPopover() {
        this._popover.update(this._controller.context());
    }

    setAnimationQuality(quality) {
        this._sprite.setFloorMs(floorForQuality(quality));
    }

    _renderPanel(ctx) {
        const s = this._controller.settings;
        const lines = V.menuLines(ctx.state, {
            showTokens: s.get_boolean('show-tokens-in-menu'),
            showCost: s.get_boolean('show-cost-in-menu'),
            showGrowth: s.get_boolean('show-growth-in-menu'),
            showLimit: s.get_boolean('show-limit-in-menu'),
            limitMode: s.get_string('limit-display-mode'),
            limitColorMode: s.get_string('menu-limit-color-mode'),
        });
        this._setLines(lines);

        const subject = ctx.state?.companion?.menuSubject;
        const sprite = subject?.sprite;
        const path = sprite && sprite.status !== 'loading' ? sprite.manifestPath ?? null : null;
        this._setSpritePath(path);
    }

    _setLines(lines) {
        const signature = JSON.stringify(lines);
        if (signature === this._lineSignature)
            return;
        // Same shape: update texts and classes in place (no relayout beyond the text width).
        const sameShape = this._lineSignature !== null &&
            JSON.stringify(JSON.parse(this._lineSignature).map(l => l.length)) ===
            JSON.stringify(lines.map(l => l.length));
        this._lineSignature = signature;
        if (!sameShape) {
            this._lines.destroy_all_children();
            for (const line of lines) {
                const row = new St.BoxLayout({style_class: 'ptb-panel-line', x_align: Clutter.ActorAlign.START});
                for (let i = 0; i < line.length; i++)
                    row.add_child(new St.Label({style_class: 'ptb-panel-text', y_align: Clutter.ActorAlign.CENTER}));
                this._lines.add_child(row);
            }
        }
        this._lines.visible = lines.length > 0;
        if (lines.length > 1)
            this._lines.add_style_class_name('ptb-two-lines');
        else
            this._lines.remove_style_class_name('ptb-two-lines');
        const rows = this._lines.get_children();
        lines.forEach((line, li) => {
            const labels = rows[li].get_children();
            line.forEach((run, ri) => {
                const l = labels[ri];
                l.text = run.text;
                for (const c of TEXT_TIER_CLASSES)
                    l.remove_style_class_name(c);
                if (run.tier)
                    l.add_style_class_name(`ptb-tier-text-${run.tier}`);
            });
        });
    }

    _setSpritePath(path) {
        if (path === this._spritePath)
            return;
        this._spritePath = path;
        this._sprite.setSource(path ? {path} : null);
    }

    _onPanelSpriteFailed() {
        // The cache was cleared: ask the helper once more for this subject (protocol §8.2).
        if (!this._controller)
            return;
        const subject = this._controller.context().state?.companion?.menuSubject;
        const req = menuSubjectRequest(subject);
        const key = JSON.stringify(req);
        if (this._spriteRetried.has(key))
            return;
        this._spriteRetried.add(key);
        this._controller.sprites.forget(req);
        this._controller.sprites.resolve(req).then(data => {
            if (this._sprite) {
                this._spritePath = data.manifestPath;
                this._sprite.setSource({path: data.manifestPath});
            }
        }).catch(() => {});
    }

    _onDestroy() {
        // Mark and detach before anything is released: super._onDestroy()
        // closes the open menu, and 'open-state-changed' must not reach us then.
        this._destroyed = true;
        if (this._menuOpenId) {
            this.menu.disconnect(this._menuOpenId);
            this._menuOpenId = 0;
        }
        if (this._tickId) {
            GLib.source_remove(this._tickId);
            this._tickId = 0;
        }
        this._secondary?.destroy();
        this._secondary = null;
        this._secondaryManager = null;
        this._sprite = null;
        this._controller = null;
        super._onDestroy();
    }
});
