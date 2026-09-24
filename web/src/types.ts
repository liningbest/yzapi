// Shared TypeScript types mirroring docs/api.md

export type Role = 'admin' | 'user';
export type ModelType = 'text' | 'image' | 'embedding' | 'custom';
export type Protocol =
  | 'openai-completions'
  | 'openai-responses'
  | 'gemini-generate'
  | 'anthropic-messages'
  | 'openai-embeddings'
  | 'openai-images'
  | 'custom-json';
export type Health = 'available' | 'cooling' | 'unavailable';
export type RangeKey = '24h' | '7d' | '30d' | 'custom';
export type ModelKind = 'model' | 'virtual' | 'group';
export type LogResult = 'success' | 'client_error' | 'upstream_error' | 'blocked' | 'rate_limited';

export interface ApiError {
  error: string;
  code?: string;
}

export interface ListResponse<T> {
  items: T[];
  total: number;
}

export interface PageParams {
  page?: number;
  page_size?: number;
}

export interface RangeParams {
  range?: RangeKey;
  from?: string;
  to?: string;
}

// ---------- Auth ----------
export interface User {
  id: number;
  username: string;
  role: Role;
  group_id: number;
  group_name: string;
  enabled: boolean;
  locked: boolean;
  must_change_password: boolean;
  note: string;
  last_login_at: string | null;
  created_at: string;
}

export interface LoginResponse {
  token: string;
  user: User;
}

export interface PublicInfo {
  site_name: string;
  version: string;
  base_url: string;
  currency?: string;
}

// ---------- Overview ----------
export interface LiveAccount {
  id: number;
  name: string;
  provider: string;
  priority: number;
  current: number;
  limit: number;
  util: number;
  health: Health;
  cooldown_until: string | null;
  last_error: string;
}

export interface LiveGroup {
  id: number;
  name: string;
  current: number;
  limit: number;
  util: number;
  tokens_used: number;
  token_quota: number;
}

export interface OverviewLive {
  active_users: number;
  streams: number;
  inflight: number;
  limit: number;
  waiting: number;
  queue_size: number;
  accounts: LiveAccount[];
  groups: LiveGroup[];
}

export interface TrendPoint {
  time: string;
  total_tokens: number;
  prompt_tokens: number;
  completion_tokens: number;
  cached_tokens: number;
  requests: number;
}

export interface OverviewUsage {
  tokens: { total: number; prompt: number; completion: number; cached: number; cache_rate: number };
  cost: { total: number; currency: string };
  requests: { total: number; success: number; failed: number; fail_rate: number };
  active_users: number;
  active_keys: number;
  trend: TrendPoint[];
}

// ---------- Providers / models ----------
export interface ProviderAccountType {
  key: string;
  name: string;
  base_url?: string;
  /** When set, this endpoint only speaks these protocols (e.g. a provider's Anthropic-compatible base). */
  protocols?: Protocol[];
}

export interface Provider {
  key: string;
  name: string;
  base_url: string;
  types: ModelType[];
  protocols: Protocol[];
  account_types?: ProviderAccountType[];
  auth_header: 'bearer' | 'x-api-key';
  discover: boolean;
  custom: boolean;
  icon: string;
  /** Pre-filled endpoint paths for custom (non-chat JSON) accounts. */
  endpoints?: string[];
  /** Preset mappings for providers without a model-listing API. */
  default_mappings?: { request_model: string; upstream_model: string }[];
}

export interface RoutableModel {
  name: string;
  type: ModelType;
  kind: ModelKind;
  provider: string;
  accounts?: number;
  models?: string[];
}

// ---------- Accounts ----------
export interface ModelMapping {
  id?: number;
  request_model: string;
  upstream_model: string;
}

export interface Account {
  id: number;
  name: string;
  provider: string;
  account_type: string;
  type: ModelType;
  base_url: string;
  has_key: boolean;
  api_key_masked: string;
  protocols: Protocol[];
  mappings: ModelMapping[];
  test_model: string;
  priority: number;
  weight: number;
  max_concurrency: number;
  /** Unmapped model names are forwarded to this account unchanged. */
  passthrough_models: boolean;
  /** Custom (non-chat JSON) accounts: client paths under /v1 this account serves. */
  endpoints: string[];
  enabled: boolean;
  health: Health;
  cooldown_until: string | null;
  last_error: string;
  note: string;
  created_at: string;
  updated_at: string;
}

