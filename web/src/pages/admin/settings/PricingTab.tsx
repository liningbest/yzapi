import { useMemo, useState } from 'react';
import { Alert, Button, Col, Form, Input, InputNumber, Modal, Popconfirm, Row, Select, Space, Switch, Table, Tooltip, Typography, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { CloudDownloadOutlined, DeleteOutlined, EditOutlined, PlusOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons';
import PriceImportModal from './PriceImportModal';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { pricesApi, providersApi, settingsApi } from '@/api';
import { NeutralTag, ProviderAvatar, SectionTitle } from '@/components';
import type { ModelPrice, ModelPriceInput, PricingSettings } from '@/types';
import { useSiteStore } from '@/stores/site';
import { SaveBar, useSaveSettings, useSyncForm } from './shared';

interface Props {
  data?: PricingSettings;
}

const PRICES_KEY = ['prices'] as const;

export default function PricingTab({ data }: Props) {
  const { t } = useTranslation(['settings', 'common']);
  const qc = useQueryClient();
  const [form] = Form.useForm<PricingSettings>();
  useSyncForm(form, data);
  const save = useSaveSettings(
    (body: PricingSettings) => settingsApi.savePricing(body),
    () => void useSiteStore.getState().refresh(),
  );

  const [q, setQ] = useState('');
  const prices = useQuery({ queryKey: [...PRICES_KEY, q], queryFn: () => pricesApi.list(q || undefined) });
  const providers = useQuery({ queryKey: ['providers'], queryFn: providersApi.list });
  const providerOptions = useMemo(
    () => [{ value: '', label: t('settings:pricing.anyProvider') }, ...(providers.data ?? []).map((p) => ({ value: p.key, label: p.name }))],
    [providers.data, t],
  );
  const providerName = (key: string) => providers.data?.find((p) => p.key === key)?.name ?? key;

  const invalidate = () => void qc.invalidateQueries({ queryKey: PRICES_KEY });
  const [editing, setEditing] = useState<ModelPrice | null | 'new'>(null);
  const [importOpen, setImportOpen] = useState(false);
  const [editForm] = Form.useForm<ModelPriceInput>();

  const upsert = useMutation({
    mutationFn: (body: ModelPriceInput) => (editing && editing !== 'new' ? pricesApi.update(editing.id, body) : pricesApi.create(body)),
    onSuccess: () => {
      message.success(t('common:common.saveSuccess'));
      setEditing(null);
      invalidate();
    },
  });
  const remove = useMutation({
    mutationFn: (id: number) => pricesApi.remove(id),
    onSuccess: () => {
      message.success(t('common:common.deleteSuccess'));
      invalidate();
    },
  });
  const reset = useMutation({
    mutationFn: () => pricesApi.resetBuiltin(),
    onSuccess: () => {
      message.success(t('settings:pricing.resetDone'));
      invalidate();
    },
  });

  const openEdit = (row: ModelPrice | 'new') => {
    setEditing(row);
    editForm.setFieldsValue(
      row === 'new'
        ? { pattern: '', provider: '', input_per_m: 0, output_per_m: 0, cached_input_per_m: 0, cache_write_per_m: 0, currency: 'USD', enabled: true, note: '' }
        : { ...row },
    );
  };

  const money = (v: number, cur: string) => `${cur === 'USD' ? '$' : '¥'}${v}`;
  const columns: ColumnsType<ModelPrice> = [
    {
      title: t('settings:pricing.pattern'),
      dataIndex: 'pattern',
      render: (v: string, r) => (
        <Space size={6}>
          <Typography.Text className="yz-mono" style={{ opacity: r.enabled ? 1 : 0.45 }}>
            {v}
          </Typography.Text>
          {r.builtin ? <NeutralTag>{t('settings:pricing.builtin')}</NeutralTag> : <NeutralTag tone="success">{t('settings:pricing.custom')}</NeutralTag>}
          {r.source ? (
            <Tooltip title={t('settings:pricing.sourceHint', { source: r.source, date: r.source_date || '-' })}>
              <span><NeutralTag>{r.source}</NeutralTag></span>
            </Tooltip>
          ) : null}
          {r.edited ? (
            <Tooltip title={t('settings:pricing.editedHint')}>
              <span><NeutralTag tone="warning">{t('settings:pricing.edited')}</NeutralTag></span>
            </Tooltip>
          ) : null}
        </Space>
      ),
    },
    {
      title: t('common:common.provider'),
      dataIndex: 'provider',
      width: 150,
      render: (v: string) =>
        v ? (
          <Space size={6}>
            <ProviderAvatar provider={v} size={16} />
            <span>{providerName(v)}</span>
          </Space>
        ) : (
          <span style={{ color: 'var(--yz-text-secondary)' }}>{t('settings:pricing.anyProvider')}</span>
        ),
    },
    { title: t('settings:pricing.input'), dataIndex: 'input_per_m', width: 100, align: 'right', render: (v: number, r) => money(v, r.currency) },
    { title: t('settings:pricing.output'), dataIndex: 'output_per_m', width: 100, align: 'right', render: (v: number, r) => money(v, r.currency) },
    { title: t('settings:pricing.cached'), dataIndex: 'cached_input_per_m', width: 100, align: 'right', render: (v: number, r) => money(v, r.currency) },
    {
      title: '',
      key: 'actions',
      width: 90,
      align: 'right',
      render: (_, r) => (
        <Space size={4}>
          <Button type="text" size="small" icon={<EditOutlined />} onClick={() => openEdit(r)} />
          <Popconfirm title={t('common:common.confirmDelete')} onConfirm={() => remove.mutate(r.id)}>
            <Button type="text" size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <Form<PricingSettings> form={form} layout="vertical" initialValues={data} onFinish={(values) => save.mutate(values)}>
        <Alert type="info" showIcon message={t('settings:pricing.notice', { date: prices.data?.builtin_updated ?? '' })} style={{ marginBottom: 20 }} />
        <Row gutter={[24, 0]}>
          <Col xs={24} lg={12}>
            <Form.Item name="currency" label={t('settings:pricing.currency.label')} extra={t('settings:pricing.currency.extra')} rules={[{ required: true }]}>
              <Select options={[{ value: 'CNY', label: 'CNY ¥' }, { value: 'USD', label: 'USD $' }]} />
            </Form.Item>
          </Col>
          <Col xs={24} lg={12}>
            <Form.Item name="usd_to_cny" label={t('settings:pricing.rate.label')} extra={t('settings:pricing.rate.extra')} rules={[{ required: true }]}>
              <InputNumber min={0.01} max={100} step={0.01} style={{ width: '100%' }} />
            </Form.Item>
          </Col>
        </Row>
        <SaveBar loading={save.isPending} />
      </Form>

      <SectionTitle
        extra={
          <Space>
            <Input allowClear size="small" prefix={<SearchOutlined />} placeholder={t('settings:pricing.search')} value={q} onChange={(e) => setQ(e.target.value)} style={{ width: 200 }} />
            <Popconfirm title={t('settings:pricing.resetConfirm')} onConfirm={() => reset.mutate()}>
              <Button size="small" icon={<ReloadOutlined />} loading={reset.isPending}>
                {t('settings:pricing.reset')}
              </Button>
            </Popconfirm>
            <Button size="small" icon={<CloudDownloadOutlined />} onClick={() => setImportOpen(true)}>
              {t('settings:pricing.import.button')}
            </Button>
            <Button size="small" type="primary" icon={<PlusOutlined />} onClick={() => openEdit('new')}>
              {t('settings:pricing.add')}
            </Button>
          </Space>
        }
      >
        {t('settings:pricing.table')}
      </SectionTitle>
      <Table<ModelPrice> rowKey="id" size="small" loading={prices.isLoading} columns={columns} dataSource={prices.data?.items ?? []} pagination={{ pageSize: 20, showSizeChanger: false }} />

      <PriceImportModal open={importOpen} onClose={() => setImportOpen(false)} onApplied={invalidate} />

      <Modal
        open={editing !== null}
        title={editing === 'new' ? t('settings:pricing.add') : t('settings:pricing.edit')}
        onCancel={() => setEditing(null)}
        onOk={() => void editForm.validateFields().then((v) => upsert.mutate(v))}
        confirmLoading={upsert.isPending}
        destroyOnClose
      >
        <Form<ModelPriceInput> form={editForm} layout="vertical">
          <Form.Item name="pattern" label={t('settings:pricing.pattern')} extra={t('settings:pricing.patternExtra')} rules={[{ required: true, message: t('common:common.required') }]}>
            <Input className="yz-mono" placeholder="gpt-6-astra" />
          </Form.Item>
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item name="provider" label={t('common:common.provider')}>
                <Select options={providerOptions} showSearch optionFilterProp="label" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="currency" label={t('settings:pricing.rowCurrency')} rules={[{ required: true }]}>
                <Select options={[{ value: 'USD', label: 'USD $' }, { value: 'CNY', label: 'CNY ¥' }]} />
              </Form.Item>
            </Col>
          </Row>
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item name="input_per_m" label={t('settings:pricing.input')} rules={[{ required: true }]}>
                <InputNumber min={0} step={0.01} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="output_per_m" label={t('settings:pricing.output')} rules={[{ required: true }]}>
                <InputNumber min={0} step={0.01} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="cached_input_per_m" label={t('settings:pricing.cached')} extra={t('settings:pricing.cachedExtra')}>
                <InputNumber min={0} step={0.001} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="cache_write_per_m" label={t('settings:pricing.cacheWrite')} extra={t('settings:pricing.cacheWriteExtra')}>
                <InputNumber min={0} step={0.01} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
          </Row>
          <Row gutter={16}>
            <Col span={16}>
              <Form.Item name="note" label={t('common:common.note')}>
                <Input maxLength={255} />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="enabled" label={t('common:common.enabled')} valuePropName="checked">
                <Switch />
              </Form.Item>
            </Col>
          </Row>
          <Tooltip title={t('settings:pricing.unitHint')}>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {t('settings:pricing.unit')}
            </Typography.Text>
          </Tooltip>
        </Form>
      </Modal>
    </div>
  );
}
