/* Frame-rate cap by decimation (from Spike A), ported from the macOS app
 * (SpriteAnimation.swift, GIFDecoder.capFrameRate).
 *
 * This module has no gi:// imports so it can be tested outside gnome-shell.
 */

/**
 * Lower the frame rate while keeping the playback speed: frames are dropped,
 * not held longer. Delays accumulate; once a group reaches `floorSec` the
 * group's first frame is emitted with the accumulated delay. A short tail is
 * added to the last emitted frame, so one loop keeps its original length.
 *
 * @template T
 * @param {{frame: T, delaySec: number}[]} frames
 * @param {number} floorSec minimum delay per emitted frame; 0 = native timing
 * @returns {{frame: T, delaySec: number}[]}
 */
export function capFrameRate(frames, floorSec) {
    if (!(floorSec > 0) || frames.length <= 1)
        return frames;

    const out = [];
    let held = null; // first frame of the current group
    let acc = 0; // accumulated delay of the current group
    for (const f of frames) {
        if (held === null)
            held = f.frame;
        acc += f.delaySec;
        // Epsilon: 0.05 * 4 accumulates to 0.20000000000000004 or
        // 0.19999999999999998; without it a group can slip by one frame.
        if (acc + 1e-9 >= floorSec) {
            out.push({frame: held, delaySec: acc});
            held = null;
            acc = 0;
        }
    }
    // A tail shorter than the floor joins the last emitted frame, which keeps
    // the total loop length (playback speed).
    if (held !== null && acc > 0) {
        if (out.length === 0)
            out.push({frame: held, delaySec: acc});
        else
            out[out.length - 1].delaySec += acc;
    }
    return out;
}
