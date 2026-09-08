import { useState } from 'react';
import { Alert, Button, Col, Form, Input, Row, Select, Space, Typography } from 'antd';
import { ExperimentOutlined } from '@ant-design/icons';
import { useMutation, useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { accountsApi, settingsApi } from '@/api';
import type { NormalizedError } from '@/api';
import { ProviderAvatar } from '@/components';
import type { VectorSettings, VectorTestResult } from '@/types';
import { formatMs } from '@/utils/format';
import { SaveBar, useSaveSettings, useSyncForm } from './shared';

interface Props {
  data?: VectorSettings;
}

export default function VectorTab({ data }: Props) {
  const { t } = useTranslation(['settings', 'common']);
  const [form] = Form.useForm<VectorSettings>();
  useSyncForm(form, data);
  const [testResult, setTestResult] = useState<VectorTestResult | null>(null);

  const accounts = useQuery({
    queryKey: ['accounts', 'embedding-enabled'],
    queryFn: () => accountsApi.list({ type: 'embedding', enabled: true, page_size: 200 }),
  });

  const save = useSaveSettings((body: VectorSettings) => settingsApi.saveVector(body));
  const test = useMutation({
    mutationFn: (body: VectorSettings) => settingsApi.testVector(body),
    onSuccess: (res) => setTestResult(res),
    onError: (e: NormalizedError) =>
      setTestResult({ ok: false, dim: 0, latency_ms: 0, message: e.message || t('common:error.network') }),
  });

  const runTest = async () => {
    const values = await form.validateFields();
    setTestResult(null);
    test.mutate(values);
  };

  const options = (accounts.data?.items ?? []).map((a) => ({
    value: a.id,
    label: (
      <Space size={8}>
        <ProviderAvatar provider={a.provider} size={20} />
        <span>{a.name}</span>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {a.provider}
        </Typography.Text>
      </Space>
    ),
    searchText: `${a.name} ${a.provider}`,
  }));

  return (
    <Form<VectorSettings>
      form={form}
      layout="vertical"
      initialValues={data}
      onFinish={(values) => save.mutate(values)}
      onValuesChange={() => setTestResult(null)}
    >
      <Alert type="info" showIcon message={t('settings:vector.notice')} style={{ marginBottom: 20 }} />
      <Row gutter={[24, 0]}>
        <Col xs={24} lg={12}>
          <Form.Item
            name="account_id"
            label={t('settings:vector.account.label')}
            extra={t('settings:vector.account.extra')}
            rules={[{ required: true, message: t('common:common.required') }]}
          >
            <Select
              showSearch
              loading={accounts.isLoading}
              options={options}
              optionFilterProp="searchText"
              placeholder={t('settings:vector.account.placeholder')}
              notFoundContent={t('settings:vector.account.empty')}
            />
          </Form.Item>
        </Col>
        <Col xs={24} lg={12}>
          <Form.Item
            name="model"
            label={t('settings:vector.model.label')}
            extra={t('settings:vector.model.extra')}
            rules={[{ required: true, message: t('common:common.required') }]}
          >
            <Input placeholder="text-embedding-3-small" className="yz-mono" />
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
              ? t('settings:vector.testOk', { dim: testResult.dim, latency: formatMs(testResult.latency_ms) })
              : t('settings:vector.testFailed', { message: testResult.message })
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
  );
}
