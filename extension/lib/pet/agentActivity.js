/* Agent activity: parsing, sanitizing and the multi-agent tracker
 * (floating-pet.md §1.6, protocol §15; upstream AgentActivity.swift).
 *
 * Pure module (no gi:// imports), unit-tested under plain `gjs -m`. Reports
 * arrive over D-Bus (lib/pet/agentBus.js) as `Report(state, label, message)`
 * or as an upstream `poketokenbar://agent?…` URL; both end in an event
 * `{activity: {state, message}, label}`. Time is passed in seconds, so tests
 * drive expirations without sleeping; the caller arms one timer at `deadline`.
 */

export const SCHEME = 'poketokenbar';
export const HOST = 'agent';

/* Size limits on untrusted input, not layout (AA:16-23). */
export const MAX_MESSAGE_LENGTH = 80;
export const MAX_LABEL_LENGTH = 24;
export const MAX_MESSAGE_SCALARS = MAX_MESSAGE_LENGTH * 4;

export const STATES = ['idle', 'working', 'waiting', 'done'];

const ZWJ = '‍';
let segmenter = null;

function graphemes(text) {
    segmenter ??= new Intl.Segmenter(undefined, {granularity: 'grapheme'});
    return Array.from(segmenter.segment(text), s => s.segment);
}

/* Swift `trimmingCharacters(in: .whitespaces)`: after the collapse below,
 * only plain horizontal whitespace can remain at the ends. */
function trimWhitespace(text) {
    return text.replace(/^\s+|\s+$/gu, '');
}

/**
 * Plain single-line text (AA:47-74): control characters become spaces, format
 * characters are dropped except U+200D, whitespace runs collapse, and anything
 * past 4 × `maxLength` scalars or `maxLength` grapheme clusters is cut with an
 * ellipsis. Empty input gives null.
 */
export function sanitized(raw, maxLength) {
    if (typeof raw !== 'string')
        return null;
    const maxScalars = maxLength * 4;
    let text = raw
        .replace(/\p{Cc}/gu, ' ')
        .replace(/\p{Cf}/gu, c => (c === ZWJ ? c : ''))
        .split(/\s+/u)
        .filter(s => s.length > 0)
        .join(' ');
    if (text.length === 0)
        return null;
    const scalars = Array.from(text);
    if (scalars.length > maxScalars)
        text = `${trimWhitespace(scalars.slice(0, maxScalars - 1).join(''))}…`;
    const clusters = graphemes(text);
    if (clusters.length <= maxLength)
        return text;
    return `${trimWhitespace(clusters.slice(0, maxLength - 1).join(''))}…`;
}

export function sanitizedMessage(raw) {
    return sanitized(raw, MAX_MESSAGE_LENGTH);
}

export function sanitizedLabel(raw) {
    return sanitized(raw, MAX_LABEL_LENGTH);
}

/**
 * The event for a state word, or null for an unknown state. The message is
 * kept only for `waiting` and `done`.
 */
export function makeEvent(state, label, message) {
    const s = typeof state === 'string' ? state.toLowerCase() : null;
    if (!STATES.includes(s))
        return null;
    const keepsMessage = s === 'waiting' || s === 'done';
    return {
        activity: {state: s, message: keepsMessage ? sanitizedMessage(message) : null},
        label: sanitizedLabel(label),
    };
}

/** `Report(sss)` arguments; empty strings mean none (protocol §15). */
export function eventFromReport(state, label, message) {
    return makeEvent(state, label || null, message || null);
}

function decode(component) {
    return decodeURIComponent(component); // keeps '+' literal, as URLComponents does
}

/**
 * Parse `poketokenbar://agent?state=…[&label=…][&message=…]` (AA:30-45).
 * Scheme and host are case-insensitive, a missing host is rejected, the first
 * `state` wins. Malformed percent-encoding rejects the URL, as `URL(string:)`
 * does. Never throws.
 */