export interface AccountInput {
  name: string;
  provider: string;
  account_type?: string;
  type: ModelType;
  base_url: string;
  api_key?: string;
  protocols: Protocol[];
  mappings: ModelMapping[];
  test_model?: string;
  priority: number;
  weight: number;
  max_concurrency: number;
  passthrough_models?: boolean;
  endpoints?: string[];
  enabled: boolean;
  note?: string;
  skip_test?: boolean;
  account_id?: number;
}

export interface AccountListParams extends PageParams {
  provider?: string;
  type?: ModelType;
  protocol?: Protocol;
  enabled?: boolean;
  health?: Health;
  q?: string;
}

export interface DiscoverInput {
  provider: string;
  account_type?: string;
  protocols?: string[];
  base_url: string;
  api_key?: string;
  account_id?: number;
}

export interface AccountTestResult {
  ok: boolean;
  latency_ms: number;
  message: string;
  model: string;
}

// ---------- Model groups ----------
export interface ModelGroup {
  id: number;
  name: string;
  type: ModelType;
  models: string[];
  note: string;
  created_at: string;
  updated_at: string;
  in_use_by_route: boolean;
  route_roles?: ('simple' | 'complex')[];
}

export interface ModelGroupInput {
  name: string;
  type: ModelType;
  models: string[];
  note?: string;
}

// ---------- Users ----------
export interface AdminUser extends User {
  api_keys_count: number;
  /** Computed server-side over all enabled admins (not just the current page). */
  is_last_admin: boolean;
}

export interface UserListParams extends PageParams {
  role?: Role;
  group_id?: number;
  enabled?: boolean;
  q?: string;
}

export interface UserCreateInput {
  username: string;
  password: string;
  group_id: number;
  role: Role;
  note?: string;
}

export interface UserUpdateInput {
  group_id: number;
  role: Role;
  note?: string;
}

// ---------- User groups ----------
export interface UserGroup {
  id: number;
  name: string;
  max_concurrency: number;
  key_max_concurrency: number;
  token_quota: number;
  tokens_per_minute: number;
  requests_per_minute: number;
  is_default: boolean;
  enabled: boolean;
  note: string;
  model_group_ids: number[];
  model_groups: { id: number; name: string }[];
  members_count: number;
  tokens_used_month: number;
  created_at: string;
}

export interface UserGroupInput {
  name: string;
  max_concurrency: number;
  key_max_concurrency: number;
  token_quota: number;
  tokens_per_minute: number;
  requests_per_minute: number;
  model_group_ids: number[];
  enabled: boolean;
  note?: string;
}

// ---------- Smart route ----------
export type RouteLabel = 'simple' | 'complex';

export interface RouteSample {
  id: number;
  label: RouteLabel;
  text: string;
  threshold: number;
  note: string;
  vector_dim: number;
  vectorized: boolean;
  created_at: string;
}

export interface RouteSampleInput {
  label: RouteLabel;
  text: string;
  threshold?: number;
  note?: string;
  build_vector?: boolean;
}

export interface RouteSampleListParams extends PageParams {
  label?: RouteLabel;
  q?: string;
  vectorized?: boolean;
}

export interface BuildResult {
  built: number;
  failed: number;
  error?: string;
}

export interface RouteTopK {
  id: number;
  label: RouteLabel;
  text: string;
  score: number;
}

export interface RoutePreview {
  label: RouteLabel;
  source: string;
  confidence: number;
  group_id: number;
  group_name: string;
  models: string[];
  top_k: RouteTopK[];
  normalized: string;
  latency_ms: number;
}

export interface RouteDecision {
  id: number;
  request_id: string;
  label: RouteLabel;
  source: string;
  confidence: number;
  selected_model: string;
  model_group: string;
  normalized_text: string;
  top_k: RouteTopK[];
  request_type: string;
  total_tokens: number;
  latency_ms: number;
  failed: boolean;
  created_at: string;
}

export interface RouteDecisionListParams extends PageParams, RangeParams {
  label?: RouteLabel;
  source?: string;
  request_type?: string;
  model?: string;
  q?: string;
}

export interface KeyCount {
  key: string;
  count: number;
  tokens?: number;
}

export interface RouteStats {
  decisions: number;
  requests: number;
  failed: number;
  total_tokens: number;
  avg_tokens: number;
  latency_ms: number;
  by_label: KeyCount[];
  by_source: KeyCount[];
  by_model: KeyCount[];
  by_token_bucket: KeyCount[];
}

// ---------- Compliance ----------
export type PolicyAction = 'block' | 'audit';
export type RiskLevel = 'low' | 'medium' | 'high';

