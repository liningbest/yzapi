import { Col, Form, InputNumber, Row, Typography } from 'antd';
import { useTranslation } from 'react-i18next';
import { settingsApi } from '@/api';
import type { PerformanceSettings } from '@/types';
import { SaveBar, useSaveSettings, useSyncForm } from './shared';

interface Props {
  data?: PerformanceSettings;
}

type Unit = 'seconds' | 'kib' | 'count' | 'times';

interface FieldDef {
  name: keyof PerformanceSettings;
  min: number;
  max?: number;
  unit: Unit;
}

const FIELDS: FieldDef[] = [
  { name: 'max_concurrency', min: 0, unit: 'count' },
  { name: 'queue_size', min: 0, unit: 'count' },
  { name: 'queue_timeout_sec', min: 1, max: 3600, unit: 'seconds' },
  { name: 'request_timeout_sec', min: 1, max: 3600, unit: 'seconds' },
  { name: 'stream_idle_timeout_sec', min: 1, max: 3600, unit: 'seconds' },
  { name: 'max_body_kb', min: 1, unit: 'kib' },
  { name: 'cooldown_sec', min: 0, max: 86400, unit: 'seconds' },
  { name: 'max_retries', min: 0, max: 20, unit: 'times' },
  { name: 'upstream_connect_timeout_sec', min: 1, max: 300, unit: 'seconds' },
];

export default function PerformanceTab({ data }: Props) {
  const { t } = useTranslation(['settings', 'common']);
  const [form] = Form.useForm<PerformanceSettings>();
  useSyncForm(form, data);
  const save = useSaveSettings((body: PerformanceSettings) => settingsApi.savePerformance(body));

  const unitLabel = (u: Unit) => {
    switch (u) {
      case 'seconds':
        return t('common:common.seconds');
      case 'kib':
        return 'KiB';
      case 'times':
        return t('settings:unit.times');
      default:
        return undefined;
    }
  };

  return (
    <Form<PerformanceSettings>
      form={form}
      layout="vertical"
      initialValues={data}
      onFinish={(values) => save.mutate(values)}
    >
      <Typography.Paragraph type="secondary" style={{ marginBottom: 20 }}>
        {t('settings:performance.intro')}
      </Typography.Paragraph>
      <Row gutter={[24, 0]}>
        {FIELDS.map((f) => (
          <Col xs={24} lg={12} key={f.name}>
            <Form.Item
              name={f.name}
              label={t(`settings:performance.${f.name}.label`)}
              extra={t(`settings:performance.${f.name}.extra`)}
              rules={[{ required: true, message: t('common:common.required') }]}
            >
              <InputNumber
                min={f.min}
                max={f.max}
                precision={0}
                style={{ width: '100%' }}
                addonAfter={unitLabel(f.unit)}
              />
            </Form.Item>
          </Col>
        ))}
      </Row>
      <SaveBar loading={save.isPending} />
    </Form>
  );
}
