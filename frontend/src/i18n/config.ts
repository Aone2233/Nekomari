import i18next from "i18next";
import { initReactI18next } from "react-i18next";
import en from "./locales/en.json";
import {
  normalizeLanguage,
  readStoredLanguage,
  writeLanguageCookie,
} from "@/utils/language";

/**
 * i18n 的懒加载初始化（性能审计 F1）。
 *
 * English 是唯一静态 import 的语言：它是 `fallbackLng`，也是首访看到它的那部分用户的语言。
 * 另外 4 个语言的 JSON 只有 `import()` 才会下载——合计 329 KiB raw / 100 KiB gzip（5 个语言
 * 共 407 KiB raw），之前每一个入口（主 entry、admin、5 个 standalone 页面）都无条件带着它们。
 *
 * `LanguageCode` 同时是三样东西：i18next 的 language code、`locales/<code>.json` 的文件名，
 * 以及语言菜单里的取值。cookie 与 localStorage 写出去的是 BCP-47 的 `zh-CN` 形态
 * （`writeLanguageCookie` 负责规范化），`readStoredLanguage()` 再把它映射回这里的 code。
 */
export type LanguageCode = "en" | "zh_CN" | "zh_TW" | "ja_JP" | "id_ID";

export interface SupportedLanguage {
  /** i18next 的 language code，同时也是 `locales/<code>.json` 的文件名。 */
  code: LanguageCode;
  /** 菜单里显示的名字，用它自己的语言写。 */
  name: string;
}

/**
 * 语言菜单的数据源，取代了原先「从 `resources` 里筛有 name 字段的条目」的做法：`resources`
 * 现在只有 en 是静态的，菜单不能在启动时枚举所有语言。
 */
export const SUPPORTED_LANGUAGES: readonly SupportedLanguage[] = [
  { code: "en", name: "English" },
  { code: "id_ID", name: "Bahasa Indonesia" },
  { code: "ja_JP", name: "日本語" },
  { code: "zh_CN", name: "简体中文" },
  { code: "zh_TW", name: "繁體中文" },
];

/**
 * 浏览器/旧版本存下来的语言标签 → 受支持语言的映射。
 *
 * 只列出与 code 不同形的那些；`zh-CN`、`en-US` 这类会先经 `normalizeLanguage`（`_`→`-`）
 * 再查表。键是小写的：查表前统一小写。
 */
const LANGUAGE_ALIASES: Record<string, LanguageCode> = {
  en: "en",
  ja: "ja_JP",
  id: "id_ID",
  in: "id_ID", // 印尼语的旧 ISO 639-1 代码
  zh: "zh_CN",
  "zh-cn": "zh_CN",
  "zh-sg": "zh_CN", // 新加坡用简体
  "zh-tw": "zh_TW",
  "zh-hk": "zh_TW", // 香港、澳门用繁体
  "zh-mo": "zh_TW",
};

/**
 * resolveLanguage 把一个任意语言标签映射到受支持语言。
 *
 * 规格要求的映射：zh / zh-CN / zh-SG → zh_CN；zh-TW / zh-HK / zh-MO → zh_TW；ja → ja_JP；
 * id → id_ID；其余 en。这里额外按子标签收口（`zh-Hant` → zh_TW，`zh-Hans` → zh_CN），
 * 因为浏览器真的会报出这些标签。
 */
export function resolveLanguage(tag?: string | null): LanguageCode {
  const normalized = normalizeLanguage(tag).toLowerCase();
  if (!normalized) return "en";

  const direct = LANGUAGE_ALIASES[normalized];
  if (direct) return direct;

  const base = normalized.split("-")[0];
  if (base === "zh") {
    return /-(tw|hk|mo|hant)(-|$)/.test(normalized) ? "zh_TW" : "zh_CN";
  }
  return LANGUAGE_ALIASES[base] ?? "en";
}

/**
 * detectLanguage 先看用户在菜单里选过的语言，再看浏览器。
 *
 * 存下来的值优先于 `navigator.language`（与旧的 LanguageDetector 的 localStorage→navigator
 * 顺序一致）：用户显式选过英文，浏览器却是中文时，不应该每次刷新都被改回去。
 */
