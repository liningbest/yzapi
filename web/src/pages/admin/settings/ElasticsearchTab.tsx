import { useState } from 'react';
import { Alert, Button, Col, Descriptions, Form, Input, InputNumber, Radio, Row, Switch, Tag } from 'antd';
import { ExperimentOutlined } from '@ant-design/icons';
import { useMutation, useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { settingsApi } from '@/api';
import type { NormalizedError } from '@/api';
import { SectionTitle, TimeCell } from '@/components';
import type { ElasticsearchSettings, EsTestResult } from '@/types';
import { formatBytes, formatNumber } from '@/utils/format';
import { SaveBar, useSaveSettings, useSyncForm } from './shared';

interface Props {
  data?: ElasticsearchSettings;
}

const STATUS_INTERVAL = 10_000;

export default function ElasticsearchTab({ data }: Props) {
  const { t } = useTranslation(['settings', 'common']);
  const [form] = Form.useForm<ElasticsearchSettings>();
  useSyncForm(form, data);
  const [testResult, setTestResult] = useState<EsTestResult | null>(null);

  const save = useSaveSettings((body: ElasticsearchSettings) => settingsApi.saveElasticsearch(body));
  const test = useMutation({
    mutationFn: (body: ElasticsearchSettings) => settingsApi.testElasticsearch(body),
    onSuccess: (res) => setTestResult(res),
    onError: (e: NormalizedError) =>
      setTestResult({ ok: false, version: '', message: e.message || t('common:error.network') }),
  });
  const status = useQuery({
    queryKey: ['settings', 'es-status'],
    queryFn: settingsApi.esStatus,
    refetchInterval: STATUS_INTERVAL,
  });

  const runTest = async () => {
    const values = await form.validateFields();
    setTestResult(null);
    test.mutate(values);
  };

  const st = status.data;

  return (
    <div>
      <Form<ElasticsearchSettings>
        form={form}
        layout="vertical"
        initialValues={data}
        onFinish={(values) => save.mutate(values)}
        onValuesChange={() => setTestResult(null)}
      >
        <Alert type="info" showIcon message={t('settings:es.notice')} style={{ marginBottom: 20 }} />
        <Row gutter={[24, 0]}>
          <Col xs={24} lg={12}>
            <Form.Item
              name="enabled"
              label={t('settings:es.enabled.label')}
              extra={t('settings:es.enabled.extra')}
              valuePropName="checked"
            >
              <Switch />
            </Form.Item>
          </Col>
          <Col xs={24} lg={12}>
            <Form.Item
              name="url"
              label={t('settings:es.url.label')}
              extra={t('settings:es.url.extra')}
              rules={[{ required: true, message: t('common:common.required') }]}
            >
              <Input placeholder="https://es.example.com:9200" className="yz-mono" />
            </Form.Item>
          </Col>
          <Col xs={24} lg={12}>
            <Form.Item name="auth_type" label={t('settings:es.authType.label')} extra={t('settings:es.authType.extra')}>
              <Radio.Group
                optionType="button"
                buttonStyle="solid"
                options={[
                  { value: 'apikey', label: t('settings:es.authType.apikey') },
                  { value: 'basic', label: t('settings:es.authType.basic') },
                ]}
              />
            </Form.Item>
          </Col>
          <Form.Item noStyle shouldUpdate={(prev, cur) => prev.auth_type !== cur.auth_type}>
            {({ getFieldValue }) =>
              getFieldValue('auth_type') === 'basic' ? (
                <>
                  <Col xs={24} lg={12}>
                    <Form.Item name="username" label={t('settings:es.username.label')} extra={t('settings:es.username.extra')}>
                      <Input autoComplete="off" />
                    </Form.Item>
                  </Col>
                  <Col xs={24} lg={12}>
                    <Form.Item name="password" label={t('settings:es.password.label')} extra={t('settings:es.secretExtra')}>
                      <Input.Password autoComplete="new-password" />
                    </Form.Item>
                  </Col>
                </>
              ) : (
                <Col xs={24} lg={12}>
                  <Form.Item name="api_key" label={t('settings:es.apiKey.label')} extra={t('settings:es.secretExtra')}>
                    <Input.Password autoComplete="new-password" />
                  </Form.Item>
                </Col>
              )
            }
          </Form.Item>
          <Col xs={24} lg={12}>
            <Form.Item
              name="index_prefix"
              label={t('settings:es.indexPrefix.label')}
              extra={t('settings:es.indexPrefix.extra')}
              rules={[{ required: true, message: t('common:common.required') }]}
            >
              <Input placeholder="yzapi" className="yz-mono" />
            </Form.Item>
          </Col>
          <Col xs={24} lg={12}>
            <Form.Item
              name="request_kb"
              label={t('settings:es.requestKb.label')}
              extra={t('settings:es.requestKb.extra')}
              rules={[{ required: true, message: t('common:common.required') }]}
            >
              <InputNumber min={0} precision={0} style={{ width: '100%' }} addonAfter="KiB" />
            </Form.Item>
          </Col>
          <Col xs={24} lg={12}>
            <Form.Item
              name="response_kb"
              label={t('settings:es.responseKb.label')}
              extra={t('settings:es.responseKb.extra')}
              rules={[{ required: true, message: t('common:common.required') }]}
            >
              <InputNumber min={0} precision={0} style={{ width: '100%' }} addonAfter="KiB" />
            </Form.Item>
          </Col>
          <Col xs={24} lg={12}>
            <Form.Item
              name="retention_days"
              label={t('settings:es.retentionDays.label')}
              extra={t('settings:es.retentionDays.extra')}
              rules={[{ required: true, message: t('common:common.required') }]}
            >
              <InputNumber min={0} precision={0} style={{ width: '100%' }} addonAfter={t('settings:unit.days')} />
            </Form.Item>
          </Col>
        </Row>
        {testResult ? (
          <Alert
            type={testResult.ok ? 'success' : 'error'}
            showIcon
            style={{ marginBottom: 16 }}
            message={
              testResult.ok
                ? t('settings:es.testOk', { version: testResult.version || '-' })
                : t('settings:es.testFailed', { message: testResult.message })
            }
          />
        ) : null}
        <SaveBar
          loading={save.isPending}
          extra={
            <Button icon={<ExperimentOutlined />} loading={test.isPending} onClick={() => void runTest()}>
              {t('common:action.testConnection')}
            </Button>
          }
        />
      </Form>

      <SectionTitle style={{ marginTop: 28 }}>{t('settings:es.status.title')}</SectionTitle>
      <Descriptions bordered size="small" column={{ xs: 1, sm: 2, xl: 3 }}>
        <Descriptions.Item label={t('settings:es.status.configured')}>
          {st ? (
            st.configured ? (
              <Tag color="success" style={{ marginInlineEnd: 0 }}>
                {t('settings:es.status.configuredYes')}
              </Tag>
            ) : (
              <Tag style={{ marginInlineEnd: 0 }}>{t('settings:es.status.notConfigured')}</Tag>
            )
          ) : (
            '-'
          )}
        </Descriptions.Item>
        <Descriptions.Item label={t('settings:es.status.queueCount')}>
          {st ? formatNumber(st.queue_count) : '-'}
        </Descriptions.Item>
        <Descriptions.Item label={t('settings:es.status.queueBytes')}>
          {st ? formatBytes(st.queue_bytes) : '-'}
        </Descriptions.Item>
        <Descriptions.Item label={t('settings:es.status.dropped')}>{st ? formatNumber(st.dropped) : '-'}</Descriptions.Item>
        <Descriptions.Item label={t('settings:es.status.lastSuccessAt')}>
          <TimeCell value={st?.last_success_at} emptyText={t('common:common.never')} />
        </Descriptions.Item>
        <Descriptions.Item label={t('settings:es.status.failingSince')}>
          {st?.failing_since ? (
            <span style={{ color: '#ef4444' }}>
              <TimeCell value={st.failing_since} />
            </span>
          ) : (
            <Tag color="success" style={{ marginInlineEnd: 0 }}>
              {t('settings:es.status.healthy')}
            </Tag>
          )}
        </Descriptions.Item>
      </Descriptions>
    </div>
  );
}
