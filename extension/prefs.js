/* PokeTokenBar preferences (libadwaita), every key of protocol §9.1 grouped
 * as ui.md §4: General, Top bar, Floating pet, Limits and alerts, Growth.
 * Every row applies at once.
 *
 * Runs in the gnome-extensions-app process, never in the shell: it imports
 * Gtk/Adw, the pure lib/prefsModel.js and the Backup & Transfer page
 * (lib/backupPage.js, which talks to the extension over D-Bus). The
 * representative Pokémon is chosen in the popover's Pokédex, not here (it
 * lives in the helper's save).
 */

import Adw from 'gi://Adw';
import Gio from 'gi://Gio';
import Gtk from 'gi://Gtk';

import {ExtensionPreferences, gettext as _, ngettext} from 'resource:///org/gnome/Shell/Extensions/js/extensions/prefs.js';

import {BackupPage} from './lib/backupPage.js';
import {setTranslations} from './lib/i18n.js';
import * as P from './lib/prefsModel.js';

function stringList(labels) {
    const model = new Gtk.StringList();
    for (const label of labels)
        model.append(label);
    return model;
}

export default class PokeTokenBarPreferences extends ExtensionPreferences {
    fillPreferencesWindow(window) {
        const settings = this.getSettings();
        // Handlers on the settings object are dropped when the window closes,
        // so a reopened window does not drive the widgets of a closed one.
        const ids = [];
        this._watch = (key, fn) => ids.push(settings.connect(`changed::${key}`, fn));
        window.connect('close-request', () => {
            for (const id of ids)
                settings.disconnect(id);
            ids.length = 0;
            return false;
        });
        window.search_enabled = true;

        const page = new Adw.PreferencesPage({title: _('PokeTokenBar'), icon_name: 'preferences-system-symbolic'});
        page.add(this._general(settings));
        page.add(this._topBar(settings));
        page.add(this._floatingPet(settings));
        page.add(this._limits(settings));
        page.add(this._growth(settings));
        window.add(page);

        // Backup & Transfer (save-transfer.md §5.2): its texts come from the pure model.
        setTranslations(_, ngettext);
        window.add(new BackupPage(window).page);
    }

    /** A ComboRow over string choices, kept in sync both ways. */
    _enumRow(settings, key, title, options, subtitle = null) {
        const values = options.map(([value]) => value);
        const row = new Adw.ComboRow({
            title,
            subtitle,
            model: stringList(options.map(([, label]) => label)),
            selected: Math.max(0, values.indexOf(settings.get_string(key))),
        });
        row.connect('notify::selected', () => {
            const value = values[row.selected];
            if (value !== undefined && value !== settings.get_string(key))
                settings.set_string(key, value);
        });
        this._watch(key, () => {
            const i = values.indexOf(settings.get_string(key));
            if (i >= 0 && i !== row.selected)
                row.selected = i;
        });
        return row;
    }

    _switchRow(settings, key, title, subtitle = null) {
        const row = new Adw.SwitchRow({title, subtitle});
        settings.bind(key, row, 'active', Gio.SettingsBindFlags.DEFAULT);
        return row;
    }

    /* ---- General ------------------------------------------------------- */

    _general(settings) {
        const group = new Adw.PreferencesGroup({title: _('General')});

        // Refresh interval: the presets only (0 = manual); an out-of-band value
        // written with gsettings shows as its nearest preset, as the helper uses it.
        const labels = {0: _('Manual'), 60: _('1 min'), 120: _('2 min'), 300: _('5 min'), 900: _('15 min')};
        const refresh = new Adw.ComboRow({
            title: _('Refresh interval'),
            subtitle: _('How often token usage and limits are read'),
            model: stringList(P.REFRESH_PRESETS.map(s => labels[s])),
            selected: P.REFRESH_PRESETS.indexOf(P.nearestPreset(settings.get_int('refresh-interval'))),
        });
        refresh.connect('notify::selected', () => {
            const seconds = P.REFRESH_PRESETS[refresh.selected];
            if (seconds !== undefined && seconds !== settings.get_int('refresh-interval'))
                settings.set_int('refresh-interval', seconds);
        });
        this._watch('refresh-interval', () => {
            const i = P.REFRESH_PRESETS.indexOf(P.nearestPreset(settings.get_int('refresh-interval')));
            if (i !== refresh.selected)
                refresh.selected = i;
        });
        group.add(refresh);

        group.add(this._enumRow(settings, 'animation-quality', _('Animation'), [
            ['powerSaver', _('Power saver')],
            ['balanced', _('Balanced')],
            ['smooth', _('Smooth')],
        ], _('Smoother uses more battery')));

        group.add(this._enumRow(settings, 'daily-trend-window', _('Daily trend period'), [
            ['7d', _('Last 7 days')],
            ['14d', _('Last 14 days')],
            ['21d', _('Last 21 days')],
            ['month', _('This month')],
        ]));

        group.add(this._enumRow(settings, 'week-start', _('First day of the week'), [
            ['locale', _('Follow the system')],
            ['monday', _('Monday')],
            ['sunday', _('Sunday')],
            ['saturday', _('Saturday')],
        ], _('Where “This week” starts')));
        return group;
    }

