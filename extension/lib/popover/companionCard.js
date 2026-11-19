/* Companion card (ui.md §2.1 A): sprite tile, name, rarity and type badges,
 * stage and nature, growth bar with hover detail, status line, evolution line.
 */

import Clutter from 'gi://Clutter';
import Gio from 'gi://Gio';
import St from 'gi://St';

import {_, fmt} from '../i18n.js';
import {compact} from '../format.js';
import {LimitBar} from '../limitBar.js';
import {SpriteAnimator} from '../spriteAnimator.js';
import {spriteRequestKey, staticPngPath} from '../sprites.js';
import * as V from '../viewModel.js';
import {eggGuaranteeText} from '../shop/shopModel.js';
import {playSparkle, playXpCapsule} from '../shop/feedbackFx.js';
import {hbox, label, vbox, wrapLabel} from './widgets.js';

/* The 76 px tile holds a 64 px sprite box; the popover animates at native timing. */
const TILE_SPRITE_BOX = 64;
const EVO_ICON_SIZE = 34;

/* Type chart colors (ui.md §6, Badge.swift:7-42); unknown types are gray. */
const TYPE_COLORS = {
    normal: '#AAAA99', fire: '#FF4422', water: '#3399FF', electric: '#FFCC33', grass: '#77CC55',
    ice: '#66CCFF', fighting: '#BB5544', poison: '#AA5599', ground: '#DDBB55', flying: '#8899FF',
    psychic: '#FF5599', bug: '#AABB22', rock: '#BBAA66', ghost: '#6666BB', dragon: '#7766EE',
    dark: '#775544', steel: '#AAAABB', fairy: '#EE99EE',
};
const RARITIES = ['common', 'uncommon', 'rare', 'legendary'];

function clickGesture(cb) {
    const gesture = new Clutter.ClickGesture();
    gesture.connect('recognize', () => cb());
    return gesture;
}

export class CompanionCard {
    /**
     * @param {object} deps  {tooltip, sprites: SpriteResolver, openDexEntry(speciesId, form)}
     */
    constructor(deps) {
        this._deps = deps;
        this._spriteKey = null;
        this._retried = new Set();
        this._evoSignature = null;

        this.actor = vbox('ptb-companion', {x_expand: true});

        const row = hbox('ptb-companion-row', {x_expand: true});
        this.actor.add_child(row);

        this._tile = new St.Bin({style_class: 'ptb-sprite-tile', y_align: Clutter.ActorAlign.START});
        this._sprite = new SpriteAnimator({
            boxHeight: TILE_SPRITE_BOX,
            maxWidth: TILE_SPRITE_BOX,
            floorMs: 0,
            fixedWidth: true,
            allowUpscale: true,
        });
        this._sprite.onLoadFailed = () => this._onSpriteFailed();
        this._tile.set_child(this._sprite);
        // The tile sits under a layer for the Bag feedback effects (bag-shop.md §2.5).
        const tileStack = new St.Widget({layout_manager: new Clutter.BinLayout(), y_align: Clutter.ActorAlign.START});
        tileStack.add_child(this._tile);
        this._fxLayer = new St.Widget({x_expand: true, y_expand: true});
        tileStack.add_child(this._fxLayer);
        row.add_child(tileStack);
        // The hatched companion links to its Pokédex detail (ui.md §2.1 A, DexEntryLink).
        this._link = null; // {speciesId, form} while hatched
        deps.tooltip.attach(this._tile, () => (this._link ? _('Open in the Pokédex') : null));
        this._tile.add_action(clickGesture(() => {
            if (this._link)
                deps.openDexEntry?.(this._link.speciesId, this._link.form);
        }));

        const info = vbox('ptb-companion-info', {x_expand: true, y_align: Clutter.ActorAlign.CENTER});
        row.add_child(info);

        const nameRow = hbox('ptb-name-row');
        this._name = label('', 'ptb-companion-name');
        this._shiny = label('✨', 'ptb-shiny');
        this._rarity = label('', 'ptb-badge');
        nameRow.add_child(this._name);
        nameRow.add_child(this._shiny);
        nameRow.add_child(this._rarity);
        info.add_child(nameRow);

        this._types = hbox('ptb-type-row');
        info.add_child(this._types);

        const stageRow = hbox('ptb-stage-row');
        this._stage = label('', 'ptb-secondary ptb-small');
        this._boost = label('', 'ptb-badge ptb-boost-badge');
        stageRow.add_child(this._stage);
        stageRow.add_child(this._boost);
        info.add_child(stageRow);

        this._bar = new LimitBar({style_class: 'ptb-limit-bar ptb-growth-bar'});
        this._barDetail = '';
        deps.tooltip.attach(this._bar, () => this._barDetail);
        info.add_child(this._bar);

        this._remaining = label('', 'ptb-tertiary ptb-small');
        info.add_child(this._remaining);

        this._status = wrapLabel('', 'ptb-secondary ptb-small ptb-status');
        info.add_child(this._status);

        this._warning = wrapLabel('', 'ptb-warning-text ptb-small');
        info.add_child(this._warning);

        this._evo = hbox('ptb-evo-line', {x_align: Clutter.ActorAlign.START});
        this.actor.add_child(this._evo);

        // getSprite replies can land after disable() (pending commands fail
        // with helperExited on stop): drop the actor references with the actor.
        this.actor.connect('destroy', () => {
            this._sprite = null;
            this._spriteKey = null;
        });
    }

