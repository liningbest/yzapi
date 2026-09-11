import { del, get, patch, post, put } from './client';
import type {
  ModelMapping,
  Account,
  AccountInput,
  AccountListParams,
  AccountTestResult,
  AdminUser,
  AllSettings,
  AuditLog,
  AuditLogListParams,
  BasicSettings,
  BuildResult,
  CallLog,
  ComplianceSample,
  ComplianceSampleInput,
  ComplianceSettings,
  ComplianceTestResult,
  DiscoverInput,
  ElasticsearchSettings,
  EsStatus,
  EsTestResult,
  ListResponse,
  LogFilters,
  LogListParams,
  ModelGroup,
  ModelGroupInput,
  OverviewLive,
  OverviewUsage,
  PageParams,
  PerformanceSettings,
  PolicyGroup,
  PolicyGroupInput,
  Provider,
  RangeKey,
  RoutableModel,
  RouteDecision,
  RouteDecisionListParams,
  RoutePreview,
  RouteSample,
  RouteSampleInput,
  RouteSampleListParams,
  RouteStats,
  SensitiveWord,
  SensitiveWordInput,
  SmartRouteSettings,
  SystemInfo,
  UsageParams,
  UsageResponse,
  UserCreateInput,
  UserGroup,
  UserGroupInput,
  UserListParams,
  UserUpdateInput,
  VectorSettings,
  VectorTestResult,
  WordListParams,
  ModelPrice,
  ConfigSnapshotRow,
  ConfigSnapshotDetail,
  CacheCheckResult,
  ModelPriceInput,
  PricingSettings
} from '@/types';

const A = '/api/admin';

export const overviewApi = {
  live: () => get<OverviewLive>(`${A}/overview/live`),
  usage: (range: RangeKey) => get<OverviewUsage>(`${A}/overview/usage`, { range }),
};

export const providersApi = {
  list: () => get<Provider[]>(`${A}/providers`),
  models: () => get<RoutableModel[]>(`${A}/models`),
};

export const accountsApi = {
  list: (params: AccountListParams) => get<ListResponse<Account>>(`${A}/accounts`, params),
  get: (id: number) => get<Account>(`${A}/accounts/${id}`),
  create: (body: AccountInput) => post<Account>(`${A}/accounts`, body),
  update: (id: number, body: AccountInput) => put<Account>(`${A}/accounts/${id}`, body),
  remove: (id: number) => del<void>(`${A}/accounts/${id}`),
  setEnabled: (id: number, enabled: boolean) => patch<Account>(`${A}/accounts/${id}/enabled`, { enabled }),
  resetHealth: (id: number) => post<Account>(`${A}/accounts/${id}/reset-health`),
  discover: (body: DiscoverInput) => post<{ models: string[] }>(`${A}/accounts/discover`, body),
  test: (body: AccountInput) => post<AccountTestResult>(`${A}/accounts/test`, body, { skipErrorToast: true }),
  cacheCheck: (id: number, model: string) => post<CacheCheckResult>(`${A}/accounts/${id}/cache-check`, { model }, { skipErrorToast: true }),
  testModel: (id: number, model: string) =>
    post<AccountTestResult>(`${A}/accounts/${id}/test-model`, { model }, { skipErrorToast: true }),
  updateMappings: (id: number, mappings: ModelMapping[]) => put<Account>(`${A}/accounts/${id}/mappings`, { mappings }),
};

export const modelGroupsApi = {
  list: (params?: { type?: string; q?: string } & PageParams) =>
    get<ListResponse<ModelGroup>>(`${A}/model-groups`, params),
  get: (id: number) => get<ModelGroup>(`${A}/model-groups/${id}`),
  create: (body: ModelGroupInput) => post<ModelGroup>(`${A}/model-groups`, body),
  update: (id: number, body: ModelGroupInput) => put<ModelGroup>(`${A}/model-groups/${id}`, body),
  remove: (id: number) => del<void>(`${A}/model-groups/${id}`),
};

