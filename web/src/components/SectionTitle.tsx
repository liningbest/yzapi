import type { ReactNode } from 'react';

interface Props {
  children: ReactNode;
  extra?: ReactNode;
  style?: React.CSSProperties;
}

/** Small section heading used inside drawers / cards. */
export default function SectionTitle({ children, extra, style }: Props) {
  return (
    <div className="yz-section-title" style={style}>
      <span>{children}</span>
      {extra ? <span style={{ fontWeight: 400 }}>{extra}</span> : null}
    </div>
  );
}