    /* ---- Top bar ------------------------------------------------------- */

    _topBar(settings) {
        const group = new Adw.PreferencesGroup({title: _('Top bar')});
        group.add(this._switchRow(settings, 'show-tokens-in-menu', _('Today’s tokens')));
        group.add(this._switchRow(settings, 'show-cost-in-menu', _('Today’s cost')));
        group.add(this._switchRow(settings, 'show-growth-in-menu', _('Companion growth'),
            _('“Hatch 42%” for the egg, “Growth 42%” once hatched')));
        group.add(this._switchRow(settings, 'show-limit-in-menu', _('Limit percentage'),
            _('The 5-hour limit, or the weekly one when there is none')));

        const color = this._enumRow(settings, 'menu-limit-color-mode', _('Limit color'), [
            ['gauge', _('By pace')],
            ['attention', _('Only when ahead of pace')],
            ['off', _('Off')],
        ]);
        settings.bind('show-limit-in-menu', color, 'visible', Gio.SettingsBindFlags.GET);
        group.add(color);

        const syncHint = () => {
            const off = P.allMenuItemsOff({
                tokens: settings.get_boolean('show-tokens-in-menu'),
                cost: settings.get_boolean('show-cost-in-menu'),
                growth: settings.get_boolean('show-growth-in-menu'),
                limit: settings.get_boolean('show-limit-in-menu'),
            });
            group.description = off ? _('With everything off, the top bar shows only the Pokémon.') : '';
        };
        for (const key of ['show-tokens-in-menu', 'show-cost-in-menu', 'show-growth-in-menu', 'show-limit-in-menu'])
            this._watch(key, syncHint);
        syncHint();
        return group;
    }

    /* ---- Floating pet (floating-pet.md §4.5) ---------------------------- */

    _floatingPet(settings) {
        const group = new Adw.PreferencesGroup({title: _('Floating pet')});
        group.add(this._switchRow(settings, 'floating-pet-enabled', _('Show floating pet'),
            _('Your Pokémon floats over the screen — drag to reposition')));
        // Upstream lists the back view here; it also turns the top-bar sprite.
        group.add(this._switchRow(settings, 'always-faces-back', _('Show the back view'),
            _('Draw the Pokémon facing away, here and in the top bar')));

        const size = new Adw.ActionRow({title: _('Size')});
        const value = new Gtk.Label({css_classes: ['numeric', 'dim-label'], valign: Gtk.Align.CENTER,
            width_chars: 6, xalign: 1});
        const scale = new Gtk.Scale({
            orientation: Gtk.Orientation.HORIZONTAL,
            adjustment: new Gtk.Adjustment({lower: P.PET_SIZE_MIN, upper: P.PET_SIZE_MAX,
                step_increment: P.PET_SIZE_STEP, page_increment: P.PET_SIZE_STEP * 4}),
            draw_value: false,
            hexpand: true,
            width_request: 200,
            valign: Gtk.Align.CENTER,
        });
        scale.add_mark(P.PET_SIZE_DEFAULT, Gtk.PositionType.BOTTOM, null);
        scale.update_property([Gtk.AccessibleProperty.LABEL], [_('Floating pet size')]);
        size.add_suffix(scale);
        size.add_suffix(value);
        let syncing = false;
        const show = v => {
            value.label = `${v} px`;
            syncing = true;
            scale.set_value(v);
            syncing = false;
        };
        scale.connect('value-changed', () => {
            if (syncing)
                return;
            const v = P.snapPetSize(scale.get_value());
            value.label = `${v} px`;
            if (v !== settings.get_int('floating-pet-size'))
                settings.set_int('floating-pet-size', v);
        });
        this._watch('floating-pet-size', () => {
            const v = P.snapPetSize(settings.get_int('floating-pet-size'));
            if (P.snapPetSize(scale.get_value()) !== v)
                show(v);
        });
        show(P.snapPetSize(settings.get_int('floating-pet-size')));
        group.add(size);

        const bubbles = this._switchRow(settings, 'floating-pet-bubble-alerts', _('Show notifications as bubbles'),
            _('Limit alerts also pop up over the pet'));
        const agents = this._switchRow(settings, 'floating-pet-agent-activity', _('React to agent activity'),
            _('Coding agents drive it over D-Bus — see the README'));
        // The pet's own rows show only while it is on (SettingsView.swift:373).
        for (const row of [size, bubbles, agents]) {
            settings.bind('floating-pet-enabled', row, 'visible', Gio.SettingsBindFlags.GET);
            group.add(row);
        }
        return group;
    }

