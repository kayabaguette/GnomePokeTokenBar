/* The floating desktop pet (floating-pet.md §2.2, §3; protocol §9.1, §15).
 *
 * One chrome actor restacked just above global.window_group: over every app
 * window, under the top bar, banners, menus, modal dialogs and the overview.
 * Only the drawn sprite rectangle is reactive, so clicks on the transparent
 * box margin and on the bubbles pass through to the windows below.
 *
 * One sync() owns visibility: shown only while the setting is on, the overview
 * is closed, the pet's monitor has no fullscreen window and the display is
 * awake. Hidden means unmapped, so SpriteAnimator's timer stops by itself; the
 * bob timer is ours and stops with it.
 *
 * The logic (agents, runs, bubbles, hearts, event callout) lives in the pure
 * PetController; this file only draws it and feeds it input.
 */

import Clutter from 'gi://Clutter';
import Cogl from 'gi://Cogl';
import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import Pango from 'gi://Pango';
import St from 'gi://St';

import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import * as PopupMenu from 'resource:///org/gnome/shell/ui/popupMenu.js';

import {_} from '../i18n.js';
import {SpriteAnimator, floorForQuality} from '../spriteAnimator.js';
import {visibleTopInset} from '../spriteFit.js';
import {menuSubjectRequest} from '../sprites.js';
import * as V from '../viewModel.js';
import {bubbleContent} from './bubbleText.js';
import {pangoMeasure} from './pangoMeasure.js';
import {PetController} from './petController.js';
import * as M from './petModel.js';

const LOG = 'PokeTokenBar';
const CALLOUT_GAP = 6;     // FPP:715-771: 6 pt above the pet
const EDGE = 6;            // keep chrome this far inside the monitor
const FADE_MS = 200;       // bubble pop-in (floating-pet.md §3 "Spring transitions")
const BOB_SCALE_X = 1.03;  // FPP:1193-1210
const BOB_SCALE_Y = 0.94;
const EGG_GLYPH = 0.62;    // CV:244
const ORIGIN_KEY = 'floating-pet-origin';

const COMPANION_EVENTS = ['hatch', 'evolve', 'graduate', 'badge']; // not candyGranted (CS:1521-1522)

function coglContext() {
    return global.stage.context.get_backend().get_cogl_context();
}

/** A NEAREST-scaled pixel-art actor (hearts, bubble tail), `pixel` stage px per cell. */
function pixelArtActor(rows, pixel) {
    const w = rows[0].length, h = rows.length;
    const content = St.ImageContent.new_with_preferred_size(w, h);
    content.set_bytes(coglContext(), new GLib.Bytes(M.pixelArtRgba(rows)), Cogl.PixelFormat.RGBA_8888, w, h, w * 4);
    const actor = new Clutter.Actor({width: w * pixel, height: h * pixel, reactive: false, visible: false});
    actor.set_content_scaling_filters(Clutter.ScalingFilter.NEAREST, Clutter.ScalingFilter.NEAREST);
    actor.set_content(content);
    return actor;
}

function readOrigin(settings) {
    const v = settings.get_value(ORIGIN_KEY).deepUnpack();
    return Array.isArray(v) && v.length === 2 ? {x: v[0], y: v[1]} : null;
}

