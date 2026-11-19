/* Sprite fitting inside a box, shared by SpriteAnimator and the floating pet.
 *
 * Pure module (no gi:// imports), unit-tested under plain `gjs -m`.
 */

/**
 * Where a `w`×`h` canvas lands inside a `boxW`×`boxH` box.
 *
 * `upscale` decides what happens when the canvas is smaller than the box:
 *   - 'none': keep 1×;
 *   - 'integer': the largest whole scale that fits (M2 decision 28: uneven
 *     pixel columns at 1.4× nearest-neighbour), fractional only to shrink;
 *   - 'fractional': aspect-fit exactly (upstream SpriteFit, SL:363-375).
 * `align` is 'bottom' (feet on one baseline) or 'center' (upstream); x is centred.
 *
 * @returns {{x: number, y: number, width: number, height: number, scale: number}}
 */
export function fitRect(boxW, boxH, w, h, {upscale = 'none', align = 'bottom'} = {}) {
    if (!(w > 0) || !(h > 0)) {
        const side = Math.min(boxW, boxH);
        return {x: (boxW - side) / 2, y: align === 'bottom' ? boxH - side : (boxH - side) / 2,
            width: side, height: side, scale: 1};
    }
    let scale = Math.min(boxW / w, boxH / h);
    if (scale > 1) {
        if (upscale === 'none')
            scale = 1;
        else if (upscale === 'integer')
            scale = Math.floor(scale);
    }
    const width = w * scale;
    const height = h * scale;
    return {
        x: (boxW - width) / 2,
        y: align === 'bottom' ? boxH - height : (boxH - height) / 2,
        width,
        height,
        scale,
    };
}

/**
 * Distance from the box top to the sprite's first visible row: the fitted
 * rect's top plus the manifest's `visibleTopFraction` of its height
 * (SpriteVisibleTop.inset, SL:416-419; protocol §8.2). A missing or
 * out-of-range fraction counts as 0.
 */
export function visibleTopInset(rect, fraction) {
    const f = Number.isFinite(fraction) ? Math.min(Math.max(fraction, 0), 1) : 0;
    return rect.y + f * rect.height;
}

/** A manifest's `visibleTopFraction`, or 0 when absent or invalid (protocol §8.2). */
export function manifestVisibleTop(manifest) {
    const v = manifest?.visibleTopFraction;
    return typeof v === 'number' && v >= 0 && v < 1 ? v : 0;
}
