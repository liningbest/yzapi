import { useMemo } from 'react';
import { RouterProvider } from 'react-router-dom';
import { App as AntdApp, ConfigProvider, theme as antdTheme } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import zhTW from 'antd/locale/zh_TW';
import enUS from 'antd/locale/en_US';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useThemeStore } from '@/stores/theme';
import { useLocaleStore } from '@/stores/locale';
import { router } from '@/router';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: false, refetchOnWindowFocus: false, staleTime: 10_000 },
    mutations: { retry: false },
  },
});

const ANTD_LOCALES = { 'zh-CN': zhCN, 'zh-TW': zhTW, en: enUS } as const;

export default function App() {
  const mode = useThemeStore((s) => s.mode);
  const locale = useLocaleStore((s) => s.locale);

  const themeConfig = useMemo(
    () => ({
      algorithm: mode === 'dark' ? antdTheme.darkAlgorithm : antdTheme.defaultAlgorithm,
      token: {
        colorPrimary: '#4f46e5',
        colorInfo: '#4f46e5',
        colorSuccess: '#10b981',
        colorWarning: '#f59e0b',
        colorError: '#ef4444',
        borderRadius: 10,
        borderRadiusLG: 12,
        borderRadiusSM: 8,
        fontFamily:
          "'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', 'Helvetica Neue', Arial, sans-serif",
        colorBgLayout: mode === 'dark' ? '#0b1220' : '#f5f7fa',
        colorBgContainer: mode === 'dark' ? '#111a2e' : '#ffffff',
        colorBgElevated: mode === 'dark' ? '#16213a' : '#ffffff',
        colorBorderSecondary: mode === 'dark' ? 'rgba(255,255,255,0.08)' : 'rgba(15,23,42,0.08)',
        boxShadowSecondary: '0 8px 24px rgba(15,23,42,0.12)',
      },
      components: {
        Card: { paddingLG: 20 },
        Table: { cellPaddingBlockSM: 8, headerBg: mode === 'dark' ? '#16213a' : '#f8fafc' },
        Layout: { headerBg: mode === 'dark' ? '#111a2e' : '#ffffff', siderBg: '#0f172a' },
        Drawer: { paddingLG: 24 },
        Segmented: { itemSelectedBg: mode === 'dark' ? '#4f46e5' : '#ffffff', itemSelectedColor: mode === 'dark' ? '#fff' : '#0f172a' },
      },
    }),
    [mode],
  );

  return (
    <ConfigProvider theme={themeConfig} locale={ANTD_LOCALES[locale]}>
      <AntdApp>
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
        </QueryClientProvider>
      </AntdApp>
    </ConfigProvider>
  );
}