export class FloatingPet {
    /**
     * @param {object} host  {settings, theme, sprites, context(), openPopover()}
     */
    constructor(host) {
        this._host = host;
        this._settings = host.settings;
        this._destroyed = false;
        this._timers = new Set();
        this._bobId = 0;
        this._squashed = false;
        this._dragging = false;
        this._hovering = false;
        this._displayOn = true;
        this._inOverview = Main.overview.visible;
        this._spritePath = undefined;
        this._spriteRetried = new Set();
        this._subjectIsEgg = true;
        this._detector = new M.PettingDetector();
        this._themeContext = St.ThemeContext.get_for_stage(global.stage);
        this._stSettings = St.Settings.get();
        this._powerProfiles = Gio.PowerProfileMonitor.dup_default();

        this._buildActors();
        this._buildMenu();
        this._controller = new PetController(this._controllerDeps());

        this._settings.connectObject('changed', (_s, key) => this._onSetting(key), this);
        Main.overview.connectObject(
            'showing', () => this._setOverview(true),
            'hidden', () => this._setOverview(false), this);
        global.display.connectObject(
            'in-fullscreen-changed', () => this.sync(),
            'workareas-changed', () => this._relayout(), this);
        Main.layoutManager.connectObject('monitors-changed', () => this._relayout(), this);
        this._themeContext.connectObject('notify::scale-factor', () => this._relayout(), this);
        this._stSettings.connectObject('notify::enable-animations', () => this._onMotionSetting(), this);
        this._powerProfiles.connectObject('notify::power-saver-enabled', () => this._onMotionSetting(), this);

        this._sprite.setPaused(this._powerProfiles.power_saver_enabled);
        this._relayout();
    }

    /* ---- construction --------------------------------------------------- */

    _buildActors() {
        this._root = new St.Widget({style_class: 'ptb-pet', reactive: false, visible: false});
        this._body = new St.Widget({reactive: false});
        this._root.add_child(this._body);

        this._sprite = new SpriteAnimator({
            boxHeight: this._sizeLogical,
            maxWidth: this._sizeLogical,
            floorMs: floorForQuality(this._settings.get_string('animation-quality')),
            fixedWidth: true,
            allowUpscale: true,
            styleClass: 'ptb-pet-sprite',
        });
        this._sprite.onLoadFailed = () => this._onSpriteFailed();
        this._sprite.onLayout = () => this._onSpriteLayout();
        this._body.add_child(this._sprite);

        // The only reactive actor: the drawn image rectangle (risk 1, floating-pet.md §6).
        this._hit = new St.Widget({style_class: 'ptb-pet-hit', reactive: true, track_hover: true});
        this._body.add_child(this._hit);
        this._hit.connect('notify::hover', () => this._onHover());
        this._addGestures();

        const sf = this._scale;
        this._hearts = pixelArtActor(M.HEART_ROWS, M.HEART_PIXEL * sf);
        this._root.add_child(this._hearts);

        this._bubble = new St.BoxLayout({
            style_class: 'ptb-pet-bubble',
            orientation: Clutter.Orientation.VERTICAL,
            reactive: false,
            visible: false,
        });
        this._bubbleTitle = new St.Label({style_class: 'ptb-pet-bubble-title'});
        this._bubbleBody = new St.Label({style_class: 'ptb-pet-bubble-body'});
        this._bubbleBody.clutter_text.line_wrap = true;
        this._bubbleBody.clutter_text.line_wrap_mode = Pango.WrapMode.WORD_CHAR;
        this._bubbleBody.clutter_text.ellipsize = Pango.EllipsizeMode.NONE;
        this._bubble.add_child(this._bubbleTitle);
        this._bubble.add_child(this._bubbleBody);
        this._root.add_child(this._bubble);
        this._tail = pixelArtActor(M.TAIL_ROWS, M.HEART_PIXEL * sf);
        this._root.add_child(this._tail);
        this._bubbleKey = null;

        this._callout = new St.Label({style_class: 'ptb-pet-callout', reactive: false, visible: false});
        this._callout.clutter_text.line_wrap = true;
        this._root.add_child(this._callout);
        // Text sizes settle at allocation (style, wrapping): place both again then.
        this._bubble.connect('notify::height', () => this._placeBubble());
        this._callout.connect('notify::height', () => this._placeCallout());

        // The shell can tear the stage down without disabling us (exit): stop our timers then.
        this._root.connect('destroy', () => {
            if (this._destroyed)
                return;
            this._destroyed = true; // every handler below bails out from here on
            this._stopTimers();
        });
        Main.layoutManager.addChrome(this._root);
        // Just above the windows: under the top bar, banners and every menu (floating-pet.md §2.2).
        Main.layoutManager.uiGroup.set_child_above_sibling(this._root, global.window_group);
        this._host.theme.track(this._root);
    }

