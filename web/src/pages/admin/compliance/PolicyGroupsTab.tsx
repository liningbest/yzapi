import { useMemo, useState } from 'react';
import { Alert, App, Button, Form, Input, Popconfirm, Radio, Space, Switch, Table, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { complianceApi } from '@/api';
import { ActionTag, EmptyState, FilterBar, FormDrawer, RiskTag, TimeCell } from '@/components';
import type { PolicyAction, PolicyGroup, PolicyGroupInput, RiskLevel } from '@/types';
import { formatNumber } from '@/utils/format';
import { POLICY_GROUPS_KEY } from './keys';

interface Props {
  groups: PolicyGroup[];
  loading: boolean;
}

interface GroupForm {
  name: string;
  action: PolicyAction;
  risk_level: RiskLevel;
  enabled: boolean;
  description?: string;
}

function PolicyGroupDrawer({ open, group, onClose }: { open: boolean; group: PolicyGroup | null; onClose: () => void }) {
  const { t } = useTranslation(['compliance', 'common']);
  const { message } = App.useApp();
  const qc = useQueryClient();
  const [form] = Form.useForm<GroupForm>();
  const action = Form.useWatch('action', form);

  const save = useMutation({
    mutationFn: (values: GroupForm) => {
      const body: PolicyGroupInput = {
        name: values.name.trim(),
        action: values.action,
        risk_level: values.risk_level,
        enabled: values.enabled,
        description: values.description?.trim() ?? '',
      };
      return group ? complianceApi.updatePolicyGroup(group.id, body) : complianceApi.createPolicyGroup(body);
    },
    onSuccess: () => {
      message.success(group ? t('common:common.updateSuccess') : t('common:common.createSuccess'));
      onClose();
      void qc.invalidateQueries({ queryKey: POLICY_GROUPS_KEY });
    },
  });

  const submit = async () => {
    const values = await form.validateFields();
    save.mutate(values);
  };

  const initialValues: GroupForm = group
    ? { name: group.name, action: group.action, risk_level: group.risk_level, enabled: group.enabled, description: group.description }
    : { name: '', action: 'audit', risk_level: 'medium', enabled: true, description: '' };

  return (
    <FormDrawer
      open={open}
      title={group ? t('compliance:groups.edit') : t('compliance:groups.add')}
      onClose={onClose}
      onSubmit={submit}
      submitting={save.isPending}
      width={560}
    >
      <Form form={form} layout="vertical" initialValues={initialValues} requiredMark="optional">
        <Form.Item
          name="name"
          label={t('compliance:groups.name')}
          rules={[
            { required: true, message: t('common:common.required') },
            { whitespace: true, message: t('common:common.required') },
          ]}
        >
          <Input placeholder={t('compliance:groups.namePlaceholder')} maxLength={64} />
        </Form.Item>
        <Form.Item name="action" label={t('compliance:groups.action')} extra={t('compliance:groups.actionExtra')}>
          <Radio.Group optionType="button" buttonStyle="solid">
            <Radio.Button value="audit">{t('common:policyAction.audit')}</Radio.Button>
            <Radio.Button value="block">{t('common:policyAction.block')}</Radio.Button>
          </Radio.Group>
        </Form.Item>
        {action === 'block' ? (
          <Alert type="warning" showIcon message={t('compliance:groups.blockWarning')} style={{ marginBottom: 24 }} />
        ) : null}
        <Form.Item name="risk_level" label={t('compliance:groups.risk')} extra={t('compliance:groups.riskExtra')}>
          <Radio.Group optionType="button" buttonStyle="solid">
            <Radio.Button value="low">{t('common:risk.low')}</Radio.Button>
            <Radio.Button value="medium">{t('common:risk.medium')}</Radio.Button>
            <Radio.Button value="high">{t('common:risk.high')}</Radio.Button>
          </Radio.Group>
        </Form.Item>
        <Form.Item name="enabled" label={t('common:common.status')} valuePropName="checked" extra={t('compliance:groups.enabledExtra')}>
          <Switch checkedChildren={t('common:common.enabled')} unCheckedChildren={t('common:common.disabled')} />
        </Form.Item>
        <Form.Item name="description" label={t('common:common.description')}>
          <Input.TextArea rows={3} maxLength={500} showCount placeholder={t('compliance:groups.descriptionPlaceholder')} />
        </Form.Item>
      </Form>
    </FormDrawer>
  );
}

export default function PolicyGroupsTab({ groups, loading }: Props) {
  const { t } = useTranslation(['compliance', 'common']);
  const { message } = App.useApp();
  const qc = useQueryClient();
  const [q, setQ] = useState('');
  const [drawer, setDrawer] = useState<{ open: boolean; group: PolicyGroup | null }>({ open: false, group: null });

  const invalidate = () => qc.invalidateQueries({ queryKey: POLICY_GROUPS_KEY });

  const toggle = useMutation({
    mutationFn: ({ id, enabled }: { id: number; enabled: boolean }) => complianceApi.setPolicyGroupEnabled(id, enabled),
    onSuccess: () => {
      message.success(t('common:common.operationSuccess'));
      void invalidate();
    },
  });

  const remove = useMutation({
    mutationFn: (id: number) => complianceApi.removePolicyGroup(id),
    onSuccess: () => {
      message.success(t('common:common.deleteSuccess'));
      void invalidate();
    },
  });

  const rows = useMemo(() => {
    const kw = q.trim().toLowerCase();
    if (!kw) return groups;
    return groups.filter((g) => g.name.toLowerCase().includes(kw) || g.description?.toLowerCase().includes(kw));
  }, [groups, q]);

  const columns: ColumnsType<PolicyGroup> = [
    {
      title: t('compliance:groups.name'),
      dataIndex: 'name',
      render: (name: string) => <span style={{ fontWeight: 600 }}>{name}</span>,
    },
    {
      title: t('compliance:groups.action'),
      dataIndex: 'action',
      width: 100,
      render: (a: PolicyAction) => <ActionTag action={a} />,
    },
    {
      title: t('compliance:groups.risk'),
      dataIndex: 'risk_level',
      width: 100,
      render: (r: RiskLevel) => <RiskTag risk={r} />,
    },
    {
      title: t('common:common.status'),
      dataIndex: 'enabled',
      width: 90,
      align: 'center',
      render: (enabled: boolean, r) => (
        <Switch
          size="small"
          checked={enabled}
          loading={toggle.isPending && toggle.variables?.id === r.id}
          onChange={(v) => toggle.mutate({ id: r.id, enabled: v })}
        />
      ),
    },
    {
      title: t('compliance:groups.wordsCount'),
      dataIndex: 'words_count',
      width: 100,
      align: 'right',
      render: (v: number) => <span style={{ fontVariantNumeric: 'tabular-nums' }}>{formatNumber(v ?? 0)}</span>,
    },
    {
      title: t('compliance:groups.samplesCount'),
      dataIndex: 'samples_count',
      width: 100,
      align: 'right',
      render: (v: number) => <span style={{ fontVariantNumeric: 'tabular-nums' }}>{formatNumber(v ?? 0)}</span>,
    },
    {
      title: t('common:common.description'),
      dataIndex: 'description',
      width: 260,
      render: (d: string) =>
        d ? (
          <Typography.Text ellipsis={{ tooltip: d }} style={{ maxWidth: 260, display: 'block' }}>
            {d}
          </Typography.Text>
        ) : (
          <Typography.Text type="secondary">-</Typography.Text>
        ),
    },
    {
      title: t('common:common.createdAt'),
      dataIndex: 'created_at',
      width: 130,
      render: (v: string) => <TimeCell value={v} />,
    },
    {
      title: t('common:common.actions'),
      key: 'actions',
      width: 100,
      align: 'center',
      fixed: 'right',
      render: (_, r) => {
        const inUse = (r.words_count ?? 0) + (r.samples_count ?? 0) > 0;
        return (
          <Space size={0}>
            <Tooltip title={t('common:action.edit')}>
              <Button type="text" size="small" icon={<EditOutlined />} onClick={() => setDrawer({ open: true, group: r })} />
            </Tooltip>
            {inUse ? (
              <Tooltip title={t('compliance:groups.deleteDisabled')}>
                <Button type="text" size="small" danger disabled icon={<DeleteOutlined />} />
              </Tooltip>
            ) : (
              <Popconfirm
                title={t('common:common.confirmDeleteName', { name: r.name })}
                okText={t('common:action.delete')}
                okButtonProps={{ danger: true }}
                onConfirm={() => remove.mutate(r.id)}
              >
                <Tooltip title={t('common:action.delete')}>
                  <Button type="text" size="small" danger icon={<DeleteOutlined />} />
                </Tooltip>
              </Popconfirm>
            )}
          </Space>
        );
      },
    },
  ];

  return (
    <div>
      <FilterBar
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setDrawer({ open: true, group: null })}>
            {t('compliance:groups.add')}
          </Button>
        }
      >
        <Input.Search
          allowClear
          placeholder={t('compliance:groups.searchPlaceholder')}
          style={{ width: 240 }}
          onSearch={(v) => setQ(v)}
          onChange={(e) => {
            if (!e.target.value) setQ('');
          }}
        />
      </FilterBar>

      <Table<PolicyGroup>
        className="yz-table"
        size="middle"
        rowKey="id"
        loading={loading}
        columns={columns}
        dataSource={rows}
        pagination={{ pageSize: 20, showSizeChanger: false, hideOnSinglePage: true }}
        scroll={{ x: 'max-content' }}
        locale={{
          emptyText: (
            <EmptyState
              title={t('compliance:groups.empty')}
              hint={t('compliance:groups.emptyHint')}
              actionText={t('compliance:groups.add')}
              onAction={() => setDrawer({ open: true, group: null })}
            />
          ),
        }}
      />

      <PolicyGroupDrawer open={drawer.open} group={drawer.group} onClose={() => setDrawer((d) => ({ ...d, open: false }))} />
    </div>
  );
}
