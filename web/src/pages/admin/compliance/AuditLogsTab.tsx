import { useState } from 'react';
import { Button, Descriptions, Drawer, Input, Select, Table, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { EyeOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { complianceApi } from '@/api';
import { ActionTag, EmptyState, FilterBar, ProtocolTag, RangeSelector, RiskTag, SectionTitle, StatusCodeTag, TimeCell } from '@/components';
import { useRange } from '@/hooks/useRange';
import { useTableQuery } from '@/hooks/useTableQuery';
import type { AuditLog, AuditLogListParams, PolicyAction, PolicyGroup, RiskLevel } from '@/types';
import { formatPercent, shortId } from '@/utils/format';
import HitsTable, { useMethodText } from './HitsTable';
import PolicyGroupSelect from './PolicyGroupSelect';
import { AUDIT_LOGS_KEY } from './keys';

type Filters = Omit<AuditLogListParams, 'page' | 'page_size' | 'range' | 'from' | 'to'>;
const METHODS = ['keyword', 'semantic', 'degraded'];

interface Props {
  groups: PolicyGroup[];
  groupsLoading: boolean;
}

function StatusCode({ code }: { code: number }) {
  if (!code) return <Typography.Text type="secondary">-</Typography.Text>;
  return <StatusCodeTag code={code} />;
}

function AuditLogDrawer({ log, onClose }: { log: AuditLog | null; onClose: () => void }) {
  const { t } = useTranslation(['compliance', 'common']);
  const methodText = useMethodText();
  return (
    <Drawer open={Boolean(log)} onClose={onClose} width={680} title={t('compliance:logs.detail')} destroyOnHidden>
      {log ? (
        <div>
          <SectionTitle>{t('compliance:logs.info')}</SectionTitle>
          <Descriptions size="small" column={2} bordered labelStyle={{ width: 110 }}>
            <Descriptions.Item label={t('common:common.requestId')} span={2}>
              <Typography.Text className="yz-mono" copyable={{ text: log.request_id }}>
                {log.request_id || '-'}
              </Typography.Text>
            </Descriptions.Item>
            <Descriptions.Item label={t('common:common.time')} span={2}>
              <TimeCell value={log.created_at} absolute />
            </Descriptions.Item>
            <Descriptions.Item label={t('common:common.user')}>{log.username || `#${log.user_id}`}</Descriptions.Item>
            <Descriptions.Item label={t('common:common.model')}>
              <span className="yz-mono">{log.request_model || '-'}</span>
            </Descriptions.Item>
            <Descriptions.Item label={t('compliance:logs.clientProtocol')}>
              {log.protocol ? <ProtocolTag protocol={log.protocol} /> : '-'}
            </Descriptions.Item>
            <Descriptions.Item label={t('common:common.statusCode')}>
              <StatusCode code={log.status_code} />
            </Descriptions.Item>
            <Descriptions.Item label={t('compliance:hits.action')}>
              <ActionTag action={log.action} />
            </Descriptions.Item>
            <Descriptions.Item label={t('compliance:hits.risk')}>
              <RiskTag risk={log.risk_level} />
            </Descriptions.Item>
            <Descriptions.Item label={t('compliance:logs.method')}>{methodText(log.detect_method)}</Descriptions.Item>
            <Descriptions.Item label={t('common:common.confidence')}>{formatPercent(log.confidence)}</Descriptions.Item>
          </Descriptions>

          <SectionTitle>{t('compliance:logs.hits')}</SectionTitle>
          <HitsTable hits={log.hits} />

          <SectionTitle>{t('compliance:logs.policy')}</SectionTitle>
          <Descriptions size="small" column={1} bordered labelStyle={{ width: 110 }}>
            <Descriptions.Item label={t('compliance:policyGroup')}>
              {log.policy_group || <Typography.Text type="secondary">-</Typography.Text>}
            </Descriptions.Item>
            <Descriptions.Item label={t('compliance:logs.evidence')}>
              <span style={{ wordBreak: 'break-all' }}>{log.evidence || '-'}</span>
            </Descriptions.Item>
          </Descriptions>

          {log.snippet ? (
            <>
              <SectionTitle>{t('compliance:logs.snippet')}</SectionTitle>
              <div className="yz-code-block">{log.snippet}</div>
            </>
          ) : null}
        </div>
      ) : null}
    </Drawer>
  );
}

export default function AuditLogsTab({ groups, groupsLoading }: Props) {
  const { t } = useTranslation(['compliance', 'common']);
  const methodText = useMethodText();
  const { range, setRange, params: rangeParams } = useRange('24h');
  const { filters, setFilters, params, pagination } = useTableQuery<Filters>({});
  const [current, setCurrent] = useState<AuditLog | null>(null);

  const queryParams: AuditLogListParams = { ...params, ...rangeParams };
  const list = useQuery({
    queryKey: [...AUDIT_LOGS_KEY, queryParams],
    queryFn: () => complianceApi.auditLogs(queryParams),
    placeholderData: (prev) => prev,
  });

  const columns: ColumnsType<AuditLog> = [
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
    { title: t('common:common.user'), dataIndex: 'username', width: 120, render: (v: string, r) => v || `#${r.user_id}` },
    {
      title: t('common:common.model'),
      dataIndex: 'request_model',
      width: 200,
      render: (_, r) => (
        <div style={{ lineHeight: 1.3 }}>
          <div className="yz-mono" style={{ fontSize: 13 }}>
            {r.request_model || '-'}
          </div>
          {r.protocol ? (
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {r.protocol}
            </Typography.Text>
          ) : null}
        </div>
      ),
    },
    { title: t('compliance:hits.action'), dataIndex: 'action', width: 90, render: (a: PolicyAction) => <ActionTag action={a} /> },
    { title: t('compliance:hits.risk'), dataIndex: 'risk_level', width: 90, render: (r: RiskLevel) => <RiskTag risk={r} /> },
    { title: t('compliance:logs.method'), dataIndex: 'detect_method', width: 100, render: (m: string) => methodText(m) },
    { title: t('compliance:policyGroup'), dataIndex: 'policy_group', width: 140, render: (v: string) => v || '-' },
    {
      title: t('compliance:logs.evidence'),
      dataIndex: 'evidence',
      render: (v: string) => (
        <Typography.Text ellipsis={{ tooltip: v }} style={{ maxWidth: 240, display: 'block' }}>
          {v || '-'}
        </Typography.Text>
      ),
    },
    {
      title: t('common:common.confidence'),
      dataIndex: 'confidence',
      width: 90,
      align: 'right',
      render: (v: number) => <span style={{ fontVariantNumeric: 'tabular-nums' }}>{formatPercent(v)}</span>,
    },
    {
      title: t('common:common.statusCode'),
      dataIndex: 'status_code',
      width: 90,
      align: 'center',
      render: (c: number) => <StatusCode code={c} />,
    },
    { title: t('common:common.time'), dataIndex: 'created_at', width: 130, render: (v: string) => <TimeCell value={v} /> },
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
          placeholder={t('compliance:logs.filterAction')}
          style={{ width: 110 }}
          value={filters.action}
          onChange={(v) => setFilters({ action: v })}
          options={[
            { value: 'block', label: t('common:policyAction.block') },
            { value: 'audit', label: t('common:policyAction.audit') },
          ]}
        />
        <Select
          allowClear
          placeholder={t('compliance:logs.filterRisk')}
          style={{ width: 110 }}
          value={filters.risk_level}
          onChange={(v) => setFilters({ risk_level: v })}
          options={(['low', 'medium', 'high'] as RiskLevel[]).map((r) => ({ value: r, label: t(`common:risk.${r}`) }))}
        />
        <Select
          allowClear
          placeholder={t('compliance:logs.filterMethod')}
          style={{ width: 120 }}
          value={filters.detect_method}
          onChange={(v) => setFilters({ detect_method: v })}
          options={METHODS.map((m) => ({ value: m, label: t(`compliance:method.${m}`, { defaultValue: m }) }))}
        />
        <PolicyGroupSelect
          groups={groups}
          loading={groupsLoading}
          allowClear
          value={filters.policy_group_id}
          onChange={(v) => setFilters({ policy_group_id: v })}
          style={{ width: 220 }}
        />
        <Input.Search
          allowClear
          placeholder={t('compliance:logs.searchPlaceholder')}
          style={{ width: 280 }}
          onSearch={(v) => setFilters({ q: v.trim() || undefined })}
        />
      </FilterBar>

      <Table<AuditLog>
        className="yz-table"
        size="middle"
        rowKey="id"
        loading={list.isLoading}
        columns={columns}
        dataSource={list.data?.items ?? []}
        pagination={pagination(list.data?.total)}
        scroll={{ x: 'max-content' }}
        onRow={(r) => ({ className: 'yz-clickable-row', onClick: () => setCurrent(r) })}
        locale={{ emptyText: <EmptyState title={t('compliance:logs.empty')} hint={t('compliance:logs.emptyHint')} /> }}
      />

      <AuditLogDrawer log={current} onClose={() => setCurrent(null)} />
    </div>
  );
}
