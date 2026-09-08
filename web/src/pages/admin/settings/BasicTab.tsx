import { Alert, Col, Form, Input, InputNumber, Row, Switch } from 'antd';
import { useTranslation } from 'react-i18next';
import { settingsApi } from '@/api';
import type { BasicSettings } from '@/types';
import { SaveBar, useSaveSettings, useSyncForm } from './shared';

interface Props {
  data?: BasicSettings;
}

const BASE_URL_RE = /^https?:\/\/.+\/v1$/;

export default function BasicTab({ data }: Props) {
  const { t } = useTranslation(['settings', 'common']);
  const [form] = Form.useForm<BasicSettings>();
  useSyncForm(form, data);
  const save = useSaveSettings((body: BasicSettings) => settingsApi.saveBasic(body));

  return (
    <Form<BasicSettings>
      form={form}
      layout="vertical"
      initialValues={data}
      onFinish={(values) => save.mutate({ ...values, base_url: values.base_url.trim() })}
    >
      <Row gutter={[24, 0]}>
        <Col xs={24} lg={12}>
          <Form.Item
            name="site_name"
            label={t('settings:basic.siteName.label')}
            extra={t('settings:basic.siteName.extra')}
            rules={[{ required: true, message: t('common:common.required') }]}
          >
            <Input maxLength={64} />
          </Form.Item>
        </Col>
        <Col xs={24} lg={12}>
          <Form.Item
            name="base_url"
            label={t('settings:basic.baseUrl.label')}
            extra={t('settings:basic.baseUrl.extra')}
            rules={[
              { required: true, message: t('common:common.required') },
              {
                validator: (_, v: string) =>
                  !v || BASE_URL_RE.test(v.trim())
                    ? Promise.resolve()
                    : Promise.reject(new Error(t('settings:basic.baseUrl.invalid'))),
              },
            ]}
          >
            <Input placeholder="https://gateway.example.com/v1" className="yz-mono" />
          </Form.Item>
        </Col>
        <Col xs={24} lg={12}>
          <Form.Item
            name="log_retention_days"
            label={t('settings:basic.logRetentionDays.label')}
            extra={t('settings:basic.logRetentionDays.extra')}
            rules={[{ required: true, message: t('common:common.required') }]}
          >
            <InputNumber min={1} max={365} precision={0} style={{ width: '100%' }} addonAfter={t('settings:unit.days')} />
          </Form.Item>
        </Col>
        <Col xs={24} lg={12}>
          <Form.Item
            name="protocol_conversion"
            label={t('settings:basic.protocolConversion.label')}
            extra={t('settings:basic.protocolConversion.extra')}
            valuePropName="checked"
          >
            <Switch />
          </Form.Item>
        </Col>
      </Row>
      <Alert type="warning" showIcon message={t('settings:basic.protocolConversion.warning')} style={{ marginBottom: 16 }} />
      <SaveBar loading={save.isPending} />
    </Form>
  );
}
