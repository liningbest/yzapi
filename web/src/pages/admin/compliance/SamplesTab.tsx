import { useState } from 'react';
import { App, Button, Checkbox, Form, Input, Popconfirm, Select, Space, Switch, Table, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { DeleteOutlined, EditOutlined, PlusOutlined, ThunderboltOutlined } from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { complianceApi } from '@/api';
import { EmptyState, FilterBar, FormDrawer, TimeCell, VectorizedBadge } from '@/components';
import { useTableQuery } from '@/hooks/useTableQuery';
import type { BuildResult, ComplianceSample, ComplianceSampleInput, PolicyGroup } from '@/types';
import PolicyGroupSelect, { GroupTags } from './PolicyGroupSelect';
import { POLICY_GROUPS_KEY, SAMPLES_KEY } from './keys';

interface Filters {
  policy_group_id?: number;
  q?: string;
  vectorized?: boolean;
}

interface Props {
  groups: PolicyGroup[];
  groupsLoading: boolean;
}

interface SampleForm {
  policy_group_id: number;
  text: string;
  note?: string;
  enabled: boolean;
  build_vector: boolean;
}

function SampleDrawer({
  open,
  sample,
  groups,
  onClose,
}: {
  open: boolean;
  sample: ComplianceSample | null;
  groups: PolicyGroup[];
  onClose: () => void;
}) {
  const { t } = useTranslation(['compliance', 'common']);
  const { message } = App.useApp();
  const qc = useQueryClient();
  const [form] = Form.useForm<SampleForm>();

  const save = useMutation({
    mutationFn: (values: SampleForm) => {
      const body: ComplianceSampleInput = {
        policy_group_id: values.policy_group_id,
        text: values.text.trim(),
        note: values.note?.trim() ?? '',
        enabled: values.enabled,
        build_vector: values.build_vector,
      };
      return sample ? complianceApi.updateSample(sample.id, body) : complianceApi.createSample(body);
    },
    onSuccess: () => {
      message.success(sample ? t('common:common.updateSuccess') : t('common:common.createSuccess'));
      onClose();
      void qc.invalidateQueries({ queryKey: SAMPLES_KEY });
      void qc.invalidateQueries({ queryKey: POLICY_GROUPS_KEY });
    },
  });

  const submit = async () => {
    const values = await form.validateFields();
    save.mutate(values);
  };

  const initialValues: Partial<SampleForm> = sample
    ? { policy_group_id: sample.policy_group_id, text: sample.text, note: sample.note, enabled: sample.enabled, build_vector: true }
    : { policy_group_id: groups.length === 1 ? groups[0].id : undefined, text: '', note: '', enabled: true, build_vector: true };

  return (
    <FormDrawer
      open={open}
      title={sample ? t('compliance:samples.edit') : t('compliance:samples.add')}
      onClose={onClose}
      onSubmit={submit}
      submitting={save.isPending}
      width={560}
    >
      <Form form={form} layout="vertical" initialValues={initialValues} requiredMark="optional">
        <Form.Item
          name="policy_group_id"
          label={t('compliance:policyGroup')}
          rules={[{ required: true, message: t('common:common.required') }]}
        >
          <PolicyGroupSelect groups={groups} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item
          name="text"
          label={t('compliance:samples.text')}
          extra={t('compliance:samples.textExtra')}
          rules={[
            { required: true, message: t('common:common.required') },
            { whitespace: true, message: t('common:common.required') },
          ]}
        >
          <Input.TextArea rows={6} maxLength={65536} showCount placeholder={t('compliance:samples.textPlaceholder')} />
        </Form.Item>
        <Form.Item name="note" label={t('common:common.note')}>
          <Input placeholder={t('common:common.notePlaceholder')} maxLength={200} />
        </Form.Item>
        <Form.Item name="enabled" label={t('common:common.status')} valuePropName="checked">
          <Switch checkedChildren={t('common:common.enabled')} unCheckedChildren={t('common:common.disabled')} />
        </Form.Item>
        <Form.Item name="build_vector" valuePropName="checked" extra={t('compliance:samples.buildVectorExtra')}>
          <Checkbox>{t('compliance:samples.buildVector')}</Checkbox>
        </Form.Item>
      </Form>
    </FormDrawer>
  );
}

export default function SamplesTab({ groups, groupsLoading }: Props) {
  const { t } = useTranslation(['compliance', 'common']);
  const { message } = App.useApp();
  const qc = useQueryClient();
  const { filters, setFilters, params, pagination } = useTableQuery<Filters>({});
  const [selected, setSelected] = useState<number[]>([]);
  const [drawer, setDrawer] = useState<{ open: boolean; sample: ComplianceSample | null }>({ open: false, sample: null });

  const list = useQuery({
    queryKey: [...SAMPLES_KEY, params],
    queryFn: () => complianceApi.samples(params),
    placeholderData: (prev) => prev,
  });

  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: SAMPLES_KEY });
    void qc.invalidateQueries({ queryKey: POLICY_GROUPS_KEY });
  };

  const reportBuild = (res: BuildResult) => {
    const base = t('compliance:samples.buildResult', { built: res.built, failed: res.failed });
    const text = res.error ? `${base}: ${res.error}` : base;
    if (res.failed > 0) message.warning(text);
    else message.success(text);
  };

  const build = useMutation({
    mutationFn: (body: { ids?: number[]; all?: boolean }) => complianceApi.buildSamples(body),
    onSuccess: (res) => {
      reportBuild(res);
      setSelected([]);
      invalidate();
    },
  });

  const toggle = useMutation({
    mutationFn: ({ sample, enabled }: { sample: ComplianceSample; enabled: boolean }) =>
      complianceApi.updateSample(sample.id, {
        policy_group_id: sample.policy_group_id,
        text: sample.text,
        note: sample.note,
        enabled,
        build_vector: false,
      }),
    onSuccess: () => {
      message.success(t('common:common.operationSuccess'));
      invalidate();
    },
  });

  const remove = useMutation({
    mutationFn: (id: number) => complianceApi.removeSample(id),
    onSuccess: () => {
      message.success(t('common:common.deleteSuccess'));
      invalidate();
    },
  });

  const columns: ColumnsType<ComplianceSample> = [
    {
      title: t('compliance:samples.text'),
      dataIndex: 'text',
      render: (text: string) => (
        <Typography.Paragraph
          ellipsis={{ rows: 2, expandable: true, symbol: t('common:action.expand') }}
          style={{ marginBottom: 0, maxWidth: 460, whiteSpace: 'pre-wrap' }}
        >
          {text}
        </Typography.Paragraph>
      ),
    },
    {
      title: t('compliance:policyGroup'),
      dataIndex: 'policy_group',
      width: 260,
      render: (_, r) => <GroupTags group={r.policy_group} compact />,
    },
    {
      title: t('compliance:samples.vector'),
      dataIndex: 'vectorized',
      width: 120,
      render: (_, r) => <VectorizedBadge vectorized={r.vectorized} dim={r.vector_dim} />,
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
          loading={toggle.isPending && toggle.variables?.sample.id === r.id}
          onChange={(v) => toggle.mutate({ sample: r, enabled: v })}
        />
      ),
    },
    {
      title: t('common:common.note'),
      dataIndex: 'note',
      width: 180,
      render: (note: string) =>
        note ? (
          <Typography.Text ellipsis={{ tooltip: note }} style={{ maxWidth: 180, display: 'block' }}>
            {note}
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
      render: (_, r) => (
        <Space size={0}>
          <Tooltip title={t('common:action.edit')}>
            <Button type="text" size="small" icon={<EditOutlined />} onClick={() => setDrawer({ open: true, sample: r })} />
          </Tooltip>
          <Popconfirm
            title={t('compliance:samples.confirmDelete')}
            okText={t('common:action.delete')}
            okButtonProps={{ danger: true }}
            onConfirm={() => remove.mutate(r.id)}
          >
            <Tooltip title={t('common:action.delete')}>
              <Button type="text" size="small" danger icon={<DeleteOutlined />} />
            </Tooltip>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <FilterBar
        extra={
          <>
            {selected.length ? (
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                {t('common:common.selected', { count: selected.length })}
              </Typography.Text>
            ) : null}
            <Button
              icon={<ThunderboltOutlined />}
              disabled={!selected.length}
              loading={build.isPending && !build.variables?.all}
              onClick={() => build.mutate({ ids: selected })}
            >
              {t('common:action.buildSelected')}
            </Button>
            <Popconfirm
              title={t('compliance:samples.confirmBuildAll')}
              okText={t('common:action.confirm')}
              onConfirm={() => build.mutate({ all: true })}
            >
              <Button icon={<ThunderboltOutlined />} loading={build.isPending && Boolean(build.variables?.all)}>
                {t('common:action.buildAll')}
              </Button>
            </Popconfirm>
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setDrawer({ open: true, sample: null })}>
              {t('compliance:samples.add')}
            </Button>
          </>
        }
      >
        <PolicyGroupSelect
          groups={groups}
          loading={groupsLoading}
          allowClear
          value={filters.policy_group_id}
          onChange={(v) => setFilters({ policy_group_id: v })}
          style={{ width: 220 }}
        />
        <Select
          allowClear
          placeholder={t('compliance:samples.filterVectorized')}
          style={{ width: 140 }}
          value={filters.vectorized}
          onChange={(v) => setFilters({ vectorized: v })}
          options={[
            { value: true, label: t('common:common.vectorized') },
            { value: false, label: t('common:common.notVectorized') },
          ]}
        />
        <Input.Search
          allowClear
          placeholder={t('compliance:samples.searchPlaceholder')}
          style={{ width: 240 }}
          onSearch={(v) => setFilters({ q: v.trim() || undefined })}
        />
      </FilterBar>

      <Table<ComplianceSample>
        className="yz-table"
        size="middle"
        rowKey="id"
        loading={list.isLoading}
        columns={columns}
        dataSource={list.data?.items ?? []}
        pagination={pagination(list.data?.total)}
        scroll={{ x: 'max-content' }}
        rowSelection={{ selectedRowKeys: selected, onChange: (keys) => setSelected(keys as number[]) }}
        locale={{
          emptyText: (
            <EmptyState
              title={t('compliance:samples.empty')}
              hint={t('compliance:samples.emptyHint')}
              actionText={t('compliance:samples.add')}
              onAction={() => setDrawer({ open: true, sample: null })}
            />
          ),
        }}
      />

      <SampleDrawer
        open={drawer.open}
        sample={drawer.sample}
        groups={groups}
        onClose={() => setDrawer((d) => ({ ...d, open: false }))}
      />
    </div>
  );
}
