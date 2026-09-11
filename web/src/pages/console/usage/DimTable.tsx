import { useMemo } from 'react';
import { Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useTranslation } from 'react-i18next';
import { EmptyState, NeutralTag, ProportionBar, TokenText, useChartTheme } from '@/components';
import type { UsageDim } from '@/types';
import { formatMoney, formatNumber } from '@/utils/format';

interface Props {
  rows: UsageDim[] | undefined;
  currency?: string;
  loading?: boolean;
}

const DELETED_RE = /\s*\((已删除|已刪除|deleted)\)\s*$/i;

/** Splits "name (已删除)" into { label, deleted }. */
function parseName(name: string): { label: string; deleted: boolean } {
  const deleted = DELETED_RE.test(name);
  return { label: deleted ? name.replace(DELETED_RE, '') : name, deleted };
}

/** Compact per-dimension breakdown: name / requests / tokens / cached / cost / share. */
export default function DimTable({ rows, currency, loading }: Props) {
  const { t } = useTranslation(['console', 'common']);
  const { palette } = useChartTheme();

  const sorted = useMemo(() => [...(rows ?? [])].sort((a, b) => b.total_tokens - a.total_tokens), [rows]);
  const sum = useMemo(() => sorted.reduce((acc, r) => acc + (r.total_tokens || 0), 0), [sorted]);

  const columns: ColumnsType<UsageDim> = [
    {
      title: t('console:usage.dim.name'),
      dataIndex: 'name',
      ellipsis: true,
      render: (name: string) => {
        const { label, deleted } = parseName(name || '');
        return (
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, maxWidth: '100%' }}>
            <Typography.Text ellipsis={{ tooltip: label }} style={{ maxWidth: 180 }}>
              {label || '-'}
            </Typography.Text>
            {deleted ? (
              <NeutralTag>{t('common:common.deleted')}</NeutralTag>
            ) : null}
          </span>
        );
      },
    },
    {
      title: t('console:usage.dim.requests'),
      dataIndex: 'requests',
      align: 'right',
      width: 90,
      sorter: (a, b) => a.requests - b.requests,
      render: (v: number) => <span style={{ fontVariantNumeric: 'tabular-nums' }}>{formatNumber(v)}</span>,
    },
    {
      title: t('console:usage.dim.totalTokens'),
      dataIndex: 'total_tokens',
      align: 'right',
      width: 100,
      sorter: (a, b) => a.total_tokens - b.total_tokens,
      defaultSortOrder: 'descend',
      render: (v: number) => <TokenText value={v} style={{ fontWeight: 600 }} />,
    },
    {
      title: t('console:usage.dim.cachedTokens'),
      dataIndex: 'cached_tokens',
      align: 'right',
      width: 100,
      sorter: (a, b) => a.cached_tokens - b.cached_tokens,
      render: (v: number) => <TokenText value={v} />,
    },
    {
      title: t('console:usage.dim.cost'),
      dataIndex: 'cost',
      align: 'right',
      width: 100,
      sorter: (a, b) => (a.cost ?? 0) - (b.cost ?? 0),
      render: (v: number | undefined) => <span style={{ fontVariantNumeric: 'tabular-nums' }}>{formatMoney(v ?? 0, currency)}</span>,
    },
    {
      title: t('console:usage.dim.proportion'),
      key: 'proportion',
      width: 170,
      render: (_, row, index) => (
        <ProportionBar value={row.total_tokens} total={sum} color={palette[index % palette.length]} width={90} />
      ),
    },
  ];

  return (
    <Table
      className="yz-table"
      size="small"
      rowKey={(r) => String(r.key)}
      columns={columns}
      dataSource={sorted}
      loading={loading}
      pagination={sorted.length > 8 ? { pageSize: 8, size: 'small', showSizeChanger: false } : false}
      scroll={{ x: 'max-content' }}
      locale={{ emptyText: <EmptyState style={{ padding: '16px 0' }} /> }}
    />
  );
}
