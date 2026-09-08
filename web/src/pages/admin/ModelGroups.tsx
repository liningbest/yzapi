import { useState } from 'react';
import { Link } from 'react-router-dom';
import { App, Button, Card, Input, Popconfirm, Segmented, Space, Table, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { modelGroupsApi } from '@/api';
import { EmptyState, FilterBar, NeutralTag, PageHeader, TimeCell, TypeTag } from '@/components';
import { useTableQuery } from '@/hooks/useTableQuery';
import type { ModelGroup, ModelType } from '@/types';
import { MODEL_TYPES } from '@/utils/constants';
import ModelGroupDrawer from './model-groups/ModelGroupDrawer';

interface Filters {
  type?: ModelType;
  q?: string;
}

const ALL = 'all';

export default function ModelGroups() {
  const { t } = useTranslation(['modelGroups', 'common']);
  const { message } = App.useApp();
  const qc = useQueryClient();
  const { filters, setFilters, params, pagination } = useTableQuery<Filters>({});
  const [drawer, setDrawer] = useState<{ key: number; open: boolean; group: ModelGroup | null }>({
    key: 0,
    open: false,
    group: null,
  });

  const listQ = useQuery({
    queryKey: ['admin', 'model-groups', params],
    queryFn: () => modelGroupsApi.list(params),
    placeholderData: keepPreviousData,
  });

  const invalidate = () => void qc.invalidateQueries({ queryKey: ['admin', 'model-groups'] });
  const removeMut = useMutation({
    mutationFn: (id: number) => modelGroupsApi.remove(id),
    onSuccess: () => {
      message.success(t('common:common.deleteSuccess'));
      invalidate();
    },
  });

  const openDrawer = (group: ModelGroup | null) => setDrawer((d) => ({ key: d.key + 1, open: true, group }));
  const closeDrawer = () => setDrawer((d) => ({ ...d, open: false }));

  const columns: ColumnsType<ModelGroup> = [
    {
      title: t('modelGroups:columns.name'),
      dataIndex: 'name',
      key: 'name',
      width: 240,
      render: (v: string, g) => (
        <Space size={6} wrap>
          <span style={{ fontWeight: 600 }}>{v}</span>
          {(g.route_roles ?? []).map((r) => (
            <Tooltip key={r} title={t('modelGroups:routeRoleHint')}>
              <Link to="/admin/settings?tab=smart_route">
                <NeutralTag>{t(`modelGroups:routeRole.${r}`)}</NeutralTag>
              </Link>
            </Tooltip>
          ))}
        </Space>
      ),
    },
    {
      title: t('modelGroups:columns.type'),
      dataIndex: 'type',
      key: 'type',
      width: 90,
      render: (v: ModelType) => <TypeTag type={v} />,
    },
    {
      title: t('modelGroups:columns.models'),
      dataIndex: 'models',
      key: 'models',
      render: (models: string[]) => {
        const list = models ?? [];
        const shown = list.slice(0, 3);
        const rest = list.slice(3);
        return (
          <Space size={4} wrap>
            {shown.map((m, i) => (
              <NeutralTag key={`${m}-${i}`} mono>
                {m}
              </NeutralTag>
            ))}
            {rest.length ? (
              <Tooltip
                title={
                  <div>
                    <div style={{ marginBottom: 4, opacity: 0.75 }}>{t('modelGroups:moreModels', { count: rest.length })}</div>
                    {rest.map((m, i) => (
                      <div key={`${m}-${i}`} className="yz-mono">
                        {shown.length + i + 1}. {m}
                      </div>
                    ))}
                  </div>
                }
              >
                <NeutralTag style={{ cursor: 'default' }}>+{rest.length}</NeutralTag>
              </Tooltip>
            ) : null}
          </Space>
        );
      },
    },
    {
      title: t('modelGroups:columns.note'),
      dataIndex: 'note',
      key: 'note',
      width: 220,
      ellipsis: true,
      render: (v: string) =>
        v ? (
          <Typography.Text type="secondary" ellipsis={{ tooltip: v }} style={{ maxWidth: 200 }}>
            {v}
          </Typography.Text>
        ) : (
          <Typography.Text type="secondary">-</Typography.Text>
        ),
    },
    {
      title: t('modelGroups:columns.updatedAt'),
      dataIndex: 'updated_at',
      key: 'updated_at',
      width: 120,
      render: (v: string) => <TimeCell value={v} />,
    },
    {
      title: t('modelGroups:columns.actions'),
      key: 'actions',
      width: 90,
      align: 'center',
      fixed: 'right',
      render: (_, g) => (
        <Space size={0}>
          <Tooltip title={t('common:action.edit')}>
            <Button type="text" icon={<EditOutlined />} onClick={() => openDrawer(g)} />
          </Tooltip>
          {g.in_use_by_route ? (
            <Tooltip
              title={
                <span>
                  {t('modelGroups:deleteDisabledDetail', {
                    roles: (g.route_roles ?? []).map((r) => t(`modelGroups:routeRole.${r}`)).join(' / '),
                  })}
                  <br />
                  <Link to="/admin/settings?tab=smart_route" style={{ color: '#93c5fd' }}>
                    {t('modelGroups:goToRouteSettings')}
                  </Link>
                </span>
              }
            >
              <Button type="text" danger icon={<DeleteOutlined />} disabled />
            </Tooltip>
          ) : (
            <Popconfirm
              title={t('common:action.delete')}
              description={t('modelGroups:delete.confirm', { name: g.name })}
              okButtonProps={{ danger: true }}
              onConfirm={() => removeMut.mutate(g.id)}
            >
              <Tooltip title={t('common:action.delete')}>
                <Button type="text" danger icon={<DeleteOutlined />} />
              </Tooltip>
            </Popconfirm>
          )}
        </Space>
      ),
    },
  ];

  return (
    <>
      <PageHeader
        title={t('modelGroups:title')}
        subtitle={t('modelGroups:subtitle')}
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={() => openDrawer(null)}>
            {t('modelGroups:add')}
          </Button>
        }
      />
      <Card className="yz-card">
        <FilterBar>
          <Segmented<string>
            value={filters.type ?? ALL}
            onChange={(v) => setFilters({ type: v === ALL ? undefined : (v as ModelType) })}
            options={[
              { label: t('common:common.all'), value: ALL },
              ...MODEL_TYPES.map((x) => ({ label: t(`common:type.${x}`), value: x })),
            ]}
          />
          <Input.Search
            allowClear
            placeholder={t('modelGroups:filter.keywordPlaceholder')}
            style={{ width: 240 }}
            defaultValue={filters.q}
            onSearch={(v) => setFilters({ q: v.trim() || undefined })}
          />
        </FilterBar>

        <Table<ModelGroup>
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
              <EmptyState
                hint={t('modelGroups:empty.hint')}
                actionText={t('modelGroups:add')}
                onAction={() => openDrawer(null)}
              />
            ),
          }}
        />
      </Card>

      <ModelGroupDrawer
        key={drawer.key}
        open={drawer.open}
        group={drawer.group}
        onClose={closeDrawer}
        onSaved={closeDrawer}
      />
    </>
  );
}
