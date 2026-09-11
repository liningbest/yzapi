import { useMemo, useState } from 'react';
import { Button, Card, Col, Input, Row, Segmented, Select, Space, Tooltip } from 'antd';
import {
  ApiOutlined,
  ClusterOutlined,
  DatabaseOutlined,
  DownloadOutlined,
  ReloadOutlined,
  SendOutlined,
  TeamOutlined,
  ThunderboltOutlined,
  UploadOutlined,
  UserOutlined,
  AppstoreOutlined,
  BarsOutlined,
} from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { logsApi, usageApi } from '@/api';
import { FilterBar, PageHeader, ProviderAvatar, RangeSelector, SectionTitle, StatCard, StatGroup } from '@/components';
import { useRange } from '@/hooks/useRange';
import type { UsageParams } from '@/types';
import { CHART_PALETTE, MODEL_TYPES } from '@/utils/constants';
import { formatNumber, formatTokens, formatMoney } from '@/utils/format';
import DimTable from './usage/DimTable';
import TrendChart from './usage/TrendChart';

type GroupBy = NonNullable<UsageParams['group_by']>;

interface Filters {
  user_id?: number;
  account_id?: number;
  provider?: string;
  api_type?: string;
  model?: string;
  api_key_id?: number;
}

export default function Usage() {
  const { t } = useTranslation(['usage', 'common']);
  const { range, setRange, custom, setCustom, params: rangeParams } = useRange('7d');
  const [filters, setFilters] = useState<Filters>({});
  const [groupBy, setGroupBy] = useState<GroupBy>('model');
  const [keyInput, setKeyInput] = useState('');

  const filterOpts = useQuery({ queryKey: ['logs', 'filters'], queryFn: logsApi.filters, staleTime: 60_000 });

  const queryParams = useMemo<UsageParams>(
    () => ({ ...filters, ...rangeParams, group_by: groupBy }),
    [filters, rangeParams, groupBy],
  );
  const usage = useQuery({
    queryKey: ['usage', queryParams],
    queryFn: () => usageApi.query(queryParams),
    placeholderData: (prev) => prev,
  });

  const patch = (p: Partial<Filters>) => setFilters((prev) => ({ ...prev, ...p }));

  const userOptions = useMemo(
    () => (filterOpts.data?.users ?? []).map((u) => ({ value: u.id, label: u.username })),
    [filterOpts.data],
  );
  const accountOptions = useMemo(
    () => (filterOpts.data?.accounts ?? []).map((a) => ({ value: a.id, label: a.name, provider: a.provider })),
    [filterOpts.data],
  );
  const providerOptions = useMemo(
    () => (filterOpts.data?.providers ?? []).map((p) => ({ value: p, label: p })),
    [filterOpts.data],
  );
  const modelOptions = useMemo(
    () => (filterOpts.data?.models ?? []).map((m) => ({ value: m, label: m })),
    [filterOpts.data],
  );

  const data = usage.data;
  const s = data?.summary;
  const loading = usage.isLoading;

  const stat = (value: number | undefined) => (
    <Tooltip title={value !== undefined ? formatNumber(value) : undefined}>
      <span>{value !== undefined ? formatTokens(value) : '-'}</span>
    </Tooltip>
  );

  const stats = [
    {
      key: 'requests',
      title: t('common:common.requests'),
      value: s ? formatNumber(s.requests) : '-',
      hint: s ? t('usage:stats.requestsHint', { success: formatNumber(s.success), failed: formatNumber(s.failed) }) : undefined,
      icon: <SendOutlined />,
    },
    {
      key: 'prompt',
      title: t('common:common.promptTokens'),
      value: stat(s?.prompt_tokens),
      icon: <UploadOutlined />,
    },
    {
      key: 'completion',
      title: t('common:common.completionTokens'),
      value: stat(s?.completion_tokens),
      icon: <DownloadOutlined />,
    },
    {
      key: 'total',
      title: t('common:common.totalTokens'),
      value: stat(s?.total_tokens),
      icon: <DatabaseOutlined />,
    },
    {
      key: 'cost',
      title: t('common:common.cost'),
      value: s ? formatMoney(s.cost, data?.currency) : '-',
      hint: s?.cost_unverified ? t('usage:stats.costUnverified', { count: s.cost_unverified }) : t('usage:stats.costHint'),
      icon: <DatabaseOutlined />,
    },
    {
      key: 'cached',
      title: t('common:common.cachedTokens'),
      value: stat(s?.cached_tokens),
      hint:
        s && s.total_tokens > 0
          ? t('usage:stats.cacheRate', { rate: ((s.cached_tokens / s.total_tokens) * 100).toFixed(1) })
          : undefined,
      icon: <ThunderboltOutlined />,
    },
  ];

  const dims = [
    { key: 'by_provider', title: t('common:common.provider'), icon: <ApiOutlined />, items: data?.by_provider, provider: true },
    { key: 'by_model', title: t('common:common.model'), icon: <AppstoreOutlined />, items: data?.by_model },
    { key: 'by_model_group', title: t('common:common.modelGroup'), icon: <ClusterOutlined />, items: data?.by_model_group },
    { key: 'by_account', title: t('usage:dim.account'), icon: <DatabaseOutlined />, items: data?.by_account },
    { key: 'by_group', title: t('common:common.userGroup'), icon: <TeamOutlined />, items: data?.by_group },
    { key: 'by_user', title: t('common:common.user'), icon: <UserOutlined />, items: data?.by_user },
  ] as const;

  return (
    <div>
      <PageHeader
        title={t('usage:title')}
        subtitle={t('usage:subtitle')}
        extra={
          <Button icon={<ReloadOutlined />} onClick={() => void usage.refetch()} loading={usage.isFetching}>
            {t('common:action.refresh')}
          </Button>
        }
      />

      <Card className="yz-card" style={{ marginBottom: 16 }}>
        <FilterBar
          style={{ marginBottom: 0 }}
          extra={
            <RangeSelector value={range} onChange={setRange} allowCustom custom={custom} onCustomChange={setCustom} />
          }
        >
          <Select
            style={{ width: 160 }}
            allowClear
            showSearch
            optionFilterProp="label"
            placeholder={t('common:common.user')}
            value={filters.user_id}
            onChange={(v) => patch({ user_id: v })}
            options={userOptions}
            loading={filterOpts.isLoading}
          />
          <Select
            style={{ width: 190 }}
            allowClear
            showSearch
            optionFilterProp="label"
            placeholder={t('common:common.account')}
            value={filters.account_id}
            onChange={(v) => patch({ account_id: v })}
            options={accountOptions}
            optionRender={(opt) => (
              <Space size={6}>
                <ProviderAvatar provider={opt.data.provider} size={18} />
                <span>{opt.data.label}</span>
              </Space>
            )}
          />
          <Select
            style={{ width: 160 }}
            allowClear
            showSearch
            optionFilterProp="label"
            placeholder={t('common:common.provider')}
            value={filters.provider}
            onChange={(v) => patch({ provider: v })}
            options={providerOptions}
            optionRender={(opt) => (
              <Space size={6}>
                <ProviderAvatar provider={String(opt.value)} size={18} />
                <span>{opt.data.label}</span>
              </Space>
            )}
          />
          <Select
            style={{ width: 130 }}
            allowClear
            placeholder={t('common:common.apiType')}
            value={filters.api_type}
            onChange={(v) => patch({ api_type: v })}
            options={MODEL_TYPES.map((m) => ({ value: m, label: t(`common:type.${m}`) }))}
          />
          <Select
            style={{ width: 200 }}
            allowClear
            showSearch
            optionFilterProp="label"
            placeholder={t('common:common.model')}
            value={filters.model}
            onChange={(v) => patch({ model: v })}
            options={modelOptions}
          />
          <Input.Search
            style={{ width: 170 }}
            allowClear
            placeholder={t('usage:apiKeyId')}
            value={keyInput}
            onChange={(e) => {
              const v = e.target.value.replace(/[^0-9]/g, '');
              setKeyInput(v);
              if (!v) patch({ api_key_id: undefined });
            }}
            onSearch={(v) => patch({ api_key_id: v ? Number(v) : undefined })}
          />
        </FilterBar>
      </Card>

      <StatGroup>
        {stats.map((st) => (
          <StatCard key={st.key} title={st.title} value={st.value} hint={st.hint} icon={st.icon} loading={loading} />
        ))}
      </StatGroup>

      <Card className="yz-card" style={{ marginBottom: 16 }}>
        <SectionTitle
          extra={
            <Segmented
              size="small"
              value={groupBy}
              onChange={(v) => setGroupBy(v as GroupBy)}
              options={[
                { value: 'model', label: t('common:common.model'), icon: <AppstoreOutlined /> },
                { value: 'api_key', label: t('common:common.apiKey'), icon: <BarsOutlined /> },
              ]}
            />
          }
        >
          {t('usage:trend.title')}
        </SectionTitle>
        <TrendChart trend={data?.trend} range={range} loading={loading} height={320} />
      </Card>

      <Row gutter={[16, 16]}>
        {dims.map((d, i) => (
          <Col key={d.key} xs={24} md={12} xl={8}>
            <DimTable
              title={d.title}
              icon={d.icon}
              items={d.items}
              currency={data?.currency}
              loading={loading}
              provider={'provider' in d ? d.provider : false}
              color={CHART_PALETTE[i % CHART_PALETTE.length]}
            />
          </Col>
        ))}
      </Row>
    </div>
  );
}
