/* Translation hook for the pure modules (format, chart and view models).
 *
 * Those modules must not import gi:// or resource:// modules, so that they run
 * under a plain `gjs -m` in the unit tests. The extension injects the shell's
 * gettext once at enable(); tests keep the identity function, so they see the
 * English source strings.
 */

let gettextFn = s => s;
let ngettextFn = (s, p, n) => (n === 1 ? s : p);

/**
 * @param {(s: string) => string} gettext
 * @param {(s: string, p: string, n: number) => string} [ngettext]
 */
export function setTranslations(gettext, ngettext) {
    gettextFn = typeof gettext === 'function' ? gettext : (s => s);
    ngettextFn = typeof ngettext === 'function' ? ngettext : ((s, p, n) => (n === 1 ? s : p));
}

/** gettext. Call it on string literals only, so xgettext can extract them. */
export function _(s) {
    return gettextFn(s);
}

/** ngettext. */
export function ngettext(singular, plural, n) {
    return ngettextFn(singular, plural, n);
}

/**
 * printf-style substitution for translated templates: `%s` and `%d` are
 * replaced in order, `%%` gives a literal percent sign. Positional reordering
 * (`%1$s`) is also accepted so translators can reorder arguments.
 */
export function fmt(template, ...args) {
    let next = 0;
    return String(template).replace(/%(?:(\d+)\$)?([sd%])/g, (match, pos, kind) => {
        if (kind === '%')
            return '%';
        const value = pos ? args[Number(pos) - 1] : args[next++];
        if (kind === 'd')
            return String(Math.trunc(Number(value) || 0));
        return value === undefined || value === null ? '' : String(value);
    });
}