export interface PolicyGroup {
  id: number;
  name: string;
  action: PolicyAction;
  risk_level: RiskLevel;
  enabled: boolean;
  description: string;
  words_count: number;
  samples_count: number;
  created_at: string;
}

export interface PolicyGroupInput {
  name: string;
  action: PolicyAction;
  risk_level: RiskLevel;
  enabled: boolean;
  description?: string;
}

export interface PolicyGroupRef {
  id: number;
  name: string;
  action: PolicyAction;
  risk_level: RiskLevel;
}

export interface SensitiveWord {
  id: number;
  policy_group_id: number;
  policy_group: PolicyGroupRef | null;
  word: string;
  note: string;
  enabled: boolean;
  created_at: string;
}

export interface SensitiveWordInput {
  policy_group_id: number;
  word: string;
  note?: string;
  enabled: boolean;
}

export interface WordListParams extends PageParams {
  policy_group_id?: number;
  enabled?: boolean;
  q?: string;
}

export interface ComplianceSample {
  id: number;
  policy_group_id: number;
  policy_group: PolicyGroupRef | null;
  text: string;
  note: string;
  enabled: boolean;
  vector_dim: number;
  vectorized: boolean;
  created_at: string;
}

export interface ComplianceSampleInput {
  policy_group_id: number;
  text: string;
  note?: string;
  enabled: boolean;
  build_vector?: boolean;
}

export interface ComplianceHit {
  method: string;
  policy_group: string;
  evidence: string;
  score: number;
  action: PolicyAction;
  risk_level: RiskLevel;
}

export interface AuditLog {
  id: number;
  request_id: string;
  user_id: number;
  username: string;
  request_model: string;
  protocol: string;
  action: PolicyAction;
  risk_level: RiskLevel;
  detect_method: string;
  policy_group_id: number;
  policy_group: string;
  evidence: string;
  confidence: number;
  status_code: number;
  hits: ComplianceHit[];
  snippet: string;
  created_at: string;
}

export interface AuditLogListParams extends PageParams, RangeParams {
  action?: PolicyAction;
  risk_level?: RiskLevel;
  detect_method?: string;
  policy_group_id?: number;
  q?: string;
}

export interface ComplianceTestResult {
  hit: boolean;
  block: boolean;
  hits: ComplianceHit[];
}

// ---------- Logs ----------
export type UsageStatus = 'confirmed' | 'partial' | 'unknown' | 'none';

export interface LogAttempt {
  account_id: number;
  account_name: string;
  provider: string;
  protocol: string;
  model: string;
  status_code: number;
  latency_ms: number;
  error: string;
}

export interface CallLog {
  id: number;
  request_id: string;
  user_id: number;
  username: string;
  group_id: number;
  group_name: string;
  api_key_id: number;
  api_key_name: string;
  account_id?: number;
  account_name?: string;
  provider?: string;
  request_model: string;
  upstream_model: string;
  model_group: string;
  api_type: ModelType | string;
  client_protocol: string;
  upstream_protocol: string;
  stream: boolean;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  cached_tokens: number;
  tokens_known: boolean;
  /** Request-level usage was re-derived from attempt records during an upgrade. */
  usage_corrected?: boolean;
  /** Stored cost in ledger micro-units (USD); use `cost` for display. */
  cost_micros?: number;
  /** false when an attempt had no price or its usage is unknown / partial: the figure is a lower bound. */
  cost_known?: boolean;
  /** Estimated cost in the display currency; 0 when cost_unverified. */
  cost?: number;
  /** The row's stored currency could not be established after an upgrade; its amount is not counted anywhere. */
  cost_unverified?: boolean;
  usage_status?: UsageStatus;
  est_prompt_tokens?: number;
  result: LogResult;
  status_code: number;
  latency_ms: number;
  upstream_latency_ms: number;
  /** Time blocked writing to the client (streams); upstream wait ≈ upstream_latency_ms − client_write_ms. */
  client_write_ms?: number;
  /** Upstream response headers arrived, from request start (includes queueing and earlier attempts). */
  first_byte_ms: number;
  /** First event carrying generated content written to the client, from request start. */
  first_content_ms?: number;
  /** Time spent waiting for gateway / group / key concurrency slots. */
  queue_wait_ms?: number;
  /** Coding client detected from User-Agent / headers (claude-code, codex, opencode, ...). */
  client?: string;
  user_agent?: string;
  error: string;
  attempts: LogAttempt[];
  route_label: string;
  client_ip: string;
  created_at: string;
}

