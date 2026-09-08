import { useState } from 'react';
import { Button, Input, Select, Tooltip, Typography, Table } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { EyeOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { routeApi } from '@/api';
import { EmptyState, FilterBar, LabelTag, NeutralTag, RangeSelector, TimeCell, TokenText } from '@/components';
import { useRange } from '@/hooks/useRange';
import { useTableQuery } from '@/hooks/useTableQuery';
import type { RouteDecision, RouteDecisionListParams, RouteLabel } from '@/types';
import { formatMs, formatPercent, shortId } from '@/utils/format';
import DecisionDrawer from './DecisionDrawer';
import SourceTag, { useRequestTypeText } from './SourceTag';

type Filters = Omit<RouteDecisionListParams, 'page' | 'page_size' | 'range' | 'from' | 'to'>;

const SOURCES = ['rule', 'context', 'vector', 'fallback'];
const REQUEST_TYPES = ['chat', 'responses', 'messages'];

export default function DecisionsTab() {
  const { t } = useTranslation(['route', 'common']);
  const requestType = useRequestTypeText();
  const { range, setRange, params: rangeParams } = useRange('24h');
  const { filters, setFilters, params, pagination } = useTableQuery<Filters>({});
  const [current, setCurrent] = useState<RouteDecision | null>(null);

  const queryParams: RouteDecisionListParams = { ...params, ...rangeParams };
  const list = useQuery({
    queryKey: ['route', 'decisions', queryParams],
    queryFn: () => routeApi.decisions(queryParams),
    placeholderData: (prev) => prev,
  });

  const columns: ColumnsType<RouteDecision> = [
    {
      title: t('common:common.requestId'),
      dataIndex: 'request_id',
      width: 140,
      render: (id: string) => (
        <Typography.Text
          className="yz-mono"
          copyable={{ text: id, tooltips: [t('common:action.copy'), t('common:action.copied')] }}
          onClick={(e) => e.stopPropagation()}
        >
          {shortId(id)}
        </Typography.Text>
      ),
    },
    {
      title: t('route:decisions.classification'),
      dataIndex: 'label',
      width: 90,
      render: (label: RouteLabel) => <LabelTag label={label} />,
    },
    {
      title: t('common:common.source'),
      dataIndex: 'source',
      width: 110,
      render: (s: string) => <SourceTag source={s} />,
    },
    {
      title: t('common:common.confidence'),
      dataIndex: 'confidence',
      width: 90,
      align: 'right',
      render: (v: number) => <span style={{ fontVariantNumeric: 'tabular-nums' }}>{formatPercent(v)}</span>,
    },
    {
      title: t('route:decisions.selectedModel'),
      dataIndex: 'selected_model',
      width: 220,
      render: (_, r) => (
        <div style={{ lineHeight: 1.3 }}>
          <div className="yz-mono" style={{ fontSize: 13 }}>
            {r.selected_model || '-'}
          </div>
          {r.model_group ? (
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {r.model_group}
            </Typography.Text>
          ) : null}
        </div>
      ),
    },
    {
      title: t('route:decisions.requestType'),
      dataIndex: 'request_type',
      width: 130,
      render: (v: string) => requestType(v),
    },
    {
      title: t('common:common.tokens'),
      dataIndex: 'total_tokens',
      width: 90,
      align: 'right',
      render: (v: number) => <TokenText value={v} />,
    },
    {
      title: t('common:common.latency'),
      dataIndex: 'latency_ms',
      width: 90,
      align: 'right',
      render: (v: number) => <span style={{ fontVariantNumeric: 'tabular-nums' }}>{formatMs(v)}</span>,
    },
    {
      title: t('route:decisions.failed'),
      dataIndex: 'failed',
      width: 80,
      align: 'center',
      render: (failed: boolean) =>
        failed ? (
          <NeutralTag tone="danger">{t('route:decisions.failedTag')}</NeutralTag>
        ) : (
          <Typography.Text type="secondary">-</Typography.Text>
        ),
    },
    {
      title: t('common:common.time'),
      dataIndex: 'created_at',
      width: 130,
      render: (v: string) => <TimeCell value={v} />,
    },
    {
      title: t('common:common.actions'),
      key: 'actions',
      width: 70,
      align: 'center',
      fixed: 'right',
      render: (_, r) => (
        <Tooltip title={t('common:action.view')}>
          <Button
            type="text"
            size="small"
            icon={<EyeOutlined />}
            onClick={(e) => {
              e.stopPropagation();
              setCurrent(r);
            }}
          />
        </Tooltip>
      ),
    },
  ];

  return (
    <div>
      <FilterBar extra={<RangeSelector value={range} onChange={setRange} />}>
        <Select
          allowClear
          placeholder={t('route:decisions.classification')}
          style={{ width: 120 }}
          value={filters.label}
          onChange={(v) => setFilters({ label: v })}
          options={[
            { value: 'simple', label: t('common:label.simple') },
            { value: 'complex', label: t('common:label.complex') },
          ]}
        />
        <Select
          allowClear
          placeholder={t('route:decisions.filterSource')}
          style={{ width: 140 }}
          value={filters.source}
          onChange={(v) => setFilters({ source: v })}
          options={SOURCES.map((s) => ({ value: s, label: t(`route:source.${s}`, { defaultValue: s }) }))}
        />
        <Select
          allowClear
          placeholder={t('route:decisions.filterRequestType')}
          style={{ width: 160 }}
          value={filters.request_type}
          onChange={(v) => setFilters({ request_type: v })}
          options={REQUEST_TYPES.map((s) => ({ value: s, label: t(`route:requestType.${s}`, { defaultValue: s }) }))}
        />
        <Input
          allowClear
          placeholder={t('route:decisions.modelPlaceholder')}
          style={{ width: 180 }}
          value={filters.model ?? ''}
          onChange={(e) => setFilters({ model: e.target.value || undefined })}
        />
        <Input.Search
          allowClear
          placeholder={t('route:decisions.searchPlaceholder')}
          style={{ width: 260 }}
          onSearch={(v) => setFilters({ q: v.trim() || undefined })}
        />
      </FilterBar>

      <Table<RouteDecision>
        className="yz-table"
        size="middle"
        rowKey="id"
        loading={list.isLoading}
        columns={columns}
        dataSource={list.data?.items ?? []}
        pagination={pagination(list.data?.total)}
        scroll={{ x: 'max-content' }}
        onRow={(r) => ({ className: 'yz-clickable-row', onClick: () => setCurrent(r) })}
        locale={{
          emptyText: <EmptyState title={t('route:decisions.empty')} hint={t('route:decisions.emptyHint')} />,
        }}
      />

      <DecisionDrawer decision={current} onClose={() => setCurrent(null)} />
    </div>
  );
}