    /* ---- Limits and alerts --------------------------------------------- */

    _limits(settings) {
        const group = new Adw.PreferencesGroup({
            title: _('Limits and alerts'),
            description: _('Official Claude limits, read from your Claude Code sign-in'),
        });

        const display = new Adw.ActionRow({title: _('Show limits as')});
        const toggles = new Adw.ToggleGroup({valign: Gtk.Align.CENTER});
        toggles.add(new Adw.Toggle({name: 'used', label: _('Used')}));
        toggles.add(new Adw.Toggle({name: 'remaining', label: _('Remaining')}));
        settings.bind('limit-display-mode', toggles, 'active-name', Gio.SettingsBindFlags.DEFAULT);
        display.add_suffix(toggles);
        group.add(display);

        group.add(this._switchRow(settings, 'limit-notifications', _('Limit alerts'),
            _('Notify when a limit crosses the warning or critical level')));

        // Warn and crit keep a 5-point gap; crit wins (protocol §9.1). Both
        // also set the gauge colors, so they stay visible with alerts off.
        const spin = (key, title, subtitle, lower, upper) => new Adw.SpinRow({
            title,
            subtitle,
            adjustment: new Gtk.Adjustment({lower, upper, step_increment: P.THRESHOLD_STEP,
                page_increment: P.THRESHOLD_STEP * 2, value: settings.get_int(key)}),
            snap_to_ticks: true,
            numeric: true,
        });
        const warn = spin('warn-threshold', _('Warning level (%)'), _('Orange from here; a quiet alert'),
            P.WARN_MIN, P.WARN_MAX);
        const crit = spin('crit-threshold', _('Critical level (%)'), _('Red from here; an alert with sound'),
            P.CRIT_MIN, P.CRIT_MAX);
        let applying = false;
        const apply = (warnValue, critValue) => {
            if (applying)
                return;
            applying = true;
            try {
                const r = P.applyThresholdRule(warnValue, critValue);
                if (settings.get_int('crit-threshold') !== r.crit)
                    settings.set_int('crit-threshold', r.crit);
                if (settings.get_int('warn-threshold') !== r.warn)
                    settings.set_int('warn-threshold', r.warn);
                if (crit.value !== r.crit)
                    crit.value = r.crit;
                if (warn.value !== r.warn)
                    warn.value = r.warn;
            } finally {
                applying = false;
            }
        };
        warn.connect('notify::value', () => apply(warn.value, crit.value));
        crit.connect('notify::value', () => apply(warn.value, crit.value));
        this._watch('warn-threshold', () => apply(settings.get_int('warn-threshold'), settings.get_int('crit-threshold')));
        this._watch('crit-threshold', () => apply(settings.get_int('warn-threshold'), settings.get_int('crit-threshold')));
        group.add(warn);
        group.add(crit);
        return group;
    }

    /* ---- Growth -------------------------------------------------------- */

