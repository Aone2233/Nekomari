import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import test from 'node:test';

/**
 * 语言包按需加载（性能审计 F1）在源码层面的契约。
 *
 * 背景：@@src/i18n/config.ts@@ 之前静态 import 了 5 个语言的 JSON（合计 407 KiB raw / 125 KiB
 * gzip），而主 entry、@@/admin@@ 与 5 个 standalone 页面都 import 同一份 config，于是每一个入口
 * 都无条件带着 4 个大多数人不会用到的语言。改成只静态保留 en 之后，「别再把它们同步 import
 * 回来」和「入口必须等语言包就位再 render」这两件事都没有类型系统兜底——一个
 * @@import zh_CN from ...@@ 就会让约 100 KiB gzip 悄悄回到 entry 里，而一个忘了 await 的入口会在
 * 首帧画出一屏 key。这就是这个文件存在的理由。
 *
 * 与 @@i18n-parity.test.mjs@@ 分工：那个管「译文是否齐全」（数据），这个管「数据是怎么被加载的」
 * （打包与入口顺序）。
 */

const read = (relative) => readFileSync(new URL('../' + relative, import.meta.url), 'utf8');

const config = read('src/i18n/config.ts');

const LOCALES = readdirSync(new URL('../src/i18n/locales/', import.meta.url))
  .filter((name) => name.endsWith('.json'))
  .map((name) => name.replace(/\.json$/, ''));

/** 唯一静态 import 的语言：它是 fallbackLng，也是首访看到它的那部分用户的语言。 */
const EAGER = 'en';
const LAZY = LOCALES.filter((code) => code !== EAGER);