export function eventFromUrl(url) {
    if (typeof url !== 'string')
        return null;
    const m = /^([A-Za-z][A-Za-z0-9+.-]*):\/\/([^/?#]*)([^?#]*)(?:\?([^#]*))?/.exec(url);
    if (!m || m[1].toLowerCase() !== SCHEME)
        return null;
    const host = m[2].replace(/^.*@/, '').replace(/:\d*$/, '');
    if (host.toLowerCase() !== HOST || m[4] === undefined)
        return null;
    const items = [];
    try {
        for (const part of m[4].split('&')) {
            if (part.length === 0)
                continue;
            const eq = part.indexOf('=');
            items.push(eq < 0
                ? {name: decode(part), value: null}
                : {name: decode(part.slice(0, eq)), value: decode(part.slice(eq + 1))});
        }
    } catch {
        return null;
    }
    const first = name => items.find(i => i.name === name)?.value ?? null;
    const state = first('state');
    if (state === null)
        return null;
    return makeEvent(state, first('label'), first('message'));
}

/* ---- tracker (AA:95-188) ------------------------------------------------ */

export const WORKING_TIMEOUT_S = 10 * 60;
export const DONE_DURATION_S = 6;
export const MAX_AGENTS = 8;

/** The one activity of a summary: done, then waiting, then working. */
export function activityOf(summary) {
    if (summary.done)
        return {state: 'done', message: summary.done.message};
    if (summary.waiting)
        return {state: 'waiting', message: summary.waiting.message};
    return {state: summary.working ? 'working' : 'idle', message: null};
}

export class AgentActivityTracker {
    constructor() {
        /** label ('' = unlabeled) → {activity, deadline (s) | null, sequence} */
        this.agents = new Map();
        this._sequence = 0;
    }

    /** The earliest moment any agent falls back to idle on its own, or null. */
    get deadline() {
        let min = null;
        for (const e of this.agents.values()) {
            if (e.deadline !== null && (min === null || e.deadline < min))
                min = e.deadline;
        }
        return min;
    }

    get summary() {
        const note = (key, message) => ({label: key === '' ? null : key, message});
        const summary = {done: null, waiting: null, otherWaitingCount: 0, working: false};
        let done = null, waiting = null, waitingCount = 0;
        for (const [key, e] of this.agents) {
            switch (e.activity.state) {
            case 'working':
                summary.working = true;
                break;
            case 'waiting':
                waitingCount++;
                if (!waiting || e.sequence > waiting.e.sequence)
                    waiting = {key, e};
                break;
            case 'done':
                if (!done || e.sequence > done.e.sequence)
                    done = {key, e};
                break;
            }
        }
        if (done)
            summary.done = note(done.key, done.e.activity.message);
        if (waiting) {
            summary.waiting = note(waiting.key, waiting.e.activity.message);
            summary.otherWaitingCount = waitingCount - 1;
        }
        return summary;
    }

    get activity() {
        return activityOf(this.summary);
    }

    /** An event replaces only its own label's state. */
    apply(event, now) {
        const key = event.label ?? '';
        this.expire(now);
        let deadline;
        switch (event.activity.state) {
        case 'idle':
            this.agents.delete(key);
            return;
        case 'working':
            deadline = now + WORKING_TIMEOUT_S;
            break;
        case 'done':
            deadline = now + DONE_DURATION_S;
            break;
        default:
            deadline = null;
        }
        if (!this.agents.has(key) && this.agents.size >= MAX_AGENTS) {
            let oldest = null;
            for (const [k, e] of this.agents) {
                if (oldest === null || e.sequence < this.agents.get(oldest).sequence)
                    oldest = k;
            }
            this.agents.delete(oldest);
        }
        this._sequence++;
        this.agents.set(key, {activity: {...event.activity}, deadline, sequence: this._sequence});
    }

    /** Drop entries whose deadline passed; returns whether any changed. */
    expire(now) {
        let changed = false;
        for (const [k, e] of this.agents) {
            if (e.deadline !== null && !(now < e.deadline)) {
                this.agents.delete(k);
                changed = true;
            }
        }
        return changed;
    }

    /** A click answers every question and acknowledges results; work goes on. */
    dismissBubble() {
        let changed = false;
        for (const [k, e] of this.agents) {
            if (e.activity.state !== 'working') {
                this.agents.delete(k);
                changed = true;
            }
        }
        return changed;
    }

    reset() {
        this.agents.clear();
    }
}
