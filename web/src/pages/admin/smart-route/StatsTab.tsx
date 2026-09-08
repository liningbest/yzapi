import { useMemo } from 'react';
import { Card, Col, Row, Table } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  BranchesOutlined,
  ClockCircleOutlined,
  CloseCircleOutlined,
  DatabaseOutlined,
  PercentageOutlined,
  SendOutlined,
} from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import type { EChartsOption } from 'echarts';
import { routeApi } from '@/api';
import { Chart, EmptyState, FilterBar, RangeSelector, StatCard, StatGroup, TokenText, ProportionBar, useChartTheme } from '@/components';
import { useRange } from '@/hooks/useRange';
import type { KeyCount, RangeKey } from '@/types';
import { CHART_PALETTE } from '@/utils/constants';
import { formatMs, formatNumber, formatTokens } from '@/utils/format';

const LABEL_COLORS: Record<string, string> = {
  simple: CHART_PALETTE[3],
  complex: CHART_PALETTE[2],
};

export default function StatsTab() {
  const { t } = useTranslation(['route', 'common']);
  const theme = useChartTheme();
  const { range, setRange, params } = useRange('24h');
  const rangeKey: RangeKey = params.range ?? '24h';

  const stats = useQuery({
    queryKey: ['route', 'stats', rangeKey],
    queryFn: () => routeApi.stats(rangeKey),
  });
  const data = stats.data;
  const loading = stats.isLoading;

  const labelText = (key: string) => t(`common:label.${key}`, { defaultValue: key });
  const sourceText = (key: string) => t(`route:source.${key}`, { defaultValue: key });

  const byLabelOption = useMemo<EChartsOption>(() => {
    const items = data?.by_label ?? [];
    return {
      tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
      grid: { left: 12, right: 24, top: 12, bottom: 8, containLabel: true },
      xAxis: { type: 'value', minInterval: 1, axisLabel: { formatter: (v: number) => formatTokens(v, 0) } },
      yAxis: { type: 'category', data: items.map((i) => labelText(i.key)), splitLine: { show: false } },
      series: [
        {
          type: 'bar',
          name: t('common:common.count'),
          barMaxWidth: 28,
          data: items.map((i, idx) => ({
            value: i.count,
            itemStyle: { color: LABEL_COLORS[i.key] ?? CHART_PALETTE[idx % CHART_PALETTE.length], borderRadius: [0, 2, 2, 0] },
          })),
          label: { show: true, position: 'right', color: theme.textColor, formatter: (p) => formatNumber(Number(p.value)) },
        },
      ],
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data?.by_label, theme.textColor, t]);

  const verticalBar = (items: KeyCount[], nameOf: (k: string) => string): EChartsOption => ({
    tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
    grid: { left: 12, right: 16, top: 24, bottom: 8, containLabel: true },
    xAxis: { type: 'category', data: items.map((i) => nameOf(i.key)), axisLabel: { interval: 0 } },
    yAxis: { type: 'value', minInterval: 1, axisLabel: { formatter: (v: number) => formatTokens(v, 0) } },
    series: [
      {
        type: 'bar',
        name: t('common:common.count'),
        barMaxWidth: 36,
        data: items.map((i, idx) => ({
          value: i.count,
          itemStyle: { color: CHART_PALETTE[idx % CHART_PALETTE.length], borderRadius: [2, 2, 0, 0] },
        })),
        label: { show: true, position: 'top', color: theme.textColor, formatter: (p) => formatNumber(Number(p.value)) },
      },
    ],
  });

  const bySourceOption = useMemo(() => verticalBar(data?.by_source ?? [], sourceText), [data?.by_source, theme.textColor, t]); // eslint-disable-line react-hooks/exhaustive-deps
  const byBucketOption = useMemo(() => verticalBar(data?.by_token_bucket ?? [], (k) => k), [data?.by_token_bucket, theme.textColor, t]); // eslint-disable-line react-hooks/exhaustive-deps

  const byModel = data?.by_model ?? [];
  const modelTotal = byModel.reduce((s, i) => s + i.count, 0);
  const modelColumns: ColumnsType<KeyCount> = [
    {
      title: t('common:common.model'),
      dataIndex: 'key',
      render: (k: string) => <span className="yz-mono" style={{ fontSize: 13 }}>{k || '-'}</span>,
    },
    {
      title: t('common:common.count'),
      dataIndex: 'count',
      width: 90,
      align: 'right',
      render: (v: number) => <span style={{ fontVariantNumeric: 'tabular-nums' }}>{formatNumber(v)}</span>,
    },
    {
      title: t('common:common.tokens'),
      dataIndex: 'tokens',
      width: 90,
      align: 'right',
      render: (v: number | undefined) => <TokenText value={v} />,
    },
    {
      title: t('common:common.proportion'),
      key: 'ratio',
      width: 180,
      render: (_, r, idx) => (
        <ProportionBar value={r.count} total={modelTotal} color={CHART_PALETTE[idx % CHART_PALETTE.length]} width={100} />
      ),
    },
  ];

  const empty = <EmptyState title={t('route:stats.empty')} />;
  const cardBody = { padding: 16 };

  return (
    <div>
      <FilterBar>
        <RangeSelector value={range} onChange={setRange} />
      </FilterBar>

      <StatGroup>
        <StatCard
          title={t('route:stats.decisions')}
          value={formatNumber(data?.decisions ?? 0)}
          icon={<BranchesOutlined />}
          loading={loading}
        />
        <StatCard
          title={t('route:stats.requests')}
          value={formatNumber(data?.requests ?? 0)}
          icon={<SendOutlined />}
          loading={loading}
        />
        <StatCard
          title={t('route:stats.failed')}
          value={formatNumber(data?.failed ?? 0)}
          icon={<CloseCircleOutlined />}
          loading={loading}
        />
        <StatCard
          title={t('route:stats.totalTokens')}
          value={formatTokens(data?.total_tokens ?? 0)}
          icon={<DatabaseOutlined />}
          loading={loading}
          tooltip={formatNumber(data?.total_tokens ?? 0)}
        />
        <StatCard
          title={t('route:stats.avgTokens')}
          value={formatTokens(Math.round(data?.avg_tokens ?? 0))}
          icon={<PercentageOutlined />}
          loading={loading}
        />
        <StatCard
          title={t('route:stats.latency')}
          value={formatMs(data?.latency_ms ?? 0)}
          icon={<ClockCircleOutlined />}
          loading={loading}
        />
      </StatGroup>

      <Row gutter={[16, 16]}>
        <Col xs={24} xl={12}>
          <Card className="yz-card" title={t('route:stats.byLabel')} styles={{ body: cardBody }}>
            {data?.by_label?.length ? <Chart option={byLabelOption} height={260} loading={loading} /> : empty}
          </Card>
        </Col>
        <Col xs={24} xl={12}>
          <Card className="yz-card" title={t('route:stats.bySource')} styles={{ body: cardBody }}>
            {data?.by_source?.length ? <Chart option={bySourceOption} height={260} loading={loading} /> : empty}
          </Card>
        </Col>
        <Col xs={24} xl={12}>
          <Card className="yz-card" title={t('route:stats.byModel')} styles={{ body: cardBody }}>
            {byModel.length ? (
              <Table<KeyCount>
                className="yz-table"
                size="small"
                rowKey="key"
                columns={modelColumns}
                dataSource={byModel}
                pagination={false}
                scroll={{ x: 'max-content' }}
              />
            ) : (
              empty
            )}
          </Card>
        </Col>
        <Col xs={24} xl={12}>
          <Card className="yz-card" title={t('route:stats.byTokenBucket')} styles={{ body: cardBody }}>
            {data?.by_token_bucket?.length ? <Chart option={byBucketOption} height={260} loading={loading} /> : empty}
          </Card>
        </Col>
      </Row>
    </div>
  );
}
