import { useMemo, useState } from 'react';
import { Button, Card, Input, Select, Space, Table, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { ArrowRightOutlined, EyeOutlined, ReloadOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { useSiteStore } from '@/stores/site';
import { logsApi } from '@/api';
import { EmptyState, FilterBar, PageHeader, ProviderAvatar, RangeSelector, ResultTag, StatusCodeTag, TimeCell, UsageStatusTag } from '@/components';
import { useRange } from '@/hooks/useRange';
import { useTableQuery } from '@/hooks/useTableQuery';
import type { CallLog, LogResult } from '@/types';
import { MODEL_TYPES } from '@/utils/constants';
import { formatMs, formatNumber, formatTokens, shortId, formatMoney } from '@/utils/format';
import LogDetailDrawer from './logs/LogDetailDrawer';

interface Filters {
  user_id?: number;
  account_id?: number;
  provider?: string;
  api_type?: string;
  result?: LogResult;
  status_code?: number;
  model?: string;
  q?: string;
}

const RESULTS: LogResult[] = ['success', 'client_error', 'upstream_error', 'blocked', 'rate_limited'];
const STATUS_CODES = [200, 400, 401, 403, 404, 429, 500, 502, 503, 504];

function Small({ children, secondary }: { children: React.ReactNode; secondary?: boolean }) {
  return (
    <Typography.Text type={secondary ? 'secondary' : undefined} style={{ fontSize: 12, display: 'block', lineHeight: 1.5 }}>
      {children}
    </Typography.Text>
  );
}

export default function Logs() {
  const { t } = useTranslation(['logs', 'common']);
  const currency = useSiteStore((s) => s.currency);
  const { filters, setFilters, params, pagination } = useTableQuery<Filters>({});
  const { range, setRange, custom, setCustom, params: rangeParams } = useRange('24h');
  const [detail, setDetail] = useState<CallLog | null>(null);

  const filterOpts = useQuery({ queryKey: ['logs', 'filters'], queryFn: logsApi.filters, staleTime: 60_000 });

  const queryParams = useMemo(() => ({ ...params, ...rangeParams }), [params, rangeParams]);
  const list = useQuery({
    queryKey: ['logs', 'list', queryParams],
    queryFn: () => logsApi.list(queryParams),
    placeholderData: (prev) => prev,
  });

  const userOptions = useMemo(
    () => (filterOpts.data?.users ?? []).map((u) => ({ value: u.id, label: u.username })),
    [filterOpts.data],
  );
  const accountOptions = useMemo(
    () =>
      (filterOpts.data?.accounts ?? []).map((a) => ({
        value: a.id,
        label: a.name,
        provider: a.provider,
      })),
    [filterOpts.data],
  );
  const providerOptions = useMemo(
    () => (filterOpts.data?.providers ?? []).map((p) => ({ value: p, label: p })),
    [filterOpts.data],
  );
  const modelOptions = useMemo(
    () => (filterOpts.data?.models ?? []).map((m) => ({ value: m, label: m })),
    [filterOpts.data],
  );

  const columns: ColumnsType<CallLog> = [
    {
      title: t('common:common.requestId'),
      dataIndex: 'request_id',
      width: 130,
      render: (v: string) => (
        <Typography.Text
          className="yz-mono"
          copyable={{ text: v, tooltips: [t('common:action.copy'), t('common:action.copied')] }}
          onClick={(e) => e.stopPropagation()}
        >
          <Tooltip title={v}>{shortId(v)}</Tooltip>
        </Typography.Text>
      ),
    },
    {
      title: `${t('common:common.provider')} / ${t('common:common.account')}`,
      key: 'account',
      width: 180,
      render: (_, r) =>
        r.provider || r.account_name ? (
          <Space size={8}>
            <ProviderAvatar provider={r.provider} size={24} />
            <div>
              <Small>{r.account_name || '-'}</Small>
              <Small secondary>{r.provider || '-'}</Small>
            </div>
          </Space>
        ) : (
          <Typography.Text type="secondary">-</Typography.Text>
        ),
    },
    {
      title: t('common:common.model'),
      key: 'model',
      width: 200,
      render: (_, r) => (
        <div>
          <Small>{r.request_model || '-'}</Small>
          {r.upstream_model && r.upstream_model !== r.request_model ? (
            <Small secondary>
              <ArrowRightOutlined style={{ fontSize: 10, marginRight: 4 }} />
              {r.upstream_model}
            </Small>
          ) : null}
        </div>
      ),
    },
    {
      title: `${t('common:common.user')} / ${t('common:common.userGroup')}`,
      key: 'user',
      width: 150,
      render: (_, r) => (
        <div>
          <Small>{r.username || '-'}</Small>
          <Small secondary>{r.group_name || '-'}</Small>
        </div>
      ),
    },
    {
      title: t('common:common.tokens'),
      key: 'tokens',
      width: 170,
      align: 'right',
      render: (_, r) =>
        r.usage_status === 'none' || (r.usage_status !== 'partial' && r.tokens_known === false) ? (
          <UsageStatusTag status={r.usage_status ?? 'unknown'} estPrompt={r.est_prompt_tokens} />
        ) : (
          <Tooltip
            title={
              <div>
                <div>
                  {t('common:common.promptTokens')}: {formatNumber(r.prompt_tokens)}
                </div>
                <div>
                  {t('common:common.completionTokens')}: {formatNumber(r.completion_tokens)}
                </div>
                <div>
                  {t('common:common.totalTokens')}: {formatNumber(r.total_tokens)}
                </div>
                <div>
                  {t('common:common.cachedTokens')}: {formatNumber(r.cached_tokens)}
                </div>
              </div>
            }
          >
            <div style={{ fontVariantNumeric: 'tabular-nums', lineHeight: 1.4 }}>
              <div style={{ fontWeight: 600 }}>{formatTokens(r.total_tokens)}</div>
              <div style={{ fontSize: 11, color: 'var(--yz-text-secondary)' }}>
                {formatTokens(r.prompt_tokens)} / {formatTokens(r.completion_tokens)}
                {r.cached_tokens > 0 ? ` · ${t('logs:cachedShort')} ${formatTokens(r.cached_tokens)}` : ''}
              </div>
            </div>
          </Tooltip>
        ),
    },
    {
      title: t('common:common.cost'),
      key: 'cost',
      width: 90,
      align: 'right',
      render: (_, r) =>
        r.cost_known === false && (r.cost ?? 0) > 0 ? (
          <Tooltip title={t('logs:costPartial')}>
            <span style={{ fontVariantNumeric: 'tabular-nums', borderBottom: '1px dotted currentColor', cursor: 'help' }}>≥ {formatMoney(r.cost ?? 0, currency)}</span>
          </Tooltip>
        ) : r.cost_known === false && (r.total_tokens ?? 0) > 0 ? (
          <Tooltip title={t('logs:costUnpriced')}>
            <span style={{ color: 'var(--yz-text-secondary)' }}>{t('logs:unpriced')}</span>
          </Tooltip>
        ) : (
          <span style={{ fontVariantNumeric: 'tabular-nums' }}>{formatMoney(r.cost ?? 0, currency)}</span>
        ),
    },
    {
      title: t('common:common.result'),
      dataIndex: 'result',
      width: 110,
      render: (v: LogResult) => <ResultTag result={v} />,
    },
    {
      title: t('common:common.statusCode'),
      dataIndex: 'status_code',
      width: 90,
      align: 'center',
      render: (v: number) => <StatusCodeTag code={v} />,
    },
    {
      title: t('common:common.totalLatency'),
      dataIndex: 'latency_ms',
      width: 100,
      align: 'right',
      render: (v: number) => <span style={{ fontVariantNumeric: 'tabular-nums' }}>{formatMs(v)}</span>,
    },
    {
      title: t('common:common.upstreamLatency'),
      dataIndex: 'upstream_latency_ms',
      width: 100,
      align: 'right',
      render: (v: number) => <span style={{ fontVariantNumeric: 'tabular-nums' }}>{formatMs(v)}</span>,
    },
    {
      title: t('common:common.time'),
      dataIndex: 'created_at',
      width: 120,
      render: (v: string) => <TimeCell value={v} />,
    },
    {
      title: t('common:common.actions'),
      key: 'actions',
      width: 60,
      fixed: 'right',
      align: 'center',
      render: (_, r) => (
        <Tooltip title={t('common:action.view')}>
          <Button
            type="text"
            size="small"
            icon={<EyeOutlined />}
            onClick={(e) => {
              e.stopPropagation();
              setDetail(r);
            }}
          />
        </Tooltip>
      ),
    },
  ];

  return (
    <div>
      <PageHeader
        title={t('logs:title')}
        subtitle={t('logs:subtitle')}
        extra={
          <Button icon={<ReloadOutlined />} onClick={() => void list.refetch()} loading={list.isFetching}>
            {t('common:action.refresh')}
          </Button>
        }
      />
      <Card className="yz-card">
        <FilterBar
          extra={
            <RangeSelector
              value={range}
              onChange={setRange}
              allowCustom
              custom={custom}
              onCustomChange={setCustom}
            />
          }
        >
          <Select
            style={{ width: 160 }}
            allowClear
            showSearch
            optionFilterProp="label"
            placeholder={t('common:common.user')}
            value={filters.user_id}
            onChange={(v) => setFilters({ user_id: v })}
            options={userOptions}
            loading={filterOpts.isLoading}
          />
          <Select
            style={{ width: 190 }}
            allowClear
            showSearch
            optionFilterProp="label"
            placeholder={t('common:common.account')}
            value={filters.account_id}
            onChange={(v) => setFilters({ account_id: v })}
            options={accountOptions}
            loading={filterOpts.isLoading}
            optionRender={(opt) => (
              <Space size={6}>
                <ProviderAvatar provider={opt.data.provider} size={18} />
                <span>{opt.data.label}</span>
              </Space>
            )}
          />
          <Select
            style={{ width: 160 }}
            allowClear
            showSearch
            optionFilterProp="label"
            placeholder={t('common:common.provider')}
            value={filters.provider}
            onChange={(v) => setFilters({ provider: v })}
            options={providerOptions}
            optionRender={(opt) => (
              <Space size={6}>
                <ProviderAvatar provider={String(opt.value)} size={18} />
                <span>{opt.data.label}</span>
              </Space>
            )}
          />
          <Select
            style={{ width: 130 }}
            allowClear
            placeholder={t('common:common.apiType')}
            value={filters.api_type}
            onChange={(v) => setFilters({ api_type: v })}
            options={MODEL_TYPES.map((m) => ({ value: m, label: t(`common:type.${m}`) }))}
          />
          <Select
            style={{ width: 140 }}
            allowClear
            placeholder={t('common:common.result')}
            value={filters.result}
            onChange={(v) => setFilters({ result: v })}
            options={RESULTS.map((r) => ({ value: r, label: t(`common:result.${r}`) }))}
          />
          <Select
            style={{ width: 120 }}
            allowClear
            showSearch
            placeholder={t('common:common.statusCode')}
            value={filters.status_code}
            onChange={(v) => setFilters({ status_code: v })}
            options={STATUS_CODES.map((c) => ({ value: c, label: String(c) }))}
          />
          <Select
            style={{ width: 200 }}
            allowClear
            showSearch
            optionFilterProp="label"
            placeholder={t('common:common.model')}
            value={filters.model}
            onChange={(v) => setFilters({ model: v })}
            options={modelOptions}
          />
          <Input.Search
            style={{ width: 280 }}
            allowClear
            placeholder={t('logs:searchPlaceholder')}
            onSearch={(v) => setFilters({ q: v.trim() || undefined })}
          />
        </FilterBar>
        <Table<CallLog>
          className="yz-table"
          size="middle"
          rowKey="id"
          columns={columns}
          dataSource={list.data?.items ?? []}
          loading={list.isLoading}
          scroll={{ x: 'max-content' }}
          pagination={pagination(list.data?.total)}
          rowClassName="yz-clickable-row"
          onRow={(r) => ({ onClick: () => setDetail(r) })}
          locale={{ emptyText: <EmptyState title={t('logs:empty')} hint={t('logs:emptyHint')} /> }}
        />
      </Card>
      <LogDetailDrawer open={Boolean(detail)} log={detail} onClose={() => setDetail(null)} />
    </div>
  );
}
