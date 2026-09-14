import { create } from 'zustand';

export type Locale = 'zh-CN' | 'zh-TW' | 'en';
export const LOCALES: { key: Locale; label: string }[] = [
  { key: 'zh-CN', label: '简体中文' },
  { key: 'zh-TW', label: '繁體中文' },
  { key: 'en', label: 'English' },
];
const KEY = 'yz_locale';

/** Map one BCP 47 tag from the browser to a supported locale, or null. */
export function matchLocale(tag: string): Locale | null {
  if (/^zh(-|$)/i.test(tag)) {
    // Traditional Chinese regions and the explicit Hant script; everything else Chinese is Simplified.
    return /^zh-(TW|HK|MO|Hant)(-|$)/i.test(tag) ? 'zh-TW' : 'zh-CN';
  }
  if (/^en(-|$)/i.test(tag)) return 'en';
  return null;
}

/**
 * Pick the locale for a first visit: an explicit choice saved in localStorage wins;
 * otherwise the browser's language preference list is walked in order (this follows
 * the OS language on every major browser) and the first supported language is used;
 * a browser configured for a language the UI does not have falls back to English.
 */
export function detectLocale(): Locale {
  const saved = localStorage.getItem(KEY);
  if (saved === 'zh-CN' || saved === 'zh-TW' || saved === 'en') return saved;
  const prefs = navigator.languages && navigator.languages.length > 0 ? navigator.languages : [navigator.language || ''];
  for (const tag of prefs) {
    const hit = matchLocale(tag);
    if (hit) return hit;
  }
  return 'en';
}

interface LocaleState {
  locale: Locale;
  setLocale: (l: Locale) => void;
}

export const useLocaleStore = create<LocaleState>((set) => ({
  locale: detectLocale(),
  setLocale: (locale) => {
    localStorage.setItem(KEY, locale);
    set({ locale });
  },
}));
