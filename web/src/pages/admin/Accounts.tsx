import { useState } from 'react';
import {
  App,
  Button,
  Card,
  Checkbox,
  Input,
  Popconfirm,
  Popover,
  Select,
  Space,
  Switch,
  Table,
  Tooltip,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  ArrowRightOutlined,
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  ReloadOutlined,
  SettingOutlined,
} from '@ant-design/icons';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { accountsApi, providersApi } from '@/api';
import { EmptyState, FilterBar, HealthTag, NeutralTag, PageHeader, ProtocolTag, ProviderAvatar, TimeCell, TypeTag } from '@/components';
import { useTableQuery } from '@/hooks/useTableQuery';
import type { Account, AccountListParams, Health, ModelType, Protocol } from '@/types';
import { MODEL_TYPES, PROTOCOLS, PROTOCOL_LABELS } from '@/utils/constants';
import AccountDrawer from './accounts/AccountDrawer';

const COLS_KEY = 'yz_accounts_cols';
type OptionalCol = 'mappings' | 'priority' | 'max_concurrency';
const OPTIONAL_COLS: OptionalCol[] = ['mappings', 'priority', 'max_concurrency'];
const HEALTHS: Health[] = ['available', 'cooling', 'unavailable'];

function loadCols(): OptionalCol[] {
  try {
    const raw = localStorage.getItem(COLS_KEY);
    if (raw) {
      const arr: unknown = JSON.parse(raw);
      if (Array.isArray(arr)) return OPTIONAL_COLS.filter((c) => arr.includes(c));
    }
  } catch {
    /* ignore */
  }
  return [...OPTIONAL_COLS];
}

function saveCols(cols: OptionalCol[]) {
  try {
    localStorage.setItem(COLS_KEY, JSON.stringify(cols));
  } catch {
    /* ignore */
  }
}

