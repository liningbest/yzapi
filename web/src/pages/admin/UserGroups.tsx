import { useMemo, useState } from 'react';
import {
  App,
  Button,
  Card,
  Form,
  Input,
  InputNumber,
  Popconfirm,
  Select,
  Space,
  Switch,
  Table,
  Tooltip,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { CrownOutlined, DeleteOutlined, EditOutlined, PlusOutlined, TeamOutlined } from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { modelGroupsApi, userGroupsApi } from '@/api';
import { EmptyState, FilterBar, FormDrawer, NeutralTag, PageHeader, ProportionBar, TokenText, TypeTag } from '@/components';
import { useTableQuery } from '@/hooks/useTableQuery';
import type { UserGroup, UserGroupInput } from '@/types';
import { PRIMARY, SEMANTIC } from '@/utils/constants';
import { QUOTA_UNITS, formatNumber, formatTokens, joinQuota, splitQuota } from '@/utils/format';

interface Filters {
  q?: string;
}

interface FormValues {
  name: string;
  max_concurrency: number;
  key_max_concurrency: number;
  quota_amount: number;
  quota_unit: string;
  model_group_ids: number[];
  enabled: boolean;
  note?: string;
}

const DEFAULT_VALUES: FormValues = {
  name: '',
  max_concurrency: 0,
  key_max_concurrency: 0,
  quota_amount: 0,
  quota_unit: 'token',
  model_group_ids: [],
  enabled: true,
  note: '',
};

function toFormValues(g: UserGroup): FormValues {
  const q = splitQuota(g.token_quota);
  return {
    name: g.name,
    max_concurrency: g.max_concurrency,
    key_max_concurrency: g.key_max_concurrency,
    quota_amount: q.amount,
    quota_unit: q.unit,
    model_group_ids: g.model_group_ids ?? [],
    enabled: g.enabled,
    note: g.note,
  };
}

function quotaColor(used: number, quota: number): string {
  if (!quota) return PRIMARY;
  const r = used / quota;
  if (r >= 0.9) return SEMANTIC.danger;
  if (r >= 0.7) return SEMANTIC.warning;
  return PRIMARY;
}

export default function UserGroups() {
  const { t } = useTranslation(['userGroups', 'common']);
  const { message } = App.useApp();
  const qc = useQueryClient();
  const { params, pagination, setFilters } = useTableQuery<Filters>({});
  const [drawer, setDrawer] = useState<{ open: boolean; record?: UserGroup }>({ open: false });
  const [form] = Form.useForm<FormValues>();

  const list = useQuery({
    queryKey: ['user-groups', params],
    queryFn: () => userGroupsApi.list(params),
    placeholderData: (prev) => prev,
  });
  const modelGroups = useQuery({
    queryKey: ['model-groups', 'options'],
    queryFn: () => modelGroupsApi.list({ page_size: 200 }),
  });

  const modelGroupOptions = useMemo(
    () =>
      (modelGroups.data?.items ?? []).map((g) => ({
        value: g.id,
        label: g.name,
        type: g.type,
      })),
    [modelGroups.data],
  );

  const invalidate = () => void qc.invalidateQueries({ queryKey: ['user-groups'] });

  const createMut = useMutation({
    mutationFn: (body: UserGroupInput) => userGroupsApi.create(body),
    onSuccess: () => {
      message.success(t('common:common.createSuccess'));
      setDrawer({ open: false });
      invalidate();
    },
  });
  const updateMut = useMutation({
    mutationFn: ({ id, body }: { id: number; body: UserGroupInput }) => userGroupsApi.update(id, body),
    onSuccess: () => {
      message.success(t('common:common.updateSuccess'));
      setDrawer({ open: false });
      invalidate();
    },
  });
  const removeMut = useMutation({
    mutationFn: (id: number) => userGroupsApi.remove(id),
    onSuccess: () => {
      message.success(t('common:common.deleteSuccess'));
      invalidate();
    },
  });
  const enabledMut = useMutation({
    mutationFn: ({ id, enabled }: { id: number; enabled: boolean }) => userGroupsApi.setEnabled(id, enabled),
    onSuccess: () => {
      message.success(t('common:common.operationSuccess'));
      invalidate();
    },
  });

  const openCreate = () => setDrawer({ open: true });
  const openEdit = (record: UserGroup) => setDrawer({ open: true, record });

  const submit = async () => {
    const v = await form.validateFields();
    const body: UserGroupInput = {
      name: v.name.trim(),
      max_concurrency: v.max_concurrency ?? 0,
      key_max_concurrency: v.key_max_concurrency ?? 0,
      token_quota: joinQuota(v.quota_amount ?? 0, v.quota_unit),
      model_group_ids: v.model_group_ids ?? [],
      enabled: drawer.record?.is_default ? true : v.enabled,
      note: v.note?.trim() ?? '',
    };
    if (drawer.record) updateMut.mutate({ id: drawer.record.id, body });
    else createMut.mutate(body);
  };

  const unlimited = t('common:common.unlimited');

  const columns: ColumnsType<UserGroup> = [
    {
      title: t('common:common.name'),
      dataIndex: 'name',
      render: (v: string, r) => (
        <Space size={8}>
          <Typography.Text strong>{v}</Typography.Text>
          {r.is_default ? (
            <NeutralTag>
              <CrownOutlined style={{ marginRight: 4, fontSize: 10 }} />
              {t('common:common.default')}
            </NeutralTag>
          ) : null}
        </Space>
      ),
    },
    {
      title: t('userGroups:maxConcurrency'),
      dataIndex: 'max_concurrency',
      width: 120,
      align: 'right',
      render: (v: number) => (v > 0 ? formatNumber(v) : <Typography.Text type="secondary">{unlimited}</Typography.Text>),
    },
    {
      title: t('userGroups:keyMaxConcurrency'),
      dataIndex: 'key_max_concurrency',
      width: 140,
      align: 'right',
      render: (v: number) =>
        v > 0 ? formatNumber(v) : <Typography.Text type="secondary">{t('userGroups:inheritGroup')}</Typography.Text>,
    },
    {
      title: t('userGroups:tokenQuota'),
      dataIndex: 'token_quota',
      width: 120,
      align: 'right',
      render: (v: number) =>
        v > 0 ? (
          <Tooltip title={formatNumber(v)}>
            <span style={{ fontVariantNumeric: 'tabular-nums' }}>{formatTokens(v)}</span>
          </Tooltip>
        ) : (
          <Typography.Text type="secondary">{unlimited}</Typography.Text>
        ),
    },
    {
      title: t('userGroups:modelGroups'),
      dataIndex: 'model_groups',
      render: (v: UserGroup['model_groups']) =>
        v && v.length ? (
          <Space size={[4, 4]} wrap>
            {v.map((g) => (
              <NeutralTag key={g.id}>{g.name}</NeutralTag>
            ))}
          </Space>
        ) : (
          <Typography.Text type="secondary">{t('userGroups:allModels')}</Typography.Text>
        ),
    },
    {
      title: t('userGroups:members'),
      dataIndex: 'members_count',
      width: 100,
      align: 'right',
      render: (v: number) => (
        <Space size={4}>
          <TeamOutlined style={{ color: 'var(--yz-text-secondary)' }} />
          <span style={{ fontVariantNumeric: 'tabular-nums' }}>{v ?? 0}</span>
        </Space>
      ),
    },
    {
      title: t('userGroups:monthUsage'),
      dataIndex: 'tokens_used_month',
      width: 220,
      render: (v: number, r) => (
        <div>
          <TokenText value={v} style={{ fontWeight: 500 }} />
          {r.token_quota > 0 ? (
            <div style={{ marginTop: 4 }}>
              <ProportionBar value={v} total={r.token_quota} color={quotaColor(v, r.token_quota)} width={100} />
            </div>
          ) : null}
        </div>
      ),
    },
    {
      title: t('common:common.status'),
      dataIndex: 'enabled',
      width: 90,
      render: (v: boolean, r) => {
        const node = (
          <Switch
            size="small"
            checked={v}
            disabled={r.is_default}
            loading={enabledMut.isPending && enabledMut.variables?.id === r.id}
            onChange={(checked) => enabledMut.mutate({ id: r.id, enabled: checked })}
          />
        );
        return r.is_default ? <Tooltip title={t('common:error.is_default')}>{node}</Tooltip> : node;
      },
    },
    {
      title: t('common:common.actions'),
      key: 'actions',
      width: 100,
      fixed: 'right',
      render: (_, r) => {
        const deleteDisabled = r.is_default || r.members_count > 0;
        const tip = r.is_default
          ? t('common:error.is_default')
          : r.members_count > 0
            ? t('common:error.has_members')
            : t('common:action.delete');
        return (
          <Space size={0}>
            <Tooltip title={t('common:action.edit')}>
              <Button type="text" size="small" icon={<EditOutlined />} onClick={() => openEdit(r)} />
            </Tooltip>
            <Tooltip title={tip}>
              {deleteDisabled ? (
                <Button type="text" size="small" danger icon={<DeleteOutlined />} disabled />
              ) : (
                <Popconfirm
                  title={t('common:common.confirmDeleteName', { name: r.name })}
                  okButtonProps={{ danger: true }}
                  onConfirm={() => removeMut.mutate(r.id)}
                >
                  <Button type="text" size="small" danger icon={<DeleteOutlined />} />
                </Popconfirm>
              )}
            </Tooltip>
          </Space>
        );
      },
    },
  ];

  const editing = drawer.record;

  return (
    <div>
      <PageHeader
        title={t('userGroups:title')}
        subtitle={t('userGroups:subtitle')}
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            {t('userGroups:add')}
          </Button>
        }
      />
      <Card className="yz-card">
        <FilterBar>
          <Input.Search
            style={{ width: 240 }}
            allowClear
            placeholder={t('userGroups:searchPlaceholder')}
            onSearch={(v) => setFilters({ q: v.trim() || undefined })}
          />
        </FilterBar>
        <Table<UserGroup>
          className="yz-table"
          size="middle"
          rowKey="id"
          columns={columns}
          dataSource={list.data?.items ?? []}
          loading={list.isLoading}
          scroll={{ x: 'max-content' }}
          pagination={pagination(list.data?.total)}
          locale={{
            emptyText: (
              <EmptyState
                title={t('userGroups:empty')}
                hint={t('userGroups:emptyHint')}
                actionText={t('userGroups:add')}
                onAction={openCreate}
              />
            ),
          }}
        />
      </Card>

      <FormDrawer
        open={drawer.open}
        title={editing ? t('userGroups:editTitle', { name: editing.name }) : t('userGroups:addTitle')}
        onClose={() => setDrawer({ open: false })}
        onSubmit={() => void submit()}
        submitting={createMut.isPending || updateMut.isPending}
        width={560}
      >
        <Form
          key={editing?.id ?? 'create'}
          form={form}
          layout="vertical"
          autoComplete="off"
          initialValues={editing ? toFormValues(editing) : DEFAULT_VALUES}
        >
          <Form.Item
            name="name"
            label={t('common:common.name')}
            extra={t('userGroups:form.nameExtra')}
            rules={[
              { required: true, message: t('common:common.required') },
              { max: 64, message: t('userGroups:form.nameMax') },
            ]}
          >
            <Input placeholder={t('userGroups:form.namePlaceholder')} autoFocus />
          </Form.Item>
          <Form.Item
            name="max_concurrency"
            label={t('userGroups:maxConcurrency')}
            extra={t('userGroups:form.maxConcurrencyExtra')}
            rules={[{ required: true, message: t('common:common.required') }]}
          >
            <InputNumber min={0} max={100000} precision={0} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item
            name="key_max_concurrency"
            label={t('userGroups:keyMaxConcurrency')}
            extra={t('userGroups:form.keyMaxConcurrencyExtra')}
            dependencies={['max_concurrency']}
            rules={[
              { required: true, message: t('common:common.required') },
              ({ getFieldValue }) => ({
                validator: (_, v: number) => {
                  const g = Number(getFieldValue('max_concurrency') ?? 0);
                  if (v > 0 && g > 0 && v > g) return Promise.reject(new Error(t('userGroups:form.keyExceedsGroup')));
                  return Promise.resolve();
                },
              }),
            ]}
          >
            <InputNumber min={0} max={100000} precision={0} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item label={t('userGroups:tokenQuota')} extra={t('userGroups:form.tokenQuotaExtra')} required>
            <Space.Compact style={{ width: '100%' }}>
              <Form.Item name="quota_amount" noStyle rules={[{ required: true, message: t('common:common.required') }]}>
                <InputNumber min={0} precision={0} style={{ width: '100%' }} />
              </Form.Item>
              <Form.Item name="quota_unit" noStyle>
                <Select
                  style={{ width: 120 }}
                  options={QUOTA_UNITS.map((u) => ({ value: u.key, label: t(`common:quotaUnit.${u.key}`) }))}
                />
              </Form.Item>
            </Space.Compact>
          </Form.Item>
          <Form.Item
            name="model_group_ids"
            label={t('userGroups:modelGroups')}
            extra={t('userGroups:form.modelGroupsExtra')}
            rules={[{ type: 'array', max: 100, message: t('userGroups:form.modelGroupsMax') }]}
          >
            <Select
              mode="multiple"
              allowClear
              showSearch
              optionFilterProp="label"
              placeholder={t('userGroups:form.modelGroupsPlaceholder')}
              loading={modelGroups.isLoading}
              options={modelGroupOptions}
              optionRender={(opt) => (
                <Space size={8}>
                  <span>{opt.data.label}</span>
                  <TypeTag type={opt.data.type} />
                </Space>
              )}
            />
          </Form.Item>
          <Form.Item
            name="enabled"
            label={t('common:common.status')}
            valuePropName="checked"
            extra={editing?.is_default ? t('common:error.is_default') : t('userGroups:form.enabledExtra')}
          >
            <Switch disabled={Boolean(editing?.is_default)} />
          </Form.Item>
          <Form.Item name="note" label={t('common:common.note')} rules={[{ max: 255, message: t('userGroups:form.noteMax') }]}>
            <Input.TextArea rows={3} maxLength={255} showCount placeholder={t('common:common.notePlaceholder')} />
          </Form.Item>
        </Form>
      </FormDrawer>
    </div>
  );
}
