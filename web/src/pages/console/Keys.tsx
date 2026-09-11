import { useState } from 'react';
import { Alert, App, Button, Card, Form, Input, InputNumber, Modal, Popconfirm, Result, Select, Space, Switch, Table, Tag, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { CopyOutlined, DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { userApi } from '@/api';
import { EmptyState, FormDrawer, PageHeader, TimeCell } from '@/components';
import type { ApiKey, CreateKeyResponse, KeyInput } from '@/types';

const QUERY_KEY = ['user', 'keys'];

interface KeyForm {
  name: string;
  /** 'keep' (edit only) | 'never' | days */
  expires_in: string;
  allowed_models: string[];
  tokens_per_minute?: number | null;
  requests_per_minute?: number | null;
}

const EXPIRY_DAYS = ['7', '30', '90', '365'];

function toKeyInput(v: KeyForm, current?: ApiKey): KeyInput {
  let expires_at: string | null | undefined = null;
  if (v.expires_in === 'keep') expires_at = current?.expires_at ?? null;
  else if (v.expires_in !== 'never') expires_at = new Date(Date.now() + Number(v.expires_in) * 86400000).toISOString();
  return {
    name: v.name.trim(),
    expires_at,
    allowed_models: v.allowed_models ?? [],
    tokens_per_minute: v.tokens_per_minute ?? 0,
    requests_per_minute: v.requests_per_minute ?? 0,
  };
}

export default function Keys() {
  const { t } = useTranslation(['console', 'common']);
  const { message } = App.useApp();
  const qc = useQueryClient();

  const [createOpen, setCreateOpen] = useState(false);
  const [created, setCreated] = useState<CreateKeyResponse | null>(null);
  const [renaming, setRenaming] = useState<ApiKey | null>(null);
  const [createForm] = Form.useForm<KeyForm>();
  const [renameForm] = Form.useForm<KeyForm>();

  const keys = useQuery({ queryKey: QUERY_KEY, queryFn: userApi.keys });
  const models = useQuery({ queryKey: ['user', 'models'], queryFn: userApi.models });
  const modelOptions = (models.data?.models ?? []).map((m) => ({ value: m.name, label: m.name }));
  const invalidate = () => qc.invalidateQueries({ queryKey: QUERY_KEY });

  const createMut = useMutation({
    mutationFn: (body: KeyInput) => userApi.createKey(body),
    onSuccess: (res) => {
      setCreated(res);
      void invalidate();
    },
  });

  const renameMut = useMutation({
    mutationFn: ({ id, body }: { id: number; body: KeyInput }) => userApi.updateKey(id, body),
    onSuccess: () => {
      message.success(t('common:common.saveSuccess'));
      setRenaming(null);
      void invalidate();
    },
  });

  const toggleMut = useMutation({
    mutationFn: ({ id, enabled }: { id: number; enabled: boolean }) => userApi.setKeyEnabled(id, enabled),
    onSuccess: (_, vars) => {
      message.success(vars.enabled ? t('console:keys.enabledSuccess') : t('console:keys.disabledSuccess'));
      void invalidate();
    },
  });

  const removeMut = useMutation({
    mutationFn: (id: number) => userApi.removeKey(id),
    onSuccess: () => {
      message.success(t('common:common.deleteSuccess'));
      void invalidate();
    },
  });

  const openCreate = () => {
    setCreated(null);
    createForm.resetFields();
    setCreateOpen(true);
  };

  const closeCreate = () => {
    setCreateOpen(false);
    setCreated(null);
    createForm.resetFields();
  };

  const openRename = (row: ApiKey) => {
    renameForm.setFieldsValue({
      name: row.name,
      expires_in: 'keep',
      allowed_models: row.allowed_models ?? [],
      tokens_per_minute: row.tokens_per_minute || null,
      requests_per_minute: row.requests_per_minute || null,
    });
    setRenaming(row);
  };

  const expiryOptions = (withKeep: boolean) => [
    ...(withKeep ? [{ value: 'keep', label: t('console:keys.expiryKeep') }] : []),
    { value: 'never', label: t('console:keys.expiryNever') },
    ...EXPIRY_DAYS.map((d) => ({ value: d, label: t('console:keys.expiryDays', { days: d }) })),
  ];

  const restrictionFields = (withKeep: boolean) => (
    <>
      <Form.Item name="expires_in" label={t('console:keys.expiry')} extra={t('console:keys.expiryExtra')}>
        <Select options={expiryOptions(withKeep)} />
      </Form.Item>
      <Form.Item name="allowed_models" label={t('console:keys.allowedModels')} extra={t('console:keys.allowedModelsExtra')}>
        <Select mode="multiple" allowClear showSearch options={modelOptions} placeholder={t('console:keys.allowedModelsPlaceholder')} loading={models.isLoading} />
      </Form.Item>
      <Space style={{ width: '100%' }} styles={{ item: { flex: 1 } }}>
        <Form.Item name="tokens_per_minute" label={t('console:keys.tpm')} extra={t('console:keys.limitExtra')}>
          <InputNumber min={0} precision={0} style={{ width: '100%' }} placeholder="0" />
        </Form.Item>
        <Form.Item name="requests_per_minute" label={t('console:keys.rpm')} extra={t('console:keys.limitExtra')}>
          <InputNumber min={0} precision={0} style={{ width: '100%' }} placeholder="0" />
        </Form.Item>
      </Space>
    </>
  );

  const restrictionSummary = (row: ApiKey) => {
    const parts: string[] = [];
    if (row.allowed_models?.length) parts.push(t('console:keys.modelsCount', { count: row.allowed_models.length }));
    if (row.tokens_per_minute > 0) parts.push(`${row.tokens_per_minute} tok/min`);
    if (row.requests_per_minute > 0) parts.push(`${row.requests_per_minute} req/min`);
    return parts.length ? parts.join(' · ') : '-';
  };

  const copyKey = (key: string) => {
    void navigator.clipboard.writeText(key).then(() => message.success(t('common:action.copied')));
  };

  const nameRules = [
    { required: true, message: t('common:common.required') },
    { min: 1, max: 64, message: t('console:keys.nameRule') },
  ];

  const columns: ColumnsType<ApiKey> = [
    {
      title: t('console:keys.name'),
      dataIndex: 'name',
      render: (name: string) => <Typography.Text strong>{name}</Typography.Text>,
    },
    {
      title: t('console:keys.key'),
      dataIndex: 'masked',
      render: (masked: string) => (
        <Tooltip title={t('console:keys.keyTooltip')}>
          <Typography.Text code className="yz-mono">
            {masked}
          </Typography.Text>
        </Tooltip>
      ),
    },
    {
      title: t('console:keys.status'),
      dataIndex: 'enabled',
      width: 90,
      render: (enabled: boolean, row) => (
        <Switch
          size="small"
          checked={enabled}
          loading={toggleMut.isPending && toggleMut.variables?.id === row.id}
          onChange={(v) => toggleMut.mutate({ id: row.id, enabled: v })}
        />
      ),
    },
    {
      title: t('console:keys.expiry'),
      dataIndex: 'expires_at',
      width: 150,
      render: (v: string | null, row) =>
        v ? (
          row.expired ? (
            <Tag color="error">{t('console:keys.expired')}</Tag>
          ) : (
            <TimeCell value={v} absolute />
          )
        ) : (
          <span style={{ color: 'var(--yz-text-secondary)' }}>{t('console:keys.expiryNever')}</span>
        ),
    },
    {
      title: t('console:keys.restrictions'),
      key: 'restrictions',
      width: 200,
      render: (_, row) => (
        <Tooltip title={row.allowed_models?.length ? row.allowed_models.join(', ') : undefined}>
          <span style={{ fontSize: 12.5 }}>{restrictionSummary(row)}</span>
        </Tooltip>
      ),
    },
    {
      title: t('console:keys.createdAt'),
      dataIndex: 'created_at',
      width: 180,
      render: (v: string) => <TimeCell value={v} absolute />,
    },
    {
      title: t('console:keys.lastUsed'),
      dataIndex: 'last_used_at',
      width: 140,
      render: (v: string | null) => <TimeCell value={v} />,
    },
    {
      title: t('common:common.actions'),
      key: 'actions',
      width: 100,
      align: 'right',
      render: (_, row) => (
        <Space size={0}>
          <Tooltip title={t('console:keys.rename')}>
            <Button type="text" size="small" icon={<EditOutlined />} onClick={() => openRename(row)} />
          </Tooltip>
          <Popconfirm
            title={t('console:keys.deleteTitle', { name: row.name })}
            description={
              <span style={{ color: 'var(--yz-danger)', display: 'inline-block', maxWidth: 320 }}>
                {t('console:keys.deleteWarn')}
              </span>
            }
            okText={t('common:action.delete')}
            okButtonProps={{ danger: true, loading: removeMut.isPending }}
            cancelText={t('common:action.cancel')}
            onConfirm={() => removeMut.mutate(row.id)}
          >
            <Tooltip title={t('common:action.delete')}>
              <Button type="text" size="small" danger icon={<DeleteOutlined />} />
            </Tooltip>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  const data = keys.data ?? [];

  return (
    <div>
      <PageHeader
        title={t('console:keys.title')}
        subtitle={t('console:keys.subtitle')}
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            {t('console:keys.create')}
          </Button>
        }
      />

      <Card className="yz-card">
        {!keys.isLoading && data.length === 0 ? (
          <EmptyState
            title={t('console:keys.empty')}
            hint={t('console:keys.emptyHint')}
            actionText={t('console:keys.create')}
            onAction={openCreate}
          />
        ) : (
          <Table
            className="yz-table"
            size="middle"
            rowKey="id"
            columns={columns}
            dataSource={data}
            loading={keys.isLoading}
            pagination={false}
            scroll={{ x: 'max-content' }}
          />
        )}
      </Card>

      {/* Create drawer: form first, then a one-time reveal of the full key (footer collapses to Close). */}
      <FormDrawer
        open={createOpen}
        title={created ? t('console:keys.created') : t('console:keys.create')}
        onClose={closeCreate}
        onSubmit={created ? undefined : () => createForm.submit()}
        submitText={t('common:action.create')}
        submitting={createMut.isPending}
        width={560}
      >
        {created ? (
          <div>
            <Result
              status="success"
              title={t('console:keys.created')}
              subTitle={t('console:keys.createdSub', { name: created.item.name })}
              style={{ padding: '8px 0 16px' }}
            />
            <div className="yz-code-block" style={{ fontSize: 13.5 }}>
              <Typography.Text copyable={{ text: created.key }} className="yz-mono" style={{ fontSize: 13.5 }}>
                {created.key}
              </Typography.Text>
            </div>
            <Button
              type="primary"
              block
              icon={<CopyOutlined />}
              style={{ marginTop: 12 }}
              onClick={() => copyKey(created.key)}
            >
              {t('console:keys.copyKey')}
            </Button>
            <Alert type="error" showIcon message={t('console:keys.onceWarn')} style={{ marginTop: 16 }} />
          </div>
        ) : (
          <Form<KeyForm>
            form={createForm}
            layout="vertical"
            requiredMark={false}
            initialValues={{ expires_in: 'never', allowed_models: [] }}
            onFinish={(v) => createMut.mutate(toKeyInput(v))}
          >
            <Form.Item name="name" label={t('console:keys.name')} rules={nameRules}>
              <Input maxLength={64} showCount placeholder={t('console:keys.namePlaceholder')} autoFocus />
            </Form.Item>
            {restrictionFields(false)}
            <Typography.Text type="secondary" style={{ fontSize: 12.5 }}>
              {t('console:keys.keyTooltip')}
            </Typography.Text>
          </Form>
        )}
      </FormDrawer>

      <Modal
        open={renaming !== null}
        title={t('console:keys.rename')}
        onCancel={() => setRenaming(null)}
        onOk={() => renameForm.submit()}
        okText={t('common:action.save')}
        cancelText={t('common:action.cancel')}
        confirmLoading={renameMut.isPending}
        destroyOnClose
      >
        <Form<KeyForm>
          form={renameForm}
          layout="vertical"
          requiredMark={false}
          onFinish={(v) => renaming && renameMut.mutate({ id: renaming.id, body: toKeyInput(v, renaming) })}
        >
          <Form.Item name="name" label={t('console:keys.name')} rules={nameRules}>
            <Input maxLength={64} showCount placeholder={t('console:keys.namePlaceholder')} autoFocus />
          </Form.Item>
          {restrictionFields(true)}
        </Form>
      </Modal>
    </div>
  );
}
