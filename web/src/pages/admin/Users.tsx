import { useMemo, useState } from 'react';
import {
  Alert,
  App,
  Avatar,
  Badge,
  Button,
  Card,
  Form,
  Input,
  Modal,
  Popconfirm,
  Radio,
  Select,
  Space,
  Switch,
  Table,
  Tooltip,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  DeleteOutlined,
  EditOutlined,
  KeyOutlined,
  LockOutlined,
  PlusOutlined,
  ReloadOutlined,
  UnlockOutlined,
} from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { userGroupsApi, usersApi } from '@/api';
import type { NormalizedError } from '@/api';
import { EmptyState, FilterBar, FormDrawer, PageHeader, RoleTag, TimeCell } from '@/components';
import { useTableQuery } from '@/hooks/useTableQuery';
import { useAuthStore } from '@/stores/auth';
import type { AdminUser, Role, UserCreateInput, UserUpdateInput } from '@/types';
import { CHART_PALETTE } from '@/utils/constants';

interface Filters {
  role?: Role;
  group_id?: number;
  enabled?: boolean;
  q?: string;
}

interface CreateFormValues extends UserCreateInput {
  confirm: string;
}

interface ResetFormValues {
  password: string;
  confirm: string;
}

const USERNAME_RE = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;

function avatarColor(name: string): string {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  return CHART_PALETTE[h % CHART_PALETTE.length];
}

