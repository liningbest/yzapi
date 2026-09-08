import { Suspense, lazy, useMemo } from 'react';
import { Skeleton } from 'antd';
import type { EChartsOption } from 'echarts';
import { useThemeStore } from '@/stores/theme';
import { CHART_PALETTE } from '@/utils/constants';

const ReactECharts = lazy(() => import('echarts-for-react'));

interface Props {
  option: EChartsOption;
  height?: number | string;
  loading?: boolean;
  style?: React.CSSProperties;
  onEvents?: Record<string, (params: unknown) => void>;
}

/** Base theme tokens for ECharts, dark-mode aware. */
export function useChartTheme() {
  const mode = useThemeStore((s) => s.mode);
  return useMemo(() => {
    const dark = mode === 'dark';
    const text = dark ? '#a1a1aa' : '#71717a';
    const line = dark ? '#27272a' : '#e4e4e7';
    const axis = dark ? '#3f3f46' : '#d4d4d8';
    return {
      dark,
      palette: CHART_PALETTE,
      textColor: text,
      splitLineColor: line,
      axisLineColor: axis,
      tooltip: {
        backgroundColor: dark ? '#18181b' : '#ffffff',
        borderColor: dark ? '#27272a' : '#e4e4e7',
        borderWidth: 1,
        padding: [6, 10],
        textStyle: { color: dark ? '#fafafa' : '#18181b', fontSize: 12 },
        extraCssText: 'box-shadow:0 4px 12px rgba(0,0,0,0.06);border-radius:6px;',
      },
      base: {
        color: CHART_PALETTE,
        textStyle: { fontFamily: 'inherit' },
        grid: { left: 12, right: 16, top: 36, bottom: 8, containLabel: true },
        legend: { textStyle: { color: text, fontSize: 12 }, icon: 'rect', itemWidth: 10, itemHeight: 10, top: 0 },
        xAxis: {
          axisLine: { lineStyle: { color: axis } },
          axisTick: { show: false },
          axisLabel: { color: text, fontSize: 11 },
        },
        yAxis: {
          axisLine: { show: false },
          axisTick: { show: false },
          splitLine: { lineStyle: { color: line } },
          axisLabel: { color: text, fontSize: 11 },
        },
      },
    };
  }, [mode]);
}

/** Deep-ish merge: base tokens are applied under the provided option. */
function mergeOption(base: Record<string, unknown>, option: EChartsOption): EChartsOption {
  const out: Record<string, unknown> = { ...base, ...(option as Record<string, unknown>) };
  for (const key of ['grid', 'legend', 'tooltip', 'textStyle']) {
    if (base[key] && (option as Record<string, unknown>)[key] && !Array.isArray((option as Record<string, unknown>)[key])) {
      out[key] = { ...(base[key] as object), ...((option as Record<string, unknown>)[key] as object) };
    }
  }
  for (const key of ['xAxis', 'yAxis']) {
    const b = base[key] as object;
    const o = (option as Record<string, unknown>)[key];
    if (!o) continue;
    out[key] = Array.isArray(o) ? o.map((x) => ({ ...b, ...(x as object) })) : { ...b, ...(o as object) };
  }
  return out as EChartsOption;
}

export default function Chart({ option, height = 300, loading, style, onEvents }: Props) {
  const theme = useChartTheme();
  const merged = useMemo(
    () => mergeOption({ ...theme.base, tooltip: theme.tooltip }, option),
    [option, theme],
  );
  return (
    <Suspense fallback={<Skeleton active paragraph={{ rows: 5 }} />}>
      <ReactECharts
        option={merged}
        notMerge
        lazyUpdate
        showLoading={loading}
        loadingOption={{ text: '', color: '#2563eb', maskColor: 'transparent' }}
        style={{ height, width: '100%', ...style }}
        onEvents={onEvents}
        opts={{ renderer: 'canvas' }}
      />
    </Suspense>
  );
}
