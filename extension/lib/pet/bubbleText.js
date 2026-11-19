/* Speech-bubble text: titles, bodies and their fitting (FPP:115-184).
 *
 * Pure module (no gi:// imports). Measuring is injected: `measure(title, body)`
 * returns `{titleWidth, bodyLines}` in logical px for the bubble's real fonts
 * and its 164 px column (lib/pet/pangoMeasure.js builds one on Pango).
 */

import {_} from '../i18n.js';
import * as V from '../viewModel.js';
import {BUBBLE_BODY_LINE_LIMIT, BUBBLE_CONTENT_WIDTH} from './petModel.js';

function graphemes(text) {
    return Array.from(new Intl.Segmenter(undefined, {granularity: 'grapheme'}).segment(text), s => s.segment);
}

function trimEnd(text) {
    return text.replace(/^\s+|\s+$/gu, '');
}

export function wouldTruncate(measure, title, body) {
    return measure(title, body).bodyLines > BUBBLE_BODY_LINE_LIMIT;
}

/**
 * "<label> · <status>" (or the plain title without a label), then "+N". The
 * label, or the plain title, is cut with "…" so the title stays on one line,
 * preferring a word boundary (FPP:143-164).
 */
export function agentTitle(measure, label, unlabeled, status, count) {
    const lead = label ?? unlabeled;
    let suffix = label === null || label === undefined ? '' : ` · ${status}`;
    if (count)
        suffix += ` ${count}`;
    const fits = title => measure(title, '').titleWidth <= BUBBLE_CONTENT_WIDTH;
    if (fits(lead + suffix))
        return lead + suffix;
    const chars = graphemes(lead);
    const cut = n => `${trimEnd(chars.slice(0, n).join(''))}…${suffix}`;
    // Invariant: `low` fits (or is the shortest cut), `high` does not.
    let low = 0, high = chars.length;
    while (high - low > 1) {
        const mid = Math.floor((low + high) / 2);
        if (fits(cut(mid)))
            low = mid;
        else
            high = mid;
    }
    const space = chars.slice(0, low).lastIndexOf(' ');
    if (space >= 0 && space >= Math.floor(low / 2))
        return cut(space);
    return cut(low);
}

/** Cut `text` with "…" until it fits the body line limit (FPP:166-184). */
export function fittedBubbleBody(measure, text, title) {
    if (!wouldTruncate(measure, title, text))
        return text;
    const chars = graphemes(text);
    const ellipsized = n => `${trimEnd(chars.slice(0, n).join(''))}…`;
    // Invariant: `low` fits, `high` does not.
    let low = 0, high = chars.length;
    while (high - low > 1) {
        const mid = Math.floor((low + high) / 2);
        if (wouldTruncate(measure, title, ellipsized(mid)))
            high = mid;
        else
            low = mid;
    }
    return ellipsized(low);
}

/**
 * Title, body and criticality for a bubble from petModel.bubble(). The limit
 * bubble reuses the notification text (floating-pet.md §4.3); the port has a
 * single account, so there is no account suffix to drop.
 */
export function bubbleContent(b, measure) {
    switch (b?.kind) {
    case 'limit': {
        const text = V.notificationText(b.alert);
        return {title: text?.title ?? '', body: text?.body ?? '', isCritical: b.alert.tier === 'crit'};
    }
    case 'agentWaiting': {
        const title = agentTitle(measure, b.label, _('Your agent needs you'), _('waiting'),
            b.others > 0 ? `+${b.others}` : null);
        return agentContent(measure, title, b.message ?? _("It's waiting for your input."));
    }
    case 'agentDone': {
        const title = agentTitle(measure, b.label, _('Done!'), _('done'), null);
        return agentContent(measure, title, b.message ?? _('Your agent finished its task.'));
    }
    default:
        return null;
    }
}

function agentContent(measure, title, body) {
    return {title, body: fittedBubbleBody(measure, body, title), isCritical: false};
}
