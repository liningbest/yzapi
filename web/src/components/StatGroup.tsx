import { Children, type ReactNode } from 'react';

interface Props {
  children: ReactNode;
  style?: React.CSSProperties;
  className?: string;
}

/**
 * One bordered panel that lays stats out in equal columns separated by 1px dividers.
 * Wraps to 3 columns on narrow desktops and 2 on mobile.
 */
export default function StatGroup({ children, style, className }: Props) {
  const items = Children.toArray(children).filter(Boolean);
  return (
    <div className={`yz-stat-group${className ? ` ${className}` : ''}`} style={style}>
      {items.map((child, i) => (
        <div className="yz-stat-cell" key={i}>
          {child}
        </div>
      ))}
    </div>
  );
}
