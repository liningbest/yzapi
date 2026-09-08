import { App, Checkbox, Form, Input, Modal, Radio } from 'antd';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { routeApi } from '@/api';
import type { RouteLabel } from '@/types';

interface Props {
  open: boolean;
  onClose: () => void;
}

interface FormValues {
  label: RouteLabel;
  lines: string;
  build_vector: boolean;
}

export function splitLines(raw: string): string[] {
  return raw
    .split(/\r?\n/)
    .map((s) => s.trim())
    .filter(Boolean);
}

export default function BatchAddModal({ open, onClose }: Props) {
  const { t } = useTranslation(['route', 'common']);
  const { message } = App.useApp();
  const qc = useQueryClient();
  const [form] = Form.useForm<FormValues>();

  const submit = useMutation({
    mutationFn: (values: FormValues) =>
      routeApi.batchSamples(
        splitLines(values.lines).map((text) => ({ label: values.label, text })),
        values.build_vector,
      ),
    onSuccess: (res) => {
      message.success(t('route:samples.batch.created', { created: res.created }));
      onClose();
      void qc.invalidateQueries({ queryKey: ['route', 'samples'] });
    },
  });

  const onOk = async () => {
    const values = await form.validateFields();
    submit.mutate(values);
  };

  return (
    <Modal
      open={open}
      title={t('route:samples.batch.title')}
      onCancel={onClose}
      onOk={onOk}
      okText={t('route:samples.batch.submit')}
      confirmLoading={submit.isPending}
      destroyOnHidden
      width={640}
    >
      <Form
        form={form}
        layout="vertical"
        initialValues={{ label: 'simple', lines: '', build_vector: true }}
        style={{ marginTop: 16 }}
      >
        <Form.Item name="label" label={t('common:common.label')} rules={[{ required: true }]}>
          <Radio.Group optionType="button" buttonStyle="solid">
            <Radio.Button value="simple">{t('common:label.simple')}</Radio.Button>
            <Radio.Button value="complex">{t('common:label.complex')}</Radio.Button>
          </Radio.Group>
        </Form.Item>
        <Form.Item
          name="lines"
          label={t('route:samples.batch.lines')}
          extra={t('route:samples.batch.linesExtra')}
          rules={[
            {
              validator: (_, v: string) =>
                splitLines(v ?? '').length > 0 ? Promise.resolve() : Promise.reject(new Error(t('common:common.required'))),
            },
          ]}
        >
          <Input.TextArea rows={10} placeholder={t('route:samples.batch.linesPlaceholder')} />
        </Form.Item>
        <Form.Item name="build_vector" valuePropName="checked" extra={t('route:samples.buildVectorExtra')} style={{ marginBottom: 0 }}>
          <Checkbox>{t('route:samples.buildVector')}</Checkbox>
        </Form.Item>
      </Form>
    </Modal>
  );
}
