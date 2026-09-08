import { Button } from 'antd';
import { InboxOutlined } from '@ant-design/icons';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

interface Props {
  title?: ReactNode;
  hint?: ReactNode;
  actionText?: ReactNode;
  onAction?: () => void;
  image?: ReactNode;
  style?: React.CSSProperties;
}

/** Plain empty state: small gray icon, 13px text, one text button. */
export default function EmptyState({ title, hint, actionText, onAction, image, style }: Props) {
  const { t } = useTranslation();
  return (
    <div style={{ padding: '40px 0', textAlign: 'center', ...style }}>
      <div style={{ color: 'var(--yz-text-tertiary)', fontSize: 22, lineHeight: 1, marginBottom: 10 }}>
        {image ?? <InboxOutlined />}
      </div>
      <div style={{ fontSize: 13, color: 'var(--yz-text-secondary)' }}>{title ?? t('common.noData')}</div>
      {hint ? <div style={{ color: 'var(--yz-text-tertiary)', fontSize: 12, marginTop: 4 }}>{hint}</div> : null}
      {onAction ? (
        <Button type="link" size="small" onClick={onAction} style={{ marginTop: 8, fontSize: 13 }}>
          {actionText ?? t('action.add')}
        </Button>
      ) : null}
    </div>
  );
}