    /** @param {object} ctx  {state} */
    update(ctx) {
        const companion = ctx.state?.companion;
        if (!companion) {
            this._name.text = _('Token Egg');
            this._hideAllDetails();
            return;
        }
        if (companion.phase === 'active' && companion.active)
            this._updateActive(companion);
        else
            this._updateEgg(companion);
        const a = companion.phase === 'active' ? companion.active : null;
        this._link = a ? {speciesId: a.speciesId, form: a.form ?? null} : null;
        if (this._link)
            this._tile.add_style_class_name('ptb-linkable');
        else
            this._tile.remove_style_class_name('ptb-linkable');
        this._updateSprite(companion);
    }

    _hideAllDetails() {
        for (const a of [this._shiny, this._rarity, this._types, this._boost, this._warning, this._evo])
            a.hide();
        this._stage.text = '';
        this._remaining.text = '';
        this._status.text = '';
        this._bar.setValue(0);
    }

    _setRarity(rarity) {
        const r = RARITIES.includes(rarity) ? rarity : 'common';
        for (const other of RARITIES) {
            this._tile.remove_style_class_name(`ptb-rarity-${other}`);
            this._rarity.remove_style_class_name(`ptb-rarity-${other}`);
        }
        this._tile.add_style_class_name(`ptb-rarity-${r}`);
        this._rarity.add_style_class_name(`ptb-rarity-${r}`);
        this._rarity.text = V.rarityName(r);
        this._rarity.show();
    }

    _updateActive(companion) {
        const a = companion.active;
        this._name.text = a.displayName ?? `#${a.speciesId}`;
        this._shiny.visible = Boolean(a.isShiny);
        this._setRarity(a.rarity);

        this._types.destroy_all_children();
        for (const t of a.types ?? []) {
            const badge = label(t.name ?? t.key, 'ptb-badge ptb-type-badge');
            badge.style = `background-color: ${TYPE_COLORS[t.key] ?? '#999999'};`;
            this._types.add_child(badge);
        }
        this._types.visible = (a.types ?? []).length > 0;

        this._stage.text = V.stageText(a);
        this._stage.remove_style_class_name('ptb-orange-text');
        this._boost.text = fmt(_('%d× growth'), 2);
        this._boost.visible = Boolean(a.growthBoost);

        const frac = a.threshold > 0 ? a.usedAtStage / a.threshold : 0;
        this._bar.setValue(frac);
        this._barDetail = V.progressDetail(a.usedAtStage, a.threshold);
        this._remaining.text = V.remainingText(a.usedAtStage, a.threshold, a.isFinalStage ? 'graduation' : 'evolve');

        this._status.text = V.moodText(companion);
        if (companion.justGraduated) {
            this._warning.text = fmt(_('%s graduated — saved to your Pokédex. A new egg has arrived!'),
                companion.justGraduated);
            this._warning.show();
        } else {
            this._warning.hide();
        }
        this._updateEvoLine(a);
    }

    _updateEgg(companion) {
        const egg = companion.egg ?? {used: 0, threshold: 0};
        this._name.text = _('Token Egg');
        this._shiny.hide();
        this._setGuarantee(egg.guarantee);
        this._types.hide();
        this._boost.hide();
        for (const r of RARITIES)
            this._tile.remove_style_class_name(`ptb-rarity-${r}`);
        this._tile.add_style_class_name('ptb-rarity-common');

        this._stage.text = egg.imminent ? _('About to hatch!') : _('Incubating');
        if (egg.imminent)
            this._stage.add_style_class_name('ptb-orange-text');
        else
            this._stage.remove_style_class_name('ptb-orange-text');

        this._bar.setValue(egg.threshold > 0 ? egg.used / egg.threshold : 0);
        this._barDetail = V.progressDetail(egg.used, egg.threshold);
        this._remaining.text = V.remainingText(egg.used, egg.threshold, 'egg');

        if (!(egg.used > 0)) {
            this._status.text = fmt(_('Grows from your local AI coding usage. Your egg hatches after ~%s tokens.'),
                compact(egg.threshold));
        } else {
            this._status.text = V.moodText(companion);
        }

        if (egg.hatchDelayed) {
            this._warning.text = _('Hatching is delayed — retrying on the next refresh.');
            this._warning.show();
        } else if (companion.justGraduated) {
            this._warning.text = fmt(_('%s graduated — saved to your Pokédex. A new egg has arrived!'),
                companion.justGraduated);
            this._warning.show();
        } else {
            this._warning.hide();
        }
        this._evo.hide();
        this._evoSignature = null;
    }

