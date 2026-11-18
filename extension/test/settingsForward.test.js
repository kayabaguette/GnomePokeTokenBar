// gjs -m extension/test/settingsForward.test.js
import {assert, run, test} from './harness.js';
import * as S from '../lib/settingsForward.js';

test('language from GLib.get_language_names()', () => {
    assert.equal(S.languageFromNames(['fr_FR.UTF-8', 'fr_FR', 'fr', 'C']), 'fr-fr');
    assert.equal(S.languageFromNames(['de_DE@euro', 'de']), 'de-de');
    assert.equal(S.languageFromNames(['ja', 'C']), 'ja');
    assert.equal(S.languageFromNames(['C']), 'en');
    assert.equal(S.languageFromNames(['POSIX']), 'en');
    assert.equal(S.languageFromNames(['C.UTF-8']), 'en');
    assert.equal(S.languageFromNames([]), 'en');
    assert.equal(S.languageFromNames(['pt_BR.utf8']), 'pt-br');
});

test('explicit week starts', () => {
    assert.equal(S.resolveWeekStart('monday'), 1);
    assert.equal(S.resolveWeekStart('sunday'), 7);
    assert.equal(S.resolveWeekStart('saturday'), 6);
});

test('locale week start: each source alone, in priority order', () => {
    assert.equal(S.resolveWeekStart('locale', {}), 1, 'nothing known → Monday');
    assert.equal(S.resolveWeekStart('locale', {intlDay: 7}), 7);
    assert.equal(S.resolveWeekStart('locale', {desktopDay: 6}), 6);
    assert.equal(S.resolveWeekStart('locale', {shellWeekStart: 0}), 7, 'shell 0 = Sunday → ISO 7');
    assert.equal(S.resolveWeekStart('locale', {shellWeekStart: 1}), 1);
    assert.equal(S.resolveWeekStart('locale', {intlDay: 1, desktopDay: 7, shellWeekStart: 0}), 1, 'Intl wins');
    assert.equal(S.resolveWeekStart('locale', {desktopDay: 7, shellWeekStart: 1}), 7, 'desktop setting beats locale');
    assert.equal(S.resolveWeekStart('locale', {intlDay: 9, desktopDay: 0, shellWeekStart: 9}), 1, 'out of range ignored');
    assert.equal(S.resolveWeekStart('bogus', {shellWeekStart: 0}), 7, 'unknown value treated as locale');
});

test('intlFirstDay tolerates missing APIs', () => {
    assert.equal(S.intlFirstDay('en-US'), null, 'GJS 1.90 has neither getWeekInfo nor weekInfo');
    class WithMethod { getWeekInfo() { return {firstDay: 7}; } }
    class WithGetter { get weekInfo() { return {firstDay: 6}; } }
    class Throws { constructor() { throw new RangeError('bad tag'); } }
    assert.equal(S.intlFirstDay('en-US', WithMethod), 7);
    assert.equal(S.intlFirstDay('ar-EG', WithGetter), 6);
    assert.equal(S.intlFirstDay('??', Throws), null);
    assert.equal(S.intlFirstDay('en', null), null);
});

test('forwarded set has every protocol field with the right types', () => {
    const values = {
        'refresh-interval': 120, 'warn-threshold': 80, 'crit-threshold': 95,
        'growth-difficulty': 1.25, 'always-faces-back': false,
    };
    const set = S.forwardedSettings(k => values[k], 'en', 1);
    assert.deepEqual(set, {
        refreshInterval: 120, warnThreshold: 80, critThreshold: 95, growthDifficulty: 1.25,
        weekStart: 1, language: 'en', alwaysFacesBack: false,
    });
    assert.deepEqual(S.FORWARDED_KEYS.sort(), ['always-faces-back', 'crit-threshold', 'growth-difficulty',
        'refresh-interval', 'warn-threshold', 'week-start']);
});

run('settingsForward.test.js');