export interface LogListParams extends PageParams, RangeParams {
  user_id?: number;
  group_id?: number;
  account_id?: number;
  provider?: string;
  api_type?: string;
  result?: LogResult;
  status_code?: number;
  model?: string;
  q?: string;
}

export interface LogFilters {
  users: { id: number; username: string }[];
  accounts: { id: number; name: string; provider: string }[];
  clients?: string[];
  providers: string[];
  models: string[];
}

// ---------- Usage ----------
export interface UsageSummary {
  requests: number;
  success: number;
  failed: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  cached_tokens: number;
  cost: number;
  /** Requests whose cost currency is unverified; excluded from cost. */
  cost_unverified?: number;
}

export interface UsageTrendPoint {
  time: string;
  requests: number;
  total_tokens: number;
  series: Record<string, number>;
}

export interface UsageDim {
  key: string | number;
  name: string;
  requests: number;
  /** Upstream attempts booked on this dimension; tokens follow attempts, requests follow the final answer. */
  attempts?: number;
  total_tokens: number;
  cached_tokens: number;
  cost?: number;
}

export interface UsageResponse {
  summary: UsageSummary;
  currency?: string;
  trend: UsageTrendPoint[];
  by_provider?: UsageDim[];
  by_model?: UsageDim[];
  by_model_group?: UsageDim[];
  by_account?: UsageDim[];
  by_group?: UsageDim[];
  by_user?: UsageDim[];
  by_api_key?: UsageDim[];
  /** Aggregated from raw call logs (retention window), not from the hourly rollup. */
  by_client?: UsageDim[];
}

export interface UsageParams extends RangeParams {
  user_id?: number;
  group_id?: number;
  account_id?: number;
  provider?: string;
  api_type?: string;
  model?: string;
  api_key_id?: number;
  group_by?: 'model' | 'api_key';
}

// ---------- Settings ----------
export interface BasicSettings {
  base_url: string;
  log_retention_days: number;
  protocol_conversion: boolean;
  site_name: string;
  reasoning_to_content?: boolean;
}

export interface PerformanceSettings {
  max_concurrency: number;
  queue_size: number;
  queue_timeout_sec: number;
  request_timeout_sec: number;
  stream_idle_timeout_sec: number;
  max_body_kb: number;
  cooldown_sec: number;
  max_retries: number;
  upstream_connect_timeout_sec: number;
  max_body_memory_mb: number;
  vector_max_concurrency: number;
  vector_timeout_sec: number;
}

export interface VectorSettings {
  account_id: number;
  model: string;
}

export interface SmartRouteSettings {
  enabled: boolean;
  virtual_model: string;
  simple_group_id: number;
  complex_group_id: number;
  threshold: number;
  confidence_gap: number;
  top_k: number;
  // present in backend struct but not in api.md; optional
  rule_max_chars?: number;
  context_complex?: number;
}

export interface ComplianceSettings {
  enabled: boolean;
  semantic_threshold: number;
  check_system_prompt?: boolean;
  on_failure?: 'allow' | 'block';
}

export interface ElasticsearchSettings {
  enabled: boolean;
  url: string;
  auth_type: 'apikey' | 'basic';
  api_key: string;
  username: string;
  password: string;
  index_prefix: string;
  request_kb: number;
  response_kb: number;
  retention_days: number;
}

export interface ConfigSnapshotRow {
  id: number;
  actor: string;
  reason: string;
  created_at: string;
}

export interface ConfigSnapshotDetail extends ConfigSnapshotRow {
  accounts: { id: number; name: string; provider: string; base_url: string; enabled: boolean; mappings: number; priority: number; weight: number }[];
  model_groups: { id: number; name: string; type: string; models: string[] }[];
  settings: Record<string, string>;
  meta: Record<string, number>;
}

export interface PricingSettings {
  currency: 'CNY' | 'USD';
  usd_to_cny: number;
}

export interface ModelPrice {
  id: number;
  pattern: string;
  provider: string;
  input_per_m: number;
  output_per_m: number;
  cached_input_per_m: number;
  cache_write_per_m: number;
  currency: 'USD' | 'CNY';
  builtin: boolean;
  enabled: boolean;
  note: string;
  /** '' for manual / built-in rows, otherwise the import source ('litellm', 'easycpa', ...). */
  source?: string;
  source_date?: string;
  /** Set once an administrator changed the row by hand; imports keep such rows unless told to overwrite. */
  edited?: boolean;
  updated_at: string;
}

