/* Animated sprite actor: the one module that draws helper sprites (Spike A).
 *
 * Kept in one place on purpose: the live CPU gate is still pending (plan
 * Phase 0 gate), and a failed gate must only change the defaults below.
 *
 * Cost model
 *   - load: manifest.json and frames.rgba are read with load_contents_async
 *     (no synchronous IO, no decode); each frame is uploaded once into its own
 *     texture-backed St.ImageContent.
 *   - per tick: one set_content() on a fixed-size Clutter.Actor. No IO, no
 *     decode, no size change, so no relayout. The timer is removed while the
 *     actor is unmapped or paused.
 *   - while the frames load, frame-000.png (or a static sprite) is decoded off
 *     the main thread (GdkPixbuf async) and shown as a 2-frame bob.
 */

import Clutter from 'gi://Clutter';
import Cogl from 'gi://Cogl';
import GdkPixbuf from 'gi://GdkPixbuf';
import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import GObject from 'gi://GObject';
import St from 'gi://St';

import {capFrameRate} from './capFrameRate.js';

/* ---- Defaults (change these if the CPU or the look test fails) ---------- */

/* Top-bar box, in logical px. The panel is 2.2em (≈32 px at the default
 * 11 pt font) and the panel button draws a 3 px border, which leaves ≈26 px.
 * 22 px matches the macOS menu bar canvas (ui.md §1.3) and leaves 2 px of air
 * above and below. Spike A's 16 px box (the panel icon size) downscaled the
 * 46–89 px canvases 3–5× and lost the pixel shapes. */
export const PANEL_BOX_HEIGHT = 22;
/* Width cap: wide sprites (Charizard) shrink to fit instead of pushing the
 * labels away. macOS caps at 36 px for a 20 px sprite (1.8:1); 40 keeps that
 * ratio at 22 px. */
export const PANEL_MAX_WIDTH = 40;

/* Minification: the panel downscales 2–4×. NEAREST drops whole pixel rows and
 * columns, so outlines break up; TRILINEAR samples a mipmap level close to the
 * target size and keeps every outline, at the cost of slightly soft edges.
 * Magnification (HiDPI, popover tile): NEAREST keeps pixel art crisp. */
export const MIN_FILTER = Clutter.ScalingFilter.TRILINEAR;
export const MAG_FILTER = Clutter.ScalingFilter.NEAREST;

/* Frame-rate floors per `animation-quality` (ui.md §1.3). The popover uses
 * native timing (0). */
export const QUALITY_FLOOR_MS = {powerSaver: 400, balanced: 200, smooth: 100};

/* Static sprites and the egg bob: 2 frames, 0.5 s each, 1 px up. */
const BOB_FRAME_MS = 500;
const BOB_OFFSET_PX = 1;

/* Sanity limits before handing bytes to C. */
const MAX_DIMENSION = 1024;
const MAX_FRAMES = 1024;
const MIN_DELAY_MS = 20;

const LOG = 'PokeTokenBar';

export function floorForQuality(quality) {
    return QUALITY_FLOOR_MS[quality] ?? QUALITY_FLOOR_MS.powerSaver;
}

function coglContext() {
    return global.stage.context.get_backend().get_cogl_context();
}

function isCancelled(e) {
    return e?.matches?.(Gio.IOErrorEnum, Gio.IOErrorEnum.CANCELLED);
}

/**
 * Validate a parsed manifest against the blob size. Returns the frame list or
 * throws: a bad offset handed to set_bytes would read past the buffer in C.
 */
function validateFrames(manifest, blobLength) {
    const {width: w, height: h} = manifest;
    if (!Number.isInteger(w) || !Number.isInteger(h) || w < 1 || h < 1 || w > MAX_DIMENSION || h > MAX_DIMENSION)
        throw new Error(`bad sprite size ${w}x${h}`);
    if (!Array.isArray(manifest.frames) || manifest.frames.length < 1 || manifest.frames.length > MAX_FRAMES)
        throw new Error('bad frame list');
    const size = w * h * 4;
    return manifest.frames.map((f, i) => {
        const offset = f?.offset;
        if (!Number.isInteger(offset) || offset < 0 || offset + size > blobLength)
            throw new Error(`frame ${i} offset ${offset} outside the ${blobLength}-byte blob`);
        const delay = Number(f.delayMs);
        return {offset, delayMs: Number.isFinite(delay) && delay >= MIN_DELAY_MS ? delay : 100};
    });
}

