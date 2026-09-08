import { Button, Empty } from 'antd';
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

export default function EmptyState({ title, hint, actionText, onAction, image, style }: Props) {
  const { t } = useTranslation();
  return (
    <Empty
      image={image ?? Empty.PRESENTED_IMAGE_SIMPLE}
      style={{ padding: '32px 0', ...style }}
      description={
        <div>
          <div style={{ fontWeight: 500 }}>{title ?? t('common.noData')}</div>
          {hint ? <div style={{ color: 'var(--yz-text-secondary)', fontSize: 12, marginTop: 4 }}>{hint}</div> : null}
        </div>
      }
    >
      {onAction ? (
        <Button type="primary" onClick={onAction}>
          {actionText ?? t('action.add')}
        </Button>
      ) : null}
    </Empty>
  );
}
