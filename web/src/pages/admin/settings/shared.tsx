import { useEffect } from 'react';
import { App, Button, Col, InputNumber, Row, Slider, Space } from 'antd';
import type { FormInstance } from 'antd';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { useSiteStore } from '@/stores/site';
import type { ReactNode } from 'react';

export const SETTINGS_KEY = ['settings'] as const;

/** Mutation wrapper: success toast + settings cache invalidation. */
export function useSaveSettings<T>(fn: (body: T) => Promise<unknown>, onSuccess?: () => void) {
  const qc = useQueryClient();
  const { message } = App.useApp();
  const { t } = useTranslation('common');
  return useMutation({
    mutationFn: fn,
    onSuccess: () => {
      message.success(t('common.saveSuccess'));
      void qc.invalidateQueries({ queryKey: SETTINGS_KEY });
      void useSiteStore.getState().refresh();
      onSuccess?.();
    },
  });
}

/** Keep form values in sync with loaded settings (initialValues would not update). */
export function useSyncForm<T extends object>(form: FormInstance<T>, data: T | undefined) {
  useEffect(() => {
    if (data) form.setFieldsValue(data as Parameters<typeof form.setFieldsValue>[0]);
  }, [data, form]);
}

interface SaveBarProps {
  loading?: boolean;
  extra?: ReactNode;
  disabled?: boolean;
}

/** Primary save button (submits the enclosing form) with optional extra actions. */
export function SaveBar({ loading, extra, disabled }: SaveBarProps) {
  const { t } = useTranslation('common');
  return (
    <div style={{ marginTop: 8, paddingTop: 16, borderTop: '1px solid var(--yz-border)' }}>
      <Space wrap>
        <Button type="primary" htmlType="submit" loading={loading} disabled={disabled}>
          {t('action.save')}
        </Button>
        {extra}
      </Space>
    </div>
  );
}

interface SliderInputProps {
  value?: number;
  onChange?: (v: number) => void;
  min?: number;
  max?: number;
  step?: number;
  disabled?: boolean;
}

/** Slider with a synced InputNumber; works as an antd Form.Item control. */
export function SliderInput({ value, onChange, min = 0, max = 1, step = 0.01, disabled }: SliderInputProps) {
  const v = value ?? min;
  return (
    <Row gutter={12} align="middle">
      <Col flex="auto">
        <Slider min={min} max={max} step={step} value={v} onChange={(n) => onChange?.(n)} disabled={disabled} />
      </Col>
      <Col flex="110px">
        <InputNumber
          min={min}
          max={max}
          step={step}
          value={v}
          onChange={(n) => {
            if (typeof n === 'number') onChange?.(n);
          }}
          disabled={disabled}
          style={{ width: '100%' }}
        />
      </Col>
    </Row>
  );
}
