import { Suspense, useMemo } from 'react';
import { RouterProvider } from 'react-router-dom';
import { App as AntdApp, ConfigProvider, theme as antdTheme } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import zhTW from 'antd/locale/zh_TW';
import enUS from 'antd/locale/en_US';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useThemeStore } from '@/stores/theme';
import { useLocaleStore } from '@/stores/locale';
import '@/stores/site';
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

  const themeConfig = useMemo(() => {
    const dark = mode === 'dark';
    const border = dark ? '#27272a' : '#e4e4e7';
    const surface = dark ? '#111113' : '#ffffff';
    const accent = dark ? '#3b82f6' : '#2563eb';
    return {
      algorithm: dark ? antdTheme.darkAlgorithm : antdTheme.defaultAlgorithm,
      token: {
        colorPrimary: accent,
        colorInfo: accent,
        colorLink: accent,
        colorSuccess: dark ? '#22c55e' : '#16a34a',
        colorWarning: dark ? '#f59e0b' : '#d97706',
        colorError: dark ? '#ef4444' : '#dc2626',
        borderRadius: 6,
        borderRadiusLG: 8,
        borderRadiusSM: 4,
        borderRadiusXS: 4,
        fontSize: 13,
        fontSizeSM: 12,
        fontSizeLG: 14,
        fontSizeHeading4: 18,
        fontSizeHeading5: 15,
        controlHeight: 32,
        controlHeightSM: 26,
        controlHeightLG: 36,
        controlOutline: dark ? 'rgba(59,130,246,0.25)' : 'rgba(37,99,235,0.2)',
        controlOutlineWidth: 2,
        fontFamily:
          "-apple-system, BlinkMacSystemFont, 'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', 'Segoe UI', Roboto, Helvetica, Arial, sans-serif",
        fontFamilyCode: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
        colorBgLayout: dark ? '#09090b' : '#fafafa',
        colorBgContainer: surface,
        colorBgElevated: dark ? '#18181b' : '#ffffff',
        colorBorder: border,
        colorBorderSecondary: border,
        colorSplit: border,
        colorText: dark ? '#fafafa' : '#18181b',
        colorTextSecondary: dark ? '#a1a1aa' : '#71717a',
        colorTextTertiary: dark ? '#71717a' : '#a1a1aa',
        colorTextQuaternary: dark ? '#52525b' : '#d4d4d8',
        colorFillTertiary: dark ? '#18181b' : '#f4f4f5',
        colorFillSecondary: dark ? '#27272a' : '#e4e4e7',
        colorFillQuaternary: dark ? '#141416' : '#fafafa',
        boxShadow: 'none',
        boxShadowSecondary: dark
          ? '0 0 0 1px #27272a, 0 4px 12px rgba(0,0,0,0.4)'
          : '0 0 0 1px #e4e4e7, 0 4px 12px rgba(0,0,0,0.06)',
        boxShadowTertiary: 'none',
        lineWidth: 1,
      },
      components: {
        Card: { paddingLG: 16, headerHeight: 44, headerFontSize: 13, boxShadowTertiary: 'none' },
        Table: {
          cellPaddingBlock: 8,
          cellPaddingInline: 12,
          cellPaddingBlockSM: 6,
          cellPaddingInlineSM: 8,
          headerBg: surface,
          headerColor: dark ? '#a1a1aa' : '#71717a',
          headerSplitColor: 'transparent',
          rowHoverBg: dark ? '#18181b' : '#f4f4f5',
          borderColor: border,
          headerBorderRadius: 6,
          fontSize: 13,
        },
        Layout: { headerBg: surface, siderBg: dark ? '#111113' : '#fafafa', bodyBg: dark ? '#09090b' : '#fafafa' },
        Menu: {
          itemBg: 'transparent',
          subMenuItemBg: 'transparent',
          itemSelectedBg: dark ? '#27272a' : 'rgba(228,228,231,0.4)',
          itemSelectedColor: dark ? '#fafafa' : '#18181b',
          itemColor: dark ? '#a1a1aa' : '#52525b',
          itemHoverBg: dark ? '#18181b' : '#f4f4f5',
          itemHoverColor: dark ? '#fafafa' : '#18181b',
          itemBorderRadius: 6,
          itemHeight: 32,
          itemMarginInline: 0,
          itemMarginBlock: 1,
          iconSize: 15,
          collapsedIconSize: 16,
          groupTitleFontSize: 11,
          groupTitleColor: dark ? '#71717a' : '#a1a1aa',
          activeBarBorderWidth: 0,
        },
        Drawer: { paddingLG: 20 },
        Modal: { titleFontSize: 15 },
        Segmented: {
          itemSelectedBg: surface,
          itemSelectedColor: dark ? '#fafafa' : '#18181b',
          trackBg: dark ? '#18181b' : '#f4f4f5',
          borderRadius: 6,
          borderRadiusSM: 4,
        },
        Button: { fontWeight: 500, defaultShadow: 'none', primaryShadow: 'none', dangerShadow: 'none' },
        Input: { activeShadow: dark ? '0 0 0 2px rgba(59,130,246,0.25)' : '0 0 0 2px rgba(37,99,235,0.2)' },
        Select: { optionSelectedBg: dark ? '#27272a' : '#f4f4f5' },
        Tag: { borderRadiusSM: 4, fontSizeSM: 11 },
        Tabs: { titleFontSize: 13, horizontalItemPadding: '10px 0', horizontalMargin: '0 0 12px 0' },
        Descriptions: { labelBg: dark ? '#18181b' : '#fafafa' },
        Tooltip: { colorBgSpotlight: dark ? '#27272a' : '#18181b', borderRadius: 4 },
        Badge: { dotSize: 6 },
        Progress: { defaultColor: accent, remainingColor: dark ? '#27272a' : '#f4f4f5', lineBorderRadius: 2 },
      },
    };
  }, [mode]);

  return (
    <ConfigProvider theme={themeConfig} locale={ANTD_LOCALES[locale]}>
      <AntdApp>
        <QueryClientProvider client={queryClient}>
          <Suspense fallback={<div style={{ minHeight: '100vh' }} />}>
            <RouterProvider router={router} />
          </Suspense>
        </QueryClientProvider>
      </AntdApp>
    </ConfigProvider>
  );
}