    _addGestures() {
        const pan = new Clutter.PanGesture({begin_threshold: 4}); // FPP:36 (16 pt²)
        pan.connect('recognize', () => this._onDragBegin(pan));
        pan.connect('pan-update', () => this._onDragUpdate(pan));
        pan.connect('end', () => this._onDragEnd());
        pan.connect('cancel', () => this._onDragEnd());
        this._hit.add_action(pan);

        const click = new Clutter.ClickGesture();
        click.set_required_button(Clutter.BUTTON_PRIMARY);
        click.connect('recognize', () => {
            if (!this._destroyed)
                this._controller.petClicked(this._subjectIsEgg);
        });
        this._hit.add_action(click);

        const secondary = new Clutter.ClickGesture({recognize_on_press: true});
        secondary.set_required_button(Clutter.BUTTON_SECONDARY);
        secondary.connect('recognize', () => this._toggleMenu());
        this._hit.add_action(secondary);

        const motion = new Clutter.MotionController();
        motion.connect('motion', (_c, _s, x) => this._onMotion(x));
        motion.connect('leave', () => this._detector.reset());
        this._hit.add_action(motion);
    }

    _buildMenu() {
        const menu = new PopupMenu.PopupMenu(this._hit, 0.5, St.Side.TOP);
        menu.actor.add_style_class_name('panel-menu');
        Main.uiGroup.add_child(menu.actor);
        menu.actor.hide();
        this._menuManager = new PopupMenu.PopupMenuManager(this._hit);
        this._menuManager.addMenu(menu);
        menu.addAction(_('Open Token Bar'), () => this._controller.openFromMenu());
        menu.addAction(_('Turn off floating pet'), () => this._settings?.set_boolean('floating-pet-enabled', false));
        this._backItem = new PopupMenu.PopupSwitchMenuItem(_('Show back view'),
            this._settings.get_boolean('always-faces-back'));
        this._backItem.connect('toggled', (_i, state) => this._settings?.set_boolean('always-faces-back', state));
        menu.addMenuItem(this._backItem);
        menu.connect('open-state-changed', () => this._render());
        this._menu = menu;
        this._host.theme.track(menu.actor);
    }

    _controllerDeps() {
        return {
            now: () => GLib.get_monotonic_time() / 1e6,
            setTimer: (fn, ms) => this._setTimer(fn, ms),
            clearTimer: id => this._clearTimer(id),
            openPopover: () => this._host.openPopover(),
            persistHome: o => this._settings.set_value(ORIGIN_KEY, new GLib.Variant('m(dd)', [o.x, o.y])),
            home: () => this._home,
            petSize: () => this._sizePx,
            scale: () => this._scale,
            motionAllowed: () => this._motionAllowed,
            agentEnabled: () => this._enabled && this._settings.get_boolean('floating-pet-agent-activity'),
            bubbleAlertsEnabled: () => this._enabled && this._settings.get_boolean('floating-pet-bubble-alerts'),
            visible: () => this._root.visible,
            cursor: () => {
                const [x, y] = global.get_pointer();
                return {x, y};
            },
            workAreaAt: p => this._workAreaAt(p.x, p.y),
            animate: (target, done) => this._animateTo(target, done),
            stopAnimation: () => this._root.remove_all_transitions(),
            changed: () => this._render(),
        };
    }

    /* ---- inputs from the extension -------------------------------------- */

    /** A new protocol state: the sprite and the hover text follow it. */
    update(state) {
        const subject = state?.companion?.menuSubject;
        this._subjectIsEgg = !subject || Boolean(subject.egg);
        const sprite = subject?.sprite;
        const path = sprite && sprite.status !== 'loading' ? sprite.manifestPath ?? null : null;
        if (path !== this._spritePath) {
            this._spritePath = path;
            this._sprite.setSource(path ? {path} : null);
        }
        if (this._callout.visible)
            this._render();
    }

