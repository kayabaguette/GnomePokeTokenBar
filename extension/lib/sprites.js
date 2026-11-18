/* getSprite results, cached per request (protocol §8.1).
 *
 * The helper owns the sprite cache on disk; this only remembers which path a
 * request resolved to, so the popover does not ask again on every state. A
 * failed load (cache cleared) forgets the entry, and the next resolve asks the
 * helper once more (§8.2 "Missing files").
 */

export function spriteRequestKey(req) {
    if (req?.egg)
        return 'egg';
    return JSON.stringify([req.speciesId, req.form ?? null, Boolean(req.shiny), Boolean(req.back),
        req.animated !== false]);
}

/** The menu subject's request, used when its manifest went missing. */
export function menuSubjectRequest(subject) {
    if (!subject || subject.egg)
        return {egg: true};
    return {speciesId: subject.speciesId, form: subject.form ?? null, shiny: Boolean(subject.shiny),
        back: Boolean(subject.back), animated: true};
}

export class SpriteResolver {
    /** @param {import('./helperClient.js').HelperClient} client */
    constructor(client) {
        this._client = client;
        this._cache = new Map(); // key → Promise<{key, status, manifestPath}>
    }

    /**
     * @param {object} req  SpriteRequest args
     * @param {{timeoutMs?: number}} [options]
     * @returns {Promise<{key: string, status: string, manifestPath: string}>}
     */
    resolve(req, options = {}) {
        const key = spriteRequestKey(req);
        let p = this._cache.get(key);
        if (!p) {
            p = this._client.request('getSprite', req, options).then(data => {
                if (typeof data?.manifestPath !== 'string' || data.manifestPath.length === 0)
                    throw new Error('getSprite returned no path');
                return data;
            });
            p.catch(() => {
                if (this._cache.get(key) === p)
                    this._cache.delete(key);
            });
            this._cache.set(key, p);
        }
        return p;
    }

    forget(req) {
        this._cache.delete(spriteRequestKey(req));
    }

    clear() {
        this._cache.clear();
    }
}

/** A static PNG for icons: the path itself, or frame-000.png next to a manifest. */
export function staticPngPath(manifestPath) {
    if (typeof manifestPath !== 'string')
        return null;
    if (manifestPath.endsWith('.png'))
        return manifestPath;
    const slash = manifestPath.lastIndexOf('/');
    return slash >= 0 ? `${manifestPath.slice(0, slash)}/frame-000.png` : null;
}
