import type { ReactNode } from 'react';

interface Props {
  children: ReactNode;
  extra?: ReactNode;
  style?: React.CSSProperties;
}

/** Compact toolbar: filters on the left (wrapping), actions on the right. */
export default function FilterBar({ children, extra, style }: Props) {
  return (
    <div className="yz-filter-bar" style={style}>
      <div className="yz-filter-bar-left">{children}</div>
      {extra ? <div className="yz-filter-bar-right">{extra}</div> : null}
    </div>
  );
}
