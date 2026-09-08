import { create } from 'zustand';

export type Locale = 'zh-CN' | 'zh-TW' | 'en';
export const LOCALES: { key: Locale; label: string }[] = [
  { key: 'zh-CN', label: '简体中文' },
  { key: 'zh-TW', label: '繁體中文' },
  { key: 'en', label: 'English' },
];
const KEY = 'yz_locale';

function initial(): Locale {
  const saved = localStorage.getItem(KEY);
  if (saved === 'zh-CN' || saved === 'zh-TW' || saved === 'en') return saved;
  const nav = navigator.language || '';
  if (/^zh-(TW|HK|MO|Hant)/i.test(nav)) return 'zh-TW';
  if (/^zh/i.test(nav)) return 'zh-CN';
  if (/^en/i.test(nav)) return 'en';
  return 'zh-CN';
}

interface LocaleState {
  locale: Locale;
  setLocale: (l: Locale) => void;
}

export const useLocaleStore = create<LocaleState>((set) => ({
  locale: initial(),
  setLocale: (locale) => {
    localStorage.setItem(KEY, locale);
    set({ locale });
  },
}));
