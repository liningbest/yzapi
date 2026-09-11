import { useMemo } from 'react';
import { Card, Table, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useTranslation } from 'react-i18next';
import { EmptyState, NeutralTag, ProportionBar, ProviderAvatar, TokenText } from '@/components';
import type { UsageDim } from '@/types';
import { CHART_PALETTE } from '@/utils/constants';
import { formatMoney, formatNumber } from '@/utils/format';

interface Props {
  title: React.ReactNode;
  icon?: React.ReactNode;
  items: UsageDim[] | undefined;
  currency?: string;
  loading?: boolean;
  /** Render a provider avatar next to the name (for by_provider). */
  provider?: boolean;
  color?: string;
  /** Extra explanation shown next to the count (e.g. data comes from raw logs). */
  hint?: string;
}

const DELETED_RE = /\((已删除|已刪除|deleted)\)\s*$/i;

export function splitDeleted(name: string): { label: string; deleted: boolean } {
  const m = name.match(DELETED_RE);
  if (!m) return { label: name, deleted: false };
  return { label: name.replace(DELETED_RE, '').trim(), deleted: true };
}

/** Compact distribution table shared by all six usage dimensions. */
export default function DimTable({ title, icon, items, currency, loading, provider, color = CHART_PALETTE[0], hint }: Props) {
  const { t } = useTranslation(['usage', 'common']);
  const rows = useMemo(() => [...(items ?? [])].sort((a, b) => b.total_tokens - a.total_tokens), [items]);
  const totalTokens = useMemo(() => rows.reduce((s, r) => s + (r.total_tokens || 0), 0), [rows]);

  const columns: ColumnsType<UsageDim> = [
    {
      title: t('common:common.name'),
      dataIndex: 'name',
      ellipsis: true,
      render: (v: string, r) => {
        const { label, deleted } = splitDeleted(v || String(r.key));
        return (
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, maxWidth: '100%' }}>
            {provider ? <ProviderAvatar provider={String(r.key)} size={18} /> : null}
            <Typography.Text ellipsis={{ tooltip: label }} style={{ maxWidth: 160 }}>
              {label}
            </Typography.Text>
            {deleted ? (
              <NeutralTag>{t('common:common.deleted')}</NeutralTag>
            ) : null}
          </span>
        );
      },
    },
    {
      title: t('common:common.requests'),
      dataIndex: 'requests',
      width: 90,
      align: 'right',
      sorter: (a, b) => a.requests - b.requests,
      render: (v: number, r) => {
        const cell = <span style={{ fontVariantNumeric: 'tabular-nums' }}>{formatNumber(v)}</span>;
        if (r.attempts != null && r.attempts !== v) {
          return (
            <Tooltip title={t('usage:dim.attemptsHint', { count: r.attempts })}>
              <span style={{ borderBottom: '1px dotted currentColor', cursor: 'help' }}>{cell}</span>
            </Tooltip>
          );
        }
        return cell;
      },
    },
    {
      title: t('common:common.totalTokens'),
      dataIndex: 'total_tokens',
      width: 100,
      align: 'right',
      sorter: (a, b) => a.total_tokens - b.total_tokens,
      defaultSortOrder: 'descend',
      render: (v: number) => <TokenText value={v} style={{ fontWeight: 500 }} />,
    },
    {
      title: t('common:common.cachedTokens'),
      dataIndex: 'cached_tokens',
      width: 100,
      align: 'right',
      sorter: (a, b) => a.cached_tokens - b.cached_tokens,
      render: (v: number) => <TokenText value={v} />,
    },
    {
      title: t('common:common.cost'),
      dataIndex: 'cost',
      width: 100,
      align: 'right',
      sorter: (a, b) => (a.cost ?? 0) - (b.cost ?? 0),
      render: (v: number | undefined) => <span style={{ fontVariantNumeric: 'tabular-nums' }}>{formatMoney(v ?? 0, currency)}</span>,
    },
    {
      title: t('common:common.proportion'),
      key: 'ratio',
      width: 150,
      render: (_, r) => <ProportionBar value={r.total_tokens} total={totalTokens} color={color} width={70} />,
    },
  ];

  return (
    <Card
      className="yz-card"
      size="small"
      title={
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
          {icon ? <span style={{ color: 'var(--yz-text-tertiary)' }}>{icon}</span> : null}
          <span>{title}</span>
        </span>
      }
      extra={
        <Tooltip title={hint ? `${hint} · ${formatNumber(totalTokens)}` : formatNumber(totalTokens)}>
          <Typography.Text type="secondary" style={{ fontSize: 12, borderBottom: hint ? '1px dotted currentColor' : undefined, cursor: hint ? 'help' : undefined }}>
            {t('usage:dim.count', { count: rows.length })}
          </Typography.Text>
        </Tooltip>
      }
      styles={{ body: { padding: 0 } }}
    >
      <Table<UsageDim>
        className="yz-table"
        size="small"
        rowKey={(r) => String(r.key)}
        columns={columns}
        dataSource={rows}
        loading={loading}
        showSorterTooltip={false}
        scroll={{ x: 'max-content' }}
        pagination={rows.length > 8 ? { pageSize: 8, simple: true, size: 'small', showSizeChanger: false } : false}
        locale={{ emptyText: <EmptyState title={t('common:common.noData')} style={{ padding: '20px 0' }} /> }}
      />
    </Card>
  );
}
