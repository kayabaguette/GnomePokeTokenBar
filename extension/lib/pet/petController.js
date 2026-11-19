/* Floating pet state machine (floating-pet.md §5.1 item 3): agent reactions,
 * the run to the cursor and back, the limit bubble, hearts and the event
 * callout. Upstream FloatingPetController (FPP:389-513, :592-692, :824-843).
 *
 * Pure module (no gi:// imports): the St view (lib/pet/floatingPet.js)
 * injects time, timers, geometry and side effects, so the Swift wiring tests
 * run under plain `gjs -m` with fake timers.
 */

import {AgentActivityTracker} from './agentActivity.js';
import * as M from './petModel.js';

/**
 * @typedef {object} PetDeps
 * @property {() => number} now                      seconds
 * @property {(fn: Function, ms: number) => any} setTimer
 * @property {(id: any) => void} clearTimer
 * @property {() => void} openPopover
 * @property {(origin: {x, y}) => void} persistHome  pet bottom-left
 * @property {() => {x, y}} home                     the resolved home
 * @property {() => number} petSize                stage px
 * @property {() => number} [scale]                  stage px per logical px (default 1)
 * @property {() => boolean} motionAllowed
 * @property {() => boolean} agentEnabled            floating-pet-enabled && agent-activity
 * @property {() => boolean} bubbleAlertsEnabled     floating-pet-enabled && bubble-alerts
 * @property {() => boolean} visible
 * @property {() => {x, y}|null} cursor
 * @property {(p: {x, y}) => {x, y, width, height}|null} workAreaAt
 * @property {(petOrigin: {x, y}, done: () => void) => void} animate
 * @property {() => void} stopAnimation
 * @property {() => void} changed                    re-render bubble, hearts, bob, position
 */
export class PetController {
    /** @param {PetDeps} deps */
    constructor(deps) {
        this._d = deps;
        this.tracker = new AgentActivityTracker();
        /** @type {'home'|'away'|'returning'} */
        this.runPhase = 'home';
        this.awayPetOrigin = null;
        this.runGeneration = 0;
        this._agentTimer = null;
        this.agentExpiryFireTime = null;

        this.limitAlert = null;
        this._alertShownAt = 0;
        this._alertBatch = [];
        this._batchTimer = null;
        this._ttlTimer = null;

        this.hearts = false;
        this._heartTimer = null;

        this.eventText = null;
        this._eventTimer = null;
        this._eventSeq = 0;
        this._lastSeenSeq = 0;
    }

    /* ---- derived state ------------------------------------------------- */

    get summary() {
        return this.tracker.summary;
    }

    get bubble() {
        return M.bubble(this.limitAlert, this.summary);
    }

    /** Hearts draw only while no speech bubble is up. */
    get heartsVisible() {
        return this.hearts && this.bubble === null;
    }

    get showingBubble() {
        return M.isShowingBubble(this.limitAlert !== null, this.hearts, this.summary);
    }

    get isBobbing() {
        return M.shouldBob(this.summary.working, this._d.motionAllowed());
    }

    /** Where the pet stands: next to the cursor while away, else home. */
    get petOrigin() {
        return this.runPhase === 'away' && this.awayPetOrigin ? this.awayPetOrigin : this._d.home();
    }

    /* ---- agent activity (FPP:389-447) ----------------------------------- */

    handleAgentEvent(event) {
        if (!event || !this._d.agentEnabled())
            return;
        this.tracker.apply(event, this._d.now());
        this._agentDidChange();
    }

    _agentDidChange() {
        const summary = this.summary;
        this._scheduleAgentExpiry();
        if (summary.done) {
            if (M.runsToCursor(this.runPhase, this._d.motionAllowed(), this._d.visible()))
                this._runToCursor();
            else
                this._d.changed();
        } else {
            this._returnHome();
        }
    }

    _scheduleAgentExpiry() {
        this._clear('_agentTimer');
        this.agentExpiryFireTime = null;
        const deadline = this.tracker.deadline;
        if (deadline === null)
            return;
        this.agentExpiryFireTime = deadline;
        const ms = Math.max(0, (deadline - this._d.now()) * 1000);
        this._agentTimer = this._d.setTimer(() => {
            this._agentTimer = null;
            this.expireAgentActivity(this._d.now());
        }, ms);
    }

    /** Timer callback; a fire before the deadline re-arms instead of being lost. */
    expireAgentActivity(now) {
        this._clear('_agentTimer');
        this.agentExpiryFireTime = null;
        if (this.tracker.expire(now))
            this._agentDidChange();
        else
            this._scheduleAgentExpiry();
    }

    _resetAgent() {
        this.tracker.reset();
        this._clear('_agentTimer');
        this.agentExpiryFireTime = null;
        if (this.runPhase !== 'home')
            this._d.stopAnimation();
        this.runPhase = 'home';
        this.runGeneration++;
    }

    /** Settings or visibility changed: turning the agent setting off resets everything. */
    sync() {
        if (!this._d.agentEnabled() && (this.tracker.agents.size > 0 || this.runPhase !== 'home'))
            this._resetAgent();
        if (!this._d.bubbleAlertsEnabled())
            this._clearAlert();
        this._d.changed();
    }

    /** Leave home for a spot next to the cursor; returns the panel and pet origins. */
    beginRun(cursor, workArea) {
        const size = this._d.petSize();
        const scale = this._d.scale?.() ?? 1;
        const panel = M.panelSize(size, true, scale);
        const origin = M.runPanelOrigin(cursor, panel, workArea, M.RUN_CURSOR_GAP * scale);
        this.runPhase = 'away';
        this.awayPetOrigin = M.petOrigin(origin, size, panel);
        return {panelOrigin: origin, petOrigin: this.awayPetOrigin};
    }

