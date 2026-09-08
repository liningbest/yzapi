import type { ComponentType } from 'react';
import {
  ApiOutlined,
  AppstoreOutlined,
  BarChartOutlined,
  BranchesOutlined,
  CloudServerOutlined,
  DashboardOutlined,
  FileSearchOutlined,
  KeyOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  TeamOutlined,
  UserOutlined,
  UsergroupAddOutlined,
} from '@ant-design/icons';

export interface NavItem {
  path: string;
  /** i18n key under common:nav.* */
  navKey: string;
  icon: ComponentType;
  /** i18n key for group header under common:nav.* (admin only) */
  group?: string;
}

export const ADMIN_NAV: NavItem[] = [
  { path: '/admin/overview', navKey: 'overview', icon: DashboardOutlined },
  { path: '/admin/accounts', navKey: 'accounts', icon: CloudServerOutlined, group: 'management' },
  { path: '/admin/model-groups', navKey: 'modelGroups', icon: AppstoreOutlined, group: 'management' },
  { path: '/admin/users', navKey: 'users', icon: UserOutlined, group: 'management' },
  { path: '/admin/user-groups', navKey: 'userGroups', icon: TeamOutlined, group: 'management' },
  { path: '/admin/smart-route', navKey: 'smartRoute', icon: BranchesOutlined, group: 'governance' },
  { path: '/admin/compliance', navKey: 'compliance', icon: SafetyCertificateOutlined, group: 'governance' },
  { path: '/admin/usage', navKey: 'usage', icon: BarChartOutlined, group: 'insight' },
  { path: '/admin/logs', navKey: 'logs', icon: FileSearchOutlined, group: 'insight' },
  { path: '/admin/settings', navKey: 'settings', icon: SettingOutlined, group: 'system' },
];

export const CONSOLE_NAV: NavItem[] = [
  { path: '/console/models', navKey: 'models', icon: AppstoreOutlined },
  { path: '/console/keys', navKey: 'keys', icon: KeyOutlined },
  { path: '/console/usage', navKey: 'myUsage', icon: BarChartOutlined },
  { path: '/console/logs', navKey: 'myLogs', icon: FileSearchOutlined },
  { path: '/console/group', navKey: 'myGroup', icon: UsergroupAddOutlined },
];

export const ALL_NAV = [...ADMIN_NAV, ...CONSOLE_NAV, { path: '/admin/about', navKey: 'about', icon: ApiOutlined }];

export function findNav(pathname: string): NavItem | undefined {
  return ALL_NAV.find((n) => pathname === n.path || pathname.startsWith(n.path + '/'));
}
