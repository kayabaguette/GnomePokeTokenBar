/* Prayer and Ditto-reveal suites for the sandbox driver (M3 phase 20).
 *
 * PTB_SHOT_SUITE=pet-pray, with prepare-sandbox.sh fixture state-egg.json,
 * eventsAfter -1 and PTB_STUB_EXTRA='"eggPrayers": 28':
 *   - the hover callout before (28 prayers), clicks on the egg (each confirmed
 *     from the stub's count), the 🙏 mid-flight, the quip change at 30 and the
 *     one throttled `refresh` (manual: false) that the 30th prayer sends;
 *   - with enable-animations off a click still counts, with no particle.
 *
 * PTB_SHOT_SUITE=pet-reveal, with fixture state-active.json and eventsAfter
 * 10: the popover stays open on Home until the stub's dittoReveal event (the
 * 4th line of events.ndjson), then the card flash with the 🎭 and the pet
 * callout. The stub does not swap the sprite to Ditto: only the effect is shown.
 */

import Clutter from 'gi://Clutter';
import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import St from 'gi://St';

import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import {Extension} from 'resource:///org/gnome/shell/extensions/extension.js';

const TARGET = 'poketokenbar@local';

function log(msg) {
    console.log(`ptb-shot: pray: ${msg}`);
}

async function leaveOverview(d) {
    d._movePointer(400, 300);
    for (let i = 0; i < 10 && (Main.overview.visible || Main.layoutManager.overviewGroup.visible); i++) {
        Main.overview.hide();
        await d._sleep(1000);
    }
    await d._sleep(300);
}

function hitCentre(pet) {
    const [x, y] = pet._hit.get_transformed_position();
    const [w, h] = pet._hit.get_transformed_size();
    return [Math.round(x + w / 2), Math.round(y + h / 2)];
}

function petArea(pet) {
    const [x, y] = pet._root.get_transformed_position();
    const size = pet._root.width;
    const w = 320;
    const left = Math.max(0, Math.min(1280 - w, Math.round(x + size / 2 - w / 2)));
    const top = Math.max(0, Math.round(y - 110));
    return {x: left, y: top, width: w, height: Math.min(800 - top, Math.round(size + 120))};
}

async function pointerTo(d, x, y) {
    for (let i = 0; i < 4; i++) {
        d._movePointer(x, y);
        await d._sleep(120);
        const [px, py] = global.get_pointer();
        if (Math.abs(px - x) <= 2 && Math.abs(py - y) <= 2)
            return;
    }
}

function prayers(ptb) {
    return ptb.context().state?.companion?.egg?.prayers ?? null;
}

/** One primary click on the pet, retried once if the harness drops it; true once the count moved. */
async function clickPet(d, ptb) {
    const before = prayers(ptb);
    for (let attempt = 0; attempt < 2; attempt++) {
        const t = GLib.get_monotonic_time();
        d._pointer.notify_button(t, Clutter.BUTTON_PRIMARY, Clutter.ButtonState.PRESSED);
        d._pointer.notify_button(t + 1000, Clutter.BUTTON_PRIMARY, Clutter.ButtonState.RELEASED);
        for (let i = 0; i < 10; i++) {
            await d._sleep(20);
            if (prayers(ptb) !== before)
                return true;
        }
        log(`click ${attempt + 1} did not count (still ${before}); retrying`);
    }
    return false;
}

function particles(pet) {
    return pet._prayerLayer.get_children();
}

async function setup(d) {
    await d._sleep(1000);
    d._movePointer(640, 400);
    await d._sleep(3000);
    const ptb = Extension.lookupByUUID(TARGET);
    const indicator = Main.panel.statusArea[TARGET];
    const pet = ptb?._pet;
    if (!indicator || !pet) {
        console.error('ptb-shot: PokeTokenBar pet not found');
        return null;
    }
    const settings = ptb.getSettings();
    settings.set_boolean('floating-pet-enabled', true);
    await leaveOverview(d);
    return {ptb, indicator, pet, settings};
}

