import { Space } from 'antd';
import type { ReactNode } from 'react';

interface Props {
  title: ReactNode;
  subtitle?: ReactNode;
  extra?: ReactNode;
  style?: React.CSSProperties;
}

export default function PageHeader({ title, subtitle, extra, style }: Props) {
  return (
    <div className="yz-page-header" style={style}>
      <div style={{ minWidth: 0 }}>
        <h1 className="yz-page-title">{title}</h1>
        {subtitle ? <span className="yz-page-subtitle">{subtitle}</span> : null}
      </div>
      {extra ? <Space wrap>{extra}</Space> : null}
    </div>
  );
}
