import { useMemo, useState } from 'react';
import { Alert, Button, Checkbox, Descriptions, Input, Modal, Radio, Space, Table, Typography, Upload, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { UploadOutlined } from '@ant-design/icons';
import { useMutation } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { pricesApi } from '@/api';
import { NeutralTag } from '@/components';
import type { PriceImportChange, PriceImportResult } from '@/types';

interface Props {
  open: boolean;
  onClose: () => void;
  onApplied: () => void;
}

type Source = 'litellm' | 'easycpa' | 'url' | 'file';

/** Download or upload a price catalog, preview the diff, then apply it. Manual and edited rows are kept unless asked otherwise. */
export default function PriceImportModal({ open, onClose, onApplied }: Props) {
  const { t } = useTranslation(['settings', 'common']);
  const [source, setSource] = useState<Source>('litellm');
  const [url, setUrl] = useState('');
  const [file, setFile] = useState<File | null>(null);
  const [overwrite, setOverwrite] = useState(false);
  const [preview, setPreview] = useState<PriceImportResult | null>(null);

  const run = useMutation({
    mutationFn: (apply: boolean) =>
      source === 'file'
        ? pricesApi.importFile(file as File, apply, overwrite)
        : pricesApi.import({ source: source === 'url' ? 'url' : source, url: source === 'url' ? url : undefined, apply, overwrite_edited: overwrite }),
    onSuccess: (res) => {
      setPreview(res);
      if (res.applied) {
        message.success(t('settings:pricing.import.applied', { new: res.plan.new, updated: res.plan.updated, kept: res.plan.kept }));
        onApplied();
      }
    },
  });

  const canRun = source === 'file' ? !!file : source === 'url' ? /^https?:\/\//.test(url.trim()) : true;
  const reset = () => {
    setPreview(null);
    setFile(null);
  };
  const close = () => {
    reset();
    onClose();
  };

  const money = (v: [number, number, number, number]) => `${v[0]} / ${v[1]} / ${v[2]} / ${v[3]}`;
  const columns: ColumnsType<PriceImportChange> = useMemo(
    () => [
      {
        title: t('settings:pricing.import.action'),
        dataIndex: 'action',
        width: 90,
        render: (a: PriceImportChange['action']) => (
          <NeutralTag tone={a === 'new' ? 'success' : a === 'update' ? 'warning' : 'neutral'}>{t(`settings:pricing.import.actions.${a}`)}</NeutralTag>
        ),
      },
      { title: t('settings:pricing.pattern'), dataIndex: 'pattern', render: (v: string, r) => <span className="yz-mono">{r.provider ? `${r.provider} / ${v}` : v}</span> },
      { title: t('settings:pricing.import.old'), dataIndex: 'old', width: 170, render: (v?: [number, number, number, number]) => (v ? <span className="yz-mono">{money(v)}</span> : '-') },
      { title: t('settings:pricing.import.new'), dataIndex: 'new', width: 170, render: (v: [number, number, number, number]) => <span className="yz-mono">{money(v)}</span> },
    ],
    [t],
  );

  return (
    <Modal open={open} onCancel={close} width={860} title={t('settings:pricing.import.title')} footer={null} destroyOnClose>
      <Alert type="info" showIcon message={t('settings:pricing.import.notice')} style={{ marginBottom: 16 }} />
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <Radio.Group
          value={source}
          onChange={(e) => {
            setSource(e.target.value as Source);
            reset();
          }}
          options={[
            { value: 'litellm', label: t('settings:pricing.import.sources.litellm') },
            { value: 'easycpa', label: t('settings:pricing.import.sources.easycpa') },
            { value: 'url', label: t('settings:pricing.import.sources.url') },
            { value: 'file', label: t('settings:pricing.import.sources.file') },
          ]}
        />
        {source === 'url' ? <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://…/model_prices.json" /> : null}
        {source === 'file' ? (
          <Upload
            accept=".json,application/json"
            maxCount={1}
            beforeUpload={(f) => {
              setFile(f);
              return false;
            }}
            onRemove={() => setFile(null)}
          >
            <Button icon={<UploadOutlined />}>{t('settings:pricing.import.chooseFile')}</Button>
          </Upload>
        ) : null}
        <Checkbox checked={overwrite} onChange={(e) => setOverwrite(e.target.checked)}>
          {t('settings:pricing.import.overwrite')}
        </Checkbox>
        <Space>
          <Button onClick={() => run.mutate(false)} loading={run.isPending && !run.variables} disabled={!canRun}>
            {t('settings:pricing.import.preview')}
          </Button>
          <Button type="primary" onClick={() => run.mutate(true)} loading={run.isPending && !!run.variables} disabled={!canRun || !preview || preview.applied}>
            {t('settings:pricing.import.apply')}
          </Button>
        </Space>
      </Space>

      {preview ? (
        <div style={{ marginTop: 16 }}>
          <Descriptions size="small" bordered column={4}>
            <Descriptions.Item label={t('settings:pricing.import.source')}>{preview.plan.source}{preview.plan.date ? ` · ${preview.plan.date}` : ''}</Descriptions.Item>
            <Descriptions.Item label={t('settings:pricing.import.total')}>{preview.plan.total}</Descriptions.Item>
            <Descriptions.Item label={t('settings:pricing.import.actions.new')}>{preview.plan.new}</Descriptions.Item>
            <Descriptions.Item label={t('settings:pricing.import.actions.update')}>{preview.plan.updated}</Descriptions.Item>
            <Descriptions.Item label={t('settings:pricing.import.same')}>{preview.plan.same}</Descriptions.Item>
            <Descriptions.Item label={t('settings:pricing.import.actions.keep')}>{preview.plan.kept}</Descriptions.Item>
            <Descriptions.Item label={t('settings:pricing.import.skipped')}>{preview.plan.skipped}</Descriptions.Item>
            <Descriptions.Item label={t('settings:pricing.import.status')}>
              {preview.applied ? <NeutralTag tone="success">{t('settings:pricing.import.done')}</NeutralTag> : <NeutralTag>{t('settings:pricing.import.previewOnly')}</NeutralTag>}
            </Descriptions.Item>
          </Descriptions>
          <Typography.Text type="secondary" style={{ display: 'block', margin: '12px 0 6px', fontSize: 12 }}>
            {t('settings:pricing.import.changesHint')}
          </Typography.Text>
          <Table<PriceImportChange> rowKey={(r) => `${r.provider}|${r.pattern}`} size="small" columns={columns} dataSource={preview.plan.changes} pagination={{ pageSize: 10, size: 'small', showSizeChanger: false }} scroll={{ x: 'max-content' }} />
        </div>
      ) : null}
    </Modal>
  );
}
