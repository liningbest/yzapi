import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import type { EChartsOption } from 'echarts';
import { Chart, EmptyState, useChartTheme } from '@/components';
import type { RangeKey, UsageTrendPoint } from '@/types';
import { dayjs, formatNumber, formatTokens } from '@/utils/format';

interface Props {
  trend: UsageTrendPoint[] | undefined;
  range: RangeKey;
  loading?: boolean;
  height?: number;
}

/** Stacked bars (tokens per series key) plus a request-count line on a secondary axis. */
export default function TrendChart({ trend, range, loading, height = 320 }: Props) {
  const { t } = useTranslation(['usage', 'common']);
  const theme = useChartTheme();

  const option = useMemo<EChartsOption | null>(() => {
    if (!trend || trend.length === 0) return null;
    const fmt = range === '24h' ? 'MM-DD HH:mm' : 'MM-DD';
    const times = trend.map((p) => dayjs(p.time).format(fmt));
    const keys = Array.from(new Set(trend.flatMap((p) => Object.keys(p.series ?? {}))));
    const barSeries = keys.map((k, i) => ({
      name: k,
      type: 'bar' as const,
      stack: 'total',
      barMaxWidth: 28,
      emphasis: { focus: 'series' as const },
      itemStyle: { color: theme.palette[i % theme.palette.length] },
      data: trend.map((p) => p.series?.[k] ?? 0),
    }));
    const reqName = t('common:common.requests');
    const lineSeries = {
      name: reqName,
      type: 'line' as const,
      yAxisIndex: 1,
      smooth: true,
      showSymbol: false,
      lineStyle: { width: 2, color: theme.dark ? '#f1f5f9' : '#0f172a' },
      itemStyle: { color: theme.dark ? '#f1f5f9' : '#0f172a' },
      data: trend.map((p) => p.requests),
      z: 10,
    };
    return {
      tooltip: {
        trigger: 'axis',
        axisPointer: { type: 'shadow' },
        formatter: (raw: unknown) => {
          const ps = raw as { seriesName: string; value: number; marker: string; axisValue: string }[];
          if (!ps.length) return '';
          const rows = ps
            .filter((p) => p.value)
            .map(
              (p) =>
                `<div style="display:flex;justify-content:space-between;gap:16px"><span>${p.marker}${p.seriesName}</span><b>${
                  p.seriesName === reqName ? formatNumber(p.value) : formatTokens(p.value)
                }</b></div>`,
            )
            .join('');
          const total = ps.filter((p) => p.seriesName !== reqName).reduce((s, p) => s + (p.value || 0), 0);
          return `<div style="font-weight:600;margin-bottom:4px">${ps[0].axisValue}</div>${rows}<div style="margin-top:4px;padding-top:4px;border-top:1px solid ${theme.splitLineColor};display:flex;justify-content:space-between"><span>${t('common:common.totalTokens')}</span><b>${formatTokens(total)}</b></div>`;
        },
      },
      legend: { type: 'scroll', top: 0, data: [...keys, reqName] },
      grid: { left: 12, right: 16, top: 40, bottom: 8, containLabel: true },
      xAxis: { type: 'category', data: times, boundaryGap: true },
      yAxis: [
        { type: 'value', axisLabel: { formatter: (v: number) => formatTokens(v, 0) } },
        {
          type: 'value',
          splitLine: { show: false },
          axisLabel: { formatter: (v: number) => formatNumber(v) },
        },
      ],
      series: [...barSeries, lineSeries],
    };
  }, [trend, range, theme, t]);

  if (!option) {
    return loading ? (
      <Chart option={{}} height={height} loading />
    ) : (
      <EmptyState title={t('usage:trend.empty')} hint={t('usage:trend.emptyHint')} />
    );
  }
  return <Chart option={option} height={height} loading={loading} />;
}