    /** One helper event, before the notification gate (floating-pet.md §4.3). */
    handleEvent(event) {
        if (this._destroyed || !event)
            return;
        if (event.kind === 'limitAlert') {
            this._controller.handleLimitAlert(event);
        } else if (COMPANION_EVENTS.includes(event.kind)) {
            const text = V.notificationText(event);
            if (text?.body)
                this._controller.handleCompanionEvent(text.body);
        }
    }

    /** One parsed agent report (lib/pet/agentBus.js). */
    handleAgentEvent(event) {
        if (!this._destroyed)
            this._controller.handleAgentEvent(event);
    }

    /** Display power-save from the monitor manager (extension.js owns the signal). */
    setDisplayOn(on) {
        if (this._displayOn === on)
            return;
        this._displayOn = on;
        this.sync();
    }

    /* ---- settings and geometry ------------------------------------------ */

    get _enabled() {
        return this._settings.get_boolean('floating-pet-enabled');
    }

    get _scale() {
        return this._themeContext.scale_factor;
    }

    get _sizeLogical() {
        return M.snapSize(this._settings.get_int('floating-pet-size'));
    }

    get _sizePx() {
        return this._sizeLogical * this._scale;
    }

    get _motionAllowed() {
        return M.allowsMotion(this._stSettings.enable_animations, this._powerProfiles.power_saver_enabled);
    }

    _onSetting(key) {
        switch (key) {
        case 'floating-pet-enabled':
        case 'floating-pet-agent-activity':
        case 'floating-pet-bubble-alerts':
            this.sync();
            break;
        case 'floating-pet-size':
        case ORIGIN_KEY:
            this._relayout();
            break;
        case 'animation-quality':
            this._sprite.setFloorMs(floorForQuality(this._settings.get_string(key)));
            break;
        case 'always-faces-back':
            this._backItem.setToggleState(this._settings.get_boolean(key));
            break;
        }
    }

    _onMotionSetting() {
        // Power saver: a static frame and no motion (FPP:622-629); the bob reads motionAllowed.
        this._sprite.setPaused(this._powerProfiles.power_saver_enabled);
        if (!this._motionAllowed && this._controller.runPhase !== 'home') {
            this._root.remove_all_transitions();
            this._controller.hidden(); // back home, no run in flight
        }
        this._render();
    }

    _setOverview(shown) {
        console.debug(`${LOG}: pet overview ${shown}`);
        this._inOverview = shown;
        this.sync();
    }

    _workAreas() {
        return Main.layoutManager.monitors.map((_m, i) => {
            const r = Main.layoutManager.getWorkAreaForMonitor(i);
            return {x: r.x, y: r.y, width: r.width, height: r.height};
        });
    }

    _workAreaAt(x, y) {
        const monitor = Main.layoutManager.findMonitorForPoint(x, y);
        return monitor ? this._workAreas()[monitor.index] ?? null : null;
    }

    /** Size, home (saved, clamped or recovered) and position, then visibility. */
    _relayout() {
        if (this._destroyed)
            return;
        const sf = this._scale;
        const size = this._sizeLogical;
        this._sprite.setBox(size, size);
        this._sprite.setPlaceholderSize(size * EGG_GLYPH);
        this._root.set_size(this._sizePx, this._sizePx);
        this._body.set_size(this._sizePx, this._sizePx);
        this._body.set_pivot_point(0.5, 1);
        for (const art of [this._hearts, this._tail]) {
            const content = art.get_content();
            const [, w, h] = content.get_preferred_size();
            art.set_size(w * M.HEART_PIXEL * sf, h * M.HEART_PIXEL * sf);
        }
        const areas = this._workAreas();
        this._home = M.resolveHome(readOrigin(this._settings), areas, Math.max(0, Main.layoutManager.primaryIndex),
            this._sizePx);
        this.sync();
    }

