import { Skeleton, Tooltip } from 'antd';
import type { ReactNode } from 'react';

interface Props {
  title: ReactNode;
  value: ReactNode;
  /** Optional glyph rendered as a small secondary-colored icon before the label. */
  icon?: ReactNode;
  /** Kept for API compatibility; no longer paints a tinted bubble. */
  color?: string;
  suffix?: ReactNode;
  hint?: ReactNode;
  tooltip?: ReactNode;
  loading?: boolean;
  footer?: ReactNode;
  style?: React.CSSProperties;
  size?: 'default' | 'small';
  /** Render without its own border (used automatically inside StatGroup). */
  bare?: boolean;
}

/** Compact stat: label on top, tabular value, tertiary hint. */
export default function StatCard({
  title,
  value,
  icon,
  suffix,
  hint,
  tooltip,
  loading,
  footer,
  style,
  size = 'default',
  bare,
}: Props) {
  const body = (
    <div className="yz-stat">
      <div className="yz-stat-label">
        {icon ? <span style={{ display: 'inline-flex', lineHeight: 0 }}>{icon}</span> : null}
        <span style={{ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis' }}>{title}</span>
      </div>
      {loading ? (
        <Skeleton.Input active size="small" style={{ display: 'block', width: 88, height: 22 }} />
      ) : (
        <div className={`yz-stat-value${size === 'small' ? ' small' : ''}`}>
          {value}
          {suffix ? <span className="yz-stat-suffix">{suffix}</span> : null}
        </div>
      )}
      {hint ? <div className="yz-stat-hint">{hint}</div> : null}
      {footer ? <div style={{ marginTop: 8 }}>{footer}</div> : null}
    </div>
  );
  const node = (
    <div className={bare ? undefined : 'yz-stat-card'} style={style}>
      {body}
    </div>
  );
  return tooltip ? <Tooltip title={tooltip}>{node}</Tooltip> : node;
}
