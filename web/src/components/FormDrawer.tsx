import { Button, Drawer, Space } from 'antd';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useIsMobile } from '@/hooks/useMediaQuery';

interface Props {
  open: boolean;
  title: ReactNode;
  onClose: () => void;
  onSubmit?: () => void;
  submitText?: ReactNode;
  submitting?: boolean;
  width?: number;
  children: ReactNode;
  /** Extra buttons placed left of Cancel/Save. */
  footerExtra?: ReactNode;
  destroyOnClose?: boolean;
  submitDisabled?: boolean;
}

/** Right-side drawer with a sticky footer for forms (width 560 by default). */
export default function FormDrawer({
  open,
  title,
  onClose,
  onSubmit,
  submitText,
  submitting,
  width = 560,
  children,
  footerExtra,
  destroyOnClose = true,
  submitDisabled,
}: Props) {
  const { t } = useTranslation();
  const mobile = useIsMobile();
  return (
    <Drawer
      open={open}
      title={title}
      onClose={onClose}
      width={mobile ? '100%' : width}
      destroyOnClose={destroyOnClose}
      maskClosable={false}
      styles={{ body: { paddingBottom: 24 } }}
      footer={
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 8 }}>
          <div>{footerExtra}</div>
          <Space>
            <Button onClick={onClose}>{t('action.cancel')}</Button>
            {onSubmit ? (
              <Button type="primary" onClick={onSubmit} loading={submitting} disabled={submitDisabled}>
                {submitText ?? t('action.save')}
              </Button>
            ) : null}
          </Space>
        </div>
      }
    >
      {children}
    </Drawer>
  );
}