export function detectLanguage(): LanguageCode {
  const stored = readStoredLanguage();
  if (stored) return resolveLanguage(stored);
  const navigatorLanguage = typeof navigator === "undefined" ? "" : navigator.language;
  return resolveLanguage(navigatorLanguage);
}

type LocaleBundle = Record<string, unknown>;

/**
 * 一个语言包怎么加载：en 已经在内存里，其余 4 个是 `import()`。
 *
 * 这个表是这张映射的唯一出处——`script/i18n-lazy.test.mjs` 会读这个文件，断言只有 en 出现在
 * 静态 import 里，其余 4 个出现在动态 import 里。
 */
const localeLoaders: Record<LanguageCode, () => Promise<{ default: unknown }>> = {
  en: () => Promise.resolve({ default: en }),
  zh_CN: () => import("./locales/zh_CN.json"),
  zh_TW: () => import("./locales/zh_TW.json"),
  ja_JP: () => import("./locales/ja_JP.json"),
  id_ID: () => import("./locales/id_ID.json"),
};

/** 已经注册进 i18next 的语言。en 在 init 时静态提供，所以一开始就在里面。 */
const loadedLanguages = new Set<LanguageCode>(["en"]);

async function fetchLocale(code: LanguageCode): Promise<LocaleBundle> {
  const { default: bundle } = await localeLoaders[code]();
  return bundle as LocaleBundle;
}

/** 把一个语言包注册进 i18next；已经注册过就直接返回。 */
async function loadLocale(code: LanguageCode): Promise<void> {
  if (loadedLanguages.has(code)) return;
  i18next.addResourceBundle(code, "translation", await fetchLocale(code), true, true);
  loadedLanguages.add(code);
}

async function bootstrap(): Promise<void> {
  const language = detectLanguage();
  const resources: Record<string, { translation: LocaleBundle }> = {
    en: { translation: en as LocaleBundle },
  };
  let lng: LanguageCode = "en";

  if (language !== "en") {
    try {
      resources[language] = { translation: await fetchLocale(language) };
      loadedLanguages.add(language);
      lng = language;
    } catch (error) {
      // 语言包拿不到（首访离线、发版后旧 chunk 404）不应该让首帧空着：退回英文，
      // 页面照常渲染，用户下次切换语言时 ensureLanguage 会再试一次。
      console.warn(
        `i18n: could not load the ${language} bundle, falling back to en`,
        error,
      );
    }
  }

  await i18next.use(initReactI18next).init({
    resources,
    lng,
    fallbackLng: "en",
    interpolation: {
      escapeValue: false, // React handles XSS
    },
  });
}

// 尽早把 localStorage 里的选择同步成 cookie：服务端渲染的主题页面读 cookie 决定语言，
// 推迟到 initI18n 之后会让首帧带上错误的 lang。
writeLanguageCookie(readStoredLanguage());

i18next.on("languageChanged", (language) => {
  writeLanguageCookie(language);
});

let initialization: Promise<void> | null = null;

/**
 * initI18n 初始化 i18next，并把当前语言的语言包准备好。
 *
 * 幂等：重复调用返回同一个 promise，不会二次 init。入口在 `createRoot().render()` 之前
 * `await` 它，首帧才不会先画出 key 再被替换。
 */
export function initI18n(): Promise<void> {
  if (!initialization) {
    initialization = bootstrap().catch((error: unknown) => {
      // 失败的初始化要能重试，而不是把入口永久卡在一个 rejected promise 上。
      initialization = null;
      throw error;
    });
  }
  return initialization;
}

/**
 * ensureLanguage 确保 `code` 的语言包已经注册进 i18next。
 *
 * 语言切换菜单在 `changeLanguage(code)` 之前调用它：先把语言包装上，再切语言，切换后的
 * 第一帧就已是翻译后的文本，而不是一串 key。
 */
export async function ensureLanguage(code: string): Promise<void> {
  await initI18n();
  await loadLocale(resolveLanguage(code));
}

const i18n = i18next;

export default i18n;
