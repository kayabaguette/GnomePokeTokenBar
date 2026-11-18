/* PokeTokenBar sprite-animation spike (GNOME Shell 51).
 *
 * Shows a Gen-V sprite animation in the top bar from frames that the Go
 * converter already composited (manifest.json + frame-NNN.png).
 *
 * Cost model:
 *   - enable: every PNG is decoded once and uploaded once into its own
 *     St.ImageContent (a GPU texture);
 *   - per tick: one set_content() on a fixed-size actor plus a redraw. No file
 *     IO, no decoding, no size change, so no relayout;
 *   - while the actor is unmapped the timer is removed.
 *
 * Upload API, as used by Shell 51 itself (screenshot.js, qrCode.js):
 *   St.ImageContent.new_with_preferred_size(w, h)
 *   content.set_bytes(coglContext, GLib.Bytes, Cogl.PixelFormat.RGBA_8888, w, h, rowstride)
 * with coglContext = global.stage.context.get_backend().get_cogl_context().
 * Clutter.Image no longer exists in Clutter-51.
 */

import Clutter from 'gi://Clutter';
import Cogl from 'gi://Cogl';
import GdkPixbuf from 'gi://GdkPixbuf';
import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import GObject from 'gi://GObject';
import St from 'gi://St';

import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import * as PanelMenu from 'resource:///org/gnome/shell/ui/panelMenu.js';
import {Extension} from 'resource:///org/gnome/shell/extensions/extension.js';

import {capFrameRate} from './capFrameRate.js';

/* Directory holding manifest.json and frame-NNN.png (the Go converter's
 * output). null means <extension dir>/frames. */
const FRAMES_DIR = null;

/* Frame-rate floor in seconds: 0.4 Power saver (default), 0.2 Balanced,
 * 0.1 Smooth, 0 native GIF timing. */
const FLOOR_SEC = 0.4;

/* Sprite box in logical px. The sprite is aspect-fit into
 * MAX_WIDTH x BOX_HEIGHT and bottom-aligned in a BOX_HEIGHT-high box, so the
 * feet stay on one baseline. 16 px is the panel icon size (theme:
 * `icon-size: 1.091em`); the macOS menu bar used a 20 pt sprite in a 22 pt box. */
const BOX_HEIGHT = 16;
const MAX_WIDTH = 29; // 36/20 of the height, the macOS width cap ratio

/* Minification filter. Sprite canvases (46-89 px) are larger than the box, so
 * at scale 1 they are downscaled. NEAREST is pixel-exact but drops rows and
 * columns; try Clutter.ScalingFilter.LINEAR or TRILINEAR to compare. */
const MIN_FILTER = Clutter.ScalingFilter.NEAREST;

/* Log tick and relayout counters every N seconds (0 = off): the evidence for
 * "no relayout per frame". */
const STATS_INTERVAL_SEC = 10;

const TAG = 'ptb-sprite-spike';

/** Decode one PNG and upload it into a texture-backed St.ImageContent. */
function uploadPng(coglContext, path) {
    let pixbuf = GdkPixbuf.Pixbuf.new_from_file(path);
    // Go writes 3-channel PNGs for fully opaque frames; normalise to RGBA so
    // the pixel format and the row stride always match.
    if (!pixbuf.has_alpha)
        pixbuf = pixbuf.add_alpha(false, 0, 0, 0);

    const content = St.ImageContent.new_with_preferred_size(pixbuf.width, pixbuf.height);
    content.set_bytes(
        coglContext,
        pixbuf.read_pixel_bytes(),
        Cogl.PixelFormat.RGBA_8888,
        pixbuf.width,
        pixbuf.height,
        pixbuf.rowstride);
    return content;
}

/** Read the manifest, upload every frame once, build the capped timeline. */
function loadSprite(dir, floorSec) {
    const t0 = GLib.get_monotonic_time();
    const file = Gio.File.new_for_path(GLib.build_filenamev([dir, 'manifest.json']));
    const [, raw] = file.load_contents(null);
    const manifest = JSON.parse(new TextDecoder().decode(raw));

    const coglContext = global.stage.context.get_backend().get_cogl_context();
    const contents = manifest.frames.map(f =>
        uploadPng(coglContext, GLib.build_filenamev([dir, f.file])));
    const t1 = GLib.get_monotonic_time();

    // capFrameRate works in seconds, like the Swift original; `frame` is the
    // index of the preloaded content to show.
    const native = manifest.frames.map((f, i) => ({frame: i, delaySec: f.delayMs / 1000}));
    const timeline = capFrameRate(native, manifest.animated ? floorSec : 0)
        .map(({frame, delaySec}) => ({frame, delayMs: Math.max(1, Math.round(delaySec * 1000))}));

    const loopMs = timeline.reduce((sum, f) => sum + f.delayMs, 0);
    console.log(`${TAG}: preloaded ${contents.length} frames (${manifest.width}x${manifest.height}) ` +
        `in ${((t1 - t0) / 1000).toFixed(1)} ms; timeline ${timeline.length} frames at floor ${floorSec} s, ` +
        `loop ${loopMs} ms, delays [${timeline.map(f => f.delayMs).join(' ')}]`);

    return {width: manifest.width, height: manifest.height, contents, timeline};
}

