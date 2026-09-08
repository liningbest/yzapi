import { Navigate, Outlet, useLocation } from 'react-router-dom';
import { useAuthStore, isAdmin } from '@/stores/auth';

export function homeFor(role: string | undefined): string {
  return role === 'admin' ? '/admin/overview' : '/console/models';
}

/** Requires a token; enforces forced password change. */
export function RequireAuth() {
  const token = useAuthStore((s) => s.token);
  const user = useAuthStore((s) => s.user);
  const location = useLocation();
  if (!token) return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  if (user?.must_change_password && location.pathname !== '/change-password') {
    return <Navigate to="/change-password" replace />;
  }
  return <Outlet />;
}

export function RequireAdmin() {
  const user = useAuthStore((s) => s.user);
  if (!isAdmin(user)) return <Navigate to="/console/models" replace />;
  return <Outlet />;
}

/** Public-only route (login). */
export function PublicOnly() {
  const token = useAuthStore((s) => s.token);
  const user = useAuthStore((s) => s.user);
  if (token) {
    if (user?.must_change_password) return <Navigate to="/change-password" replace />;
    return <Navigate to={homeFor(user?.role)} replace />;
  }
  return <Outlet />;
}

export function RedirectByRole() {
  const token = useAuthStore((s) => s.token);
  const user = useAuthStore((s) => s.user);
  if (!token) return <Navigate to="/login" replace />;
  return <Navigate to={homeFor(user?.role)} replace />;
}
