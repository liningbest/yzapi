import { useState } from 'react';
import { App, Button, Input, Popconfirm, Select, Space, Table, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  AppstoreAddOutlined,
  DeleteOutlined,
  EditOutlined,
  ExperimentOutlined,
  PlusOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { routeApi } from '@/api';
import { EmptyState, FilterBar, LabelTag, TimeCell, VectorizedBadge } from '@/components';
import { useTableQuery } from '@/hooks/useTableQuery';
import type { BuildResult, RouteLabel, RouteSample, RouteSampleListParams } from '@/types';
import SampleDrawer from './SampleDrawer';
import BatchAddModal from './BatchAddModal';
import PreviewModal from './PreviewModal';

type Filters = Omit<RouteSampleListParams, 'page' | 'page_size'>;

export default function SamplesTab() {
  const { t } = useTranslation(['route', 'common']);
  const { message } = App.useApp();
  const qc = useQueryClient();
  const { filters, setFilters, params, pagination } = useTableQuery<Filters>({});
  const [selected, setSelected] = useState<number[]>([]);
  const [drawer, setDrawer] = useState<{ open: boolean; sample: RouteSample | null }>({ open: false, sample: null });
  const [batchOpen, setBatchOpen] = useState(false);
  const [previewOpen, setPreviewOpen] = useState(false);

  const list = useQuery({
    queryKey: ['route', 'samples', params],
    queryFn: () => routeApi.samples(params),
    placeholderData: (prev) => prev,
  });

  const invalidate = () => qc.invalidateQueries({ queryKey: ['route', 'samples'] });

  const reportBuild = (res: BuildResult) => {
    const base = t('route:samples.buildResult', { built: res.built, failed: res.failed });
    const text = res.error ? `${base}: ${res.error}` : base;
    if (res.failed > 0) message.warning(text);
    else message.success(text);
  };

  const build = useMutation({
    mutationFn: (body: { ids?: number[]; all?: boolean }) => routeApi.build(body),
    onSuccess: (res) => {
      reportBuild(res);
      setSelected([]);
      void invalidate();
    },
  });

  const remove = useMutation({
    mutationFn: (id: number) => routeApi.removeSample(id),
    onSuccess: () => {
      message.success(t('common:common.deleteSuccess'));
      void invalidate();
    },
  });

  const columns: ColumnsType<RouteSample> = [
    {
      title: t('common:common.label'),
      dataIndex: 'label',
      width: 90,
      render: (label: RouteLabel) => <LabelTag label={label} />,
    },
    {
      title: t('route:samples.text'),
      dataIndex: 'text',
      render: (text: string) => (
        <Typography.Paragraph
          ellipsis={{ rows: 2, expandable: true, symbol: t('common:action.expand') }}
          style={{ marginBottom: 0, maxWidth: 480, whiteSpace: 'pre-wrap' }}
        >
          {text}
        </Typography.Paragraph>
      ),
    },
    {
      title: t('route:samples.threshold'),
      dataIndex: 'threshold',
      width: 90,
      align: 'center',
      render: (v: number) =>
        v ? (
          <span style={{ fontVariantNumeric: 'tabular-nums' }}>{v.toFixed(2)}</span>
        ) : (
          <Typography.Text type="secondary">{t('route:samples.globalThreshold')}</Typography.Text>
        ),
    },
    {
      title: t('route:samples.vector'),
      dataIndex: 'vectorized',
      width: 120,
      render: (_, r) => <VectorizedBadge vectorized={r.vectorized} dim={r.vector_dim} />,
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
            title={t('route:samples.confirmDelete')}
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
              title={t('route:samples.confirmBuildAll')}
              okText={t('common:action.confirm')}
              onConfirm={() => build.mutate({ all: true })}
            >
              <Button icon={<ThunderboltOutlined />} loading={build.isPending && Boolean(build.variables?.all)}>
                {t('common:action.buildAll')}
              </Button>
            </Popconfirm>
            <Button type="dashed" icon={<ExperimentOutlined />} onClick={() => setPreviewOpen(true)}>
              {t('route:samples.preview')}
            </Button>
            <Button icon={<AppstoreAddOutlined />} onClick={() => setBatchOpen(true)}>
              {t('common:action.batchAdd')}
            </Button>
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setDrawer({ open: true, sample: null })}>
              {t('route:samples.add')}
            </Button>
          </>
        }
      >
        <Select
          allowClear
          placeholder={t('common:common.label')}
          style={{ width: 130 }}
          value={filters.label}
          onChange={(v) => setFilters({ label: v })}
          options={[
            { value: 'simple', label: t('common:label.simple') },
            { value: 'complex', label: t('common:label.complex') },
          ]}
        />
        <Select
          allowClear
          placeholder={t('route:samples.filterVectorized')}
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
          placeholder={t('route:samples.searchPlaceholder')}
          style={{ width: 260 }}
          onSearch={(v) => setFilters({ q: v.trim() || undefined })}
        />
      </FilterBar>

      <Table<RouteSample>
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
              title={t('route:samples.empty')}
              hint={t('route:samples.emptyHint')}
              actionText={t('route:samples.add')}
              onAction={() => setDrawer({ open: true, sample: null })}
            />
          ),
        }}
      />

      <SampleDrawer
        open={drawer.open}
        sample={drawer.sample}
        onClose={() => setDrawer((d) => ({ ...d, open: false }))}
      />
      <BatchAddModal open={batchOpen} onClose={() => setBatchOpen(false)} />
      <PreviewModal open={previewOpen} onClose={() => setPreviewOpen(false)} />
    </div>
  );
}
