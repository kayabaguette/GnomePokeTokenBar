/* Small St constructors shared by the popover sections. */

import Clutter from 'gi://Clutter';
import Pango from 'gi://Pango';
import St from 'gi://St';

export function label(text = '', styleClass = '', params = {}) {
    const l = new St.Label({text, style_class: styleClass, y_align: Clutter.ActorAlign.CENTER, ...params});
    l.clutter_text.ellipsize = Pango.EllipsizeMode.END;
    return l;
}

/** A label that wraps instead of ellipsizing (hints, error text). */
export function wrapLabel(text = '', styleClass = '', params = {}) {
    const l = new St.Label({text, style_class: styleClass, x_expand: true, ...params});
    l.clutter_text.line_wrap = true;
    l.clutter_text.line_wrap_mode = Pango.WrapMode.WORD_CHAR;
    l.clutter_text.ellipsize = Pango.EllipsizeMode.NONE;
    return l;
}

export function hbox(styleClass = '', params = {}) {
    return new St.BoxLayout({style_class: styleClass, ...params});
}

export function vbox(styleClass = '', params = {}) {
    return new St.BoxLayout({style_class: styleClass, orientation: Clutter.Orientation.VERTICAL, ...params});
}

export function spacer() {
    return new St.Widget({x_expand: true});
}

export function iconButton(iconName, styleClass = 'ptb-icon-button', accessibleName = '') {
    return new St.Button({
        style_class: styleClass,
        child: new St.Icon({icon_name: iconName, style_class: 'ptb-icon'}),
        can_focus: true,
        accessible_name: accessibleName,
        y_align: Clutter.ActorAlign.CENTER,
    });
}

export function textButton(text, styleClass = 'ptb-text-button') {
    return new St.Button({
        style_class: styleClass,
        label: text,
        can_focus: true,
        y_align: Clutter.ActorAlign.CENTER,
    });
}

/** Swap the single child of a container (rebuilds rows that change shape). */
export function replaceChildren(container, children) {
    container.destroy_all_children();
    for (const c of children)
        container.add_child(c);
}