export async function runPetPray(d) {
    const env = await setup(d);
    if (!env)
        return;
    const {ptb, pet} = env;
    const iface = new Gio.Settings({schema_id: 'org.gnome.desktop.interface'});
    log(`enable-animations ${St.Settings.get().enable_animations}; motion allowed ${pet._motionAllowed}; ` +
        `subject egg ${pet._subjectIsEgg}; egg prayers ${prayers(ptb)}`);

    // Count the helper commands the pet sends (the driver wraps the host's request).
    const sent = [];
    const host = pet._host;
    const request = host.request;
    host.request = (cmd, args) => {
        sent.push(`${cmd}${args ? JSON.stringify(args) : ''}`);
        return request(cmd, args);
    };

    // 1. Hover: 28 prayers.
    await pointerTo(d, ...hitCentre(pet));
    await d._sleep(400);
    log(`hover before: '${pet._callout.text}'`);
    await d._shot('pet-pray-hover-before-dark.png', petArea(pet));

    // 2. Two quick clicks (29, 30), the 🙏 mid-flight.
    const ok29 = await clickPet(d, ptb);
    await d._sleep(120);
    const ok30 = await clickPet(d, ptb);
    await d._sleep(80);
    const kids = particles(pet);
    log(`clicks counted ${ok29}/${ok30}: prayers ${prayers(ptb)}; particles ${kids.length} ` +
        `at ${kids.map(k => `${k.x},${k.y} op ${k.opacity}`).join(' | ')}; sprite angle ` +
        `${pet._sprite.rotation_angle_z.toFixed(1)}`);
    await d._shot('pet-pray-particles-dark.png', petArea(pet));
    await d._sleep(250);
    log(`+250 ms: particles ${particles(pet).length}, opacities ${particles(pet).map(k => k.opacity).join(',')}`);

    // 3. After the flight: the quip changed at 30, one refresh went out.
    await d._sleep(1200);
    log(`after flight: particles ${particles(pet).length}; sprite angle ${pet._sprite.rotation_angle_z}; ` +
        `hover '${pet._callout.text}'`);
    log(`commands sent: ${sent.join(', ')}; refresh milestone ${pet._controller.lastRefreshedMilestone}`);
    await d._shot('pet-pray-hover-after-dark.png', petArea(pet));

    // 4. Three more clicks inside the 30 s floor: no second refresh.
    for (let i = 0; i < 3; i++) {
        await clickPet(d, ptb);
        await d._sleep(90);
    }
    await d._sleep(60);
    log(`burst of three: prayers ${prayers(ptb)}; particles ${particles(pet).length}`);
    await d._shot('pet-pray-burst-dark.png', petArea(pet));
    await d._sleep(1300);
    log(`commands sent: ${sent.join(', ')}`);

    // 5. Animations off: the prayer counts, nothing moves.
    iface.set_boolean('enable-animations', false);
    await d._sleep(300);
    const before = prayers(ptb);
    const counted = await clickPet(d, ptb);
    await d._sleep(100);
    log(`animations off: counted ${counted}, prayers ${before} → ${prayers(ptb)}; particles ${particles(pet).length}; ` +
        `sprite angle ${pet._sprite.rotation_angle_z}; hover '${pet._callout.text}'`);
    iface.reset('enable-animations');
    await d._sleep(300);

    // 6. Light scheme hover.
    iface.set_string('color-scheme', 'prefer-light');
    await d._sleep(1000);
    await pointerTo(d, 400, 300);
    await pointerTo(d, ...hitCentre(pet));
    await d._sleep(400);
    await clickPet(d, ptb);
    await d._sleep(150);
    await d._shot('pet-pray-light.png', petArea(pet));
    iface.reset('color-scheme');
    host.request = request;
    await pointerTo(d, 400, 300);
    await d._sleep(1300);
    log(`final: prayers ${prayers(ptb)}; particles ${particles(pet).length}; commands ${sent.length}`);
    log('done');
}

export async function runPetReveal(d) {
    const env = await setup(d);
    if (!env)
        return;
    const {ptb, indicator, pet} = env;
    let revealAt = null;
    const onEvent = ptb._onEvent;
    ptb._onEvent = event => {
        if (event?.kind === 'dittoReveal')
            revealAt = GLib.get_monotonic_time();
        onEvent.call(ptb, event);
    };
    indicator.menu.open();
    await d._sleep(800);
    const card = indicator._popover._companion;
    const cardArea = () => {
        const [x, y] = card.actor.get_transformed_position();
        const [w, h] = card.actor.get_transformed_size();
        return {x: Math.max(0, Math.floor(x) - 12), y: Math.max(0, Math.floor(y) - 12),
            width: Math.ceil(w) + 24, height: Math.ceil(h) + 24};
    };
    for (let i = 0; i < 400 && revealAt === null; i++) {
        if (!indicator.menu.isOpen)
            indicator.menu.open();
        await d._sleep(50);
    }
    if (revealAt === null) {
        console.error('ptb-shot: no dittoReveal event');
        return;
    }
    const at = ms => d._sleep(Math.max(0, Math.round(ms - (GLib.get_monotonic_time() - revealAt) / 1000)));
    await at(300);
    const masks = () => card._fxLayer.get_children().map(c => `${c.text} op ${c.opacity} scale ${c.scale_x.toFixed(2)}`);
    log(`reveal +300 ms: tile celebrate ${card._tile.has_style_class_name('ptb-celebrate')}; fx ${masks().join(' | ')}`);
    await d._shot('reveal-card-flash-dark.png', cardArea());
    await at(800);
    log(`reveal +800 ms: tile celebrate ${card._tile.has_style_class_name('ptb-celebrate')}; fx ${masks().join(' | ')}`);
    await d._shot('reveal-card-mask-dark.png', cardArea());
    await at(1600);
    log(`reveal +1.6 s: fx ${masks().join(' | ')}; panel '${indicator.get_style_class_name()}'`);
    await d._shot('reveal-popover-dark.png', d._menuArea(indicator));
    const [lx] = card._fxLayer.get_transformed_position();
    const [tx] = card._tile.get_transformed_position();
    const mask = card._fxLayer.get_first_child();
    log(`geometry: layer x ${lx} w ${card._fxLayer.width}; tile x ${tx} w ${card._tile.width}; ` +
        `mask x ${mask?.x} w ${mask?.width}`);
    log(`pet callout: visible ${pet._callout.visible} text '${pet._callout.text}'`);
    await d._shot('reveal-pet-callout-dark.png', petArea(pet));
    await at(3400);
    log(`reveal +3.4 s: fx ${masks().length} left`);
    indicator.menu.close();
    await d._sleep(600);
    log('done');
}
