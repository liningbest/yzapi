import { useEffect, useMemo, useRef, useState } from 'react';
import {
  Alert,
  App,
  Button,
  Checkbox,
  Divider,
  Form,
  Input,
  InputNumber,
  Radio,
  Select,
  Space,
  Spin,
  Switch,
  Typography,
} from 'antd';
import {
  ApiOutlined,
  ArrowRightOutlined,
  DeleteOutlined,
  LinkOutlined,
  PlusOutlined,
  SearchOutlined,
} from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { accountsApi } from '@/api';
import { committedResult } from '@/api/client';
import type { NormalizedError } from '@/api';
import { FormDrawer, ProviderAvatar } from '@/components';
import type { AccountInput, AccountTestResult, ModelMapping, ModelType, Protocol, Provider } from '@/types';
import { MODEL_TYPES, PROTOCOLS_BY_TYPE, PROTOCOL_LABELS } from '@/utils/constants';
import { PROVIDER_DOCS } from '@/utils/provider';
import DiscoverModal from './DiscoverModal';

interface Props {
  open: boolean;
  /** Account id when editing; undefined when creating. */
  id?: number;
  providers: Provider[];
  onClose: () => void;
  onSaved: () => void;
}

interface FormValues {
  provider: string;
  account_type?: string;
  type: ModelType;
  name: string;
  base_url: string;
  api_key?: string;
  protocols: Protocol[];
  mappings: ModelMapping[];
  priority: number;
  weight: number;
  max_concurrency: number;
  passthrough_models?: boolean;
  endpoints?: string[];
  test_model?: string;
  note?: string;
  enabled: boolean;
}


function protocolOptions(p: Provider | undefined, type: ModelType | undefined, accountType?: string): Protocol[] {
  if (!type) return [];
  const base = PROTOCOLS_BY_TYPE[type];
  if (!p || p.custom) return base.filter((x) => !p || p.protocols.includes(x));
  const at = p.account_types?.find((a) => a.key === accountType);
  const native = at?.protocols?.length ? at.protocols : p.protocols;
  return base.filter((x) => native.includes(x));
}

function defaultBaseUrl(p: Provider | undefined, accountType: string | undefined): string {
  if (!p) return '';
  const at = p.account_types?.find((a) => a.key === accountType);
  return at?.base_url || p.base_url;
}

function toPayload(v: FormValues, isEdit: boolean, skipTest: boolean, accountId?: number): AccountInput {
  const apiKey = v.api_key?.trim();
  return {
    name: v.name.trim(),
    provider: v.provider,
    account_type: v.account_type || undefined,
    type: v.type,
    base_url: v.base_url.trim(),
    api_key: apiKey || (isEdit ? undefined : ''),
    protocols: v.protocols ?? [],
    mappings: (v.mappings ?? []).map((m) => ({
      ...(m.id ? { id: m.id } : {}),
      request_model: m.request_model.trim(),
      upstream_model: m.upstream_model.trim(),
    })),
    test_model: v.test_model || undefined,
    priority: v.priority ?? 100,
    weight: v.weight ?? 1,
    max_concurrency: v.max_concurrency ?? 0,
    passthrough_models: v.passthrough_models ?? false,
    ...(v.type === 'custom' ? { endpoints: (v.endpoints ?? []).map((x) => x.trim()).filter(Boolean) } : {}),
    enabled: v.enabled ?? true,
    note: v.note?.trim() || undefined,
    ...(skipTest ? { skip_test: true } : {}),
    ...(accountId ? { account_id: accountId } : {}),
  };
}

