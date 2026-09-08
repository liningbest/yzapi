import { Tooltip } from 'antd';
import { formatNumber, formatTokens } from '@/utils/format';

interface Props {
  value: number | null | undefined;
  digits?: number;
  style?: React.CSSProperties;
}

/** Abbreviated token count (1.2K / 2.5M) with the exact number on hover. */
export default function TokenText({ value, digits, style }: Props) {
  if (value === null || value === undefined) return <span style={style}>-</span>;
  return (
    <Tooltip title={formatNumber(value)}>
      <span style={{ fontVariantNumeric: 'tabular-nums', ...style }}>{formatTokens(value, digits)}</span>
    </Tooltip>
  );
}