export default function Users() {
  const { t } = useTranslation(['users', 'common']);
  const { message } = App.useApp();
  const qc = useQueryClient();
  const me = useAuthStore((s) => s.user);
  const { filters, setFilters, params, pagination } = useTableQuery<Filters>({});

  const [drawer, setDrawer] = useState<{ open: boolean; record?: AdminUser }>({ open: false });
  const [resetTarget, setResetTarget] = useState<AdminUser | null>(null);
  const [createForm] = Form.useForm<CreateFormValues>();
  const [editForm] = Form.useForm<UserUpdateInput>();
  const [resetForm] = Form.useForm<ResetFormValues>();

  const list = useQuery({
    queryKey: ['users', params],
    queryFn: () => usersApi.list(params),
    placeholderData: (prev) => prev,
  });
  const groups = useQuery({
    queryKey: ['user-groups', 'options'],
    queryFn: () => userGroupsApi.list({ page_size: 200 }),
  });

  const groupOptions = useMemo(
    () => (groups.data?.items ?? []).map((g) => ({ value: g.id, label: g.name })),
    [groups.data],
  );
  const defaultGroupId = useMemo(() => groups.data?.items.find((g) => g.is_default)?.id, [groups.data]);
  const adminCount = useMemo(
    () => (list.data?.items ?? []).filter((u) => u.role === 'admin').length,
    [list.data],
  );

  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ['users'] });
    void qc.invalidateQueries({ queryKey: ['user-groups'] });
  };

  const handleError = (e: unknown) => {
    const err = e as NormalizedError;
    if (err.code === 'last_admin') message.error(t('common:error.last_admin'));
  };

  const createMut = useMutation({
    mutationFn: (body: UserCreateInput) => usersApi.create(body),
    onSuccess: () => {
      message.success(t('common:common.createSuccess'));
      setDrawer({ open: false });
      invalidate();
    },
  });
  const updateMut = useMutation({
    mutationFn: ({ id, body }: { id: number; body: UserUpdateInput }) => usersApi.update(id, body),
    onSuccess: () => {
      message.success(t('common:common.updateSuccess'));
      setDrawer({ open: false });
      invalidate();
    },
    onError: handleError,
  });
  const removeMut = useMutation({
    mutationFn: (id: number) => usersApi.remove(id),
    onSuccess: () => {
      message.success(t('common:common.deleteSuccess'));
      invalidate();
    },
    onError: handleError,
  });
  const enabledMut = useMutation({
    mutationFn: ({ id, enabled }: { id: number; enabled: boolean }) => usersApi.setEnabled(id, enabled),
    onSuccess: () => {
      message.success(t('common:common.operationSuccess'));
      invalidate();
    },
    onError: handleError,
  });
  const unlockMut = useMutation({
    mutationFn: (id: number) => usersApi.unlock(id),
    onSuccess: () => {
      message.success(t('users:unlockSuccess'));
      invalidate();
    },
  });
  const resetMut = useMutation({
    mutationFn: ({ id, password }: { id: number; password: string }) => usersApi.resetPassword(id, password),
    onSuccess: () => {
      message.success(t('users:resetSuccess'));
      setResetTarget(null);
      invalidate();
    },
    onError: handleError,
  });

  const openCreate = () => setDrawer({ open: true });
  const openEdit = (record: AdminUser) => setDrawer({ open: true, record });

  const submitDrawer = async () => {
    if (drawer.record) {
      const values = await editForm.validateFields();
      updateMut.mutate({ id: drawer.record.id, body: values });
    } else {
      const values = await createForm.validateFields();
      createMut.mutate({
        username: values.username.trim(),
        password: values.password,
        group_id: values.group_id,
        role: values.role,
        note: values.note,
      });
    }
  };

  const submitReset = async () => {
    if (!resetTarget) return;
    const values = await resetForm.validateFields();
    resetMut.mutate({ id: resetTarget.id, password: values.password });
  };

  const isLastAdmin = (u: AdminUser) => u.role === 'admin' && adminCount <= 1;
  const isSelf = (u: AdminUser) => me?.id === u.id;

  const columns: ColumnsType<AdminUser> = [
    {
      title: t('common:common.username'),
      dataIndex: 'username',
      render: (v: string, r) => (
        <Space size={10}>
          <Avatar size={28} style={{ background: avatarColor(v), fontWeight: 600, fontSize: 13 }}>
            {v.slice(0, 1).toUpperCase()}
          </Avatar>
          <span>
            <Typography.Text strong>{v}</Typography.Text>
            {isSelf(r) ? (
              <Typography.Text type="secondary" style={{ fontSize: 12, marginLeft: 6 }}>
                {t('users:you')}
              </Typography.Text>
            ) : null}
          </span>
        </Space>
      ),
    },
    {
      title: t('common:common.role'),
      dataIndex: 'role',
      width: 110,
      render: (v: Role) => <RoleTag role={v} />,
    },
    {
      title: t('common:common.userGroup'),
      dataIndex: 'group_name',
      render: (v: string) => v || '-',
    },
    {
      title: t('common:common.status'),
      dataIndex: 'enabled',
      width: 100,
      render: (v: boolean, r) => {
        const locked = isLastAdmin(r) && v;
        const node = (
          <Switch
            size="small"
            checked={v}
            disabled={locked}
            loading={enabledMut.isPending && enabledMut.variables?.id === r.id}
            onChange={(checked) => enabledMut.mutate({ id: r.id, enabled: checked })}
          />
        );
        return locked ? <Tooltip title={t('common:error.last_admin')}>{node}</Tooltip> : node;
      },
    },
    {
      title: t('users:lockStatus'),
      dataIndex: 'locked',
      width: 150,
      render: (v: boolean, r) =>
        v ? (
          <Space size={6}>
            <Badge status="error" text={<span style={{ color: '#ef4444' }}>{t('common:common.locked')}</span>} />
            <Button
              size="small"
              type="link"
              icon={<UnlockOutlined />}
              loading={unlockMut.isPending && unlockMut.variables === r.id}
              onClick={() => unlockMut.mutate(r.id)}
              style={{ padding: 0, height: 'auto' }}
            >
              {t('common:action.unlock')}
            </Button>
          </Space>
        ) : (
          <Typography.Text type="secondary">{t('users:normal')}</Typography.Text>
        ),
    },
    {
      title: t('common:nav.keys'),
      dataIndex: 'api_keys_count',
      width: 110,
      align: 'right',
      render: (v: number) => (
        <Space size={4}>
          <KeyOutlined style={{ color: 'var(--yz-text-secondary)' }} />
          <span style={{ fontVariantNumeric: 'tabular-nums' }}>{v ?? 0}</span>
        </Space>
      ),
    },
    {
      title: t('users:lastLogin'),
      dataIndex: 'last_login_at',
      width: 140,
      render: (v: string | null) => <TimeCell value={v} emptyText={t('common:common.never')} />,
    },
    {
      title: t('common:common.note'),
      dataIndex: 'note',
      ellipsis: true,
      width: 200,
      render: (v: string) =>
        v ? <Typography.Text ellipsis={{ tooltip: v }} style={{ maxWidth: 180 }}>{v}</Typography.Text> : '-',
    },
    {
      title: t('common:common.actions'),
      key: 'actions',
      width: 130,
      fixed: 'right',
      render: (_, r) => {
        const lastAdmin = isLastAdmin(r);
        const self = isSelf(r);
        const deleteDisabled = lastAdmin || self;
        const deleteTip = lastAdmin ? t('common:error.last_admin') : self ? t('users:cannotDeleteSelf') : t('common:action.delete');
        return (
          <Space size={0}>
            <Tooltip title={t('common:action.edit')}>
              <Button type="text" size="small" icon={<EditOutlined />} onClick={() => openEdit(r)} />
            </Tooltip>
            <Tooltip title={t('common:action.resetPassword')}>
              <Button
                type="text"
                size="small"
                icon={<ReloadOutlined />}
                onClick={() => setResetTarget(r)}
              />
            </Tooltip>
            <Tooltip title={deleteTip}>
              {deleteDisabled ? (
                <Button type="text" size="small" danger icon={<DeleteOutlined />} disabled />
              ) : (
                <Popconfirm
                  title={t('common:common.confirmDeleteName', { name: r.username })}
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
  const editRoleLocked = Boolean(editing && isLastAdmin(editing));

  return (
    <div>
      <PageHeader
        title={t('users:title')}
        subtitle={t('users:subtitle')}
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            {t('users:add')}
          </Button>
        }
      />
      <Card className="yz-card">
        <FilterBar>
          <Select
            style={{ width: 160 }}
            allowClear
            placeholder={t('common:common.role')}
            value={filters.role}
            onChange={(v) => setFilters({ role: v })}
            options={[
              { value: 'admin', label: t('common:role.admin') },
              { value: 'user', label: t('common:role.user') },
            ]}
          />
          <Select
            style={{ width: 160 }}
            allowClear
            showSearch
            optionFilterProp="label"
            placeholder={t('common:common.userGroup')}
            value={filters.group_id}
            onChange={(v) => setFilters({ group_id: v })}
            options={groupOptions}
            loading={groups.isLoading}
          />
          <Select
            style={{ width: 160 }}
            allowClear
            placeholder={t('common:common.status')}
            value={filters.enabled}
            onChange={(v) => setFilters({ enabled: v })}
            options={[
              { value: true, label: t('common:common.enabled') },
              { value: false, label: t('common:common.disabled') },
            ]}
          />
          <Input.Search
            style={{ width: 240 }}
            allowClear
            placeholder={t('users:searchPlaceholder')}
            onSearch={(v) => setFilters({ q: v.trim() || undefined })}
          />
        </FilterBar>
        <Table<AdminUser>
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
              <EmptyState title={t('users:empty')} hint={t('users:emptyHint')} actionText={t('users:add')} onAction={openCreate} />
            ),
          }}
        />
      </Card>

      <FormDrawer
        open={drawer.open}
        title={editing ? t('users:editTitle', { name: editing.username }) : t('users:addTitle')}
        onClose={() => setDrawer({ open: false })}
        onSubmit={() => void submitDrawer()}
        submitting={createMut.isPending || updateMut.isPending}
        width={560}
      >
        {editing ? (
          <Form
            key={editing.id}
            form={editForm}
            layout="vertical"
            autoComplete="off"
            initialValues={{ group_id: editing.group_id, role: editing.role, note: editing.note }}
          >
            <Form.Item label={t('common:common.username')}>
              <Input value={editing.username} readOnly disabled />
            </Form.Item>
            <Form.Item
              name="role"
              label={t('common:common.role')}
              rules={[{ required: true, message: t('common:common.required') }]}
              extra={editRoleLocked ? t('common:error.last_admin') : t('users:form.roleExtra')}
            >
              <Radio.Group
                optionType="button"
                buttonStyle="solid"
                options={[
                  { value: 'user', label: t('common:role.user'), disabled: editRoleLocked },
                  { value: 'admin', label: t('common:role.admin') },
                ]}
              />
            </Form.Item>
            <Form.Item
              name="group_id"
              label={t('common:common.userGroup')}
              rules={[{ required: true, message: t('common:common.required') }]}
              extra={t('users:form.groupExtra')}
            >
              <Select showSearch optionFilterProp="label" options={groupOptions} loading={groups.isLoading} />
            </Form.Item>
            <Form.Item name="note" label={t('common:common.note')} rules={[{ max: 255, message: t('users:form.noteMax') }]}>
              <Input.TextArea rows={3} maxLength={255} showCount placeholder={t('common:common.notePlaceholder')} />
            </Form.Item>
          </Form>
        ) : (
          <Form
            key="create"
            form={createForm}
            layout="vertical"
            autoComplete="off"
            initialValues={{ role: 'user', group_id: defaultGroupId }}
          >
            <Form.Item
              name="username"
              label={t('common:common.username')}
              extra={t('users:form.usernameExtra')}
              rules={[
                { required: true, message: t('common:common.required') },
                { min: 3, max: 64, message: t('users:form.usernameLength') },
                { pattern: USERNAME_RE, message: t('users:form.usernamePattern') },
              ]}
            >
              <Input placeholder={t('users:form.usernamePlaceholder')} autoFocus />
            </Form.Item>
            <Form.Item
              name="role"
              label={t('common:common.role')}
              rules={[{ required: true, message: t('common:common.required') }]}
              extra={t('users:form.roleExtra')}
            >
              <Radio.Group
                optionType="button"
                buttonStyle="solid"
                options={[
                  { value: 'user', label: t('common:role.user') },
                  { value: 'admin', label: t('common:role.admin') },
                ]}
              />
            </Form.Item>
            <Form.Item
              name="group_id"
              label={t('common:common.userGroup')}
              rules={[{ required: true, message: t('common:common.required') }]}
              extra={t('users:form.groupExtra')}
            >
              <Select
                showSearch
                optionFilterProp="label"
                placeholder={t('common:common.please_select')}
                options={groupOptions}
                loading={groups.isLoading}
              />
            </Form.Item>
            <Form.Item
              name="password"
              label={t('common:common.password')}
              extra={t('users:form.passwordExtra')}
              rules={[
                { required: true, message: t('common:common.required') },
                { min: 12, max: 128, message: t('users:form.passwordLength') },
              ]}
            >
              <Input.Password prefix={<LockOutlined />} placeholder={t('users:form.passwordPlaceholder')} />
            </Form.Item>
            <Form.Item
              name="confirm"
              label={t('common:common.confirmPassword')}
              dependencies={['password']}
              rules={[
                { required: true, message: t('common:common.required') },
                ({ getFieldValue }) => ({
                  validator: (_, v: string) =>
                    !v || v === getFieldValue('password')
                      ? Promise.resolve()
                      : Promise.reject(new Error(t('users:form.passwordMismatch'))),
                }),
              ]}
            >
              <Input.Password prefix={<LockOutlined />} placeholder={t('users:form.confirmPlaceholder')} />
            </Form.Item>
            <Form.Item name="note" label={t('common:common.note')} rules={[{ max: 255, message: t('users:form.noteMax') }]}>
              <Input.TextArea rows={3} maxLength={255} showCount placeholder={t('common:common.notePlaceholder')} />
            </Form.Item>
          </Form>
        )}
      </FormDrawer>

      <Modal
        open={Boolean(resetTarget)}
        title={t('users:resetTitle', { name: resetTarget?.username ?? '' })}
        onCancel={() => setResetTarget(null)}
        onOk={() => void submitReset()}
        okText={t('common:action.resetPassword')}
        confirmLoading={resetMut.isPending}
        destroyOnHidden
      >
        <Alert type="info" showIcon message={t('users:resetNotice')} style={{ marginBottom: 16 }} />
        <Form form={resetForm} layout="vertical" autoComplete="off">
          <Form.Item
            name="password"
            label={t('users:newPassword')}
            extra={t('users:form.passwordExtra')}
            rules={[
              { required: true, message: t('common:common.required') },
              { min: 12, max: 128, message: t('users:form.passwordLength') },
            ]}
          >
            <Input.Password prefix={<LockOutlined />} placeholder={t('users:form.passwordPlaceholder')} autoFocus />
          </Form.Item>
          <Form.Item
            name="confirm"
            label={t('common:common.confirmPassword')}
            dependencies={['password']}
            rules={[
              { required: true, message: t('common:common.required') },
              ({ getFieldValue }) => ({
                validator: (_, v: string) =>
                  !v || v === getFieldValue('password')
                    ? Promise.resolve()
                    : Promise.reject(new Error(t('users:form.passwordMismatch'))),
              }),
            ]}
          >
            <Input.Password prefix={<LockOutlined />} placeholder={t('users:form.confirmPlaceholder')} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
