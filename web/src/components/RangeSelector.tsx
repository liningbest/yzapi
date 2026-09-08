import { DatePicker, Segmented, Space } from 'antd';
import type { Dayjs } from 'dayjs';
import { useTranslation } from 'react-i18next';
import type { RangeKey } from '@/types';

interface Props {
  value: RangeKey;
  onChange: (v: RangeKey) => void;
  /** Include the custom option with a RangePicker. */
  allowCustom?: boolean;
  custom?: [Dayjs, Dayjs] | null;
  onCustomChange?: (v: [Dayjs, Dayjs] | null) => void;
  size?: 'small' | 'middle' | 'large';
  options?: RangeKey[];
}

export default function RangeSelector({
  value,
  onChange,
  allowCustom,
  custom,
  onCustomChange,
  size,
  options = ['24h', '7d', '30d'],
}: Props) {
  const { t } = useTranslation();
  const labels: Record<RangeKey, string> = {
    '24h': t('common.last24h'),
    '7d': t('common.last7d'),
    '30d': t('common.last30d'),
    custom: t('common.custom'),
  };
  const opts = [...options, ...(allowCustom ? (['custom'] as RangeKey[]) : [])].map((k) => ({
    value: k,
    label: labels[k],
  }));
  return (
    <Space wrap>
      <Segmented size={size} value={value} options={opts} onChange={(v) => onChange(v as RangeKey)} />
      {allowCustom && value === 'custom' ? (
        <DatePicker.RangePicker
          size={size}
          value={custom ?? null}
          onChange={(v) => onCustomChange?.(v && v[0] && v[1] ? [v[0], v[1]] : null)}
          allowClear={false}
        />
      ) : null}
    </Space>
  );
}
