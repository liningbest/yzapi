import { Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useTranslation } from 'react-i18next';
import { EmptyState, LabelTag, ProportionBar } from '@/components';
import type { RouteTopK } from '@/types';
import { CHART_PALETTE } from '@/utils/constants';

interface Props {
  items: RouteTopK[] | null | undefined;
}

/** Top K matched samples, shared by the preview modal and the decision detail drawer. */
export default function TopKTable({ items }: Props) {
  const { t } = useTranslation(['route', 'common']);
  const columns: ColumnsType<RouteTopK> = [
    { title: t('common:common.id'), dataIndex: 'id', width: 70, className: 'yz-mono' },
    {
      title: t('common:common.label'),
      dataIndex: 'label',
      width: 90,
      render: (label: string) => <LabelTag label={label} />,
    },
    {
      title: t('common:common.text'),
      dataIndex: 'text',
      render: (text: string) => (
        <Typography.Text ellipsis={{ tooltip: text }} style={{ maxWidth: 360, display: 'block' }}>
          {text}
        </Typography.Text>
      ),
    },
    {
      title: t('common:common.score'),
      dataIndex: 'score',
      width: 170,
      render: (score: number, r) => (
        <ProportionBar
          value={score}
          total={1}
          width={90}
          color={r.label === 'complex' ? CHART_PALETTE[2] : CHART_PALETTE[3]}
        />
      ),
    },
  ];
  return (
    <Table<RouteTopK>
      className="yz-table"
      size="small"
      rowKey={(r) => `${r.id}-${r.score}`}
      columns={columns}
      dataSource={items ?? []}
      pagination={false}
      scroll={{ x: 'max-content' }}
      locale={{ emptyText: <EmptyState title={t('route:preview.noTopK')} style={{ padding: '16px 0' }} /> }}
    />
  );
}
