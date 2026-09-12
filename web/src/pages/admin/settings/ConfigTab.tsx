import { useState } from 'react';
import { Alert, Button, Descriptions, Drawer, Popconfirm, Space, Table, Tag, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { CameraOutlined, EyeOutlined, RollbackOutlined } from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { configSnapshotsApi } from '@/api';
import { SectionTitle, TimeCell } from '@/components';
import type { ConfigSnapshotRow } from '@/types';
import { SETTINGS_KEY } from './shared';

const KEY = ['config-snapshots'] as const;

/** Configuration versions: automatic snapshots before every routing/settings change, manual snapshots, one-click restore. */
export default function ConfigTab() {
  const { t } = useTranslation(['settings', 'common']);
  const qc = useQueryClient();
  const list = useQuery({ queryKey: KEY, queryFn: configSnapshotsApi.list });
  const [viewing, setViewing] = useState<number | null>(null);
  const detail = useQuery({ queryKey: [...KEY, viewing], queryFn: () => configSnapshotsApi.get(viewing as number), enabled: viewing !== null });

  const invalidateAll = () => {
    void qc.invalidateQueries({ queryKey: KEY });
    void qc.invalidateQueries({ queryKey: SETTINGS_KEY });
    void qc.invalidateQueries({ queryKey: ['admin'] });
    void qc.invalidateQueries({ queryKey: ['prices'] });
  };
  const create = useMutation({
    mutationFn: () => configSnapshotsApi.create('manual'),
    onSuccess: () => {
      message.success(t('settings:config.created'));
      void qc.invalidateQueries({ queryKey: KEY });
    },
  });
  const restore = useMutation({
    mutationFn: (id: number) => configSnapshotsApi.restore(id),
    onSuccess: (res) => {
      message.success(t('settings:config.restored'));
      if (res?.missing_keys?.length) message.warning(t('settings:config.missingKeys', { names: res.missing_keys.join(', ') }), 8);
      if (res?.price_rows_merged) message.warning(t('settings:config.priceRowsMerged', { count: res.price_rows_merged }), 8);
      if (res?.price_rows_skipped?.length) {
        const rows = res.price_rows_skipped;
        message.warning(t('settings:config.priceRowsSkipped', { count: rows.length, rows: rows.slice(0, 5).join('；'), more: rows.length > 5 ? t('settings:config.priceRowsMore', { count: rows.length - 5 }) : '' }), 12);
      }
      setViewing(null);
      invalidateAll();
    },
  });

  const columns: ColumnsType<ConfigSnapshotRow> = [
    { title: '#', dataIndex: 'id', width: 70 },
    { title: t('settings:config.time'), dataIndex: 'created_at', width: 190, render: (v: string) => <TimeCell value={v} absolute /> },
    { title: t('settings:config.actor'), dataIndex: 'actor', width: 140 },
    {
      title: t('settings:config.reason'),
      dataIndex: 'reason',
      render: (v: string) => (v === 'manual' ? <Tag>{t('settings:config.manual')}</Tag> : <Typography.Text className="yz-mono" style={{ fontSize: 12 }}>{v}</Typography.Text>),
    },
    {
      title: '',
      key: 'actions',
      width: 150,
      align: 'right',
      render: (_, r) => (
        <Space size={4}>
          <Button type="text" size="small" icon={<EyeOutlined />} onClick={() => setViewing(r.id)}>
            {t('settings:config.view')}
          </Button>
          <Popconfirm title={t('settings:config.restoreConfirm', { id: r.id })} okButtonProps={{ danger: true }} onConfirm={() => restore.mutate(r.id)}>
            <Button type="text" size="small" danger icon={<RollbackOutlined />} loading={restore.isPending && restore.variables === r.id}>
              {t('settings:config.restore')}
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  const d = detail.data;
  return (
    <div>
      <Alert type="info" showIcon message={t('settings:config.notice')} style={{ marginBottom: 20 }} />
      <SectionTitle
        extra={
          <Button size="small" icon={<CameraOutlined />} onClick={() => create.mutate()} loading={create.isPending}>
            {t('settings:config.snapshotNow')}
          </Button>
        }
      >
        {t('settings:config.title')}
      </SectionTitle>
      <Table<ConfigSnapshotRow> rowKey="id" size="small" loading={list.isLoading} columns={columns} dataSource={list.data?.items ?? []} pagination={{ pageSize: 20, showSizeChanger: false }} />

      <Drawer open={viewing !== null} onClose={() => setViewing(null)} width={640} title={t('settings:config.detailTitle', { id: viewing ?? '' })} destroyOnClose>
        {d ? (
          <div>
            <Descriptions size="small" column={2} bordered style={{ marginBottom: 16 }}>
              <Descriptions.Item label={t('settings:config.time')}>
                <TimeCell value={d.created_at} absolute />
              </Descriptions.Item>
              <Descriptions.Item label={t('settings:config.actor')}>{d.actor}</Descriptions.Item>
              <Descriptions.Item label={t('settings:config.reason')} span={2}>
                {d.reason}
              </Descriptions.Item>
              <Descriptions.Item label={t('settings:config.accounts')}>{d.meta?.accounts ?? d.accounts.length}</Descriptions.Item>
              <Descriptions.Item label={t('settings:config.modelGroups')}>{d.meta?.model_groups ?? d.model_groups.length}</Descriptions.Item>
            </Descriptions>
            <SectionTitle>{t('settings:config.accounts')}</SectionTitle>
            <Table
              size="small"
              rowKey="id"
              pagination={false}
              dataSource={d.accounts}
              columns={[
                { title: t('common:common.name'), dataIndex: 'name' },
                { title: t('common:common.provider'), dataIndex: 'provider', width: 110 },
                { title: 'Base URL', dataIndex: 'base_url', ellipsis: true },
                { title: t('settings:config.mappings'), dataIndex: 'mappings', width: 70, align: 'right' },
                { title: t('common:common.enabled'), dataIndex: 'enabled', width: 70, render: (v: boolean) => (v ? '✓' : '—') },
              ]}
              style={{ marginBottom: 16 }}
            />
            <SectionTitle>{t('settings:config.modelGroups')}</SectionTitle>
            <Table
              size="small"
              rowKey="id"
              pagination={false}
              dataSource={d.model_groups}
              columns={[
                { title: t('common:common.name'), dataIndex: 'name', width: 160 },
                { title: t('common:common.model'), dataIndex: 'models', render: (v: string[]) => v.join(', ') },
              ]}
              style={{ marginBottom: 16 }}
            />
            <SectionTitle>{t('settings:config.settings')}</SectionTitle>
            <pre className="yz-code-block" style={{ fontSize: 12, maxHeight: 320, overflow: 'auto' }}>
              {JSON.stringify(Object.fromEntries(Object.entries(d.settings).map(([k, v]) => [k, JSON.parse(v)])), null, 2)}
            </pre>
            <Popconfirm title={t('settings:config.restoreConfirm', { id: d.id })} okButtonProps={{ danger: true }} onConfirm={() => restore.mutate(d.id)}>
              <Button danger icon={<RollbackOutlined />} loading={restore.isPending}>
                {t('settings:config.restore')}
              </Button>
            </Popconfirm>
          </div>
        ) : null}
      </Drawer>
    </div>
  );
}
