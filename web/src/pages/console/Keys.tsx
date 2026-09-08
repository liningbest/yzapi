import { useState } from 'react';
import {
  Alert,
  App,
  Button,
  Card,
  Form,
  Input,
  Modal,
  Popconfirm,
  Result,
  Space,
  Switch,
  Table,
  Tooltip,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { CopyOutlined, DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { userApi } from '@/api';
import { EmptyState, FormDrawer, PageHeader, TimeCell } from '@/components';
import type { ApiKey, CreateKeyResponse } from '@/types';

const QUERY_KEY = ['user', 'keys'];

interface NameForm {
  name: string;
}

export default function Keys() {
  const { t } = useTranslation(['console', 'common']);
  const { message } = App.useApp();
  const qc = useQueryClient();

  const [createOpen, setCreateOpen] = useState(false);
  const [created, setCreated] = useState<CreateKeyResponse | null>(null);
  const [renaming, setRenaming] = useState<ApiKey | null>(null);
  const [createForm] = Form.useForm<NameForm>();
  const [renameForm] = Form.useForm<NameForm>();

  const keys = useQuery({ queryKey: QUERY_KEY, queryFn: userApi.keys });
  const invalidate = () => qc.invalidateQueries({ queryKey: QUERY_KEY });

  const createMut = useMutation({
    mutationFn: (name: string) => userApi.createKey(name),
    onSuccess: (res) => {
      setCreated(res);
      void invalidate();
    },
  });

  const renameMut = useMutation({
    mutationFn: ({ id, name }: { id: number; name: string }) => userApi.renameKey(id, name),
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
    renameForm.setFieldsValue({ name: row.name });
    setRenaming(row);
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
              <span style={{ color: '#ef4444', display: 'inline-block', maxWidth: 320 }}>
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
          <Form<NameForm>
            form={createForm}
            layout="vertical"
            requiredMark={false}
            onFinish={(v) => createMut.mutate(v.name.trim())}
          >
            <Form.Item name="name" label={t('console:keys.name')} rules={nameRules}>
              <Input maxLength={64} showCount placeholder={t('console:keys.namePlaceholder')} autoFocus />
            </Form.Item>
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
        <Form<NameForm>
          form={renameForm}
          layout="vertical"
          requiredMark={false}
          onFinish={(v) => renaming && renameMut.mutate({ id: renaming.id, name: v.name.trim() })}
        >
          <Form.Item name="name" label={t('console:keys.name')} rules={nameRules}>
            <Input maxLength={64} showCount placeholder={t('console:keys.namePlaceholder')} autoFocus />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
