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
  KeyInput,
} from '@/types';

const U = '/api/user';

export const userApi = {
  models: () => get<UserModelsResponse>(`${U}/models`),
  keys: () => get<ApiKey[]>(`${U}/keys`),
  createKey: (body: KeyInput) => post<CreateKeyResponse>(`${U}/keys`, body),
  updateKey: (id: number, body: KeyInput) => put<ApiKey>(`${U}/keys/${id}`, body),
  setKeyEnabled: (id: number, enabled: boolean) => patch<ApiKey>(`${U}/keys/${id}/enabled`, { enabled }),
  removeKey: (id: number) => del<void>(`${U}/keys/${id}`),
  usage: (params: UserUsageParams) => get<UsageResponse>(`${U}/usage`, params),
  logs: (params: UserLogParams) => get<ListResponse<CallLog>>(`${U}/logs`, params),
  group: () => get<MyGroup>(`${U}/group`),
};
