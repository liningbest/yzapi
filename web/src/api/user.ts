import { del, get, patch, post, put } from './client';
import type {
  ApiKey,
  CallLog,
  CreateKeyResponse,
  ListResponse,
  MyGroup,
  UsageResponse,
  UserLogParams,
  UserModelsResponse,
  UserUsageParams,
} from '@/types';

const U = '/api/user';

export const userApi = {
  models: () => get<UserModelsResponse>(`${U}/models`),
  keys: () => get<ApiKey[]>(`${U}/keys`),
  createKey: (name: string) => post<CreateKeyResponse>(`${U}/keys`, { name }),
  renameKey: (id: number, name: string) => put<ApiKey>(`${U}/keys/${id}`, { name }),
  setKeyEnabled: (id: number, enabled: boolean) => patch<ApiKey>(`${U}/keys/${id}/enabled`, { enabled }),
  removeKey: (id: number) => del<void>(`${U}/keys/${id}`),
  usage: (params: UserUsageParams) => get<UsageResponse>(`${U}/usage`, params),
  logs: (params: UserLogParams) => get<ListResponse<CallLog>>(`${U}/logs`, params),
  group: () => get<MyGroup>(`${U}/group`),
};