// import en from "./locales/en.json";
const staticLocales = [
  ...config.matchAll(/import\s+[\w$]+\s+from\s+["']\.\/locales\/([\w-]+)\.json["']/g),
].map((match) => match[1]);

// import("./locales/zh_CN.json")
const dynamicLocales = [
  ...config.matchAll(/import\(\s*["']\.\/locales\/([\w-]+)\.json["']\s*\)/g),
].map((match) => match[1]);

test('only English is imported statically', () => {
  assert.deepEqual(
    staticLocales,
    [EAGER],
    'config.ts must import only ' +
      EAGER +
      '.json statically; a static import of another locale puts its bytes back into every entry',
  );
});

test('the other locales are loaded with a dynamic import()', () => {
  assert.deepEqual(
    [...new Set(dynamicLocales)].sort(),
    [...LAZY].sort(),
    'every locale that is not imported statically must be reachable through import()',
  );

  for (const code of LAZY) {
    assert.ok(
      !staticLocales.includes(code),
      code + ' must not be a static import: that is the ~100 KiB gzip this change removed',
    );
    assert.ok(
      dynamicLocales.includes(code),
      code + ' must be loaded with import("./locales/' + code + '.json")',
    );
  }
});

test('every locale file on disk is reachable from the loader table', () => {
  // 新增语言时最容易漏掉的一步：JSON 放进 locales/ 却在 loader 表里没有入口，结果是它既不会被打
  // 包也不会被选中。反过来，表里有一个磁盘上不存在的语言会在打包阶段才失败，那条错误离这里很远。
  assert.deepEqual(
    [...new Set([...staticLocales, ...dynamicLocales])].sort(),
    [...LOCALES].sort(),
    'the locale loader table and src/i18n/locales/ must name the same set of languages',
  );
});

test('the detected language is mapped onto a supported one', () => {
  assert.match(config, /readStoredLanguage\(\)/, 'the stored choice must be read before init');
  assert.match(config, /navigator\.language/, 'the browser language is the fallback');

  // 别名表本身：规格要求的映射都在里面（大小写不敏感，键写成小写）。
  const aliases = config.match(/const LANGUAGE_ALIASES[^=]*=\s*\{([\s\S]*?)\n\};/);
  assert.ok(aliases, 'the alias table must stay a literal object so this test can read it');

  for (const [tag, code] of [
    ['zh', 'zh_CN'],
    ['zh-CN', 'zh_CN'],
    ['zh-SG', 'zh_CN'],
    ['zh-TW', 'zh_TW'],
    ['zh-HK', 'zh_TW'],
    ['zh-MO', 'zh_TW'],
    ['ja', 'ja_JP'],
    ['id', 'id_ID'],
  ]) {
    assert.match(
      aliases[1],
      new RegExp('["\']?' + tag + '["\']?\\s*:\\s*"' + code + '"', 'i'),
      tag + ' must map to ' + code,
    );
  }
});

test('init keeps English as the fallback and React as the escaper', () => {
  assert.match(config, /fallbackLng:\s*["']en["']/, 'English is the fallback locale');
  assert.match(config, /escapeValue:\s*false/, 'React handles escaping');
  assert.equal(
    (config.match(/\.init\(\{/g) ?? []).length,
    1,
    'i18next.init must have exactly one call site: a second one throws away the loaded bundles',
  );
});

test('initI18n, ensureLanguage and SUPPORTED_LANGUAGES are exported', () => {
  assert.match(config, /export function initI18n\(/, 'initI18n is the entries initialisation hook');
  assert.match(config, /export async function ensureLanguage\(/, 'the menu loads a locale before switching');
  assert.match(config, /export const SUPPORTED_LANGUAGES/, 'the menu is no longer derived from resources');
  assert.match(config, /export default i18n;/, 'the default export stays the i18next instance');
});

const ENTRIES = [
  'src/main.tsx',
  'src/entries/admin.tsx',
  'src/entries/sla.tsx',
  'src/entries/bulk.tsx',
  'src/entries/config.tsx',
  'src/entries/forecast.tsx',
  'src/entries/maintenance.tsx',
];

test('every entry waits for the active language before the first frame', () => {
  // 7 个入口全部算上：5 个 standalone 页面、/admin 和主 entry。谁漏了都等于首帧先画 key。
  assert.equal(ENTRIES.length, 7);
  for (const entry of ENTRIES) {
    const source = read(entry);
    assert.match(
      source,
      /import\s*\{[^}]*\binitI18n\b[^}]*\}\s*from\s*["'][^"']*i18n\/config["']/,
      entry + ' must import initI18n from i18n/config',
    );
    assert.match(
      source,
      /void initI18n\(\)\.then\(\(\) => \{[\s\S]*createRoot\(/,
      entry +
        ' must call createRoot inside initI18n().then(...): rendering first paints untranslated keys',
    );
    // 裸副作用 import 不再初始化任何东西，留着它只会让人以为 i18n 已经就绪。
    assert.doesNotMatch(
      source,
      /^import ["'][^"']*i18n\/config["'];?$/m,
      entry + ' must not rely on a side-effect import of i18n/config',
    );
  }
});

test('App does not initialise i18n a second time', () => {
  const app = read('src/App.tsx');
  assert.doesNotMatch(app, /initI18n\s*\(/, 'the entries own initialisation');
  assert.doesNotMatch(app, /i18next/, 'App must not touch i18next directly');
});

test('the language menu loads a bundle before switching to it', () => {
  const menu = read('src/components/Language.tsx');
  assert.match(menu, /SUPPORTED_LANGUAGES/, 'the menu items come from the constant table');
  assert.doesNotMatch(menu, /\bresources\b/, 'the menu no longer derives itself from the resources table');

  const ensureAt = menu.indexOf('await ensureLanguage(');
  const changeAt = menu.indexOf('await i18n.changeLanguage(');
  assert.ok(ensureAt > -1, 'the menu must ensure the bundle before switching');
  assert.ok(changeAt > ensureAt, 'ensureLanguage must run before changeLanguage');
  assert.match(menu, /writeLanguageCookie\(code\)/, 'the choice is persisted alongside i18next own change');
});
