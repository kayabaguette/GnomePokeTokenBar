import Clutter from 'gi://Clutter';
import GObject from 'gi://GObject';
import St from 'gi://St';

import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import * as PanelMenu from 'resource:///org/gnome/shell/ui/panelMenu.js';
import {Extension} from 'resource:///org/gnome/shell/extensions/extension.js';

import {HelperClient} from './lib/helperClient.js';

const Indicator = GObject.registerClass(
class PtbIndicator extends PanelMenu.Button {
    _init() {
        super._init(0.0, 'PokeTokenBar', false);
        this._label = new St.Label({
            text: 'PTB…',
            style_class: 'ptb-label',
            y_align: Clutter.ActorAlign.CENTER,
        });
        this.add_child(this._label);
    }

    setText(text) {
        this._label.text = text;
    }
});

export default class PokeTokenBarExtension extends Extension {
    enable() {
        this._indicator = new Indicator();
        Main.panel.addToStatusArea(this.uuid, this._indicator);

        this._client = new HelperClient(this.path);
        this._client.onMessage = msg => {
            if (msg.type === 'hello')
                this._indicator?.setText(`PTB ${msg.version}`);
        };
        this._client.start();
    }

    disable() {
        this._client?.stop();
        this._client = null;
        this._indicator?.destroy();
        this._indicator = null;
    }
}