    _growth(settings) {
        const group = new Adw.PreferencesGroup({title: _('Growth')});

        // Log slider 0.1×–2.0× held in a draft; Save applies it (the helper
        // rescales the current egg or stage and keeps its progress).
        const stored = () => P.clampDifficulty(settings.get_double('growth-difficulty'));
        let draft = stored();

        const head = new Adw.ActionRow({
            title: _('Growth difficulty'),
            subtitle: _('Tokens needed to hatch and evolve. Progress is kept when it changes.'),
        });
        const value = new Gtk.Label({css_classes: ['numeric', 'dim-label'], valign: Gtk.Align.CENTER});
        const save = new Gtk.Button({label: _('Save'), valign: Gtk.Align.CENTER, css_classes: ['suggested-action']});
        head.add_suffix(value);
        head.add_suffix(save);
        group.add(head);

        const scale = new Gtk.Scale({
            orientation: Gtk.Orientation.HORIZONTAL,
            adjustment: new Gtk.Adjustment({lower: 0, upper: 1, step_increment: 0.01, page_increment: 0.1}),
            draw_value: false,
            hexpand: true,
        });
        scale.add_mark(P.difficultyPosition(P.DIFFICULTY_DEFAULT), Gtk.PositionType.BOTTOM, null);
        scale.update_property([Gtk.AccessibleProperty.LABEL], [_('Growth difficulty')]);
        const box = new Gtk.Box({spacing: 10, margin_start: 12, margin_end: 12, margin_top: 6, margin_bottom: 6});
        box.append(new Gtk.Label({label: _('Easy'), css_classes: ['dim-label', 'caption']}));
        box.append(scale);
        box.append(new Gtk.Label({label: _('Hard'), css_classes: ['dim-label', 'caption']}));
        group.add(new Adw.PreferencesRow({child: box, activatable: false, title: _('Growth difficulty')}));

        let syncing = false;
        const sync = () => {
            value.label = P.difficultyValueText(draft);
            save.visible = draft !== stored();
        };
        const setScale = v => {
            syncing = true;
            scale.set_value(P.difficultyPosition(v));
            syncing = false;
        };
        scale.connect('value-changed', () => {
            if (syncing)
                return;
            draft = P.difficultyAtPosition(scale.get_value());
            sync();
        });
        save.connect('clicked', () => {
            settings.set_double('growth-difficulty', P.clampDifficulty(draft));
        });
        this._watch('growth-difficulty', () => {
            draft = stored();
            setScale(draft);
            sync();
        });
        setScale(draft);
        sync();

        this._shopPrices(group, settings);

        group.add(this._switchRow(settings, 'companion-notifications', _('Companion notifications'),
            _('Hatching, evolving, graduating, Rare Candy and Pokédex medals')));
        return group;
    }

    /* Shop prices (bag-shop.md §3.4): the same log slider as growth, but it
     * applies as it moves, with no draft and no Save (M2 decision 12). Prices
     * are derived, so nothing is rescaled; the extension debounces the forward. */
    _shopPrices(group, settings) {
        const stored = () => P.clampDifficulty(settings.get_double('shop-difficulty'));

        const head = new Adw.ActionRow({
            title: _('Shop prices'),
            subtitle: _('Scales every price in the Shop. Applies right away.'),
        });
        const value = new Gtk.Label({css_classes: ['numeric', 'dim-label'], valign: Gtk.Align.CENTER});
        head.add_suffix(value);
        group.add(head);

        const scale = new Gtk.Scale({
            orientation: Gtk.Orientation.HORIZONTAL,
            adjustment: new Gtk.Adjustment({lower: 0, upper: 1, step_increment: 0.01, page_increment: 0.1}),
            draw_value: false,
            hexpand: true,
        });
        scale.add_mark(P.difficultyPosition(P.DIFFICULTY_DEFAULT), Gtk.PositionType.BOTTOM, null);
        scale.update_property([Gtk.AccessibleProperty.LABEL, Gtk.AccessibleProperty.DESCRIPTION],
            [_('Shop prices'), _('Lower: Cheap, higher: Expensive')]);
        const box = new Gtk.Box({spacing: 10, margin_start: 12, margin_end: 12, margin_top: 6, margin_bottom: 6});
        box.append(new Gtk.Label({label: _('Cheap'), css_classes: ['dim-label', 'caption']}));
        box.append(scale);
        box.append(new Gtk.Label({label: _('Expensive'), css_classes: ['dim-label', 'caption']}));
        group.add(new Adw.PreferencesRow({child: box, activatable: false, title: _('Shop prices')}));

        let syncing = false;
        const show = v => {
            value.label = P.difficultyValueText(v);
            syncing = true;
            scale.set_value(P.difficultyPosition(v));
            syncing = false;
        };
        scale.connect('value-changed', () => {
            if (syncing)
                return;
            const v = P.difficultyAtPosition(scale.get_value());
            value.label = P.difficultyValueText(v);
            if (v !== stored())
                settings.set_double('shop-difficulty', v);
        });
        this._watch('shop-difficulty', () => {
            // Our own write lands here too: leave the knob where the hand is.
            const v = stored();
            if (P.difficultyAtPosition(scale.get_value()) !== v)
                show(v);
            else
                value.label = P.difficultyValueText(v);
        });
        show(stored());
    }
}
