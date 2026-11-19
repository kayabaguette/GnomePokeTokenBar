/* One-shot effects over the Home sprite tile after a Bag use
 * (bag-shop.md §2.5): the "+XP" capsule for Rare Candy and the three-sparkle
 * burst for a Mint. Each is a transient St actor eased in, held and faded out,
 * then destroyed. `ease()` follows the shell's animation setting, so with
 * animations off the effect appears and goes without motion.
 */

import Clutter from 'gi://Clutter';
import St from 'gi://St';

import {xpText} from './shopModel.js';

/* Swift springs (response 0.4/0.35, damping 0.6/0.5) read as a small overshoot. */
const IN_MS = 400;
const OUT_MS = 400;
const CAPSULE_HOLD_MS = 1300;
const SPARKLE_IN_MS = 350;
const SPARKLE_HOLD_MS = 900;
/* The capsule rides on the tile's top edge. Upstream lifts it 16 pt above the
 * tile; the Home scroll view would clip that, so it overlaps the tile instead. */
const CAPSULE_LIFT = 4;
/* The sprite tile's CSS size, used while the layer has no valid allocation
 * yet (Home was hidden until this very moment). */
const TILE_SIZE = 76;
/* CV:893-902: three sparkles, sizes and offsets from the tile center. */
const SPARKLES = [
    {size: 22, dx: -11, dy: -9},
    {size: 15, dx: 13, dy: 5},
    {size: 12, dx: 1, dy: 13},
];

/* A transient actor whose parent (the card) can be destroyed mid-animation.
 * Only a finished ease may destroy it: an interrupted one means the card is
 * being torn down, which takes the actor with it, and destroying it from
 * inside that stop would edit Clutter's transition table mid-iteration (the
 * GLib-CRITICAL phase 20 hit with the pet's 🙏). */
function transient(actor) {
    actor._ptbGone = false;
    actor.connect('destroy', () => {
        actor._ptbGone = true;
    });
    return actor;
}

function dispose(actor) {
    if (!actor._ptbGone)
        actor.destroy();
}

function fadeOutAndDestroy(actor, delay) {
    actor.ease({
        opacity: 0,
        delay,
        duration: OUT_MS,
        mode: Clutter.AnimationMode.EASE_IN_QUAD,
        onStopped: finished => {
            if (finished)
                dispose(actor);
        },
    });
}

/** The layer's size, or the tile's CSS size in actor units before its first allocation. */
function themeScale() {
    return St.ThemeContext.get_for_stage(global.stage).scale_factor;
}

function layerSize(layer) {
    const scale = themeScale();
    const [w, h] = layer.has_allocation() ? layer.get_size() : [0, 0];
    return [w > 0 ? w : TILE_SIZE * scale, h > 0 ? h : TILE_SIZE * scale];
}

/** Spring in, hold, fade out, destroy. */
function popInOut(actor, inMs, holdMs) {
    actor.ease({
        opacity: 255,
        scale_x: 1,
        scale_y: 1,
        duration: inMs,
        mode: Clutter.AnimationMode.EASE_OUT_BACK,
        onStopped: finished => {
            if (finished && !actor._ptbGone)
                fadeOutAndDestroy(actor, holdMs);
        },
    });
}

/**
 * The "+300M XP" capsule, top-center of the tile.
 *
 * @param {Clutter.Actor} layer  a fixed-layout actor covering the tile
 * @param {number} xp            the consumed XP from the useCandy reply
 */
export function playXpCapsule(layer, xp) {
    const capsule = transient(new St.Label({style_class: 'ptb-xp-capsule', text: xpText(xp), opacity: 0}));
    layer.add_child(capsule);
    const [, natW] = capsule.get_preferred_width(-1);
    const [w] = layerSize(layer);
    capsule.set_position(Math.round((w - natW) / 2), -CAPSULE_LIFT * themeScale());
    capsule.set_pivot_point(0.5, 0.5);
    capsule.set_scale(0.6, 0.6);
    popInOut(capsule, IN_MS, CAPSULE_HOLD_MS);
}

/** The Mint sparkle: three ✨ that pop in around the sprite, then fade. */
export function playSparkle(layer) {
    const [w, h] = layerSize(layer);
    const cx = w / 2;
    const cy = h / 2;
    const scale = themeScale();
    for (const s of SPARKLES) {
        const star = transient(new St.Label({style_class: 'ptb-mint-sparkle', text: '✨', opacity: 0}));
        star.style = `font-size: ${s.size}px;`;
        layer.add_child(star);
        const [, sw] = star.get_preferred_width(-1);
        const [, sh] = star.get_preferred_height(-1);
        star.set_position(Math.round(cx + s.dx * scale - sw / 2), Math.round(cy + s.dy * scale - sh / 2));
        star.set_pivot_point(0.5, 0.5);
        star.set_scale(0.2, 0.2);
        popInOut(star, SPARKLE_IN_MS, SPARKLE_HOLD_MS);
    }
}

/* CV:718-723, :866-875: the 🎭 springs in top-centre 0.25 s after the flash
 * starts and fades out over 0.5 s from 2.6 s. Upstream lifts it 12 pt above
 * the tile; the scroll view would clip that, so it rides the top edge like
 * the XP capsule. */
const MASK_DELAY_MS = 250;
const MASK_IN_MS = 400;
const MASK_FADE_AT_MS = 2600;
const MASK_OUT_MS = 500;

/** The Ditto reveal's 🎭 over the sprite tile (M3 decision 43), on top of the CSS flash. */
export function playRevealMask(layer) {
    const mask = transient(new St.Label({style_class: 'ptb-reveal-mask', text: '🎭', opacity: 0}));
    layer.add_child(mask);
    const [, natW] = mask.get_preferred_width(-1);
    const [, natH] = mask.get_preferred_height(-1);
    const [w] = layerSize(layer);
    mask.set_position(Math.round((w - natW) / 2), -CAPSULE_LIFT * themeScale());
    mask.set_pivot_point(0.5, 0.5);
    mask.set_scale(0.4, 0.4);
    mask.translation_y = Math.round(natH * 0.15);
    mask.ease({
        opacity: 255,
        scale_x: 1,
        scale_y: 1,
        translation_y: 0,
        delay: MASK_DELAY_MS,
        duration: MASK_IN_MS,
        mode: Clutter.AnimationMode.EASE_OUT_BACK,
        // An interrupted ease means the card is being torn down: its parent destroys the
        // mask, and destroying it from inside that stop would edit Clutter's transition
        // table mid-iteration (a GLib-CRITICAL at disable).
        onStopped: finished => {
            if (!finished || mask._ptbGone)
                return;
            mask.ease({
                opacity: 0,
                delay: MASK_FADE_AT_MS - MASK_DELAY_MS - MASK_IN_MS,
                duration: MASK_OUT_MS,
                mode: Clutter.AnimationMode.EASE_OUT_QUAD,
                onStopped: done => {
                    if (done)
                        dispose(mask);
                },
            });
        },
    });
}
