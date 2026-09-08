import { Card, Skeleton, Tooltip, Typography } from 'antd';
import type { ReactNode } from 'react';

interface Props {
  title: ReactNode;
  value: ReactNode;
  icon?: ReactNode;
  color?: string;
  suffix?: ReactNode;
  hint?: ReactNode;
  tooltip?: ReactNode;
  loading?: boolean;
  footer?: ReactNode;
  style?: React.CSSProperties;
  size?: 'default' | 'small';
}

function tint(hex: string, alpha: number) {
  const h = hex.replace('#', '');
  const n = parseInt(h.length === 3 ? h.split('').map((c) => c + c).join('') : h, 16);
  const r = (n >> 16) & 255;
  const g = (n >> 8) & 255;
  const b = n & 255;
  return `rgba(${r},${g},${b},${alpha})`;
}

/** Stat card with icon in a tinted circle. */
export default function StatCard({
  title,
  value,
  icon,
  color = '#4f46e5',
  suffix,
  hint,
  tooltip,
  loading,
  footer,
  style,
  size = 'default',
}: Props) {
  const body = (
    <div style={{ display: 'flex', alignItems: 'flex-start', gap: 14 }}>
      {icon ? (
        <span
          className="yz-stat-icon"
          style={{
            background: tint(color, 0.12),
            color,
            width: size === 'small' ? 36 : 44,
            height: size === 'small' ? 36 : 44,
            fontSize: size === 'small' ? 16 : 20,
          }}
        >
          {icon}
        </span>
      ) : null}
      <div style={{ flex: 1, minWidth: 0 }}>
        <Typography.Text type="secondary" style={{ fontSize: 13 }}>
          {title}
        </Typography.Text>
        {loading ? (
          <Skeleton.Input active size="small" style={{ display: 'block', marginTop: 6, width: 100 }} />
        ) : (
          <div className="yz-stat-value" style={{ fontSize: size === 'small' ? 20 : 26 }}>
            {value}
            {suffix ? <span className="yz-stat-suffix">{suffix}</span> : null}
          </div>
        )}
        {hint ? (
          <Typography.Text type="secondary" style={{ fontSize: 12, display: 'block', marginTop: 2 }}>
            {hint}
          </Typography.Text>
        ) : null}
        {footer ? <div style={{ marginTop: 10 }}>{footer}</div> : null}
      </div>
    </div>
  );
  return (
    <Card className="yz-card" style={style} styles={{ body: { padding: size === 'small' ? 16 : 20 } }}>
      {tooltip ? <Tooltip title={tooltip}>{body}</Tooltip> : body}
    </Card>
  );
}