function uploadRgba(cogl, bytes, w, h, rowstride) {
    const content = St.ImageContent.new_with_preferred_size(w, h);
    content.set_bytes(cogl, bytes, Cogl.PixelFormat.RGBA_8888, w, h, rowstride);
    return content;
}

/**
 * An actor showing one sprite source:
 *   - `{path: '<dir>/manifest.json'}`: animated frames (protocol §8.2);
 *   - `{path: '<file>.png'}`: a static sprite, drawn as a 2-frame bob;
 *   - `null`: the egg placeholder bob (nothing usable on disk yet).
 */
export const SpriteAnimator = GObject.registerClass(
class PtbSpriteAnimator extends St.Widget {
    /**
     * @param {object} params
     * @param {number} params.boxHeight   logical px
     * @param {number} params.maxWidth    logical px
     * @param {number} [params.floorMs]   frame floor, 0 = native timing
     * @param {boolean} [params.fixedWidth]  keep the box maxWidth wide (else it hugs the sprite)
     * @param {boolean} [params.allowUpscale]  integer upscaling for small canvases
     * @param {string} [params.styleClass]
     */
    _init({boxHeight, maxWidth, floorMs = 0, fixedWidth = false, allowUpscale = false, styleClass = ''}) {
        super._init({
            layout_manager: new Clutter.BinLayout(),
            style_class: styleClass,
            x_align: Clutter.ActorAlign.CENTER,
            y_align: Clutter.ActorAlign.CENTER,
        });
        this._boxHeight = boxHeight;
        this._maxWidth = maxWidth;
        this._floorMs = floorMs;
        this._fixedWidth = fixedWidth;
        this._allowUpscale = allowUpscale;

        this._image = new Clutter.Actor({
            x_align: Clutter.ActorAlign.CENTER,
            y_align: Clutter.ActorAlign.END, // feet on one baseline
        });
        this._image.set_content_scaling_filters(MIN_FILTER, MAG_FILTER);
        this.add_child(this._image);

        this._placeholder = new St.Label({
            text: '🥚',
            style_class: 'ptb-sprite-placeholder',
            x_align: Clutter.ActorAlign.CENTER,
            y_align: Clutter.ActorAlign.CENTER,
            visible: false,
        });
        this.add_child(this._placeholder);

        this._path = undefined;
        this._gen = 0;
        this._cancellable = null;
        this._sprite = null; // {width, height, contents[], native[], animated, static}
        this._timeline = [];
        this._index = 0;
        this._timerId = 0;
        this._paused = false;
        this._destroyed = false;
        /** Called with the path when a source cannot be loaded (cache cleared). */
        this.onLoadFailed = null;

        this._themeContext = St.ThemeContext.get_for_stage(global.stage);
        this._themeContext.connectObject('notify::scale-factor', () => this._updateSize(), this);
        this.connect('notify::mapped', () => this._sync());
        this.connect('destroy', () => this._onDestroy());
        this._updateSize();
        this.setSource(null);
    }

    /** Change the frame floor (animation-quality). */
    setFloorMs(floorMs) {
        if (this._floorMs === floorMs)
            return;
        this._floorMs = floorMs;
        this._rebuildTimeline();
    }

    /** Stop ticking without unloading (e.g. while the popover shows the same companion). */
    setPaused(paused) {
        if (this._paused === paused)
            return;
        this._paused = paused;
        this._sync();
    }

    /** @param {{path: string}|null} source */
    setSource(source) {
        const path = source?.path ?? null;
        if (path === this._path)
            return;
        this._path = path;
        this._gen++;
        this._cancellable?.cancel();
        this._cancellable = new Gio.Cancellable();
        this._stopTimer();

        if (!path) {
            this._showPlaceholder();
            return;
        }
        if (path.endsWith('.png')) {
            this._loadStatic(path, this._gen, this._cancellable, false);
            return;
        }
        // Show frame-000.png while the blob loads, unless something is already shown.
        const dir = GLib.path_get_dirname(path);
        if (!this._sprite)
            this._loadStatic(GLib.build_filenamev([dir, 'frame-000.png']), this._gen, this._cancellable, true);
        this._loadAnimated(path, this._gen, this._cancellable);
    }

    /* ---- loading ------------------------------------------------------ */

    _loadAnimated(path, gen, cancellable) {
        const manifestFile = Gio.File.new_for_path(path);
        manifestFile.load_contents_async(cancellable, (f, res) => {
            if (gen !== this._gen || this._destroyed)
                return;
            let manifest;
            try {
                const [, raw] = f.load_contents_finish(res);
                manifest = JSON.parse(new TextDecoder().decode(raw));
            } catch (e) {
                if (!isCancelled(e))
                    this._fail(path, e);
                return;
            }
            const blobName = typeof manifest?.rgba === 'string' && !manifest.rgba.includes('/')
                ? manifest.rgba : 'frames.rgba';
            const blobFile = manifestFile.get_parent().get_child(blobName);
            blobFile.load_contents_async(cancellable, (bf, bres) => {
                if (gen !== this._gen || this._destroyed)
                    return;
                try {
                    const [, blob] = bf.load_contents_finish(bres);
                    this._uploadFrames(manifest, blob);
                } catch (e) {
                    if (!isCancelled(e))
                        this._fail(path, e);
                }
            });
        });
    }

    _uploadFrames(manifest, blob) {
        const t0 = GLib.get_monotonic_time();
        const frames = validateFrames(manifest, blob.length);
        const {width: w, height: h} = manifest;
        const size = w * h * 4;
        const cogl = coglContext();
        const contents = frames.map(f =>
            uploadRgba(cogl, new GLib.Bytes(blob.subarray(f.offset, f.offset + size)), w, h, w * 4));
        this._sprite = {
            width: w,
            height: h,
            contents,
            native: frames.map((f, i) => ({frame: i, delaySec: f.delayMs / 1000})),
            animated: manifest.animated !== false && frames.length > 1,
            static: false,
        };
        const ms = (GLib.get_monotonic_time() - t0) / 1000;
        console.debug(`${LOG}: uploaded ${contents.length} frames ${w}x${h} in ${ms.toFixed(1)} ms`);
        this._placeholder.hide();
        this._image.show();
        this._image.translation_y = 0;
        this._updateSize();
        this._rebuildTimeline();
    }

    _loadStatic(path, gen, cancellable, isPreview) {
        const file = Gio.File.new_for_path(path);
        file.read_async(GLib.PRIORITY_DEFAULT, cancellable, (f, res) => {
            if (gen !== this._gen || this._destroyed)
                return;
            let stream;
            try {
                stream = f.read_finish(res);
            } catch (e) {
                if (!isCancelled(e) && !isPreview)
                    this._fail(path, e);
                return;
            }
            GdkPixbuf.Pixbuf.new_from_stream_async(stream, cancellable, (_o, pres) => {
                let pixbuf = null;
                try {
                    pixbuf = GdkPixbuf.Pixbuf.new_from_stream_finish(pres);
                } catch (e) {
                    if (!isCancelled(e) && !isPreview && gen === this._gen)
                        this._fail(path, e);
                }
                stream.close_async(GLib.PRIORITY_DEFAULT, null, null);
                if (!pixbuf || gen !== this._gen || this._destroyed)
                    return;
                // A preview loses the race against the real frames.
                if (isPreview && this._sprite && !this._sprite.static)
                    return;
                if (pixbuf.width > MAX_DIMENSION || pixbuf.height > MAX_DIMENSION)
                    return;
                if (!pixbuf.has_alpha)
                    pixbuf = pixbuf.add_alpha(false, 0, 0, 0);
                const content = uploadRgba(coglContext(), pixbuf.read_pixel_bytes(),
                    pixbuf.width, pixbuf.height, pixbuf.rowstride);
                this._sprite = {
                    width: pixbuf.width,
                    height: pixbuf.height,
                    contents: [content],
                    native: [{frame: 0, delaySec: BOB_FRAME_MS / 1000}],
                    animated: false,
                    static: true,
                };
                this._placeholder.hide();
                this._image.show();
                this._updateSize();
                this._rebuildTimeline();
            });
        });
    }

    _fail(path, e) {
        console.warn(`${LOG}: cannot load sprite ${path}: ${e.message}`);
        if (!this._sprite)
            this._showPlaceholder();
        try {
            this.onLoadFailed?.(path);
        } catch (err) {
            console.error(`${LOG}: onLoadFailed threw: ${err}`);
        }
    }

    _showPlaceholder() {
        this._sprite = null;
        this._image.set_content(null);
        this._image.hide();
        this._placeholder.show();
        this._updateSize();
        this._rebuildTimeline();
    }

    /* ---- geometry ----------------------------------------------------- */

    _updateSize() {
        if (this._destroyed)
            return;
        const sf = this._themeContext.scale_factor;
        const boxH = this._boxHeight * sf;
        const maxW = this._maxWidth * sf;
        let boxW = maxW;
        if (this._sprite) {
            const {width, height} = this._sprite;
            let scale = Math.min(maxW / width, boxH / height);
            // Integer upscaling only: 1.4× nearest-neighbour makes uneven pixels.
            if (scale > 1)
                scale = this._allowUpscale ? Math.floor(scale) : 1;
            const w = Math.max(1, Math.round(width * scale));
            const h = Math.max(1, Math.round(height * scale));
            this._image.set_size(w, h);
            if (!this._fixedWidth)
                boxW = w;
        } else if (!this._fixedWidth) {
            boxW = boxH;
        }
        this.set_size(boxW, boxH);
    }

    /* ---- playback ----------------------------------------------------- */

    _rebuildTimeline() {
        this._stopTimer();
        this._image.translation_y = 0;
        this._placeholder.translation_y = 0;
        if (!this._sprite) {
            // Egg placeholder bob.
            this._timeline = [{frame: -1, delayMs: BOB_FRAME_MS}, {frame: -2, delayMs: BOB_FRAME_MS}];
        } else if (this._sprite.static || !this._sprite.animated) {
            // Static sprite or a 1-frame GIF: 2-frame bob on the first frame.
            this._timeline = [{frame: 0, delayMs: BOB_FRAME_MS, bob: 0}, {frame: 0, delayMs: BOB_FRAME_MS, bob: 1}];
        } else {
            const floorSec = (this._floorMs ?? 0) / 1000;
            this._timeline = capFrameRate(this._sprite.native, floorSec)
                .map(({frame, delaySec}) => ({frame, delayMs: Math.max(MIN_DELAY_MS, Math.round(delaySec * 1000))}));
        }
        this._index = 0;
        this._showFrame(0);
        this._sync();
    }

    _showFrame(i) {
        this._index = i;
        const step = this._timeline[i];
        if (!step)
            return;
        const lift = -BOB_OFFSET_PX * this._themeContext.scale_factor;
        if (step.frame < 0) {
            this._placeholder.translation_y = step.frame === -2 ? lift : 0;
            return;
        }
        const content = this._sprite?.contents[step.frame];
        if (content && this._image.content !== content)
            this._image.set_content(content);
        this._image.translation_y = step.bob ? lift : 0;
    }

    _sync() {
        const play = !this._destroyed && this.mapped && !this._paused && this._timeline.length > 1;
        if (play && !this._timerId)
            this._scheduleNext();
        else if (!play)
            this._stopTimer();
    }

    _scheduleNext() {
        const step = this._timeline[this._index];
        this._timerId = GLib.timeout_add(GLib.PRIORITY_DEFAULT, step?.delayMs ?? BOB_FRAME_MS, () => {
            this._timerId = 0; // the source is gone once we return SOURCE_REMOVE
            this._showFrame((this._index + 1) % this._timeline.length);
            this._sync();
            return GLib.SOURCE_REMOVE;
        });
    }

    _stopTimer() {
        if (this._timerId) {
            GLib.source_remove(this._timerId);
            this._timerId = 0;
        }
    }

    _onDestroy() {
        this._destroyed = true;
        this._gen++;
        this._stopTimer();
        this._cancellable?.cancel();
        this._cancellable = null;
        this._themeContext?.disconnectObject(this);
        this._themeContext = null;
        this._sprite = null;
        this.onLoadFailed = null;
    }
});
