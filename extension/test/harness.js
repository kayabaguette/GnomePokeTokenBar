// Minimal test harness for `gjs -m extension/test/<name>.test.js`.
// A failing assertion makes the process exit 1, which is what `make test` checks.

import System from 'system';

const tests = [];

export function test(name, fn) {
    tests.push({name, fn});
}

function show(v) {
    try {
        return JSON.stringify(v);
    } catch {
        return String(v);
    }
}

function fail(message) {
    throw new Error(message);
}

export const assert = {
    ok(value, message = 'expected truthy') {
        if (!value)
            fail(`${message} (got ${show(value)})`);
    },
    equal(actual, expected, message = '') {
        if (actual !== expected)
            fail(`${message} expected ${show(expected)}, got ${show(actual)}`.trim());
    },
    deepEqual(actual, expected, message = '') {
        if (show(actual) !== show(expected))
            fail(`${message} expected ${show(expected)}, got ${show(actual)}`.trim());
    },
    close(actual, expected, eps = 1e-9, message = '') {
        if (!(Math.abs(actual - expected) <= eps))
            fail(`${message} expected ≈${expected}, got ${actual}`.trim());
    },
    throws(fn, message = 'expected an exception') {
        try {
            fn();
        } catch {
            return;
        }
        fail(message);
    },
};

/** Run every registered test; exit 1 on any failure. */
export function run(file = '') {
    let failed = 0;
    for (const {name, fn} of tests) {
        try {
            fn();
            print(`  ok   ${name}`);
        } catch (e) {
            failed++;
            print(`  FAIL ${name}\n       ${e.message}`);
        }
    }
    print(`${file}: ${tests.length - failed}/${tests.length} passed`);
    if (failed > 0)
        System.exit(1);
}
