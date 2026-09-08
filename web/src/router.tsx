import { lazy } from 'react';
import { createBrowserRouter, Navigate } from 'react-router-dom';
import AppLayout from '@/layouts/AppLayout';
import { PublicOnly, RedirectByRole, RequireAdmin, RequireAuth } from '@/layouts/guards';

const Login = lazy(() => import('@/pages/auth/Login'));
const ChangePassword = lazy(() => import('@/pages/auth/ChangePassword'));

const Overview = lazy(() => import('@/pages/admin/Overview'));
const Accounts = lazy(() => import('@/pages/admin/Accounts'));
const ModelGroups = lazy(() => import('@/pages/admin/ModelGroups'));
const Users = lazy(() => import('@/pages/admin/Users'));
const UserGroups = lazy(() => import('@/pages/admin/UserGroups'));
const SmartRoute = lazy(() => import('@/pages/admin/SmartRoute'));
const Compliance = lazy(() => import('@/pages/admin/Compliance'));
const Logs = lazy(() => import('@/pages/admin/Logs'));
const Usage = lazy(() => import('@/pages/admin/Usage'));
const Settings = lazy(() => import('@/pages/admin/Settings'));
const About = lazy(() => import('@/pages/admin/About'));

const ConsoleModels = lazy(() => import('@/pages/console/Models'));
const ConsoleKeys = lazy(() => import('@/pages/console/Keys'));
const ConsoleUsage = lazy(() => import('@/pages/console/Usage'));
const ConsoleLogs = lazy(() => import('@/pages/console/Logs'));
const ConsoleGroup = lazy(() => import('@/pages/console/Group'));

export const router = createBrowserRouter([
  {
    element: <PublicOnly />,
    children: [{ path: '/login', element: <Login /> }],
  },
  {
    element: <RequireAuth />,
    children: [
      { path: '/change-password', element: <ChangePassword /> },
      {
        element: <RequireAdmin />,
        children: [
          {
            path: '/admin',
            element: <AppLayout area="admin" />,
            children: [
              { index: true, element: <Navigate to="/admin/overview" replace /> },
              { path: 'overview', element: <Overview /> },
              { path: 'accounts', element: <Accounts /> },
              { path: 'model-groups', element: <ModelGroups /> },
              { path: 'users', element: <Users /> },
              { path: 'user-groups', element: <UserGroups /> },
              { path: 'smart-route', element: <SmartRoute /> },
              { path: 'compliance', element: <Compliance /> },
              { path: 'logs', element: <Logs /> },
              { path: 'usage', element: <Usage /> },
              { path: 'settings', element: <Settings /> },
              { path: 'about', element: <About /> },
              { path: '*', element: <Navigate to="/admin/overview" replace /> },
            ],
          },
        ],
      },
      {
        path: '/console',
        element: <AppLayout area="console" />,
        children: [
          { index: true, element: <Navigate to="/console/models" replace /> },
          { path: 'models', element: <ConsoleModels /> },
          { path: 'keys', element: <ConsoleKeys /> },
          { path: 'usage', element: <ConsoleUsage /> },
          { path: 'logs', element: <ConsoleLogs /> },
          { path: 'group', element: <ConsoleGroup /> },
          { path: '*', element: <Navigate to="/console/models" replace /> },
        ],
      },
    ],
  },
  { path: '*', element: <RedirectByRole /> },
]);
