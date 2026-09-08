import { useMemo } from 'react';
import type { EChartsOption } from 'echarts';
import { useTranslation } from 'react-i18next';
import { Chart, EmptyState, useChartTheme } from '@/components';
import type { RangeKey, UsageTrendPoint } from '@/types';
import { dayjs, formatNumber, formatTokens } from '@/utils/format';

interface Props {
  trend: UsageTrendPoint[] | undefined;
  range: RangeKey;
  loading?: boolean;
}

/** Stacked token bars per series (model / API Key) with a request-count line on the right axis. */
export default function TrendChart({ trend, range, loading }: Props) {
  const { t } = useTranslation(['console', 'common']);
  const theme = useChartTheme();
  const points = useMemo(() => trend ?? [], [trend]);

  const hasData = points.some((p) => p.total_tokens > 0 || p.requests > 0);

  const option = useMemo<EChartsOption>(() => {
    const fmt = range === '24h' ? 'MM-DD HH:mm' : 'MM-DD';
    const keys: string[] = [];
    const seen = new Set<string>();
    for (const p of points) {
      for (const k of Object.keys(p.series ?? {})) {
        if (!seen.has(k)) {
          seen.add(k);
          keys.push(k);
        }
      }
    }
    const lineColor = theme.dark ? '#fafafa' : '#18181b';
    const requestsLabel = t('common:common.requests');

    return {
      tooltip: {
        trigger: 'axis',
        axisPointer: { type: 'shadow' },
        valueFormatter: (v) => formatNumber(typeof v === 'number' ? v : Number(v)),
      },
      legend: { type: 'scroll', top: 0 },
      grid: { top: 40, right: 24 },
      xAxis: { type: 'category', data: points.map((p) => dayjs(p.time).format(fmt)) },
      yAxis: [
        { type: 'value', axisLabel: { formatter: (v: number) => formatTokens(v, 0) } },
        {
          type: 'value',
          splitLine: { show: false },
          axisLabel: { formatter: (v: number) => formatTokens(v, 0) },
        },
      ],
      series: [
        ...keys.map((k, i) => ({
          name: k,
          type: 'bar' as const,
          stack: 'total',
          barMaxWidth: 28,
          emphasis: { focus: 'series' as const },
          itemStyle: { color: theme.palette[i % theme.palette.length] },
          data: points.map((p) => p.series?.[k] ?? 0),
        })),
        {
          name: requestsLabel,
          type: 'line' as const,
          yAxisIndex: 1,
          smooth: false,
          symbol: 'circle',
          symbolSize: 4,
          z: 10,
          lineStyle: { width: 2, color: lineColor },
          itemStyle: { color: lineColor },
          data: points.map((p) => p.requests),
        },
      ],
    };
  }, [points, range, theme, t]);

  if (!loading && !hasData) {
    return <EmptyState title={t('console:usage.trendEmpty')} hint={t('console:usage.trendEmptyHint')} />;
  }
  return <Chart option={option} height={320} loading={loading} />;
}
