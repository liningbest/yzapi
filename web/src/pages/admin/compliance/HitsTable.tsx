import { Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useTranslation } from 'react-i18next';
import { ActionTag, EmptyState, ProportionBar, RiskTag } from '@/components';
import type { ComplianceHit } from '@/types';
import { CHART_PALETTE } from '@/utils/constants';

export function useMethodText() {
  const { t } = useTranslation(['compliance']);
  return (method: string) => (method ? t(`compliance:method.${method}`, { defaultValue: method }) : '-');
}

/** Hit list shared by the test modal and the audit log detail drawer. */
export default function HitsTable({ hits }: { hits: ComplianceHit[] | null | undefined }) {
  const { t } = useTranslation(['compliance', 'common']);
  const methodText = useMethodText();
  const columns: ColumnsType<ComplianceHit> = [
    { title: t('compliance:hits.method'), dataIndex: 'method', width: 100, render: (m: string) => methodText(m) },
    { title: t('compliance:policyGroup'), dataIndex: 'policy_group', width: 140, render: (v: string) => v || '-' },
    {
      title: t('compliance:hits.evidence'),
      dataIndex: 'evidence',
      render: (v: string) => (
        <Typography.Text ellipsis={{ tooltip: v }} style={{ maxWidth: 260, display: 'block' }}>
          {v || '-'}
        </Typography.Text>
      ),
    },
    {
      title: t('common:common.score'),
      dataIndex: 'score',
      width: 160,
      render: (score: number, r) => (
        <ProportionBar value={score} total={1} width={80} color={r.action === 'block' ? CHART_PALETTE[4] : CHART_PALETTE[0]} />
      ),
    },
    { title: t('compliance:hits.action'), dataIndex: 'action', width: 90, render: (a: string) => <ActionTag action={a} /> },
    { title: t('compliance:hits.risk'), dataIndex: 'risk_level', width: 90, render: (r: string) => <RiskTag risk={r} /> },
  ];
  return (
    <Table<ComplianceHit>
      className="yz-table"
      size="small"
      rowKey={(r, i) => `${r.method}-${r.policy_group}-${i}`}
      columns={columns}
      dataSource={hits ?? []}
      pagination={false}
      scroll={{ x: 'max-content' }}
      locale={{ emptyText: <EmptyState title={t('compliance:testModal.noHits')} style={{ padding: '16px 0' }} /> }}
    />
  );
}