const SpriteIndicator = GObject.registerClass(
class SpriteIndicator extends PanelMenu.Button {
    _init(sprite) {
        super._init(0.0, 'PTB sprite spike', true);

        this._sprite = sprite;
        this._index = 0;
        this._timerId = 0;
        this._statsId = 0;
        this._ticks = 0;
        this._spriteRelayouts = 0;
        this._buttonRelayouts = 0;

        // Fixed-size actor: its size only changes with the scale factor, and
        // every frame has the same canvas, so set_content() never resizes it.
        this._actor = new Clutter.Actor({y_align: Clutter.ActorAlign.END});
        this._actor.set_content_scaling_filters(MIN_FILTER, Clutter.ScalingFilter.NEAREST);

        this._box = new St.Widget({
            layout_manager: new Clutter.BinLayout(),
            y_align: Clutter.ActorAlign.CENTER,
        });
        this._box.add_child(this._actor);
        this.add_child(this._box);

        this._themeContext = St.ThemeContext.get_for_stage(global.stage);
        this._scaleId = this._themeContext.connect('notify::scale-factor', () => this._updateSize());
        this._updateSize();
        this._showFrame(0);

        this._actor.connect('queue-relayout', () => this._spriteRelayouts++);
        this.connect('queue-relayout', () => this._buttonRelayouts++);
        this.connect('notify::mapped', () => this._syncPlayback());
        // Cleanup runs from the base class's 'destroy' handler, which calls
        // this._onDestroy(); see the override below.

        if (STATS_INTERVAL_SEC > 0) {
            this._statsId = GLib.timeout_add_seconds(GLib.PRIORITY_DEFAULT, STATS_INTERVAL_SEC, () => {
                console.log(`${TAG}: mapped=${this.mapped} ticks=${this._ticks} ` +
                    `spriteRelayouts=${this._spriteRelayouts} buttonRelayouts=${this._buttonRelayouts} ` +
                    `size=${this._actor.width}x${this._actor.height} scale=${this._themeContext.scale_factor}`);
                return GLib.SOURCE_CONTINUE;
            });
        }
    }

    _updateSize() {
        const sf = this._themeContext.scale_factor;
        const {width, height} = this._sprite;
        const fit = Math.min(MAX_WIDTH / width, BOX_HEIGHT / height);
        const w = Math.max(1, Math.round(width * fit * sf));
        const h = Math.max(1, Math.round(height * fit * sf));
        this._actor.set_size(w, h);
        this._box.set_size(w, BOX_HEIGHT * sf);
    }

    _showFrame(i) {
        this._index = i;
        this._actor.set_content(this._sprite.contents[this._sprite.timeline[i].frame]);
    }

    _syncPlayback() {
        const play = this.mapped && this._sprite?.timeline.length > 1;
        if (play && !this._timerId)
            this._scheduleNext();
        else if (!play)
            this._stop();
    }

    _scheduleNext() {
        const {delayMs} = this._sprite.timeline[this._index];
        this._timerId = GLib.timeout_add(GLib.PRIORITY_DEFAULT, delayMs, () => {
            // Clear first: the source is gone once we return SOURCE_REMOVE.
            this._timerId = 0;
            this._ticks++;
            this._showFrame((this._index + 1) % this._sprite.timeline.length);
            this._scheduleNext();
            return GLib.SOURCE_REMOVE;
        });
    }

    _stop() {
        if (this._timerId) {
            GLib.source_remove(this._timerId);
            this._timerId = 0;
        }
    }

    /* PanelMenu.Button (ButtonBox) already connects 'destroy' to
     * this._onDestroy() and uses it to destroy its menu and its container,
     * so override it and chain up instead of connecting a second handler. */
    _onDestroy() {
        this._stop();
        if (this._statsId) {
            GLib.source_remove(this._statsId);
            this._statsId = 0;
        }
        if (this._scaleId) {
            this._themeContext.disconnect(this._scaleId);
            this._scaleId = 0;
        }
        this._themeContext = null;
        this._actor = null;
        this._box = null;
        this._sprite = null;
        super._onDestroy();
    }
});

export default class PtbSpriteSpikeExtension extends Extension {
    enable() {
        const dir = FRAMES_DIR ?? GLib.build_filenamev([this.path, 'frames']);
        let sprite;
        try {
            sprite = loadSprite(dir, FLOOR_SEC);
        } catch (e) {
            // Rethrow with context: the shell marks the extension as ERROR
            // and `gnome-extensions info` shows the message.
            throw new Error(`${TAG}: cannot load frames from ${dir}: ${e.message}`);
        }
        this._indicator = new SpriteIndicator(sprite);
        Main.panel.addToStatusArea(this.uuid, this._indicator);
    }

    disable() {
        this._indicator?.destroy();
        this._indicator = null;
    }
}
