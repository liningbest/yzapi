import { Suspense, useEffect, useMemo, useState } from 'react';
import { Outlet, useLocation, useNavigate } from 'react-router-dom';
import {
  Avatar,
  Breadcrumb,
  Button,
  ConfigProvider,
  Drawer,
  Dropdown,
  Layout,
  Menu,
  Spin,
  Tooltip,
  Typography,
  message,
  theme as antdTheme,
} from 'antd';
import type { MenuProps } from 'antd';
import {
  GlobalOutlined,
  InfoCircleOutlined,
  KeyOutlined,
  LogoutOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  MoonOutlined,
  SunOutlined,
  SwapOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { authApi } from '@/api';
import { useAuthStore, isAdmin } from '@/stores/auth';
import { useThemeStore } from '@/stores/theme';
import { LOCALES, useLocaleStore } from '@/stores/locale';
import { useIsCompact, useIsMobile } from '@/hooks/useMediaQuery';
import BrandMark from '@/components/BrandMark';
import AboutModal from '@/components/AboutModal';
import { ADMIN_NAV, CONSOLE_NAV, findNav, type NavItem } from './routes';

const { Sider, Header, Content } = Layout;

interface Props {
  area: 'admin' | 'console';
}

export default function AppLayout({ area }: Props) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const location = useLocation();
  const user = useAuthStore((s) => s.user);
  const setUser = useAuthStore((s) => s.setUser);
  const clearAuth = useAuthStore((s) => s.clear);
  const themeMode = useThemeStore((s) => s.mode);
  const toggleTheme = useThemeStore((s) => s.toggle);
  const locale = useLocaleStore((s) => s.locale);
  const setLocale = useLocaleStore((s) => s.setLocale);
  const mobile = useIsMobile();
  const compact = useIsCompact();
  const [collapsed, setCollapsed] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(false);
  const [aboutOpen, setAboutOpen] = useState(false);

  // auto-collapse under 1200px
  useEffect(() => setCollapsed(compact), [compact]);
  useEffect(() => setMobileOpen(false), [location.pathname]);

  // keep user fresh (also validates the token)
  const me = useQuery({ queryKey: ['auth', 'me'], queryFn: authApi.me, staleTime: 60_000 });
  useEffect(() => {
    if (me.data) setUser(me.data);
  }, [me.data, setUser]);

  const nav = area === 'admin' ? ADMIN_NAV : CONSOLE_NAV;
  const current = findNav(location.pathname);

  const menuItems = useMemo<MenuProps['items']>(() => {
    const groups = new Map<string, NavItem[]>();
    nav.forEach((n) => {
      const g = n.group ?? '';
      groups.set(g, [...(groups.get(g) ?? []), n]);
    });
    const items: MenuProps['items'] = [];
    groups.forEach((list, g) => {
      const children = list.map((n) => {
        const Icon = n.icon;
        return { key: n.path, icon: <Icon />, label: t(`nav.${n.navKey}`) };
      });
      if (g && !collapsed) {
        items.push({ type: 'group', key: `g-${g}`, label: t(`nav.${g}`), children });
      } else {
        items.push(...children);
      }
    });
    return items;
  }, [nav, t, collapsed]);

  const onLogout = async () => {
    try {
      await authApi.logout();
    } catch {
      /* ignore */
    }
    clearAuth();
    message.success(t('header.logoutSuccess'));
    navigate('/login', { replace: true });
  };

  const userMenu: MenuProps['items'] = [
    ...(isAdmin(user)
      ? [
          {
            key: 'switch',
            icon: <SwapOutlined />,
            label: area === 'admin' ? t('header.toConsole') : t('header.toAdmin'),
            onClick: () => navigate(area === 'admin' ? '/console/models' : '/admin/overview'),
          },
          { type: 'divider' as const },
        ]
      : []),
    { key: 'pwd', icon: <KeyOutlined />, label: t('header.changePassword'), onClick: () => navigate('/change-password') },
    { key: 'about', icon: <InfoCircleOutlined />, label: t('header.about'), onClick: () => setAboutOpen(true) },
    { type: 'divider' as const },
    { key: 'logout', icon: <LogoutOutlined />, label: t('header.logout'), danger: true, onClick: onLogout },
  ];

  const langMenu: MenuProps['items'] = LOCALES.map((l) => ({
    key: l.key,
    label: l.label,
    onClick: () => setLocale(l.key),
  }));

  const siderInner = (isCollapsed: boolean) => (
    <ConfigProvider
      theme={{
        algorithm: antdTheme.darkAlgorithm,
        components: {
          Menu: {
            darkItemBg: 'transparent',
            darkSubMenuItemBg: 'transparent',
            darkItemSelectedBg: '#4f46e5',
            darkItemSelectedColor: '#ffffff',
            darkItemColor: 'rgba(255,255,255,0.72)',
            darkItemHoverBg: 'rgba(255,255,255,0.06)',
            darkItemHoverColor: '#ffffff',
            itemBorderRadius: 10,
            iconSize: 16,
            collapsedIconSize: 18,
          },
        },
      }}
    >
      <div className={`yz-sider-logo${isCollapsed ? ' collapsed' : ''}`} onClick={() => navigate(area === 'admin' ? '/admin/overview' : '/console/models')} style={{ cursor: 'pointer' }}>
        <BrandMark size={32} />
        {!isCollapsed ? (
          <div>
            <div className="brand-name">{t('app.name')}</div>
            <div className="brand-sub">{area === 'admin' ? t('app.adminConsole') : t('app.userConsole')}</div>
          </div>
        ) : null}
      </div>
      <Menu
        className="yz-sider-menu"
        theme="dark"
        mode="inline"
        inlineCollapsed={isCollapsed}
        selectedKeys={current ? [current.path] : []}
        items={menuItems}
        onClick={({ key }) => navigate(key)}
      />
      <div className="yz-sider-footer">
        <Avatar size={32} style={{ background: 'linear-gradient(135deg,#6366f1,#06b6d4)', flexShrink: 0 }} icon={<UserOutlined />}>
          {user?.username?.slice(0, 1).toUpperCase()}
        </Avatar>
        {!isCollapsed ? (
          <div style={{ minWidth: 0 }}>
            <div className="name">{user?.username}</div>
            <div className="role">{t(`role.${user?.role ?? 'user'}`)}</div>
          </div>
        ) : null}
      </div>
    </ConfigProvider>
  );

  return (
    <Layout className="yz-layout" hasSider={!mobile}>
      {mobile ? (
        <Drawer
          placement="left"
          open={mobileOpen}
          onClose={() => setMobileOpen(false)}
          width={240}
          closable={false}
          styles={{ body: { padding: 0, background: 'var(--yz-sider)', display: 'flex', flexDirection: 'column' } }}
        >
          {siderInner(false)}
        </Drawer>
      ) : (
        <Sider className="yz-sider" width={232} collapsedWidth={72} collapsed={collapsed} trigger={null} collapsible>
          {siderInner(collapsed)}
        </Sider>
      )}
      <Layout>
        <Header className="yz-header">
          <div className="yz-header-left">
            <Tooltip title={collapsed ? t('header.expand') : t('header.collapse')}>
              <Button
                type="text"
                className="yz-header-btn"
                icon={mobile ? <MenuUnfoldOutlined /> : collapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
                onClick={() => (mobile ? setMobileOpen(true) : setCollapsed((c) => !c))}
              />
            </Tooltip>
            {mobile ? (
              <div className="yz-header-title">{current ? t(`nav.${current.navKey}`) : t('app.name')}</div>
            ) : (
              <Breadcrumb
                className="yz-header-crumb"
                items={[
                  { title: area === 'admin' ? t('app.adminConsole') : t('app.userConsole') },
                  ...(current ? [{ title: <span className="yz-header-current">{t(`nav.${current.navKey}`)}</span> }] : []),
                ]}
              />
            )}
          </div>
          <div className="yz-header-right">
            <Dropdown menu={{ items: langMenu, selectedKeys: [locale] }} placement="bottomRight">
              <Button type="text" className="yz-header-btn" icon={<GlobalOutlined />} aria-label={t('header.language')} />
            </Dropdown>
            <Tooltip title={themeMode === 'dark' ? t('header.light') : t('header.dark')}>
              <Button
                type="text"
                className="yz-header-btn"
                icon={themeMode === 'dark' ? <SunOutlined /> : <MoonOutlined />}
                onClick={toggleTheme}
              />
            </Tooltip>
            <Tooltip title={t('header.about')}>
              <Button type="text" className="yz-header-btn" icon={<InfoCircleOutlined />} onClick={() => setAboutOpen(true)} />
            </Tooltip>
            <Dropdown menu={{ items: userMenu }} placement="bottomRight" trigger={['click']}>
              <div className="yz-user-chip">
                <Avatar size={30} style={{ background: 'linear-gradient(135deg,#6366f1,#06b6d4)' }}>
                  {user?.username?.slice(0, 1).toUpperCase()}
                </Avatar>
                {!mobile ? (
                  <Typography.Text strong style={{ maxWidth: 140 }} ellipsis>
                    {user?.username}
                  </Typography.Text>
                ) : null}
              </div>
            </Dropdown>
          </div>
        </Header>
        <Content>
          <div className="yz-content">
            <Suspense
              fallback={
                <div style={{ display: 'flex', justifyContent: 'center', padding: 80 }}>
                  <Spin size="large" />
                </div>
              }
            >
              <Outlet />
            </Suspense>
          </div>
        </Content>
      </Layout>
      <AboutModal open={aboutOpen} onClose={() => setAboutOpen(false)} />
    </Layout>
  );
}
