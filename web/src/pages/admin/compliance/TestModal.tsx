import { useState } from 'react';
import { Button, Input, Modal, Tag, Typography } from 'antd';
import { CheckCircleOutlined, ExperimentOutlined, StopOutlined, WarningOutlined } from '@ant-design/icons';
import { useMutation } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { complianceApi } from '@/api';
import { EmptyState, SectionTitle } from '@/components';
import HitsTable from './HitsTable';

interface Props {
  open: boolean;
  onClose: () => void;
}

export default function TestModal({ open, onClose }: Props) {
  const { t } = useTranslation(['compliance', 'common']);
  const [text, setText] = useState('');
  const test = useMutation({ mutationFn: (v: string) => complianceApi.test(v) });
  const result = test.data;

  const handleClose = () => {
    onClose();
    setText('');
    test.reset();
  };

  const verdict = result
    ? !result.hit
      ? { color: 'success', icon: <CheckCircleOutlined />, text: t('compliance:testModal.pass') }
      : result.block
        ? { color: 'error', icon: <StopOutlined />, text: t('compliance:testModal.block') }
        : { color: 'processing', icon: <WarningOutlined />, text: t('compliance:testModal.audit') }
    : null;

  return (
    <Modal open={open} title={t('compliance:testModal.title')} onCancel={handleClose} footer={null} width={820} destroyOnHidden>
      <div style={{ marginTop: 12 }}>
        <Input.TextArea
          rows={4}
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={t('compliance:testModal.placeholder')}
        />
        <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 12 }}>
          <Button
            type="primary"
            icon={<ExperimentOutlined />}
            loading={test.isPending}
            disabled={!text.trim()}
            onClick={() => test.mutate(text)}
          >
            {t('compliance:testModal.run')}
          </Button>
        </div>
      </div>

      {result && verdict ? (
        <div style={{ marginTop: 8 }}>
          <SectionTitle>{t('compliance:testModal.result')}</SectionTitle>
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 16,
              padding: '14px 16px',
              borderRadius: 10,
              background: 'var(--yz-track)',
              marginBottom: 12,
            }}
          >
            <Tag color={verdict.color} icon={verdict.icon} style={{ fontSize: 14, padding: '4px 12px', marginInlineEnd: 0 }}>
              {verdict.text}
            </Tag>
            <Typography.Text type="secondary">
              {t('compliance:testModal.hitCount', { count: result.hits?.length ?? 0 })}
            </Typography.Text>
          </div>
          <SectionTitle>{t('compliance:testModal.hitsTitle')}</SectionTitle>
          <HitsTable hits={result.hits} />
        </div>
      ) : (
        <EmptyState title={t('compliance:testModal.empty')} style={{ padding: '24px 0 8px' }} />
      )}
    </Modal>
  );
}
