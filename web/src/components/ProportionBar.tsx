import { formatPercent } from '@/utils/format';

interface Props {
  value: number;
  total: number;
  color?: string;
  width?: number;
}

/** 4px proportion bar with a percentage label. */
export default function ProportionBar({ value, total, color = 'var(--yz-primary)', width = 120 }: Props) {
  const ratio = total > 0 ? Math.min(1, value / total) : 0;
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
      <div className="yz-proportion" style={{ width }}>
        <div className="yz-proportion-fill" style={{ width: `${ratio * 100}%`, background: color }} />
      </div>
      <span
        style={{
          fontSize: 12,
          color: 'var(--yz-text-secondary)',
          minWidth: 42,
          textAlign: 'right',
          fontVariantNumeric: 'tabular-nums',
        }}
      >
        {formatPercent(ratio)}
      </span>
    </div>
  );
}
