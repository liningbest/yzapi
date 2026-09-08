import { get, post } from './client';
import type { LoginResponse, PublicInfo, User } from '@/types';

export const authApi = {
  login: (username: string, password: string) =>
    post<LoginResponse>('/api/auth/login', { username, password }, { skipErrorToast: true }),
  logout: () => post<Record<string, never>>('/api/auth/logout'),
  me: () => get<User>('/api/auth/me'),
  changePassword: (old_password: string, new_password: string) =>
    post<Record<string, never>>('/api/auth/change-password', { old_password, new_password }, { skipErrorToast: true }),
  publicInfo: () => get<PublicInfo>('/api/public/info'),
};
