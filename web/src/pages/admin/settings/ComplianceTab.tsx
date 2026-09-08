import { Alert, Col, Form, Radio, Row, Switch, Typography } from 'antd';
import { useTranslation } from 'react-i18next';
import { settingsApi } from '@/api';
import type { ComplianceSettings } from '@/types';
import { SaveBar, SliderInput, useSaveSettings, useSyncForm } from './shared';

interface Props {
  data?: ComplianceSettings;
}

export default function ComplianceTab({ data }: Props) {
  const { t } = useTranslation(['settings', 'common']);
  const [form] = Form.useForm<ComplianceSettings>();
  useSyncForm(form, data);
  const save = useSaveSettings((body: ComplianceSettings) => settingsApi.saveCompliance(body));

  return (
    <Form<ComplianceSettings>
      form={form}
      layout="vertical"
      initialValues={data}
      onFinish={(values) => save.mutate(values)}
    >
      <Typography.Paragraph type="secondary" style={{ marginBottom: 20 }}>
        {t('settings:compliance.intro')}
      </Typography.Paragraph>
      <Row gutter={[24, 0]}>
        <Col xs={24} lg={12}>
          <Form.Item
            name="enabled"
            label={t('settings:compliance.enabled.label')}
            extra={t('settings:compliance.enabled.extra')}
            valuePropName="checked"
          >
            <Switch />
          </Form.Item>
        </Col>
        <Col xs={24} lg={12}>
          <Form.Item
            name="check_system_prompt"
            label={t('settings:compliance.checkSystemPrompt.label')}
            extra={t('settings:compliance.checkSystemPrompt.extra')}
            valuePropName="checked"
          >
            <Switch />
          </Form.Item>
        </Col>
        <Col xs={24} lg={12}>
          <Form.Item
            name="on_failure"
            label={t('settings:compliance.onFailure.label')}
            extra={t('settings:compliance.onFailure.extra')}
            initialValue="allow"
          >
            <Radio.Group
              optionType="button"
              buttonStyle="solid"
              options={[
                { label: t('settings:compliance.onFailure.allow'), value: 'allow' },
                { label: t('settings:compliance.onFailure.block'), value: 'block' },
              ]}
            />
          </Form.Item>
        </Col>
        <Col xs={24}>
          <Alert type="info" showIcon message={t('settings:compliance.onFailure.hint')} style={{ marginBottom: 16 }} />
        </Col>
        <Col xs={24} lg={12}>
          <Form.Item
            name="semantic_threshold"
            label={t('settings:compliance.semanticThreshold.label')}
            extra={t('settings:compliance.semanticThreshold.extra')}
            rules={[{ required: true, message: t('common:common.required') }]}
          >
            <SliderInput min={0} max={1} step={0.01} />
          </Form.Item>
        </Col>
      </Row>
      <SaveBar loading={save.isPending} />
    </Form>
  );
}