    /** The one visibility owner (M2 decision 23). */
    sync() {
        if (this._destroyed)
            return;
        const home = this._controller.petOrigin;
        const monitor = Main.layoutManager.findMonitorForPoint(home.x + this._sizePx / 2, home.y - this._sizePx / 2);
        const fullscreen = Boolean(monitor?.inFullscreen);
        const visible = this._enabled && !this._inOverview && !fullscreen && this._displayOn;
        if (visible !== this._root.visible) {
            if (visible) {
                this._root.show();
            } else {
                this._menu.close();
                this._root.hide();
                this._dragging = false;
                this._controller.hidden();
            }
        }
        this._controller.sync(); // resets agents and the limit bubble when their setting is off; renders
    }

    /* ---- rendering ------------------------------------------------------ */

    _render() {
        if (this._destroyed)
            return;
        const c = this._controller;
        const visible = this._root.visible;
        if (visible && c.runPhase === 'home' && !this._dragging)
            this._placeAt(c.petOrigin);
        this._syncBob(visible && c.isBobbing);
        this._renderBubble(visible ? c.bubble : null);
        this._renderHearts(visible && c.heartsVisible && !this._dragging);
        this._renderCallout(visible);
    }

    _placeAt(petOrigin) {
        this._root.set_position(Math.round(petOrigin.x), Math.round(petOrigin.y - this._sizePx));
    }

    _animateTo(target, done) {
        const [x, y] = [Math.round(target.x), Math.round(target.y - this._sizePx)];
        this._root.remove_all_transitions();
        this._root.ease({
            x, y,
            duration: M.RUN_DURATION_MS,
            mode: Clutter.AnimationMode.EASE_IN_OUT_QUAD,
            onComplete: () => {
                if (!this._destroyed)
                    done();
            },
        });
    }

    /** The visible top of the sprite in root px (hearts and bubbles hug it). */
    _visibleTop() {
        return visibleTopInset(this._sprite.imageRect, this._sprite.visibleTopFraction);
    }

    /** x (root px) that keeps a `width`-wide child centred on the pet and inside the monitor. */
    _centredX(width) {
        const local = Math.round((this._sizePx - width) / 2);
        const [rx, ry] = [this._root.x, this._root.y];
        const monitor = Main.layoutManager.findMonitorForPoint(rx + this._sizePx / 2, ry + this._sizePx / 2);
        if (!monitor)
            return local;
        const edge = EDGE * this._scale;
        const stageX = Math.max(monitor.x + edge, Math.min(rx + local, monitor.x + monitor.width - width - edge));
        return Math.round(stageX - rx);
    }

    /** Keep a child's top at or below the work-area top (the top bar draws over the pet). */
    _clampTop(localY) {
        const wa = this._workAreaAt(this._root.x + this._sizePx / 2, this._root.y + this._sizePx / 2);
        return wa ? Math.max(localY, wa.y - this._root.y) : localY;
    }

    _measure() {
        const title = this._bubbleTitle, body = this._bubbleBody;
        return pangoMeasure(title.clutter_text.get_pango_context(), title.get_theme_node().get_font(),
            body.get_theme_node().get_font(), this._scale);
    }

    _renderBubble(b) {
        if (!b) {
            this._bubbleKey = null;
            this._bubble.hide();
            this._tail.hide();
            return;
        }
        if (!this._bubble.visible)
            this._tail.show();
        const key = JSON.stringify(b);
        if (key !== this._bubbleKey) {
            const content = bubbleContent(b, this._measure());
            this._bubbleTitle.text = content.title;
            this._bubbleBody.text = content.body;
            if (content.isCritical)
                this._bubble.add_style_class_name('ptb-pet-bubble-critical');
            else
                this._bubble.remove_style_class_name('ptb-pet-bubble-critical');
        }
        if (key !== this._bubbleKey && !this._bubble.visible)
            this._popIn(this._bubble);
        this._bubble.show();
        this._bubbleKey = key;
        this._placeBubble();
    }