    _runToCursor() {
        const cursor = this._d.cursor();
        const wa = cursor ? this._d.workAreaAt(cursor) : null;
        if (!wa) {
            this._d.changed();
            return;
        }
        const run = this.beginRun(cursor, wa);
        this._d.changed();
        this._animate(run.petOrigin, () => {});
    }

    _returnHome() {
        if (this.runPhase !== 'away' || !this._d.visible() || !this._d.motionAllowed()) {
            if (this.runPhase === 'away') {
                this._d.stopAnimation();
                this.runPhase = 'home';
                this.runGeneration++;
            }
            this._d.changed();
            return;
        }
        this.runPhase = 'returning';
        this._d.changed();
        this._animate(this._d.home(), () => {
            if (this.runPhase !== 'returning')
                return;
            this.runPhase = 'home';
            this._d.changed();
        });
    }

    _animate(target, completion) {
        this.runGeneration++;
        const generation = this.runGeneration;
        this._d.animate(target, () => {
            if (this.runGeneration === generation)
                completion();
        });
    }

    /**
     * The pet was moved. A user drag (the only move the view reports) becomes
     * the new home and cancels a run; run frames never persist (FPP:817-843).
     */
    petMoved(origin, userDragging) {
        const r = M.resolveMove(this.runPhase, userDragging);
        if (userDragging && this.runPhase !== 'home') {
            this.runGeneration++;
            this._d.stopAnimation();
        }
        this.runPhase = r.phase;
        if (r.persist)
            this._d.persistHome(origin);
    }

    /* ---- clicks (FPP:504-513) ------------------------------------------- */

    /** A primary click answers waiting agents, then opens the popover (eggs too). */
    petClicked(subjectIsEgg) {
        if (this.tracker.dismissBubble())
            this._agentDidChange();
        if (M.clickAction(subjectIsEgg) === 'openPopover')
            this._d.openPopover();
    }

    /** The context menu's Open always opens. */
    openFromMenu() {
        if (this.tracker.dismissBubble())
            this._agentDidChange();
        this._d.openPopover();
    }

    /* ---- limit bubble (US:2105-2179) ------------------------------------ */

    /** One `limitAlert` event; alerts of one pass are batched for 250 ms. */
    handleLimitAlert(event) {
        if (!this._d.bubbleAlertsEnabled())
            return;
        this._alertBatch.push(event);
        if (this._batchTimer !== null)
            return;
        this._batchTimer = this._d.setTimer(() => {
            this._batchTimer = null;
            this._flushAlerts();
        }, M.ALERT_BATCH_MS);
    }

    _flushAlerts() {
        const alert = M.pickBubbleAlert(this._alertBatch);
        this._alertBatch = [];
        if (!alert || !this._d.bubbleAlertsEnabled())
            return;
        const shownAt = this._d.now() * 1000;
        this.limitAlert = alert;
        this._alertShownAt = shownAt;
        // A newer alert replaces this one and restarts the TTL (US:2268-2272).
        const check = () => {
            this._ttlTimer = null;
            if (this._alertShownAt !== shownAt)
                return;
            const now = this._d.now() * 1000;
            if (M.shouldDismissBubble(shownAt, now)) {
                this.limitAlert = null;
                this._d.changed();
            } else {
                this._ttlTimer = this._d.setTimer(check, shownAt + M.BUBBLE_TTL_MS - now);
            }
        };
        this._clear('_ttlTimer');
        this._ttlTimer = this._d.setTimer(check, M.BUBBLE_TTL_MS);
        this._d.changed();
    }

    _clearAlert() {
        this._clear('_batchTimer');
        this._clear('_ttlTimer');
        this._alertBatch = [];
        this.limitAlert = null;
    }

    /* ---- hearts (FPP:642-657) ------------------------------------------- */

    stroked() {
        this.hearts = M.heartsShowing(this.limitAlert !== null, this.summary);
        if (!this.hearts)
            return;
        this._clear('_heartTimer');
        this._heartTimer = this._d.setTimer(() => {
            this._heartTimer = null;
            this.hearts = false;
            this._d.changed();
        }, M.HEART_DURATION_MS);
        this._d.changed();
    }

    /* ---- event callout (FPP:659-677) ------------------------------------ */

    /** A hatch, evolve, graduate or badge line pops the callout once, for 6 s. */
    handleCompanionEvent(text) {
        if (!text)
            return;
        this._eventSeq++;
        const pop = M.shouldPopEvent(this._eventSeq, this._lastSeenSeq, this._d.visible(), this.showingBubble);
        this._lastSeenSeq = this._eventSeq; // seen either way: a missed event is not queued
        if (!pop)
            return;
        this.eventText = text;
        this._clear('_eventTimer');
        this._eventTimer = this._d.setTimer(() => {
            this._eventTimer = null;
            this.eventText = null;
            this._d.changed();
        }, M.EVENT_POP_MS);
        this._d.changed();
    }

    /* ---- lifecycle ------------------------------------------------------ */

    /** The view hid the pet: drop transient effects; it comes back at home (FPP:679-692). */
    hidden() {
        this._clear('_eventTimer');
        this.eventText = null;
        this._clear('_heartTimer');
        this.hearts = false;
        if (this.runPhase !== 'home')
            this._d.stopAnimation();
        this.runPhase = 'home';
        this.runGeneration++;
    }

    destroy() {
        for (const k of ['_agentTimer', '_batchTimer', '_ttlTimer', '_heartTimer', '_eventTimer'])
            this._clear(k);
        this.runGeneration++;
    }

    _clear(key) {
        if (this[key] !== null) {
            this._d.clearTimer(this[key]);
            this[key] = null;
        }
    }
}