export const usersApi = {
  list: (params: UserListParams) => get<ListResponse<AdminUser>>(`${A}/users`, params),
  create: (body: UserCreateInput) => post<AdminUser>(`${A}/users`, body),
  update: (id: number, body: UserUpdateInput) => put<AdminUser>(`${A}/users/${id}`, body),
  remove: (id: number) => del<void>(`${A}/users/${id}`),
  setEnabled: (id: number, enabled: boolean) => patch<AdminUser>(`${A}/users/${id}/enabled`, { enabled }),
  resetPassword: (id: number, password: string) => post<void>(`${A}/users/${id}/reset-password`, { password }),
  unlock: (id: number) => post<void>(`${A}/users/${id}/unlock`),
};

export const userGroupsApi = {
  list: (params?: PageParams & { q?: string }) => get<ListResponse<UserGroup>>(`${A}/user-groups`, params),
  create: (body: UserGroupInput) => post<UserGroup>(`${A}/user-groups`, body),
  update: (id: number, body: UserGroupInput) => put<UserGroup>(`${A}/user-groups/${id}`, body),
  remove: (id: number) => del<void>(`${A}/user-groups/${id}`),
  setEnabled: (id: number, enabled: boolean) => patch<UserGroup>(`${A}/user-groups/${id}/enabled`, { enabled }),
};

export const routeApi = {
  samples: (params: RouteSampleListParams) => get<ListResponse<RouteSample>>(`${A}/route/samples`, params),
  createSample: (body: RouteSampleInput) => post<RouteSample>(`${A}/route/samples`, body),
  updateSample: (id: number, body: RouteSampleInput) => put<RouteSample>(`${A}/route/samples/${id}`, body),
  removeSample: (id: number) => del<void>(`${A}/route/samples/${id}`),
  batchSamples: (items: { label: string; text: string; note?: string }[], build_vector: boolean) =>
    post<{ created: number }>(`${A}/route/samples/batch`, { items, build_vector }),
  build: (body: { ids?: number[]; all?: boolean }) => post<BuildResult>(`${A}/route/samples/build`, body),
  preview: (text: string) => post<RoutePreview>(`${A}/route/preview`, { text }),
  decisions: (params: RouteDecisionListParams) => get<ListResponse<RouteDecision>>(`${A}/route/decisions`, params),
  decision: (id: number) => get<RouteDecision>(`${A}/route/decisions/${id}`),
  stats: (range: RangeKey) => get<RouteStats>(`${A}/route/stats`, { range }),
};

export const complianceApi = {
  policyGroups: (params?: PageParams & { q?: string }) =>
    get<ListResponse<PolicyGroup>>(`${A}/compliance/policy-groups`, params),
  createPolicyGroup: (body: PolicyGroupInput) => post<PolicyGroup>(`${A}/compliance/policy-groups`, body),
  updatePolicyGroup: (id: number, body: PolicyGroupInput) =>
    put<PolicyGroup>(`${A}/compliance/policy-groups/${id}`, body),
  removePolicyGroup: (id: number) => del<void>(`${A}/compliance/policy-groups/${id}`),
  setPolicyGroupEnabled: (id: number, enabled: boolean) =>
    patch<PolicyGroup>(`${A}/compliance/policy-groups/${id}/enabled`, { enabled }),

  words: (params: WordListParams) => get<ListResponse<SensitiveWord>>(`${A}/compliance/words`, params),
  createWord: (body: SensitiveWordInput) => post<SensitiveWord>(`${A}/compliance/words`, body),
  updateWord: (id: number, body: SensitiveWordInput) => put<SensitiveWord>(`${A}/compliance/words/${id}`, body),
  removeWord: (id: number) => del<void>(`${A}/compliance/words/${id}`),
  setWordEnabled: (id: number, enabled: boolean) =>
    patch<SensitiveWord>(`${A}/compliance/words/${id}/enabled`, { enabled }),
  batchWords: (policy_group_id: number, words: string[]) =>
    post<{ created: number }>(`${A}/compliance/words/batch`, { policy_group_id, words }),

  samples: (params: PageParams & { policy_group_id?: number; q?: string; vectorized?: boolean }) =>
    get<ListResponse<ComplianceSample>>(`${A}/compliance/samples`, params),
  createSample: (body: ComplianceSampleInput) => post<ComplianceSample>(`${A}/compliance/samples`, body),
  updateSample: (id: number, body: ComplianceSampleInput) =>
    put<ComplianceSample>(`${A}/compliance/samples/${id}`, body),
  removeSample: (id: number) => del<void>(`${A}/compliance/samples/${id}`),
  buildSamples: (body: { ids?: number[]; all?: boolean }) => post<BuildResult>(`${A}/compliance/samples/build`, body),

  auditLogs: (params: AuditLogListParams) => get<ListResponse<AuditLog>>(`${A}/compliance/audit-logs`, params),
  auditLog: (id: number) => get<AuditLog>(`${A}/compliance/audit-logs/${id}`),
  test: (text: string) => post<ComplianceTestResult>(`${A}/compliance/test`, { text }),
};

