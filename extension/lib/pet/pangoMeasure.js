/* A bubble measurer on Pango (floating-pet.md §5.1 item 4).
 *
 * Imports Pango only, so it runs in the shell (with the bubble labels' own
 * Pango context and fonts) and under plain `gjs -m` (with a PangoCairo
 * context) for the property tests.
 */

import Pango from 'gi://Pango';

import {BUBBLE_CONTENT_WIDTH} from './petModel.js';

/**
 * @param {Pango.Context} context
 * @param {Pango.FontDescription} titleFont
 * @param {Pango.FontDescription} bodyFont
 * @param {number} [scale]  physical px per logical px (St scale factor)
 * @returns {(title: string, body: string) => {titleWidth: number, bodyLines: number}}
 */
export function pangoMeasure(context, titleFont, bodyFont, scale = 1) {
    const title = Pango.Layout.new(context);
    title.set_font_description(titleFont);
    const body = Pango.Layout.new(context);
    body.set_font_description(bodyFont);
    body.set_wrap(Pango.WrapMode.WORD_CHAR);
    body.set_width(Math.round(BUBBLE_CONTENT_WIDTH * scale) * Pango.SCALE);
    return (titleText, bodyText) => {
        title.set_text(titleText, -1);
        const [, logical] = title.get_pixel_extents();
        body.set_text(bodyText, -1);
        return {
            titleWidth: logical.width / scale,
            bodyLines: bodyText.length > 0 ? body.get_line_count() : 0,
        };
    };
}
