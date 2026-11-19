// Build a protocol §8.2 sprite cache for sandbox runs, without the helper.
//
//   gjs -m extension/test/make-sprite-fixture.js <spike-out-dir> <dest-dir>
//
// For each Spike A output (spikes/sprite-anim/out/<id>/: manifest.json and
// composited frame-NNN.png) writes <dest>/stub-<id>/ with frames.rgba
// (non-premultiplied RGBA8888, stride width*4), frame-000.png and a
// manifest.json in the protocol shape. Also draws a static <dest>/egg.png.

import GdkPixbuf from 'gi://GdkPixbuf';
import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import System from 'system';

const [spikeOut, dest] = ARGV;
if (!spikeOut || !dest) {
    printerr('usage: make-sprite-fixture.js <spike-out-dir> <dest-dir>');
    System.exit(2);
}

function rgbaOf(pixbuf) {
    if (!pixbuf.has_alpha)
        pixbuf = pixbuf.add_alpha(false, 0, 0, 0);
    const {width: w, height: h, rowstride} = pixbuf;
    const src = pixbuf.get_pixels();
    const out = new Uint8Array(w * h * 4);
    for (let y = 0; y < h; y++)
        out.set(src.subarray(y * rowstride, y * rowstride + w * 4), y * w * 4);
    return out;
}

function writeFile(path, bytes) {
    GLib.mkdir_with_parents(GLib.path_get_dirname(path), 0o755);
    GLib.file_set_contents(path, bytes);
}

// protocol §8.2 visibleTopFraction: per frame, the first row with alpha > 16 in the central columns
// (the whole width if the band is empty), as a share of the height; the median frame wins.
function visibleTopFraction(chunks, w, h) {
    const bandStart = Math.floor(w * 0.35);
    const bandEnd = Math.min(w, Math.max(bandStart + 1, Math.floor(w * 0.65)));
    const firstRow = (rgba, from, to) => {
        for (let row = 0; row < h; row++) {
            for (let col = from; col < to; col++) {
                if (rgba[(row * w + col) * 4 + 3] > 16)
                    return row;
            }
        }
        return null;
    };
    const values = chunks.map(rgba => {
        const row = firstRow(rgba, bandStart, bandEnd) ?? firstRow(rgba, 0, w);
        return row === null ? 0 : row / h;
    }).sort((a, b) => a - b);
    return Math.round(values[Math.floor(values.length / 2)] * 10000) / 10000;
}

let built = 0;
for (const id of ['25', '6', '94']) {
    const dir = GLib.build_filenamev([spikeOut, id]);
    const manifestPath = GLib.build_filenamev([dir, 'manifest.json']);
    if (!GLib.file_test(manifestPath, GLib.FileTest.EXISTS)) {
        printerr(`skip ${id}: no ${manifestPath}`);
        continue;
    }
    const spike = JSON.parse(new TextDecoder().decode(GLib.file_get_contents(manifestPath)[1]));
    const frames = [];
    const chunks = [];
    let offset = 0;
    for (const f of spike.frames) {
        const pb = GdkPixbuf.Pixbuf.new_from_file(GLib.build_filenamev([dir, f.file]));
        if (pb.width !== spike.width || pb.height !== spike.height)
            throw new Error(`${id}/${f.file}: ${pb.width}x${pb.height} is not the canvas size`);
        const rgba = rgbaOf(pb);
        chunks.push(rgba);
        frames.push({offset, delayMs: f.delayMs});
        offset += rgba.length;
    }
    const blob = new Uint8Array(offset);
    let at = 0;
    for (const c of chunks) {
        blob.set(c, at);
        at += c.length;
    }
    const out = GLib.build_filenamev([dest, `stub-${id}`]);
    writeFile(GLib.build_filenamev([out, 'frames.rgba']), blob);
    Gio.File.new_for_path(GLib.build_filenamev([dir, 'frame-000.png'])).copy(
        Gio.File.new_for_path(GLib.build_filenamev([out, 'frame-000.png'])), Gio.FileCopyFlags.OVERWRITE, null, null);
    const manifest = {
        width: spike.width, height: spike.height, animated: frames.length > 1, rgba: 'frames.rgba', frames,
        visibleTopFraction: visibleTopFraction(chunks, spike.width, spike.height),
    };
    writeFile(GLib.build_filenamev([out, 'manifest.json']), JSON.stringify(manifest));
    print(`stub-${id}: ${frames.length} frames ${spike.width}x${spike.height}, ${blob.length} bytes`);
    built++;
}

// A small pixel egg: cream shell, green spots, dark outline.
const W = 22;
const H = 26;
const px = new Uint8Array(W * H * 4);
const set = (x, y, [r, g, b, a = 255]) => px.set([r, g, b, a], (y * W + x) * 4);
const cx = (W - 1) / 2;
const inside = (x, y) => {
    const ny = (y - 14) / (y < 14 ? 13 : 11);
    const nx = (x - cx) / 10;
    return nx * nx + ny * ny <= 1;
};
for (let y = 0; y < H; y++) {
    for (let x = 0; x < W; x++) {
        if (!inside(x, y))
            continue;
        const edge = !inside(x - 1, y) || !inside(x + 1, y) || !inside(x, y - 1) || !inside(x, y + 1);
        const spot = [[7, 9], [14, 12], [9, 18], [15, 20]].some(([sx, sy]) => (x - sx) ** 2 + (y - sy) ** 2 <= 4);
        set(x, y, edge ? [40, 40, 40] : spot ? [110, 190, 90] : [248, 244, 222]);
    }
}
GdkPixbuf.Pixbuf.new_from_bytes(new GLib.Bytes(px), GdkPixbuf.Colorspace.RGB, true, 8, W, H, W * 4)
    .savev(GLib.build_filenamev([dest, 'egg.png']), 'png', [], []);
print('egg.png drawn');

if (built === 0) {
    printerr('no sprite built: run Spike A first (spikes/sprite-anim/README.md §1)');
    System.exit(1);
}