export const logsApi = {
  list: (params: LogListParams) => get<ListResponse<CallLog>>(`${A}/logs`, params),
  get: (id: number) => get<CallLog>(`${A}/logs/${id}`),
  filters: () => get<LogFilters>(`${A}/logs/filters`),
};

export const usageApi = {
  query: (params: UsageParams) => get<UsageResponse>(`${A}/usage`, params),
};

export const configSnapshotsApi = {
  list: () => get<{ items: ConfigSnapshotRow[]; total: number }>(`${A}/config/snapshots`),
  create: (reason: string) => post<unknown>(`${A}/config/snapshots`, { reason }),
  get: (id: number) => get<ConfigSnapshotDetail>(`${A}/config/snapshots/${id}`),
  restore: (id: number) => post<{ restored: number }>(`${A}/config/snapshots/${id}/restore`, {}),
};

export const pricesApi = {
  list: (q?: string) => get<{ items: ModelPrice[]; total: number; builtin_updated: string; currency: string }>(`${A}/prices`, q ? { q } : undefined),
  create: (body: ModelPriceInput) => post<ModelPrice>(`${A}/prices`, body),
  update: (id: number, body: ModelPriceInput) => put<ModelPrice>(`${A}/prices/${id}`, body),
  remove: (id: number) => del(`${A}/prices/${id}`),
  resetBuiltin: () => post<{ builtin_updated: string }>(`${A}/prices/reset-builtin`, {}),
};

export const settingsApi = {
  all: () => get<AllSettings>(`${A}/settings`),
  savePricing: (body: PricingSettings) => put<PricingSettings>(`${A}/settings/pricing`, body),
  saveBasic: (body: BasicSettings) => put<BasicSettings>(`${A}/settings/basic`, body),
  savePerformance: (body: PerformanceSettings) => put<PerformanceSettings>(`${A}/settings/performance`, body),
  saveVector: (body: VectorSettings) => put<VectorSettings>(`${A}/settings/vector`, body),
  testVector: (body: VectorSettings) => post<VectorTestResult>(`${A}/settings/vector/test`, body, { skipErrorToast: true }),
  saveSmartRoute: (body: SmartRouteSettings) => put<SmartRouteSettings>(`${A}/settings/smart_route`, body),
  saveCompliance: (body: ComplianceSettings) => put<ComplianceSettings>(`${A}/settings/compliance`, body),
  saveElasticsearch: (body: ElasticsearchSettings) =>
    put<ElasticsearchSettings>(`${A}/settings/elasticsearch`, body),
  testElasticsearch: (body: ElasticsearchSettings) =>
    post<EsTestResult>(`${A}/settings/elasticsearch/test`, body, { skipErrorToast: true }),
  esStatus: () => get<EsStatus>(`${A}/settings/elasticsearch/status`),
};

export const systemApi = {
  info: () => get<SystemInfo>(`${A}/system/info`),
};
