import { useState } from 'react';
import { Button, Input, Modal, Typography } from 'antd';
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
      ? { color: 'var(--yz-success)', icon: <CheckCircleOutlined />, text: t('compliance:testModal.pass') }
      : result.block
        ? { color: 'var(--yz-danger)', icon: <StopOutlined />, text: t('compliance:testModal.block') }
        : { color: 'var(--yz-warning)', icon: <WarningOutlined />, text: t('compliance:testModal.audit') }
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
              padding: '12px 14px',
              borderRadius: 6,
              border: '1px solid var(--yz-border)',
              marginBottom: 12,
            }}
          >
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontWeight: 600, color: verdict.color }}>
              {verdict.icon}
              {verdict.text}
            </span>
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
