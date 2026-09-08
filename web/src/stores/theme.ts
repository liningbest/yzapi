import { create } from 'zustand';

export type ThemeMode = 'light' | 'dark';
const KEY = 'yz_theme';

function initial(): ThemeMode {
  const saved = localStorage.getItem(KEY);
  if (saved === 'light' || saved === 'dark') return saved;
  if (typeof window !== 'undefined' && window.matchMedia?.('(prefers-color-scheme: dark)').matches) return 'dark';
  return 'light';
}

interface ThemeState {
  mode: ThemeMode;
  setMode: (m: ThemeMode) => void;
  toggle: () => void;
}

export const useThemeStore = create<ThemeState>((set, get) => ({
  mode: initial(),
  setMode: (mode) => {
    localStorage.setItem(KEY, mode);
    document.documentElement.dataset.theme = mode;
    set({ mode });
  },
  toggle: () => get().setMode(get().mode === 'dark' ? 'light' : 'dark'),
}));

document.documentElement.dataset.theme = useThemeStore.getState().mode;
