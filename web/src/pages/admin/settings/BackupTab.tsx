import { useState } from 'react';
import { Alert, Button, Col, Form, InputNumber, Popconfirm, Row, Space, Switch, Table, Typography, Upload, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { CloudUploadOutlined, DeleteOutlined, DownloadOutlined, SaveOutlined } from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { backupApi, settingsApi } from '@/api';
import { SectionTitle, TimeCell } from '@/components';
import type { BackupInfo, BackupSettings } from '@/types';
import { formatBytes } from '@/utils/format';
import { SaveBar, useSaveSettings, useSyncForm } from './shared';

const KEY = ['backups'] as const;

interface Props {
  data?: BackupSettings;
}

/** Full-instance backup and restore: archives of the database, journal and secrets; restore is applied on the next start. */
export default function BackupTab({ data }: Props) {
  const { t } = useTranslation(['settings', 'common']);
  const qc = useQueryClient();
  const list = useQuery({ queryKey: KEY, queryFn: backupApi.list });
  const [form] = Form.useForm<BackupSettings>();
  useSyncForm(form, data);
  const save = useSaveSettings((body: BackupSettings) => settingsApi.saveBackup(body));
  const [restoring, setRestoring] = useState(false);

  const create = useMutation({
    mutationFn: () => backupApi.create(),
    onSuccess: (info) => {
      message.success(t('settings:backup.created', { name: info.name }));
      void qc.invalidateQueries({ queryKey: KEY });
    },
  });
  const remove = useMutation({
    mutationFn: (name: string) => backupApi.remove(name),
    onSuccess: () => void qc.invalidateQueries({ queryKey: KEY }),
  });
  const restore = useMutation({
    mutationFn: (file: File) => backupApi.restore(file),
    onSuccess: () => {
      setRestoring(true);
      message.warning(t('settings:backup.restoreStaged'), 15);
    },
  });

  const columns: ColumnsType<BackupInfo> = [
    { title: t('settings:backup.file'), dataIndex: 'name', render: (v: string) => <Typography.Text className="yz-mono" style={{ fontSize: 12 }}>{v}</Typography.Text> },
    { title: t('settings:backup.size'), dataIndex: 'size', width: 110, render: (v: number) => formatBytes(v) },
    { title: t('settings:backup.time'), dataIndex: 'created_at', width: 190, render: (v: string) => <TimeCell value={v} absolute /> },
    {
      title: t('common:common.actions'),
      width: 180,
      render: (_, r) => (
        <Space>
          <Button size="small" icon={<DownloadOutlined />} onClick={() => void backupApi.download(r.name)}>
            {t('settings:backup.download')}
          </Button>
          <Popconfirm title={t('settings:backup.deleteConfirm')} onConfirm={() => remove.mutate(r.name)}>
            <Button size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  const unsupported = list.data && !list.data.supported;

  return (
    <div>
      <Alert type="warning" showIcon message={t('settings:backup.notice')} style={{ marginBottom: 16 }} />
      {unsupported && <Alert type="info" showIcon message={t('settings:backup.unsupported')} style={{ marginBottom: 16 }} />}
      {list.data?.pending_restore && <Alert type="warning" showIcon message={t('settings:backup.pending')} style={{ marginBottom: 16 }} />}
      {restoring && <Alert type="warning" showIcon message={t('settings:backup.restoreStaged')} style={{ marginBottom: 16 }} />}

      <SectionTitle
        extra={
          <Space>
            <Upload
              accept=".tar.gz,.tgz,application/gzip"
              showUploadList={false}
              beforeUpload={(file) => {
                restore.mutate(file);
                return false;
              }}
              disabled={unsupported || restore.isPending}
            >
              <Button icon={<CloudUploadOutlined />} loading={restore.isPending} danger>
                {t('settings:backup.restore')}
              </Button>
            </Upload>
            <Button type="primary" icon={<SaveOutlined />} loading={create.isPending} disabled={unsupported} onClick={() => create.mutate()}>
              {t('settings:backup.createNow')}
            </Button>
          </Space>
        }
      >
        {t('settings:backup.listTitle')}
      </SectionTitle>
      <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
        {t('settings:backup.restoreHint')}
      </Typography.Paragraph>
      <Table<BackupInfo> rowKey="name" size="small" loading={list.isLoading} dataSource={list.data?.items ?? []} columns={columns} pagination={false} style={{ marginBottom: 24 }} />

      <SectionTitle>{t('settings:backup.autoTitle')}</SectionTitle>
      <Form<BackupSettings> form={form} layout="vertical" initialValues={data} onFinish={(v) => save.mutate({ ...data, ...v })}>
        <Row gutter={[24, 0]}>
          <Col xs={24} lg={8}>
            <Form.Item name="enabled" label={t('settings:backup.enabled')} valuePropName="checked" extra={t('settings:backup.enabledExtra')}>
              <Switch disabled={unsupported} />
            </Form.Item>
          </Col>
          <Col xs={24} lg={8}>
            <Form.Item name="hour_local" label={t('settings:backup.hour')} extra={t('settings:backup.hourExtra')} rules={[{ required: true }]}>
              <InputNumber min={0} max={23} precision={0} style={{ width: '100%' }} />
            </Form.Item>
          </Col>
          <Col xs={24} lg={8}>
            <Form.Item name="keep_count" label={t('settings:backup.keep')} extra={t('settings:backup.keepExtra')} rules={[{ required: true }]}>
              <InputNumber min={0} max={365} precision={0} style={{ width: '100%' }} />
            </Form.Item>
          </Col>
        </Row>
        <SaveBar loading={save.isPending} />
      </Form>
    </div>
  );
}