    /** The tail tip 3 px inside the sprite's visible top, the bubble on the tail. */
    _placeBubble() {
        if (this._destroyed || !this._bubble.visible)
            return;
        const sf = this._scale;
        const [, bw] = this._bubble.get_preferred_width(-1);
        const [, natH] = this._bubble.get_preferred_height(bw);
        const bh = this._bubble.height > 0 ? this._bubble.height : natH;
        const tip = Math.round(this._visibleTop() + M.HEART_TAIL_OVERLAP * sf);
        const tailTop = tip - this._tail.height;
        const wanted = Math.round(tailTop + M.HEART_PIXEL * sf - bh);
        const bubbleY = this._clampTop(wanted);
        this._bubble.set_position(this._centredX(bw), bubbleY);
        this._tail.set_position(Math.round((this._sizePx - this._tail.width) / 2), tailTop);
        this._tail.visible = bubbleY === wanted; // a bubble pushed down by the top bar sits on the sprite
    }

    _renderHearts(show) {
        if (!show) {
            this._hearts.hide();
            return;
        }
        const top = M.bubbleTopOffset(this._visibleTop(), this._hearts.height, M.HEART_TAIL_OVERLAP * this._scale);
        this._hearts.set_position(Math.round((this._sizePx - this._hearts.width) / 2), this._clampTop(top));
        if (!this._hearts.visible)
            this._popIn(this._hearts);
    }

    /** Scale and fade in from the bottom (200 ms; ease() honours enable-animations). */
    _popIn(actor) {
        actor.set_pivot_point(0.5, 1);
        actor.remove_all_transitions();
        actor.opacity = 0;
        actor.scale_x = actor.scale_y = 0.8;
        actor.show();
        actor.ease({opacity: 255, scale_x: 1, scale_y: 1, duration: FADE_MS, mode: Clutter.AnimationMode.EASE_OUT_QUAD});
    }

    _renderCallout(visible) {
        const c = this._controller;
        const show = visible && !this._dragging && !this._menu.isOpen && !c.showingBubble &&
            (this._hovering || c.eventText !== null);
        if (!show) {
            this._callout.hide();
            return;
        }
        const state = this._host.context().state;
        const s = this._settings;
        this._callout.text = M.hoverText({
            todayTokens: state?.usage?.combined?.today?.tokens ?? 0,
            limitUtilization: M.highestLimitUtilization(state),
            mode: s.get_string('limit-display-mode'),
            sulkIdleSeconds: state?.companion?.sulkIdleSeconds ?? 0,
            eventLine: c.eventText,
        });
        this._callout.show();
        this._placeCallout();
    }

    _placeCallout() {
        if (this._destroyed || !this._callout.visible)
            return;
        const [, w] = this._callout.get_preferred_width(-1);
        const [, natH] = this._callout.get_preferred_height(w);
        const h = this._callout.height > 0 ? this._callout.height : natH;
        const y = Math.round(this._visibleTop() - CALLOUT_GAP * this._scale - h);
        this._callout.set_position(this._centredX(w), this._clampTop(y));
    }

    _syncBob(on) {
        if (on && !this._bobId) {
            this._bobId = GLib.timeout_add(GLib.PRIORITY_DEFAULT, M.BOB_INTERVAL_MS, () => {
                this._squashed = !this._squashed;
                this._body.scale_x = this._squashed ? BOB_SCALE_X : 1;
                this._body.scale_y = this._squashed ? BOB_SCALE_Y : 1;
                return GLib.SOURCE_CONTINUE;
            });
        } else if (!on && this._bobId) {
            GLib.source_remove(this._bobId);
            this._bobId = 0;
            this._squashed = false;
            this._body.scale_x = this._body.scale_y = 1;
        }
    }

    /* ---- input ---------------------------------------------------------- */

    _onSpriteLayout() {
        const r = this._sprite.imageRect;
        this._hit.set_position(r.x, r.y);
        this._hit.set_size(Math.max(1, r.width), Math.max(1, r.height));
    }

    _onHover() {
        this._hovering = this._hit.hover;
        if (!this._hovering)
            this._detector.reset();
        this._render();
    }

