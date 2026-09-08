import { create } from 'zustand';
import { authApi } from '@/api';

const DEFAULT_NAME = 'YZ AI Gateway';

interface SiteState {
  name: string;
  baseUrl: string;
  version: string;
  loaded: boolean;
  refresh: () => Promise<void>;
}

/** Public site info (site name, data-plane base URL) shared by login page, sidebar and title. */
export const useSiteStore = create<SiteState>((set) => ({
  name: DEFAULT_NAME,
  baseUrl: `${window.location.origin}/v1`,
  version: '',
  loaded: false,
  refresh: async () => {
    try {
      const info = await authApi.publicInfo();
      set({
        name: info.site_name?.trim() || DEFAULT_NAME,
        baseUrl: info.base_url || `${window.location.origin}/v1`,
        version: info.version ?? '',
        loaded: true,
      });
    } catch {
      set({ loaded: true });
    }
  },
}));

export const useSiteName = () => useSiteStore((s) => s.name);

// Keep the browser tab title in sync.
useSiteStore.subscribe((s) => {
  document.title = s.name;
});
void useSiteStore.getState().refresh();
