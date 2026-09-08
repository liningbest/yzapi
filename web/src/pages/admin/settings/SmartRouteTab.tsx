import { useEffect, useState } from 'react';
import { Alert, Col, Collapse, Form, Input, InputNumber, Row, Select, Switch } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { modelGroupsApi, settingsApi } from '@/api';
import type { SmartRouteSettings } from '@/types';
import { SaveBar, SliderInput, useSaveSettings, useSyncForm } from './shared';
import { NeutralTag } from '@/components';

interface Props {
  data?: SmartRouteSettings;
}

const APPLIED_HINT_MS = 5000;

export default function SmartRouteTab({ data }: Props) {
  const { t } = useTranslation(['settings', 'common']);
  const [form] = Form.useForm<SmartRouteSettings>();
  useSyncForm(form, data);
  const [applied, setApplied] = useState(false);

  useEffect(() => {
    if (!applied) return;
    const timer = window.setTimeout(() => setApplied(false), APPLIED_HINT_MS);
    return () => window.clearTimeout(timer);
  }, [applied]);

  const groups = useQuery({
    queryKey: ['model-groups', 'text-all'],
    queryFn: () => modelGroupsApi.list({ type: 'text', page_size: 200 }),
  });

  const save = useSaveSettings(
    (body: SmartRouteSettings) => settingsApi.saveSmartRoute(body),
    () => setApplied(true),
  );

  const groupOptions = (groups.data?.items ?? []).map((g) => ({
    value: g.id,
    label: (
      <span>
        {g.name}
        <NeutralTag style={{ marginInlineStart: 8 }}>{t('settings:smartRoute.modelsCount', { count: g.models.length })}</NeutralTag>
      </span>
    ),
    searchText: g.name,
  }));

  const onFinish = (values: SmartRouteSettings) => {
    const body: SmartRouteSettings = {
      enabled: values.enabled,
      virtual_model: values.virtual_model.trim(),
      simple_group_id: values.simple_group_id,
      complex_group_id: values.complex_group_id,
      threshold: values.threshold,
      confidence_gap: values.confidence_gap,
      top_k: values.top_k,
    };
    if (values.rule_max_chars !== undefined && values.rule_max_chars !== null) body.rule_max_chars = values.rule_max_chars;
    if (values.context_complex !== undefined && values.context_complex !== null)
      body.context_complex = values.context_complex;
    save.mutate(body);
  };

  return (
    <Form<SmartRouteSettings> form={form} layout="vertical" initialValues={data} onFinish={onFinish}>
      {applied ? (
        <Alert type="success" showIcon message={t('settings:smartRoute.applied')} style={{ marginBottom: 20 }} closable />
      ) : null}
      <Row gutter={[24, 0]}>
        <Col xs={24} lg={12}>
          <Form.Item
            name="enabled"
            label={t('settings:smartRoute.enabled.label')}
            extra={t('settings:smartRoute.enabled.extra')}
            valuePropName="checked"
          >
            <Switch />
          </Form.Item>
        </Col>
        <Col xs={24} lg={12}>
          <Form.Item
            name="virtual_model"
            label={t('settings:smartRoute.virtualModel.label')}
            extra={t('settings:smartRoute.virtualModel.extra')}
            rules={[{ required: true, message: t('common:common.required') }]}
          >
            <Input placeholder="yz-auto" className="yz-mono" />
          </Form.Item>
        </Col>
        <Col xs={24} lg={12}>
          <Form.Item
            name="simple_group_id"
            label={t('settings:smartRoute.simpleGroup.label')}
            extra={t('settings:smartRoute.simpleGroup.extra')}
            rules={[{ required: true, message: t('common:common.required') }]}
          >
            <Select
              showSearch
              loading={groups.isLoading}
              options={groupOptions}
              optionFilterProp="searchText"
              placeholder={t('settings:smartRoute.groupPlaceholder')}
            />
          </Form.Item>
        </Col>
        <Col xs={24} lg={12}>
          <Form.Item
            name="complex_group_id"
            label={t('settings:smartRoute.complexGroup.label')}
            extra={t('settings:smartRoute.complexGroup.extra')}
            rules={[{ required: true, message: t('common:common.required') }]}
          >
            <Select
              showSearch
              loading={groups.isLoading}
              options={groupOptions}
              optionFilterProp="searchText"
              placeholder={t('settings:smartRoute.groupPlaceholder')}
            />
          </Form.Item>
        </Col>
        <Col xs={24} lg={12}>
          <Form.Item
            name="threshold"
            label={t('settings:smartRoute.threshold.label')}
            extra={t('settings:smartRoute.threshold.extra')}
            rules={[{ required: true, message: t('common:common.required') }]}
          >
            <SliderInput min={0} max={1} step={0.01} />
          </Form.Item>
        </Col>
        <Col xs={24} lg={12}>
          <Form.Item
            name="confidence_gap"
            label={t('settings:smartRoute.confidenceGap.label')}
            extra={t('settings:smartRoute.confidenceGap.extra')}
            rules={[{ required: true, message: t('common:common.required') }]}
          >
            <SliderInput min={0} max={1} step={0.01} />
          </Form.Item>
        </Col>
        <Col xs={24} lg={12}>
          <Form.Item
            name="top_k"
            label={t('settings:smartRoute.topK.label')}
            extra={t('settings:smartRoute.topK.extra')}
            rules={[{ required: true, message: t('common:common.required') }]}
          >
            <InputNumber min={1} max={50} precision={0} style={{ width: '100%' }} />
          </Form.Item>
        </Col>
      </Row>
      <Collapse
        ghost
        style={{ marginBottom: 16 }}
        items={[
          {
            key: 'advanced',
            label: t('settings:smartRoute.advanced'),
            children: (
              <Row gutter={[24, 0]}>
                <Col xs={24} lg={12}>
                  <Form.Item
                    name="rule_max_chars"
                    label={t('settings:smartRoute.ruleMaxChars.label')}
                    extra={t('settings:smartRoute.ruleMaxChars.extra')}
                  >
                    <InputNumber min={0} precision={0} style={{ width: '100%' }} />
                  </Form.Item>
                </Col>
                <Col xs={24} lg={12}>
                  <Form.Item
                    name="context_complex"
                    label={t('settings:smartRoute.contextComplex.label')}
                    extra={t('settings:smartRoute.contextComplex.extra')}
                  >
                    <InputNumber min={0} precision={0} style={{ width: '100%' }} />
                  </Form.Item>
                </Col>
              </Row>
            ),
          },
        ]}
      />
      <SaveBar loading={save.isPending} />
    </Form>
  );
}
