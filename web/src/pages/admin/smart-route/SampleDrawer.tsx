import { App, Checkbox, Form, Input, InputNumber, Radio } from 'antd';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { routeApi } from '@/api';
import { FormDrawer } from '@/components';
import type { RouteLabel, RouteSample, RouteSampleInput } from '@/types';

interface Props {
  open: boolean;
  sample: RouteSample | null;
  onClose: () => void;
}

interface FormValues {
  label: RouteLabel;
  text: string;
  threshold: number;
  note?: string;
  build_vector: boolean;
}

export default function SampleDrawer({ open, sample, onClose }: Props) {
  const { t } = useTranslation(['route', 'common']);
  const { message } = App.useApp();
  const qc = useQueryClient();
  const [form] = Form.useForm<FormValues>();

  const save = useMutation({
    mutationFn: (values: FormValues) => {
      const body: RouteSampleInput = {
        label: values.label,
        text: values.text.trim(),
        threshold: values.threshold ?? 0,
        note: values.note?.trim() ?? '',
        build_vector: values.build_vector,
      };
      return sample ? routeApi.updateSample(sample.id, body) : routeApi.createSample(body);
    },
    onSuccess: () => {
      message.success(sample ? t('common:common.updateSuccess') : t('common:common.createSuccess'));
      onClose();
      void qc.invalidateQueries({ queryKey: ['route', 'samples'] });
    },
  });

  const submit = async () => {
    const values = await form.validateFields();
    save.mutate(values);
  };

  const initialValues: FormValues = sample
    ? { label: sample.label, text: sample.text, threshold: sample.threshold ?? 0, note: sample.note, build_vector: true }
    : { label: 'simple', text: '', threshold: 0, note: '', build_vector: true };

  return (
    <FormDrawer
      open={open}
      title={sample ? t('route:samples.edit') : t('route:samples.add')}
      onClose={onClose}
      onSubmit={submit}
      submitting={save.isPending}
      width={560}
    >
      <Form form={form} layout="vertical" initialValues={initialValues} requiredMark="optional">
        <Form.Item name="label" label={t('common:common.label')} rules={[{ required: true }]}>
          <Radio.Group optionType="button" buttonStyle="solid">
            <Radio.Button value="simple">{t('common:label.simple')}</Radio.Button>
            <Radio.Button value="complex">{t('common:label.complex')}</Radio.Button>
          </Radio.Group>
        </Form.Item>
        <Form.Item
          name="text"
          label={t('route:samples.text')}
          extra={t('route:samples.textExtra')}
          rules={[
            { required: true, message: t('common:common.required') },
            { whitespace: true, message: t('common:common.required') },
            { max: 65536 },
          ]}
        >
          <Input.TextArea rows={6} maxLength={65536} showCount placeholder={t('route:samples.textPlaceholder')} />
        </Form.Item>
        <Form.Item name="threshold" label={t('route:samples.threshold')} extra={t('route:samples.thresholdExtra')}>
          <InputNumber min={0} max={1} step={0.01} precision={2} controls style={{ width: 180 }} />
        </Form.Item>
        <Form.Item name="note" label={t('common:common.note')}>
          <Input placeholder={t('common:common.notePlaceholder')} maxLength={200} />
        </Form.Item>
        <Form.Item name="build_vector" valuePropName="checked" extra={t('route:samples.buildVectorExtra')}>
          <Checkbox>{t('route:samples.buildVector')}</Checkbox>
        </Form.Item>
      </Form>
    </FormDrawer>
  );
}