export default function Accounts() {
  const { t } = useTranslation(['accounts', 'common']);
  const { message } = App.useApp();
  const qc = useQueryClient();
  const { filters, setFilters, params, pagination } = useTableQuery<AccountListParams>({});
  const [cols, setCols] = useState<OptionalCol[]>(loadCols);
  const [drawer, setDrawer] = useState<{ key: number; open: boolean; id?: number }>({ key: 0, open: false });

  const providersQ = useQuery({ queryKey: ['admin', 'providers'], queryFn: providersApi.list, staleTime: 5 * 60_000 });
  const listQ = useQuery({
    queryKey: ['admin', 'accounts', params],
    queryFn: () => accountsApi.list(params),
    placeholderData: keepPreviousData,
  });
  const providers = providersQ.data ?? [];
  const providerOf = (key: string) => providers.find((p) => p.key === key);

  const invalidate = () => void qc.invalidateQueries({ queryKey: ['admin', 'accounts'] });

  const enableMut = useMutation({
    mutationFn: ({ id, enabled }: { id: number; enabled: boolean }) => accountsApi.setEnabled(id, enabled),
    onSuccess: () => {
      message.success(t('common:common.operationSuccess'));
      invalidate();
    },
    onError: invalidate,
  });
  const resetMut = useMutation({
    mutationFn: (id: number) => accountsApi.resetHealth(id),
    onSuccess: () => {
      message.success(t('accounts:resetHealth.success'));
      invalidate();
    },
  });
  const removeMut = useMutation({
    mutationFn: (id: number) => accountsApi.remove(id),
    onSuccess: () => {
      message.success(t('common:common.deleteSuccess'));
      invalidate();
    },
  });

  const openDrawer = (id?: number) => setDrawer((d) => ({ key: d.key + 1, open: true, id }));
  const closeDrawer = () => setDrawer((d) => ({ ...d, open: false }));

  const onColsChange = (v: OptionalCol[]) => {
    const next = OPTIONAL_COLS.filter((c) => v.includes(c));
    setCols(next);
    saveCols(next);
  };

  const columns: ColumnsType<Account> = [
    {
      title: t('accounts:columns.account'),
      key: 'account',
      fixed: 'left',
      width: 260,
      render: (_, a) => {
        const p = providerOf(a.provider);
        const at = p?.account_types?.find((x) => x.key === a.account_type)?.name ?? a.account_type;
        return (
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, minWidth: 0 }}>
            <ProviderAvatar provider={a.provider} size={32} />
            <div style={{ minWidth: 0 }}>
              <div style={{ fontWeight: 600, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                {a.name}
              </div>
              <Typography.Text type="secondary" style={{ fontSize: 12, whiteSpace: 'nowrap' }}>
                {p?.name ?? a.provider}
                {at ? ` · ${at}` : ''}
              </Typography.Text>
            </div>
          </div>
        );
      },
    },
    {
      title: t('accounts:columns.type'),
      dataIndex: 'type',
      key: 'type',
      width: 90,
      render: (v: ModelType) => <TypeTag type={v} />,
    },
    {
      title: t('accounts:columns.protocols'),
      dataIndex: 'protocols',
      key: 'protocols',
      render: (v: Protocol[]) => (
        <Space size={4} wrap>
          {(v ?? []).map((p) => (
            <ProtocolTag key={p} protocol={p} />
          ))}
        </Space>
      ),
    },
    ...(cols.includes('mappings')
      ? ([
          {
            title: t('accounts:columns.models'),
            key: 'mappings',
            width: 120,
            render: (_, a) => {
              const n = a.mappings?.length ?? 0;
              if (!n) {
                return (
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                    {t('accounts:noModels')}
                  </Typography.Text>
                );
              }
              return (
                <Popover
                  title={t('accounts:mappingsPopoverTitle')}
                  placement="bottom"
                  content={
                    <div style={{ maxHeight: 280, overflow: 'auto', minWidth: 260 }}>
                      {a.mappings.map((m, i) => (
                        <div
                          key={m.id ?? `${m.request_model}-${i}`}
                          style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '3px 0' }}
                        >
                          <span className="yz-mono">{m.request_model}</span>
                          <ArrowRightOutlined style={{ color: 'var(--yz-text-tertiary)', fontSize: 11 }} />
                          <span className="yz-mono" style={{ color: 'var(--yz-text-secondary)' }}>
                            {m.upstream_model}
                          </span>
                        </div>
                      ))}
                    </div>
                  }
                >
                  <NeutralTag style={{ cursor: 'pointer' }}>{t('accounts:modelsCount', { count: n })}</NeutralTag>
                </Popover>
              );
            },
          },
        ] as ColumnsType<Account>)
      : []),
    ...(cols.includes('priority')
      ? ([
          {
            title: t('accounts:columns.priority'),
            dataIndex: 'priority',
            key: 'priority',
            width: 90,
            align: 'right',
            render: (v: number) => <span className="yz-mono">{v}</span>,
          },
        ] as ColumnsType<Account>)
      : []),
    ...(cols.includes('max_concurrency')
      ? ([
          {
            title: t('accounts:columns.maxConcurrency'),
            dataIndex: 'max_concurrency',
            key: 'max_concurrency',
            width: 100,
            align: 'right',
            render: (v: number) =>
              v ? (
                <span className="yz-mono">{v}</span>
              ) : (
                <Typography.Text type="secondary">{t('common:common.unlimited')}</Typography.Text>
              ),
          },
        ] as ColumnsType<Account>)
      : []),
    {
      title: t('accounts:columns.health'),
      key: 'health',
      width: 110,
      render: (_, a) => <HealthTag health={a.health} cooldownUntil={a.cooldown_until} lastError={a.last_error} />,
    },
    {
      title: t('accounts:columns.enabled'),
      key: 'enabled',
      width: 80,
      render: (_, a) => (
        <Switch
          size="small"
          checked={a.enabled}
          loading={enableMut.isPending && enableMut.variables?.id === a.id}
          onChange={(v) => enableMut.mutate({ id: a.id, enabled: v })}
        />
      ),
    },
    {
      title: t('accounts:columns.updatedAt'),
      dataIndex: 'updated_at',
      key: 'updated_at',
      width: 120,
      render: (v: string) => <TimeCell value={v} />,
    },
    {
      title: t('accounts:columns.actions'),
      key: 'actions',
      fixed: 'right',
      width: 120,
      align: 'center',
      render: (_, a) => (
        <Space size={0}>
          <Tooltip title={t('common:action.edit')}>
            <Button type="text" icon={<EditOutlined />} onClick={() => openDrawer(a.id)} />
          </Tooltip>
          {a.health === 'available' ? (
            <Tooltip title={t('accounts:resetHealth.alreadyAvailable')}>
              <Button type="text" icon={<ReloadOutlined />} disabled />
            </Tooltip>
          ) : (
            <Popconfirm
              title={t('common:action.resetHealth')}
              description={t('accounts:resetHealth.confirm')}
              onConfirm={() => resetMut.mutate(a.id)}
            >
              <Tooltip title={t('common:action.resetHealth')}>
                <Button type="text" icon={<ReloadOutlined />} loading={resetMut.isPending && resetMut.variables === a.id} />
              </Tooltip>
            </Popconfirm>
          )}
          <Popconfirm
            title={t('common:action.delete')}
            description={t('accounts:delete.confirm', { name: a.name })}
            okButtonProps={{ danger: true }}
            onConfirm={() => removeMut.mutate(a.id)}
          >
            <Tooltip title={t('common:action.delete')}>
              <Button type="text" danger icon={<DeleteOutlined />} />
            </Tooltip>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        title={t('accounts:title')}
        subtitle={t('accounts:subtitle')}
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={() => openDrawer()}>
            {t('accounts:add')}
          </Button>
        }
      />
      <Card className="yz-card">
        <FilterBar
          extra={
            <Popover
              trigger="click"
              placement="bottomRight"
              content={
                <Checkbox.Group
                  value={cols}
                  onChange={(v) => onColsChange(v as OptionalCol[])}
                  style={{ display: 'flex', flexDirection: 'column', gap: 6 }}
                  options={[
                    { label: t('accounts:columns.models'), value: 'mappings' },
                    { label: t('accounts:columns.priority'), value: 'priority' },
                    { label: t('accounts:columns.maxConcurrency'), value: 'max_concurrency' },
                  ]}
                />
              }
            >
              <Button icon={<SettingOutlined />}>{t('accounts:columns.chooser')}</Button>
            </Popover>
          }
        >
          <Select
            allowClear
            showSearch
            optionFilterProp="name"
            placeholder={t('accounts:filter.provider')}
            style={{ width: 180 }}
            value={filters.provider}
            onChange={(v: string | undefined) => setFilters({ provider: v })}
            options={providers.map((p) => ({
              value: p.key,
              name: p.name,
              label: (
                <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
                  <ProviderAvatar provider={p.key} size={16} />
                  {p.name}
                </span>
              ),
            }))}
          />
          <Select
            allowClear
            placeholder={t('accounts:filter.type')}
            style={{ width: 130 }}
            value={filters.type}
            onChange={(v: ModelType | undefined) => setFilters({ type: v })}
            options={MODEL_TYPES.map((x) => ({ value: x, label: t(`common:type.${x}`) }))}
          />
          <Select
            allowClear
            placeholder={t('accounts:filter.protocol')}
            style={{ width: 200 }}
            value={filters.protocol}
            onChange={(v: Protocol | undefined) => setFilters({ protocol: v })}
            options={PROTOCOLS.map((p) => ({ value: p, label: PROTOCOL_LABELS[p] }))}
          />
          <Select
            allowClear
            placeholder={t('accounts:filter.enabled')}
            style={{ width: 120 }}
            value={filters.enabled}
            onChange={(v: boolean | undefined) => setFilters({ enabled: v })}
            options={[
              { value: true, label: t('common:common.enabled') },
              { value: false, label: t('common:common.disabled') },
            ]}
          />
          <Select
            allowClear
            placeholder={t('accounts:filter.health')}
            style={{ width: 130 }}
            value={filters.health}
            onChange={(v: Health | undefined) => setFilters({ health: v })}
            options={HEALTHS.map((h) => ({ value: h, label: t(`common:health.${h}`) }))}
          />
          <Input.Search
            allowClear
            placeholder={t('accounts:filter.keywordPlaceholder')}
            style={{ width: 240 }}
            defaultValue={filters.q}
            onSearch={(v) => setFilters({ q: v.trim() || undefined })}
          />
        </FilterBar>

        <Table<Account>
          className="yz-table"
          size="middle"
          rowKey="id"
          columns={columns}
          dataSource={listQ.data?.items ?? []}
          loading={listQ.isFetching}
          scroll={{ x: 'max-content' }}
          pagination={pagination(listQ.data?.total)}
          locale={{
            emptyText: (
              <EmptyState hint={t('accounts:empty.hint')} actionText={t('accounts:add')} onAction={() => openDrawer()} />
            ),
          }}
        />
      </Card>

      <AccountDrawer
        key={drawer.key}
        open={drawer.open}
        id={drawer.id}
        providers={providers}
        onClose={closeDrawer}
        onSaved={closeDrawer}
      />
    </>
  );
}
