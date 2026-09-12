import axios, { AxiosError } from 'axios';
import { message } from 'antd';
import { useAuthStore } from '@/stores/auth';
import type { ApiError } from '@/types';

export const http = axios.create({
  baseURL: '/',
  timeout: 60_000,
  headers: { 'Content-Type': 'application/json' },
});

http.interceptors.request.use((config) => {
  const token = useAuthStore.getState().token;
  if (token) {
    config.headers = config.headers ?? {};
    config.headers.Authorization = `Bearer ${token}`;
  }
  return config;
});

export interface NormalizedError extends Error {
  status?: number;
  code?: string;
  silent?: boolean;
  /** The structured response body, when the server sent one (a 503 after a committed write still carries its report). */
  data?: unknown;
}

export function extractError(err: unknown): NormalizedError {
  const e = err as AxiosError<ApiError>;
  const status = e?.response?.status;
  const data = e?.response?.data;
  const text =
    (data && typeof data === 'object' && (data as ApiError).error) ||
    (typeof data === 'string' && data) ||
    e?.message ||
    'Request failed';
  const n = new Error(text) as NormalizedError;
  n.status = status;
  n.code = data && typeof data === 'object' ? (data as ApiError).code : undefined;
  n.data = data && typeof data === 'object' ? data : undefined;
  return n;
}

http.interceptors.response.use(
  (res) => res,
  (err: AxiosError<ApiError>) => {
    const n = extractError(err);
    const url = err.config?.url ?? '';
    const isLogin = url.includes('/api/auth/login');
    if (n.status === 401 && !isLogin) {
      useAuthStore.getState().clear();
      if (!window.location.pathname.startsWith('/login')) {
        window.location.replace('/login');
      }
      n.silent = true;
    }
    if (!n.silent && !(err.config as { skipErrorToast?: boolean } | undefined)?.skipErrorToast) {
      message.error(n.message);
    }
    return Promise.reject(n);
  },
);

/** Strip undefined / empty-string params so filters can be passed straight through. */
export function cleanParams<T extends object>(params: T): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  Object.entries(params as Record<string, unknown>).forEach(([k, v]) => {
    if (v === undefined || v === null || v === '') return;
    out[k] = v;
  });
  return out;
}

export async function get<T>(url: string, params?: object, opts?: { skipErrorToast?: boolean }): Promise<T> {
  const res = await http.get<T>(url, { params: params ? cleanParams(params) : undefined, ...(opts as object) });
  return res.data;
}
export async function post<T>(url: string, body?: unknown, opts?: { skipErrorToast?: boolean }): Promise<T> {
  const res = await http.post<T>(url, body ?? {}, opts as object);
  return res.data;
}
export async function put<T>(url: string, body?: unknown): Promise<T> {
  const res = await http.put<T>(url, body ?? {});
  return res.data;
}
export async function patch<T>(url: string, body?: unknown): Promise<T> {
  const res = await http.patch<T>(url, body ?? {});
  return res.data;
}
export async function del<T>(url: string): Promise<T> {
  const res = await http.delete<T>(url);
  return res.data;
}
