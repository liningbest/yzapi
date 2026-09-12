import { useMemo, useState } from 'react';
import { Alert, Button, Checkbox, Descriptions, Input, Modal, Radio, Space, Table, Tooltip, Typography, Upload, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { UploadOutlined } from '@ant-design/icons';
import { useMutation } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { pricesApi } from '@/api';
import { NeutralTag } from '@/components';
import type { PriceImportChange, PriceImportOptions, PriceImportResult } from '@/types';

interface Props {
  open: boolean;
  onClose: () => void;
  onApplied: () => void;
}

type Source = 'litellm' | 'easycpa' | 'url' | 'file';

/**
 * Download or upload a price catalog, preview the diff, then apply it. The preview is
 * bound to a server-side plan id: apply writes exactly the previewed bytes, and any
 * change to the source, file or options discards the preview so nothing unseen is
 * ever applied.
 */
export default function PriceImportModal({ open, onClose, onApplied }: Props) {
  const { t } = useTranslation(['settings', 'common']);
  const [source, setSource] = useState<Source>('litellm');
  const [url, setUrl] = useState('');
  const [file, setFile] = useState<File | null>(null);
  const [overwrite, setOverwrite] = useState(false);
  const [overwriteCurrency, setOverwriteCurrency] = useState(false);
  const [preview, setPreview] = useState<PriceImportResult | null>(null);

  const opts: PriceImportOptions = { overwrite_edited: overwrite, overwrite_currency: overwriteCurrency };

  const previewRun = useMutation({
    mutationFn: () =>
      source === 'file'
        ? pricesApi.importPreviewFile(file as File, opts)
        : pricesApi.importPreview({ source: source === 'url' ? 'url' : source, url: source === 'url' ? url : undefined, ...opts }),
    onSuccess: (res) => setPreview(res),
  });
  const applyRun = useMutation({
    mutationFn: (p: PriceImportResult) => pricesApi.importApply({ plan_id: p.plan_id, sha256: p.sha256 }),
    onSuccess: (res) => {
      setPreview(res);
      message.success(t('settings:pricing.import.applied', { new: res.plan.new, updated: res.plan.updated, kept: res.plan.kept }));
      onApplied();
    },
    onError: () => setPreview(null), // expired / mismatched plan: force a fresh preview
  });

  const canRun = source === 'file' ? !!file : source === 'url' ? /^https?:\/\//.test(url.trim()) : true;
  const stale = () => setPreview(null);
  const close = () => {
    setPreview(null);
    setFile(null);
    onClose();
  };

  const money = (v: [number, number, number, number], cur?: string) => `${v[0]} / ${v[1]} / ${v[2]} / ${v[3]}${cur ? ` ${cur}` : ''}`;
  const columns: ColumnsType<PriceImportChange> = useMemo(
    () => [
      {
        title: t('settings:pricing.import.action'),
        dataIndex: 'action',
        width: 120,
        render: (a: PriceImportChange['action'], r) => (
          <Space size={4}>
            <NeutralTag tone={a === 'new' ? 'success' : a === 'update' ? 'warning' : 'neutral'}>{t(`settings:pricing.import.actions.${a}`)}</NeutralTag>
            {r.reason ? <Typography.Text type="secondary" style={{ fontSize: 12 }}>{t(`settings:pricing.import.reasons.${r.reason}`, { defaultValue: r.reason })}</Typography.Text> : null}
          </Space>
        ),
      },
      { title: t('settings:pricing.pattern'), dataIndex: 'pattern', render: (v: string, r) => <span className="yz-mono">{r.provider ? `${r.provider} / ${v}` : v}</span> },
      {
        title: t('settings:pricing.import.old'),
        dataIndex: 'old',
        width: 200,
        render: (v: [number, number, number, number] | undefined, r) => (v ? <span className="yz-mono">{money(v, r.old_currency)}</span> : '-'),
      },
      {
        title: t('settings:pricing.import.new'),
        dataIndex: 'new',
        width: 200,
        render: (v: [number, number, number, number], r) => (
          <Space size={4}>
            <span className="yz-mono">{money(v, r.new_currency)}</span>
            {r.currency_changed ? <NeutralTag tone="warning">{t('settings:pricing.import.currencyChanged')}</NeutralTag> : null}
          </Space>
        ),
      },
    ],
    [t],
  );

  const plan = preview?.plan;
  return (
    <Modal open={open} onCancel={close} width={900} title={t('settings:pricing.import.title')} footer={null} destroyOnClose>
      <Alert type="info" showIcon message={t('settings:pricing.import.notice')} style={{ marginBottom: 16 }} />
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <Radio.Group
          value={source}
          onChange={(e) => {
            setSource(e.target.value as Source);
            setFile(null);
            stale();
          }}
          options={[
            { value: 'litellm', label: t('settings:pricing.import.sources.litellm') },
            { value: 'easycpa', label: t('settings:pricing.import.sources.easycpa') },
            { value: 'url', label: t('settings:pricing.import.sources.url') },
            { value: 'file', label: t('settings:pricing.import.sources.file') },
          ]}
        />
        {source === 'url' ? (
          <Input
            value={url}
            onChange={(e) => {
              setUrl(e.target.value);
              stale();
            }}
            placeholder="https://…/model_prices.json"
          />
        ) : null}
        {source === 'file' ? (
          <Upload
            accept=".json,application/json"
            maxCount={1}
            beforeUpload={(f) => {
              setFile(f);
              stale();
              return false;
            }}
            onRemove={() => {
              setFile(null);
              stale();
            }}
          >
            <Button icon={<UploadOutlined />}>{t('settings:pricing.import.chooseFile')}</Button>
          </Upload>
        ) : null}
        <Checkbox
          checked={overwrite}
          onChange={(e) => {
            setOverwrite(e.target.checked);
            stale();
          }}
        >
          {t('settings:pricing.import.overwrite')}
        </Checkbox>
        <Tooltip title={t('settings:pricing.import.overwriteCurrencyHint')}>
          <Checkbox
            checked={overwriteCurrency}
            onChange={(e) => {
              setOverwriteCurrency(e.target.checked);
              stale();
            }}
          >
            {t('settings:pricing.import.overwriteCurrency')}
          </Checkbox>
        </Tooltip>
        <Space>
          <Button onClick={() => previewRun.mutate()} loading={previewRun.isPending} disabled={!canRun}>
            {t('settings:pricing.import.preview')}
          </Button>
          <Button type="primary" onClick={() => preview && applyRun.mutate(preview)} loading={applyRun.isPending} disabled={!preview || preview.applied}>
            {t('settings:pricing.import.apply')}
          </Button>
          {!preview && (previewRun.isSuccess || applyRun.isError) ? (
            <Typography.Text type="warning" style={{ fontSize: 12 }}>
              {t('settings:pricing.import.stale')}
            </Typography.Text>
          ) : null}
        </Space>
      </Space>

      {preview && plan ? (
        <div style={{ marginTop: 16 }}>
          <Descriptions size="small" bordered column={4}>
            <Descriptions.Item label={t('settings:pricing.import.source')} span={2}>
              {plan.source}
              {plan.date ? ` · ${plan.date}` : ''}
              <Typography.Text type="secondary" style={{ marginLeft: 8, fontSize: 12 }}>
                {preview.origin}
              </Typography.Text>
            </Descriptions.Item>
            <Descriptions.Item label={t('settings:pricing.import.status')} span={2}>
              {preview.applied ? <NeutralTag tone="success">{t('settings:pricing.import.done')}</NeutralTag> : <NeutralTag>{t('settings:pricing.import.previewOnly')}</NeutralTag>}
            </Descriptions.Item>
            <Descriptions.Item label={t('settings:pricing.import.total')}>{plan.total}</Descriptions.Item>
            <Descriptions.Item label={t('settings:pricing.import.actions.new')}>{plan.new}</Descriptions.Item>
            <Descriptions.Item label={t('settings:pricing.import.actions.update')}>{plan.updated}</Descriptions.Item>
            <Descriptions.Item label={t('settings:pricing.import.same')}>{plan.same}</Descriptions.Item>
            <Descriptions.Item label={t('settings:pricing.import.actions.keep')}>{plan.kept}</Descriptions.Item>
            <Descriptions.Item label={t('settings:pricing.import.skipped')}>{plan.skipped}</Descriptions.Item>
            <Descriptions.Item label={t('settings:pricing.import.invalid')}>{plan.invalid}</Descriptions.Item>
            <Descriptions.Item label={t('settings:pricing.import.duplicates')}>{plan.duplicates}</Descriptions.Item>
          </Descriptions>
          {!preview.applied ? (
            <Typography.Text type="secondary" style={{ display: 'block', marginTop: 8, fontSize: 12 }}>
              {t('settings:pricing.import.boundHint', { id: preview.plan_id.slice(0, 8), sha: preview.sha256.slice(0, 12) })}
            </Typography.Text>
          ) : null}
          {plan.invalid > 0 && plan.invalid_rows?.length ? (
            <Alert
              type="warning"
              showIcon
              style={{ marginTop: 12 }}
              message={t('settings:pricing.import.invalidHint')}
              description={
                <ul style={{ margin: 0, paddingLeft: 18 }}>
                  {plan.invalid_rows.map((r) => (
                    <li key={r} className="yz-mono" style={{ fontSize: 12 }}>
                      {r}
                    </li>
                  ))}
                </ul>
              }
            />
          ) : null}
          {plan.duplicates > 0 ? <Alert type="warning" showIcon style={{ marginTop: 12 }} message={t('settings:pricing.import.duplicatesHint')} /> : null}
          <Typography.Text type="secondary" style={{ display: 'block', margin: '12px 0 6px', fontSize: 12 }}>
            {t('settings:pricing.import.changesHint')}
          </Typography.Text>
          <Table<PriceImportChange> rowKey={(r) => `${r.provider}|${r.pattern}`} size="small" columns={columns} dataSource={plan.changes} pagination={{ pageSize: 10, size: 'small', showSizeChanger: false }} scroll={{ x: 'max-content' }} />
        </div>
      ) : null}
    </Modal>
  );
}
