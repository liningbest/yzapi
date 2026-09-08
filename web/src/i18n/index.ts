import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import { useLocaleStore } from '@/stores/locale';
import { setDayjsLocale } from '@/utils/format';

// Every src/locales/<lng>/<ns>.json becomes namespace <ns> for language <lng>.
const files = import.meta.glob('../locales/*/*.json', { eager: true, import: 'default' }) as Record<
  string,
  Record<string, unknown>
>;

const resources: Record<string, Record<string, Record<string, unknown>>> = {};
const namespaces = new Set<string>();
for (const [path, content] of Object.entries(files)) {
  const m = path.match(/locales\/([^/]+)\/([^/]+)\.json$/);
  if (!m) continue;
  const [, lng, ns] = m;
  resources[lng] = resources[lng] ?? {};
  resources[lng][ns] = content;
  namespaces.add(ns);
}

const initialLocale = useLocaleStore.getState().locale;

void i18n.use(initReactI18next).init({
  resources,
  lng: initialLocale,
  fallbackLng: 'zh-CN',
  ns: Array.from(namespaces),
  defaultNS: 'common',
  interpolation: { escapeValue: false },
  returnNull: false,
});

setDayjsLocale(initialLocale);

useLocaleStore.subscribe((state) => {
  void i18n.changeLanguage(state.locale);
  setDayjsLocale(state.locale);
  document.documentElement.lang = state.locale;
});

export default i18n;