    _onMotion(x) {
        if (this._dragging)
            return;
        const [, , mods] = global.get_pointer();
        const buttons = Clutter.ModifierType.BUTTON1_MASK | Clutter.ModifierType.BUTTON2_MASK |
            Clutter.ModifierType.BUTTON3_MASK;
        if (mods & buttons) { // "no button held" (FPP:938)
            this._detector.reset();
            return;
        }
        // Stage x, as upstream feeds screen x (FPP:942).
        const [hx] = this._hit.get_transformed_position();
        const stroke = M.minStroke(this._sizePx);
        if (this._detector.feed(hx + x, GLib.get_monotonic_time() / 1e6, stroke))
            this._controller.stroked();
    }

    _onDragBegin(pan) {
        this._dragging = true;
        this._detector.reset();
        this._menu.close();
        this._root.remove_all_transitions();
        const c = pan.get_centroid_abs();
        this._dragStart = {x: this._root.x, y: this._root.y, cx: c.x, cy: c.y};
        this._render();
    }

    _onDragUpdate(pan) {
        if (!this._dragging)
            return;
        const c = pan.get_centroid_abs();
        const s = this._dragStart;
        this._root.set_position(Math.round(s.x + c.x - s.cx), Math.round(s.y + c.y - s.cy));
    }

    _onDragEnd() {
        if (!this._dragging)
            return;
        this._dragging = false;
        const size = this._sizePx;
        const dropped = {x: this._root.x, y: this._root.y + size};
        const wa = this._workAreaAt(dropped.x + size / 2, dropped.y - size / 2) ??
            this._workAreas()[Math.max(0, Main.layoutManager.primaryIndex)];
        const home = wa ? M.clampHome(dropped, wa, size) : dropped;
        this._home = home;
        // Saved on drag end only (GSettings writes reach dconf on disk); the
        // changed:: signal re-resolves the same home.
        this._controller.petMoved(home, true);
        this.sync();
    }

    _toggleMenu() {
        if (this._destroyed)
            return;
        this._backItem.setToggleState(this._settings.get_boolean('always-faces-back'));
        this._menu.toggle();
    }

    _onSpriteFailed() {
        // The cache was cleared: ask the helper once more for this subject (protocol §8.2).
        const subject = this._host.context().state?.companion?.menuSubject;
        const req = menuSubjectRequest(subject);
        const key = JSON.stringify(req);
        if (this._spriteRetried.has(key))
            return;
        this._spriteRetried.add(key);
        this._host.sprites.forget(req);
        this._host.sprites.resolve(req).then(data => {
            if (!this._destroyed) {
                this._spritePath = data.manifestPath;
                this._sprite.setSource({path: data.manifestPath});
            }
        }).catch(() => {});
    }

    /* ---- timers and teardown -------------------------------------------- */

    _setTimer(fn, ms) {
        const id = GLib.timeout_add(GLib.PRIORITY_DEFAULT, Math.max(0, Math.ceil(ms)), () => {
            this._timers.delete(id);
            if (!this._destroyed) {
                try {
                    fn();
                } catch (e) {
                    console.error(`${LOG}: pet timer failed: ${e}\n${e.stack}`);
                }
            }
            return GLib.SOURCE_REMOVE;
        });
        this._timers.add(id);
        return id;
    }

    _clearTimer(id) {
        if (this._timers.delete(id))
            GLib.source_remove(id);
    }

    _stopTimers() {
        this._controller?.destroy();
        for (const id of this._timers)
            GLib.source_remove(id);
        this._timers.clear();
        if (this._bobId) {
            GLib.source_remove(this._bobId);
            this._bobId = 0;
        }
    }

    destroy() {
        this._destroyed = true;
        this._stopTimers();
        this._settings.disconnectObject(this);
        Main.overview.disconnectObject(this);
        global.display.disconnectObject(this);
        Main.layoutManager.disconnectObject(this);
        this._themeContext.disconnectObject(this);
        this._stSettings.disconnectObject(this);
        this._powerProfiles.disconnectObject(this);
        this._menu.destroy();
        this._menuManager = null;
        this._root.remove_all_transitions();
        Main.layoutManager.removeChrome(this._root);
        this._root.destroy();
        this._root = null;
        this._sprite = null;
        this._host = null;
        this._settings = null;
    }
}
