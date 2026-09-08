import { useMemo, useState } from 'react';
import { Button, Card, Descriptions, Drawer, Input, Select, Space, Table, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { EyeOutlined } from '@ant-design/icons';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { userApi } from '@/api';
import { EmptyState, FilterBar, NeutralTag, PageHeader, RangeSelector, ResultTag, SectionTitle, TimeCell, TypeTag } from '@/components';
import { useIsMobile } from '@/hooks/useMediaQuery';
import { useRange } from '@/hooks/useRange';
import { useTableQuery } from '@/hooks/useTableQuery';
import type { CallLog, LogResult, UserLogParams } from '@/types';
import { formatDateTime, formatMs, formatNumber, shortId } from '@/utils/format';

const RESULTS: LogResult[] = ['success', 'client_error', 'upstream_error', 'blocked', 'rate_limited'];

interface Filters {
  api_key_id?: number;
  model?: string;
  result?: LogResult;
  q?: string;
}

function TokenCell({ log, unknownLabel, inLabel, outLabel }: { log: CallLog; unknownLabel: string; inLabel: string; outLabel: string }) {
  if (log.tokens_known === false) return <NeutralTag>{unknownLabel}</NeutralTag>;
  return (
    <div style={{ lineHeight: 1.3, fontVariantNumeric: 'tabular-nums' }}>
      <Typography.Text strong>{formatNumber(log.total_tokens)}</Typography.Text>
      <div style={{ fontSize: 11.5, color: 'var(--yz-text-secondary)', whiteSpace: 'nowrap' }}>
        {inLabel} {formatNumber(log.prompt_tokens)} / {outLabel} {formatNumber(log.completion_tokens)}
      </div>
    </div>
  );
}

export default function Logs() {
  const { t } = useTranslation(['console', 'common']);
  const mobile = useIsMobile();
  const tq = useTableQuery<Filters>({});
  const { range, setRange, params: rangeParams } = useRange('24h');
  const [modelInput, setModelInput] = useState('');
  const [selected, setSelected] = useState<CallLog | null>(null);

  const keys = useQuery({ queryKey: ['user', 'keys'], queryFn: userApi.keys });

  const params = useMemo<UserLogParams>(() => ({ ...tq.params, ...rangeParams }), [tq.params, rangeParams]);
  const logs = useQuery({
    queryKey: ['user', 'logs', params],
    queryFn: () => userApi.logs(params),
    placeholderData: keepPreviousData,
  });

  const unknownLabel = t('console:logs.tokensUnknown');
  const inLabel = t('console:logs.in');
  const outLabel = t('console:logs.out');

  const columns: ColumnsType<CallLog> = [
    {
      title: t('console:logs.columns.requestId'),
      dataIndex: 'request_id',
      width: 140,
      render: (id: string) => (
        <span onClick={(e) => e.stopPropagation()}>
          <Tooltip title={id}>
            <Typography.Text className="yz-mono" copyable={{ text: id }}>
              {shortId(id)}
            </Typography.Text>
          </Tooltip>
        </span>
      ),
    },
    {
      title: t('console:logs.columns.model'),
      dataIndex: 'request_model',
      render: (m: string, row) => (
        <div style={{ lineHeight: 1.3 }}>
          <div>{m || '-'}</div>
          {row.upstream_model && row.upstream_model !== m ? (
            <div style={{ fontSize: 11.5, color: 'var(--yz-text-secondary)' }}>{row.upstream_model}</div>
          ) : null}
        </div>
      ),
    },
    { title: t('console:logs.columns.apiKey'), dataIndex: 'api_key_name', render: (v: string) => v || '-' },
    {
      title: t('console:logs.columns.type'),
      dataIndex: 'api_type',
      width: 90,
      render: (v: string) => <TypeTag type={v} />,
    },
    {
      title: t('console:logs.columns.tokens'),
      key: 'tokens',
      align: 'right',
      render: (_, row) => <TokenCell log={row} unknownLabel={unknownLabel} inLabel={inLabel} outLabel={outLabel} />,
    },
    {
      title: t('console:logs.columns.result'),
      dataIndex: 'result',
      width: 110,
      render: (v: LogResult) => <ResultTag result={v} />,
    },
    {
      title: t('console:logs.columns.statusCode'),
      dataIndex: 'status_code',
      width: 80,
      align: 'center',
      render: (v: number) => <span className="yz-mono">{v || '-'}</span>,
    },
    {
      title: t('console:logs.columns.latency'),
      dataIndex: 'latency_ms',
      width: 100,
      align: 'right',
      render: (v: number) => <span style={{ fontVariantNumeric: 'tabular-nums' }}>{formatMs(v)}</span>,
    },
    {
      title: t('console:logs.columns.time'),
      dataIndex: 'created_at',
      width: 130,
      render: (v: string) => <TimeCell value={v} />,
    },
    {
      title: t('common:common.actions'),
      key: 'actions',
      width: 60,
      align: 'right',
      render: (_, row) => (
        <Tooltip title={t('common:action.view')}>
          <Button
            type="text"
            size="small"
            icon={<EyeOutlined />}
            onClick={(e) => {
              e.stopPropagation();
              setSelected(row);
            }}
          />
        </Tooltip>
      ),
    },
  ];

  const items = logs.data?.items ?? [];
  const showEmpty = !logs.isLoading && items.length === 0;

  return (
    <div>
      <PageHeader title={t('console:logs.title')} subtitle={t('console:logs.subtitle')} />

      <Card className="yz-card">
        <FilterBar
          extra={
            <Input.Search
              allowClear
              placeholder={t('console:logs.filter.q')}
              onSearch={(v) => tq.setFilters({ q: v.trim() || undefined })}
              style={{ width: 260 }}
            />
          }
        >
          <Select
            allowClear
            showSearch
            optionFilterProp="label"
            placeholder={t('console:logs.filter.apiKey')}
            value={tq.filters.api_key_id}
            onChange={(v) => tq.setFilters({ api_key_id: v })}
            loading={keys.isLoading}
            style={{ width: 220 }}
            options={(keys.data ?? []).map((k) => ({ value: k.id, label: `${k.name} (${k.masked})` }))}
          />
          <Input
            allowClear
            placeholder={t('console:logs.filter.model')}
            value={modelInput}
            onChange={(e) => {
              setModelInput(e.target.value);
              if (!e.target.value) tq.setFilters({ model: undefined });
            }}
            onPressEnter={() => tq.setFilters({ model: modelInput.trim() || undefined })}
            onBlur={() => {
              const v = modelInput.trim() || undefined;
              if (v !== tq.filters.model) tq.setFilters({ model: v });
            }}
            style={{ width: 180 }}
          />
          <Select
            allowClear
            placeholder={t('console:logs.filter.result')}
            value={tq.filters.result}
            onChange={(v) => tq.setFilters({ result: v })}
            style={{ width: 140 }}
            options={RESULTS.map((r) => ({ value: r, label: t(`common:result.${r}`) }))}
          />
          <RangeSelector value={range} onChange={setRange} />
        </FilterBar>

        {showEmpty ? (
          <EmptyState title={t('console:logs.empty')} hint={t('console:logs.emptyHint')} />
        ) : (
          <Table
            className="yz-table"
            size="middle"
            rowKey="id"
            columns={columns}
            dataSource={items}
            loading={logs.isLoading || logs.isFetching}
            pagination={tq.pagination(logs.data?.total)}
            scroll={{ x: 'max-content' }}
            rowClassName="yz-clickable-row"
            onRow={(row) => ({ onClick: () => setSelected(row) })}
          />
        )}
      </Card>

      <Drawer
        open={selected !== null}
        onClose={() => setSelected(null)}
        title={t('console:logs.detail')}
        width={mobile ? '100%' : 600}
        destroyOnClose
      >
        {selected ? (
          <>
            <Descriptions column={1} size="small" bordered styles={{ label: { width: 130 } }}>
              <Descriptions.Item label={t('console:logs.fields.requestId')}>
                <Typography.Text className="yz-mono" copyable>
                  {selected.request_id}
                </Typography.Text>
              </Descriptions.Item>
              <Descriptions.Item label={t('console:logs.fields.time')}>{formatDateTime(selected.created_at)}</Descriptions.Item>
              <Descriptions.Item label={t('console:logs.fields.apiKey')}>{selected.api_key_name || '-'}</Descriptions.Item>
              <Descriptions.Item label={t('console:logs.fields.requestModel')}>{selected.request_model || '-'}</Descriptions.Item>
              <Descriptions.Item label={t('console:logs.fields.upstreamModel')}>{selected.upstream_model || '-'}</Descriptions.Item>
              <Descriptions.Item label={t('console:logs.fields.apiType')}>
                <TypeTag type={selected.api_type} />
              </Descriptions.Item>
              <Descriptions.Item label={t('console:logs.fields.clientProtocol')}>
                {selected.client_protocol ? (
                  <Typography.Text code>{selected.client_protocol}</Typography.Text>
                ) : (
                  '-'
                )}
              </Descriptions.Item>
              <Descriptions.Item label={t('console:logs.fields.stream')}>
                {selected.stream ? t('common:common.yes') : t('common:common.no')}
              </Descriptions.Item>
              <Descriptions.Item label={t('console:logs.fields.tokens')}>
                {selected.tokens_known === false ? (
                  <NeutralTag>{unknownLabel}</NeutralTag>
                ) : (
                  <Space split={<span style={{ color: 'var(--yz-text-tertiary)' }}>/</span>} wrap>
                    <span>
                      {inLabel} <strong>{formatNumber(selected.prompt_tokens)}</strong>
                    </span>
                    <span>
                      {outLabel} <strong>{formatNumber(selected.completion_tokens)}</strong>
                    </span>
                    <span>
                      {t('common:common.total')} <strong>{formatNumber(selected.total_tokens)}</strong>
                    </span>
                    <span>
                      {t('console:logs.cached')} <strong>{formatNumber(selected.cached_tokens)}</strong>
                    </span>
                  </Space>
                )}
              </Descriptions.Item>
              <Descriptions.Item label={t('console:logs.fields.result')}>
                <ResultTag result={selected.result} />
              </Descriptions.Item>
              <Descriptions.Item label={t('console:logs.fields.statusCode')}>
                <span className="yz-mono">{selected.status_code || '-'}</span>
              </Descriptions.Item>
              <Descriptions.Item label={t('console:logs.fields.latency')}>{formatMs(selected.latency_ms)}</Descriptions.Item>
              <Descriptions.Item label={t('console:logs.fields.firstByte')}>
                {selected.first_byte_ms ? formatMs(selected.first_byte_ms) : '-'}
              </Descriptions.Item>
            </Descriptions>
            {selected.error ? (
              <>
                <SectionTitle>{t('console:logs.fields.error')}</SectionTitle>
                <pre className="yz-code-block" style={{ margin: 0, color: 'var(--yz-danger)' }}>
                  {selected.error}
                </pre>
              </>
            ) : null}
          </>
        ) : null}
      </Drawer>
    </div>
  );
}