    /** A premium egg's floor, "Rare or better", in the rarity color (bag-shop.md §1.8). */
    _setGuarantee(tier) {
        const text = eggGuaranteeText(tier);
        if (!text) {
            this._rarity.hide();
            return;
        }
        for (const other of RARITIES)
            this._rarity.remove_style_class_name(`ptb-rarity-${other}`);
        this._rarity.add_style_class_name(`ptb-rarity-${tier}`);
        this._rarity.text = text;
        this._rarity.show();
    }

    /** "+XP" capsule after a Rare Candy use (bag-shop.md §2.5). */
    playXp(xp) {
        playXpCapsule(this._fxLayer, xp);
    }

    /** Sparkle burst after a Mint use. */
    playSparkle() {
        playSparkle(this._fxLayer);
    }

    /* ---- sprites ------------------------------------------------------ */

    _cardRequest(companion) {
        if (companion.phase !== 'active' || !companion.active)
            return {egg: true};
        const a = companion.active;
        return {
            speciesId: a.speciesId,
            form: a.form ?? null,
            shiny: Boolean(a.isShiny),
            back: Boolean(companion.menuSubject?.back),
            animated: true,
        };
    }

    _updateSprite(companion) {
        const req = this._cardRequest(companion);
        const key = spriteRequestKey(req);
        if (key === this._spriteKey)
            return;
        this._spriteKey = key;
        this._spriteReq = req;
        this._resolveCardSprite(req, key);
    }

    _resolveCardSprite(req, key) {
        this._deps.sprites.resolve(req).then(data => {
            if (this._spriteKey === key && this._sprite)
                this._sprite.setSource({path: data.manifestPath});
        }).catch(() => {
            if (this._spriteKey === key && this._sprite)
                this._sprite.setSource(null);
        });
    }

    _onSpriteFailed() {
        // The cache was cleared under us: ask the helper once more (protocol §8.2).
        const key = this._spriteKey;
        if (!key || !this._sprite || this._retried.has(key))
            return;
        this._retried.add(key);
        this._deps.sprites.forget(this._spriteReq);
        this._sprite.setSource(null);
        this._resolveCardSprite(this._spriteReq, key);
    }

    _updateEvoLine(active) {
        const nodes = active.evoLine ?? [];
        const signature = JSON.stringify([nodes, active.isShiny]);
        if (signature === this._evoSignature)
            return;
        this._evoSignature = signature;
        this._evo.destroy_all_children();
        this._evo.visible = nodes.length > 0;
        nodes.forEach((node, i) => {
            if (i > 0)
                this._evo.add_child(label('›', 'ptb-evo-arrow ptb-tertiary'));
            this._evo.add_child(this._evoNode(node, active.isShiny));
        });
    }

    _evoNode(node, shiny) {
        const cell = new St.Bin({
            style_class: 'ptb-evo-node',
            width: EVO_ICON_SIZE + 6,
            height: EVO_ICON_SIZE + 6,
        });
        if (node.current)
            cell.add_style_class_name('ptb-evo-current');
        if (!node.reached)
            cell.opacity = 115; // St CSS has no opacity: dim the stages still ahead here
        if (node.speciesId === null || node.speciesId === undefined) {
            cell.set_child(label('?', 'ptb-evo-mystery', {x_align: Clutter.ActorAlign.CENTER}));
            this._deps.tooltip.attach(cell, _('Next form not decided yet'));
            return cell;
        }
        const icon = new St.Icon({icon_size: EVO_ICON_SIZE, style_class: 'ptb-evo-icon'});
        cell.set_child(icon);
        this._deps.tooltip.attach(cell, node.displayName ?? `#${node.speciesId}`);
        if (node.reached) {
            // Reached stages are in the Pokédex: link them (EvoLineView dexLinks).
            cell.add_style_class_name('ptb-linkable');
            cell.add_action(clickGesture(() => this._deps.openDexEntry?.(node.speciesId, node.form ?? null)));
        }
        const req = {speciesId: node.speciesId, form: node.form ?? null, shiny: Boolean(shiny), back: false, animated: false};
        this._deps.sprites.resolve(req).then(data => {
            const png = staticPngPath(data.manifestPath);
            if (png && !icon._ptbDestroyed)
                icon.gicon = Gio.FileIcon.new(Gio.File.new_for_path(png));
        }).catch(() => {});
        icon.connect('destroy', () => {
            icon._ptbDestroyed = true;
        });
        return cell;
    }

    destroy() {
        this.actor.destroy();
    }
}
