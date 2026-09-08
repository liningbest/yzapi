import { Descriptions, Modal, Space, Typography } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { authApi, systemApi } from '@/api';
import { useAuthStore, isAdmin } from '@/stores/auth';
import { useThemeStore } from '@/stores/theme';
import { formatDateTime, formatDuration } from '@/utils/format';
import BrandMark from './BrandMark';

interface Props {
  open: boolean;
  onClose: () => void;
}

export default function AboutModal({ open, onClose }: Props) {
  const { t } = useTranslation();
  const user = useAuthStore((s) => s.user);
  const admin = isAdmin(user);
  const dark = useThemeStore((s) => s.mode) === 'dark';
  const info = useQuery({ queryKey: ['system', 'info'], queryFn: systemApi.info, enabled: open && admin });
  const pub = useQuery({ queryKey: ['public', 'info'], queryFn: authApi.publicInfo, enabled: open });

  return (
    <Modal open={open} onCancel={onClose} footer={null} title={null} width={480}>
      <div style={{ textAlign: 'center', padding: '12px 0 20px' }}>
        <div style={{ display: 'flex', justifyContent: 'center' }}>
          <BrandMark size={40} color={dark ? '#fafafa' : '#18181b'} stroke={dark ? '#18181b' : '#ffffff'} />
        </div>
        <Typography.Title level={5} style={{ margin: '12px 0 4px' }}>
          {pub.data?.site_name || t('app.name')}
        </Typography.Title>
        <Typography.Text type="secondary">{t('app.tagline')}</Typography.Text>
      </div>
      <Descriptions column={1} size="small" bordered>
        <Descriptions.Item label={t('about.version')}>
          {info.data?.version || pub.data?.version || '-'}
        </Descriptions.Item>
        {admin ? (
          <>
            <Descriptions.Item label={t('about.goVersion')}>{info.data?.go_version || '-'}</Descriptions.Item>
            <Descriptions.Item label={t('about.dbDriver')}>{info.data?.db_driver || '-'}</Descriptions.Item>
            <Descriptions.Item label={t('about.uptime')}>{formatDuration(info.data?.uptime_sec)}</Descriptions.Item>
            <Descriptions.Item label={t('about.startedAt')}>{formatDateTime(info.data?.started_at)}</Descriptions.Item>
            <Descriptions.Item label={t('about.dataDir')}>
              <Typography.Text code>{info.data?.data_dir || '-'}</Typography.Text>
            </Descriptions.Item>
          </>
        ) : null}
        <Descriptions.Item label={t('about.links')}>
          <Space>
            <a href="https://github.com/yzapi" target="_blank" rel="noreferrer">
              {t('about.repo')}
            </a>
          </Space>
        </Descriptions.Item>
      </Descriptions>
      <Typography.Paragraph type="secondary" style={{ textAlign: 'center', marginTop: 16, marginBottom: 0, fontSize: 12 }}>
        {t('about.copyright', { year: new Date().getFullYear() })}
      </Typography.Paragraph>
    </Modal>
  );
}