export interface PriceImportChange {
  action: 'new' | 'update' | 'keep';
  pattern: string;
  provider: string;
  old?: [number, number, number, number];
  new: [number, number, number, number];
  old_currency?: string;
  new_currency: string;
  currency_changed?: boolean;
  reason?: 'manual' | 'currency' | string;
}

/** Restore reply: accounts restored without a key, and price rows a pre-1.0.27 snapshot lost to validation or de-duplication. */
export interface ConfigRestoreResult {
  restored: number;
  missing_keys?: string[];
  price_rows_skipped?: string[];
  price_rows_merged?: number;
}

export interface PriceImportOptions {
  overwrite_edited: boolean;
  overwrite_currency: boolean;
}

export interface PriceImportPlan {
  source: string;
  date: string;
  total: number;
  new: number;
  updated: number;
  same: number;
  kept: number;
  skipped: number;
  invalid: number;
  invalid_rows?: string[];
  duplicates: number;
  changes: PriceImportChange[];
}

/** A preview is bound to a plan_id (single use, expires_in seconds); apply takes that id. */
export interface PriceImportResult {
  applied: boolean;
  plan_id: string;
  sha256: string;
  origin: string;
  options: PriceImportOptions;
  expires_in?: number;
  plan: PriceImportPlan;
}

export type ModelPriceInput = Omit<ModelPrice, 'id' | 'builtin' | 'updated_at'>;

export interface BackupSettings {
  enabled: boolean;
  hour_local: number;
  keep_count: number;
}

export interface BackupInfo {
  name: string;
  size: number;
  created_at: string;
}

export interface BackupList {
  items: BackupInfo[];
  pending_restore: boolean;
  db_driver: string;
  supported: boolean;
  unsupported_reason?: string;
}

export interface AllSettings {
  backup: BackupSettings;
  pricing: PricingSettings;
  basic: BasicSettings;
  performance: PerformanceSettings;
  vector: VectorSettings;
  smart_route: SmartRouteSettings;
  compliance: ComplianceSettings;
  elasticsearch: ElasticsearchSettings;
}

export interface VectorTestResult {
  ok: boolean;
  dim: number;
  latency_ms: number;
  message: string;
}

export interface EsTestResult {
  ok: boolean;
  version: string;
  message: string;
}

export interface EsStatus {
  configured: boolean;
  queue_count: number;
  queue_bytes: number;
  dropped: number;
  last_success_at: string | null;
  failing_since: string | null;
}

export interface SystemInfo {
  version: string;
  go_version: string;
  db_driver: string;
  uptime_sec: number;
  started_at: string;
  data_dir: string;
}

// ---------- User console ----------
export interface UserModel {
  name: string;
  type: ModelType;
  kind: ModelKind;
  provider: string;
  models?: string[];
  /** Custom (non-chat JSON) models: client paths under /v1 on which the model is called. */
  endpoints?: string[];
}

export interface UserModelsResponse {
  base_url: string;
  models: UserModel[];
}

export interface ApiKey {
  id: number;
  name: string;
  prefix: string;
  suffix: string;
  masked: string;
  enabled: boolean;
  last_used_at: string | null;
  created_at: string;
  /** null = never expires */
  expires_at: string | null;
  expired: boolean;
  /** empty = every model the user's group allows */
  allowed_models: string[];
  /** trailing-60s limits, 0 = unlimited */
  tokens_per_minute: number;
  requests_per_minute: number;
}

export interface KeyInput {
  name: string;
  expires_at?: string | null;
  allowed_models?: string[];
  tokens_per_minute?: number;
  requests_per_minute?: number;
}

export interface CacheCheckResult {
  ok: boolean;
  hit?: boolean;
  protocol: string;
  model: string;
  message: string;
  first?: { prompt_tokens: number; cached_tokens: number; cache_write_tokens: number; latency_ms: number };
  second?: { prompt_tokens: number; cached_tokens: number; cache_write_tokens: number; latency_ms: number };
}

export interface CreateKeyResponse {
  key: string;
  item: ApiKey;
}

export interface UserUsageParams extends RangeParams {
  api_key_id?: number;
  model?: string;
  api_type?: string;
  group_by?: 'model' | 'api_key';
}

export interface UserLogParams extends PageParams, RangeParams {
  api_key_id?: number;
  model?: string;
  result?: LogResult;
  q?: string;
}

export interface MyGroup {
  id: number;
  name: string;
  max_concurrency: number;
  key_max_concurrency: number;
  token_quota: number;
  tokens_per_minute: number;
  requests_per_minute: number;
  tokens_used_month: number;
  model_groups: { id: number; name: string; models: string[] }[];
}
