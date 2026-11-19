/* The agent activity D-Bus object (protocol §15).
 *
 * Owns `io.github.poketokenbar.Agent` on the session bus and exports
 * `io.github.poketokenbar.Agent1` at `/io/github/poketokenbar/Agent` while the
 * extension is enabled. Both methods are fire-and-forget: they return an empty
 * reply at once and never throw, whatever the caller sends. There is no
 * service file, so nothing is D-Bus-activated while the extension is off.
 *
 * Never imported by the unit tests: they run on the host and must not touch
 * the live session bus.
 */

import Gio from 'gi://Gio';

import {eventFromReport, eventFromUrl} from './agentActivity.js';

export const BUS_NAME = 'io.github.poketokenbar.Agent';
export const OBJECT_PATH = '/io/github/poketokenbar/Agent';
export const INTERFACE_XML = `
<node>
  <interface name="io.github.poketokenbar.Agent1">
    <method name="Report">
      <arg type="s" name="state" direction="in"/>
      <arg type="s" name="label" direction="in"/>
      <arg type="s" name="message" direction="in"/>
    </method>
    <method name="OpenUrl">
      <arg type="s" name="url" direction="in"/>
    </method>
  </interface>
</node>`;

const LOG = 'PokeTokenBar';

export class AgentBus {
    /** @param {(event: object) => void} onEvent  called with each parsed event */
    constructor(onEvent) {
        this._onEvent = onEvent;
        this._exported = Gio.DBusExportedObject.wrapJSObject(INTERFACE_XML, {
            Report: (state, label, message) => this._deliver(() => eventFromReport(state, label, message)),
            OpenUrl: url => this._deliver(() => eventFromUrl(url)),
        });
        this._ownerId = 0;
        try {
            this._exported.export(Gio.DBus.session, OBJECT_PATH);
            this._ownerId = Gio.bus_own_name_on_connection(Gio.DBus.session, BUS_NAME,
                Gio.BusNameOwnerFlags.NONE, null,
                () => console.warn(`${LOG}: lost or could not own ${BUS_NAME}; agent reports are off`));
        } catch (e) {
            console.warn(`${LOG}: cannot export the agent D-Bus object: ${e.message}`);
        }
    }

    _deliver(parse) {
        try {
            const event = parse();
            if (event)
                this._onEvent?.(event);
        } catch (e) {
            console.warn(`${LOG}: dropped an agent report: ${e.message}`);
        }
    }

    destroy() {
        this._onEvent = null;
        if (this._ownerId) {
            Gio.bus_unown_name(this._ownerId);
            this._ownerId = 0;
        }
        try {
            this._exported?.unexport();
        } catch {
            // never exported
        }
        this._exported = null;
    }
}