export default function AccountDrawer({ open, id, providers, onClose, onSaved }: Props) {
  const { t } = useTranslation(['accounts', 'common']);
  const { message } = App.useApp();
  const qc = useQueryClient();
  const [form] = Form.useForm<FormValues>();
  const isEdit = id !== undefined;

  const [testResult, setTestResult] = useState<{ ok: boolean; title: string; detail?: string } | null>(null);
  const [validationError, setValidationError] = useState<string | null>(null);
  const [discover, setDiscover] = useState<{ open: boolean; models: string[] }>({ open: false, models: [] });
  const prevDefault = useRef('');

  const providerKey = Form.useWatch('provider', form);
  const type = Form.useWatch('type', form);
  const mappings = Form.useWatch('mappings', form);
  const provider = useMemo(() => providers.find((p) => p.key === providerKey), [providers, providerKey]);

  const detail = useQuery({
    queryKey: ['admin', 'account', id],
    queryFn: () => accountsApi.get(id as number),
    enabled: open && isEdit,
  });

  useEffect(() => {
    if (!open || !detail.data) return;
    const a = detail.data;
    form.setFieldsValue({
      provider: a.provider,
      account_type: a.account_type || undefined,
      type: a.type,
      name: a.name,
      base_url: a.base_url,
      api_key: '',
      protocols: a.protocols ?? [],
      mappings: (a.mappings ?? []).map((m) => ({ id: m.id, request_model: m.request_model, upstream_model: m.upstream_model })),
      priority: a.priority,
      weight: a.weight ?? 1,
      max_concurrency: a.max_concurrency,
      passthrough_models: a.passthrough_models ?? false,
      endpoints: a.endpoints ?? [],
      test_model: a.test_model || undefined,
      note: a.note,
      enabled: a.enabled,
    });
    prevDefault.current = defaultBaseUrl(
      providers.find((p) => p.key === a.provider),
      a.account_type,
    );
  }, [open, detail.data, form, providers]);

  // ---------- provider-driven defaults ----------
  const applyBaseUrl = (p: Provider | undefined, accountType: string | undefined) => {
    const def = defaultBaseUrl(p, accountType);
    const cur = ((form.getFieldValue('base_url') as string | undefined) ?? '').trim();
    if (!cur || cur === prevDefault.current) form.setFieldValue('base_url', def);
    prevDefault.current = def;
  };

  const onValuesChange = (changed: Partial<FormValues>) => {
    if ('provider' in changed) {
      const p = providers.find((x) => x.key === changed.provider);
      if (!p) return;
      let at = form.getFieldValue('account_type') as string | undefined;
      if (p.account_types?.length) {
        if (!p.account_types.some((a) => a.key === at)) {
          at = p.account_types[0].key;
          form.setFieldValue('account_type', at);
        }
      } else {
        at = undefined;
        form.setFieldValue('account_type', undefined);
      }
      let ty = form.getFieldValue('type') as ModelType | undefined;
      if (!ty || !p.types.includes(ty)) {
        ty = p.types[0];
        form.setFieldValue('type', ty);
      }
      applyBaseUrl(p, at);
      form.setFieldValue('protocols', protocolOptions(p, ty, at));
      if (p?.endpoints?.length && !(form.getFieldValue('endpoints') as string[] | undefined)?.length) {
        form.setFieldValue('endpoints', p.endpoints);
      }
    } else if ('account_type' in changed) {
      applyBaseUrl(provider, changed.account_type);
      form.setFieldValue('protocols', protocolOptions(provider, form.getFieldValue('type') as ModelType | undefined, changed.account_type));
    } else if ('type' in changed) {
      form.setFieldValue('protocols', protocolOptions(provider, changed.type, form.getFieldValue('account_type') as string | undefined));
    }
  };

  // ---------- validation ----------
  const validateAll = async (): Promise<FormValues | null> => {
    try {
      await form.validateFields({ recursive: true });
      return form.getFieldsValue(true) as FormValues;
    } catch (e) {
      const err = e as { errorFields?: { name: (string | number)[] }[] };
      const first = err.errorFields?.[0]?.name;
      if (first) form.scrollToField(first, { block: 'center', behavior: 'smooth' });
      return null;
    }
  };

  // ---------- mutations ----------
  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ['admin', 'accounts'] });
    if (isEdit) void qc.invalidateQueries({ queryKey: ['admin', 'account', id] });
  };

  const saveMut = useMutation({
    mutationFn: (body: AccountInput) => (isEdit ? accountsApi.update(id, body) : accountsApi.create(body)),
    onSuccess: () => {
      message.success(t('common:common.saveSuccess'));
      invalidate();
      onSaved();
    },
    onError: (e: NormalizedError) => {
      if (e.status === 400 && e.code === 'validation_failed') setValidationError(e.message);
      if (committedResult(e)) {
        // Saved; only the runtime refresh failed. Never re-submit the create.
        message.warning(t('common:common.savedRuntimeStale'), 8);
        invalidate();
        onSaved();
      }
    },
  });

  const testMut = useMutation({
    mutationFn: (body: AccountInput) => accountsApi.test(body),
    onSuccess: (r: AccountTestResult) => {
      if (r.ok) {
        setTestResult({
          ok: true,
          title: t('accounts:test.success'),
          detail: t('accounts:test.successDetail', { model: r.model || '-', latency: r.latency_ms ?? 0 }),
        });
      } else {
        setTestResult({ ok: false, title: t('accounts:test.failed'), detail: r.message });
      }
    },
    onError: (e: NormalizedError) => setTestResult({ ok: false, title: t('accounts:test.failed'), detail: e.message }),
  });

  const discoverMut = useMutation({
    mutationFn: accountsApi.discover,
    onSuccess: (r) => setDiscover({ open: true, models: r.models ?? [] }),
  });

  const onSubmit = async () => {
    const v = await validateAll();
    if (!v) return;
    setValidationError(null);
    saveMut.mutate(toPayload(v, isEdit, false));
  };

  const onSkipAndSave = () => {
    const v = form.getFieldsValue(true) as FormValues;
    setValidationError(null);
    saveMut.mutate(toPayload(v, isEdit, true));
  };

  const onTest = async () => {
    const v = await validateAll();
    if (!v) return;
    setTestResult(null);
    testMut.mutate(toPayload(v, isEdit, false, id));
  };

  const onDiscover = () => {
    const baseUrl = ((form.getFieldValue('base_url') as string | undefined) ?? '').trim();
    const apiKey = ((form.getFieldValue('api_key') as string | undefined) ?? '').trim();
    if (!baseUrl) {
      message.warning(t('accounts:discover.needBaseUrl'));
      form.scrollToField('base_url', { block: 'center' });
      return;
    }
    if (!apiKey && !isEdit) {
      message.warning(t('accounts:discover.needKey'));
      form.scrollToField('api_key', { block: 'center' });
      return;
    }
    discoverMut.mutate({
      provider: providerKey,
      account_type: (form.getFieldValue('account_type') as string | undefined) || undefined,
      protocols: (form.getFieldValue('protocols') as string[] | undefined) || undefined,
      base_url: baseUrl,
      api_key: apiKey || undefined,
      ...(isEdit ? { account_id: id } : {}),
    });
  };

  const onDiscoverConfirm = (names: string[]) => {
    const cur = (form.getFieldValue('mappings') as ModelMapping[] | undefined) ?? [];
    form.setFieldValue('mappings', [...cur, ...names.map((n) => ({ request_model: n, upstream_model: n }))]);
    setDiscover({ open: false, models: [] });
    void form.validateFields(['mappings']).catch(() => undefined);
  };

  // ---------- derived ----------
  const mappedNames = useMemo(
    () =>
      Array.from(
        new Set(
          (mappings ?? []).flatMap((m) => [m?.request_model, m?.upstream_model]).filter((x): x is string => Boolean(x)),
        ),
      ),
    [mappings],
  );
  const upstreamModels = useMemo(
    () => Array.from(new Set((mappings ?? []).map((m) => m?.upstream_model?.trim()).filter((x): x is string => Boolean(x)))),
    [mappings],
  );
  const protoOpts = protocolOptions(provider, type);
  const typeOptions = (provider?.types ?? MODEL_TYPES).map((x) => ({ label: t(`common:type.${x}`), value: x }));
  const docsUrl = providerKey ? PROVIDER_DOCS[providerKey] : undefined;
  const requiredRule = { required: true, message: t('common:common.required') };

  const lockedKeys = isEdit && type ? providers.filter((p) => !p.types.includes(type)).map((p) => p.key) : [];
  const providerOptions = providers.map((p) => ({
    value: p.key,
    disabled: lockedKeys.includes(p.key) && p.key !== providerKey,
    searchText: `${p.name} ${p.key}`,
    label: (
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <ProviderAvatar provider={p.key} size={20} />
        <span style={{ fontWeight: 500 }}>{p.name}</span>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {p.types.map((x) => t(`common:type.${x}`)).join(' / ')}
        </Typography.Text>
      </div>
    ),
  }));

  const Section = ({ title }: { title: string }) => (
    <Divider orientation="left" orientationMargin={0} style={{ margin: '4px 0 12px', fontSize: 13 }}>
      {title}
    </Divider>
  );
  const twoCols: React.CSSProperties = { display: 'grid', gridTemplateColumns: '1fr 1fr', columnGap: 16 };

  return (
    <FormDrawer
      open={open}
      width={760}
      title={isEdit ? t('accounts:edit') : t('accounts:add')}
      onClose={onClose}
      onSubmit={() => void onSubmit()}
      submitting={saveMut.isPending}
      footerExtra={
        <Button icon={<ApiOutlined />} loading={testMut.isPending} onClick={() => void onTest()}>
          {t('common:action.testConnection')}
        </Button>
      }
    >
      <Spin spinning={isEdit && detail.isLoading}>
        {testResult ? (
          <Alert
            type={testResult.ok ? 'success' : 'error'}
            showIcon
            closable
            onClose={() => setTestResult(null)}
            message={testResult.title}
            description={testResult.detail}
            style={{ marginBottom: 16 }}
          />
        ) : null}
        {validationError ? (
          <Alert
            type="warning"
            showIcon
            closable
            onClose={() => setValidationError(null)}
            message={t('accounts:validation.title')}
            description={validationError}
            action={
              <Button size="small" loading={saveMut.isPending} onClick={onSkipAndSave}>
                {t('accounts:validation.skip')}
              </Button>
            }
            style={{ marginBottom: 16 }}
          />
        ) : null}

        <Form<FormValues>
          form={form}
          layout="vertical"
          requiredMark="optional"
          onValuesChange={onValuesChange}
          initialValues={{ type: 'text', protocols: [], mappings: [], priority: 100, weight: 1, max_concurrency: 0, enabled: true }}
        >
          {/* provider */}
          <Section title={t('accounts:steps.provider')} />
          <div style={twoCols}>
            <Form.Item
              name="provider"
              label={t('accounts:form.provider')}
              rules={[{ required: true, message: t('accounts:form.providerRequired') }]}
              extra={
                docsUrl && provider ? (
                  <a href={docsUrl} target="_blank" rel="noreferrer">
                    <LinkOutlined style={{ marginRight: 4 }} />
                    {t('accounts:form.docs', { name: provider.name })}
                  </a>
                ) : (
                  t('accounts:form.providerHint')
                )
              }
            >
              <Select
                showSearch
                placeholder={t('accounts:form.provider')}
                options={providerOptions}
                optionFilterProp="searchText"
                optionLabelProp="label"
                listHeight={320}
              />
            </Form.Item>
            <Form.Item
              name="type"
              label={t('accounts:form.type')}
              extra={isEdit ? t('accounts:form.typeLocked') : t('accounts:form.typeExtra')}
              rules={[requiredRule]}
            >
              <Radio.Group optionType="button" buttonStyle="solid" disabled={isEdit} options={typeOptions} />
            </Form.Item>
          </div>
          {provider?.account_types?.length ? (
            <Form.Item
              name="account_type"
              label={t('accounts:form.accountType')}
              extra={t('accounts:form.accountTypeExtra')}
            >
              <Radio.Group
                optionType="button"
                buttonStyle="solid"
                options={provider.account_types.map((a) => ({ label: a.name, value: a.key }))}
              />
            </Form.Item>
          ) : null}

          {/* basics */}
          <Section title={t('accounts:steps.basic')} />
          <div style={twoCols}>
            <Form.Item
              name="name"
              label={t('accounts:form.name')}
              extra={t('accounts:form.nameExtra')}
              rules={[{ ...requiredRule, whitespace: true }, { max: 100 }]}
            >
              <Input placeholder={t('accounts:form.namePlaceholder')} maxLength={100} />
            </Form.Item>
            <Form.Item
              name="base_url"
              label={t('accounts:form.baseUrl')}
              extra={provider?.custom ? t('accounts:form.baseUrlCustomExtra') : t('accounts:form.baseUrlExtra')}
              rules={[
                { ...requiredRule, whitespace: true },
                { pattern: /^https?:\/\/\S+$/i, message: t('accounts:form.baseUrlInvalid') },
              ]}
            >
              <Input className="yz-mono" placeholder="https://api.example.com/v1" />
            </Form.Item>
          </div>
          <Form.Item
              name="api_key"
              label={t('accounts:form.apiKey')}
              extra={
                isEdit && detail.data?.has_key
                  ? t('accounts:form.apiKeyCurrent', { masked: detail.data.api_key_masked })
                  : t('accounts:form.apiKeyExtra')
              }
              rules={isEdit ? [] : [requiredRule]}
            >
              <Input.Password
                autoComplete="new-password"
                placeholder={isEdit ? t('accounts:form.apiKeyEditPlaceholder') : t('accounts:form.apiKeyPlaceholder')}
              />
            </Form.Item>

          {/* protocols */}
          <Section title={t('accounts:steps.protocols')} />
          <Form.Item
            name="protocols"
            label={t('accounts:form.protocols')}
            extra={t('accounts:form.protocolsExtra')}
            rules={[
              {
                validator: async (_rule, v: Protocol[] | undefined) => {
                  if (!v?.length) throw new Error(t('accounts:form.protocolsRequired'));
                },
              },
            ]}
          >
            <Checkbox.Group style={{ width: '100%' }}>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
                {protoOpts.map((p) => (
                  <Checkbox
                    key={p}
                    value={p}
                    style={{
                      margin: 0,
                      padding: '6px 12px 6px 10px',
                      border: '1px solid var(--yz-border)',
                      borderRadius: 6,
                      alignItems: 'center',
                    }}
                  >
                    <span style={{ fontWeight: 500 }}>{PROTOCOL_LABELS[p]}</span>
                    <span className="yz-mono" style={{ color: 'var(--yz-text-secondary)', marginLeft: 6, fontSize: 12 }}>
                      {p}
                    </span>
                  </Checkbox>
                ))}
              </div>
            </Checkbox.Group>
          </Form.Item>
          {protoOpts.length === 0 ? (
            <Alert type="warning" showIcon message={t('accounts:form.protocolsNone')} style={{ marginBottom: 16 }} />
          ) : null}

          {/* mappings */}
          <Section title={t('accounts:steps.mappings')} />
          <div>
            <Form.Item label={t('accounts:form.mappings')} extra={t('accounts:form.mappingsExtra')} required>
              <Form.List
                name="mappings"
                rules={[
                  {
                    validator: async (_rule, v: ModelMapping[] | undefined) => {
                      const n = v?.length ?? 0;
                      // With pass-through on, an account may legitimately have no explicit mapping.
                      if (n < 1 && !form.getFieldValue('passthrough_models')) throw new Error(t('accounts:form.mappingsMin'));
                      if (n > 100) throw new Error(t('accounts:form.mappingsMax'));
                    },
                  },
                ]}
              >
                {(fields, { add, remove }, { errors }) => (
                  <>
                    {fields.length === 0 ? (
                      <div
                        style={{
                          padding: '18px 12px',
                          textAlign: 'center',
                          color: 'var(--yz-text-secondary)',
                          border: '1px dashed var(--yz-border)',
                          borderRadius: 6,
                          marginBottom: 12,
                          fontSize: 13,
                        }}
                      >
                        {t('accounts:form.mappingsEmpty')}
                      </div>
                    ) : (
                      <div
                        style={{
                          display: 'grid',
                          gridTemplateColumns: '24px 1fr 20px 1fr 32px',
                          columnGap: 8,
                          alignItems: 'start',
                          marginBottom: 4,
                        }}
                      >
                        <span />
                        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                          {t('accounts:form.requestModel')}
                        </Typography.Text>
                        <span />
                        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                          {t('accounts:form.upstreamModel')}
                        </Typography.Text>
                        <span />
                      </div>
                    )}
                    {fields.map((f, idx) => (
                      <div
                        key={f.key}
                        style={{
                          display: 'grid',
                          gridTemplateColumns: '24px 1fr 20px 1fr 32px',
                          columnGap: 8,
                          alignItems: 'start',
                        }}
                      >
                        <span
                          className="yz-mono"
                          style={{ color: 'var(--yz-text-tertiary)', lineHeight: '32px', textAlign: 'right' }}
                        >
                          {idx + 1}
                        </span>
                        <Form.Item
                          name={[f.name, 'request_model']}
                          style={{ marginBottom: 8 }}
                          rules={[
                            { ...requiredRule, whitespace: true },
                            {
                              validator: async (_rule, value: string | undefined) => {
                                const v = (value ?? '').trim();
                                if (!v) return;
                                const all = (form.getFieldValue('mappings') as ModelMapping[] | undefined) ?? [];
                                const dup = all.filter((m) => (m?.request_model ?? '').trim() === v).length > 1;
                                if (dup) throw new Error(t('accounts:form.requestModelDuplicate'));
                              },
                            },
                          ]}
                        >
                          <Input className="yz-mono" placeholder={t('accounts:form.requestModelPlaceholder')} />
                        </Form.Item>
                        <ArrowRightOutlined
                          style={{ color: 'var(--yz-text-tertiary)', lineHeight: '32px', height: 32, justifySelf: 'center' }}
                        />
                        <Form.Item
                          name={[f.name, 'upstream_model']}
                          style={{ marginBottom: 8 }}
                          rules={[{ ...requiredRule, whitespace: true }]}
                        >
                          <Input className="yz-mono" placeholder={t('accounts:form.upstreamModelPlaceholder')} />
                        </Form.Item>
                        <Button type="text" danger icon={<DeleteOutlined />} onClick={() => remove(f.name)} />
                      </div>
                    ))}
                    <Form.ErrorList errors={errors} />
                    <Space style={{ marginTop: 4 }}>
                      <Button
                        icon={<PlusOutlined />}
                        disabled={fields.length >= 100}
                        onClick={() => add({ request_model: '', upstream_model: '' })}
                      >
                        {t('accounts:form.addMapping')}
                      </Button>
                      {provider?.discover ? (
                        <Button icon={<SearchOutlined />} loading={discoverMut.isPending} onClick={onDiscover}>
                          {t('accounts:form.discover')}
                        </Button>
                      ) : null}
                    </Space>
                  </>
                )}
              </Form.List>
            </Form.Item>
          </div>

          {/* scheduling */}
          <Section title={t('accounts:steps.schedule')} />
          <div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', columnGap: 16 }}>
              <Form.Item
                name="priority"
                label={t('accounts:form.priority')}
                extra={t('accounts:form.priorityExtra')}
                rules={[requiredRule]}
              >
                <InputNumber min={0} max={1000} precision={0} style={{ width: '100%' }} />
              </Form.Item>
              <Form.Item name="weight" label={t('accounts:form.weight')} extra={t('accounts:form.weightExtra')} rules={[requiredRule]}>
                <InputNumber min={1} max={1000} precision={0} style={{ width: '100%' }} />
              </Form.Item>
              <Form.Item
                name="max_concurrency"
                label={t('accounts:form.maxConcurrency')}
                extra={t('accounts:form.maxConcurrencyExtra')}
                rules={[requiredRule]}
              >
                <InputNumber min={0} max={100000} precision={0} style={{ width: '100%' }} />
              </Form.Item>
              <Form.Item
                name="passthrough_models"
                label={t('accounts:form.passthrough')}
                extra={t('accounts:form.passthroughExtra')}
                valuePropName="checked"
              >
                <Switch />
              </Form.Item>
              <Form.Item name="test_model" label={t('accounts:form.testModel')} extra={t('accounts:form.testModelExtra')}>
                <Select
                  allowClear
                  showSearch
                  placeholder={t('accounts:form.testModelPlaceholder')}
                  options={upstreamModels.map((m) => ({ value: m, label: m }))}
                />
              </Form.Item>
            </div>
            {type === 'custom' && (
              <Form.Item
                name="endpoints"
                label={t('accounts:form.endpoints')}
                extra={t('accounts:form.endpointsExtra')}
                rules={[
                  { required: true, message: t('accounts:form.endpointsRequired') },
                  {
                    validator: async (_, v: string[] | undefined) => {
                      const xs = (v ?? []).map((x) => x.trim()).filter(Boolean);
                      if (!xs.length) throw new Error(t('accounts:form.endpointsRequired'));
                      if (xs.some((x) => /[?#\s]/.test(x))) throw new Error(t('accounts:form.endpointsInvalid'));
                    },
                  },
                ]}
              >
                <Select mode="tags" tokenSeparators={[',', ' ', '\n']} placeholder={t('accounts:form.endpointsPlaceholder')} open={false} className="yz-mono" />
              </Form.Item>
            )}
            <div style={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: 24, alignItems: 'start' }}>
              <Form.Item name="note" label={t('accounts:form.note')}>
                <Input.TextArea rows={2} maxLength={500} showCount placeholder={t('common:common.notePlaceholder')} />
              </Form.Item>
              <Form.Item
                name="enabled"
                label={t('accounts:form.enabled')}
                valuePropName="checked"
                extra={t('accounts:form.enabledExtra')}
              >
                <Switch />
              </Form.Item>
            </div>
          </div>
        </Form>
      </Spin>

      <DiscoverModal
        open={discover.open}
        models={discover.models}
        mapped={mappedNames}
        onCancel={() => setDiscover({ open: false, models: [] })}
        onConfirm={onDiscoverConfirm}
      />
    </FormDrawer>
  );
}
