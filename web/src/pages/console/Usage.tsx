import { useMemo, useState } from 'react';
import { Card, Col, Input, Row, Segmented, Select, Typography } from 'antd';
import {
  ApiOutlined,
  DatabaseOutlined,
  ExportOutlined,
  ImportOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { useQuery, keepPreviousData } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { userApi } from '@/api';
import { FilterBar, PageHeader, RangeSelector, StatCard, StatGroup } from '@/components';
import { useRange } from '@/hooks/useRange';
import type { ModelType, UserUsageParams } from '@/types';
import { MODEL_TYPES } from '@/utils/constants';
import { formatNumber, formatTokens } from '@/utils/format';
import DimTable from './usage/DimTable';
import TrendChart from './usage/TrendChart';

type GroupBy = NonNullable<UserUsageParams['group_by']>;

export default function Usage() {
  const { t } = useTranslation(['console', 'common']);
  const [apiKeyId, setApiKeyId] = useState<number | undefined>();
  const [modelInput, setModelInput] = useState('');
  const [model, setModel] = useState('');
  const [apiType, setApiType] = useState<ModelType | undefined>();
  const [groupBy, setGroupBy] = useState<GroupBy>('model');
  const { range, setRange, custom, setCustom, params: rangeParams } = useRange('7d');

  const keys = useQuery({ queryKey: ['user', 'keys'], queryFn: userApi.keys });

  const params = useMemo<UserUsageParams>(
    () => ({
      ...rangeParams,
      api_key_id: apiKeyId,
      model: model || undefined,
      api_type: apiType,
      group_by: groupBy,
    }),
    [rangeParams, apiKeyId, model, apiType, groupBy],
  );

  const usage = useQuery({
    queryKey: ['user', 'usage', params],
    queryFn: () => userApi.usage(params),
    placeholderData: keepPreviousData,
  });

  const summary = usage.data?.summary;
  const loading = usage.isLoading;

  const stats = [
    {
      key: 'requests',
      title: t('console:usage.stats.requests'),
      value: formatTokens(summary?.requests ?? 0),
      tooltip: formatNumber(summary?.requests ?? 0),
      hint: t('console:usage.stats.successFailed', {
        success: formatNumber(summary?.success ?? 0),
        failed: formatNumber(summary?.failed ?? 0),
      }),
      icon: <ApiOutlined />,
    },
    {
      key: 'prompt',
      title: t('console:usage.stats.prompt'),
      value: formatTokens(summary?.prompt_tokens ?? 0),
      tooltip: formatNumber(summary?.prompt_tokens ?? 0),
      icon: <ImportOutlined />,
    },
    {
      key: 'completion',
      title: t('console:usage.stats.completion'),
      value: formatTokens(summary?.completion_tokens ?? 0),
      tooltip: formatNumber(summary?.completion_tokens ?? 0),
      icon: <ExportOutlined />,
    },
    {
      key: 'total',
      title: t('console:usage.stats.total'),
      value: formatTokens(summary?.total_tokens ?? 0),
      tooltip: formatNumber(summary?.total_tokens ?? 0),
      icon: <DatabaseOutlined />,
    },
    {
      key: 'cached',
      title: t('console:usage.stats.cached'),
      value: formatTokens(summary?.cached_tokens ?? 0),
      tooltip: formatNumber(summary?.cached_tokens ?? 0),
      icon: <ThunderboltOutlined />,
    },
  ];

  const groupLabel = groupBy === 'model' ? t('console:usage.groupByModel') : t('console:usage.groupByKey');

  return (
    <div>
      <PageHeader title={t('console:usage.title')} subtitle={t('console:usage.subtitle')} />

      <Card className="yz-card" style={{ marginBottom: 16 }} styles={{ body: { paddingBottom: 4 } }}>
        <FilterBar
          extra={
            <Segmented
              value={groupBy}
              onChange={(v) => setGroupBy(v as GroupBy)}
              options={[
                { value: 'model', label: t('console:usage.groupByModel') },
                { value: 'api_key', label: t('console:usage.groupByKey') },
              ]}
            />
          }
        >
          <Select
            allowClear
            placeholder={t('console:usage.filter.apiKey')}
            value={apiKeyId}
            onChange={(v) => setApiKeyId(v)}
            loading={keys.isLoading}
            style={{ width: 220 }}
            optionFilterProp="label"
            showSearch
            options={(keys.data ?? []).map((k) => ({
              value: k.id,
              label: `${k.name} (${k.masked})`,
            }))}
          />
          <Input
            allowClear
            placeholder={t('console:usage.filter.model')}
            value={modelInput}
            onChange={(e) => {
              setModelInput(e.target.value);
              if (!e.target.value) setModel('');
            }}
            onPressEnter={() => setModel(modelInput.trim())}
            onBlur={() => setModel(modelInput.trim())}
            style={{ width: 180 }}
          />
          <Select
            allowClear
            placeholder={t('console:usage.filter.apiType')}
            value={apiType}
            onChange={(v) => setApiType(v)}
            style={{ width: 130 }}
            options={MODEL_TYPES.map((k) => ({ value: k, label: t(`common:type.${k}`) }))}
          />
          <RangeSelector
            value={range}
            onChange={setRange}
            allowCustom
            custom={custom}
            onCustomChange={setCustom}
          />
        </FilterBar>
      </Card>

      <StatGroup>
        {stats.map((s) => (
          <StatCard
            key={s.key}
            title={s.title}
            value={s.value}
            hint={s.hint}
            tooltip={s.tooltip}
            icon={s.icon}
            loading={loading}
          />
        ))}
      </StatGroup>

      <Card
        className="yz-card"
        style={{ marginBottom: 16 }}
        title={t('console:usage.trend')}
        extra={
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {t('console:usage.trendHint', { group: groupLabel })}
          </Typography.Text>
        }
      >
        <TrendChart trend={usage.data?.trend} range={range} loading={loading} />
      </Card>

      <Row gutter={[16, 16]}>
        <Col xl={8} md={12} xs={24}>
          <Card className="yz-card" title={t('console:usage.byModel')} styles={{ body: { padding: 12 } }}>
            <DimTable rows={usage.data?.by_model} loading={loading} />
          </Card>
        </Col>
        <Col xl={8} md={12} xs={24}>
          <Card className="yz-card" title={t('console:usage.byApiKey')} styles={{ body: { padding: 12 } }}>
            <DimTable rows={usage.data?.by_api_key} loading={loading} />
          </Card>
        </Col>
        <Col xl={8} md={12} xs={24}>
          <Card className="yz-card" title={t('console:usage.byProvider')} styles={{ body: { padding: 12 } }}>
            <DimTable rows={usage.data?.by_provider} loading={loading} />
          </Card>
        </Col>
      </Row>
    </div>
  );
}
