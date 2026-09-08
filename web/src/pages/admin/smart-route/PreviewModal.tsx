import { useState } from 'react';
import { Button, Collapse, Descriptions, Input, Modal, Space, Typography } from 'antd';
import { ExperimentOutlined } from '@ant-design/icons';
import { useMutation } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { routeApi } from '@/api';
import { EmptyState, LabelTag, NeutralTag, SectionTitle } from '@/components';
import type { RoutePreview } from '@/types';
import { formatMs, formatPercent } from '@/utils/format';
import SourceTag from './SourceTag';
import TopKTable from './TopKTable';

interface Props {
  open: boolean;
  onClose: () => void;
}

export default function PreviewModal({ open, onClose }: Props) {
  const { t } = useTranslation(['route', 'common']);
  const [text, setText] = useState('');
  const preview = useMutation({ mutationFn: (v: string) => routeApi.preview(v) });
  const result: RoutePreview | undefined = preview.data;

  const handleClose = () => {
    onClose();
    setText('');
    preview.reset();
  };

  return (
    <Modal
      open={open}
      title={t('route:preview.title')}
      onCancel={handleClose}
      footer={null}
      width={820}
      destroyOnHidden
    >
      <div style={{ marginTop: 12 }}>
        <Input.TextArea
          rows={4}
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={t('route:preview.placeholder')}
          maxLength={65536}
        />
        <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 12 }}>
          <Button
            type="primary"
            icon={<ExperimentOutlined />}
            loading={preview.isPending}
            disabled={!text.trim()}
            onClick={() => preview.mutate(text.trim())}
          >
            {t('route:preview.run')}
          </Button>
        </div>
      </div>

      {result ? (
        <div style={{ marginTop: 8 }}>
          <SectionTitle>{t('route:preview.result')}</SectionTitle>
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 16,
              flexWrap: 'wrap',
              padding: '12px 14px',
              borderRadius: 6,
              border: '1px solid var(--yz-border)',
              marginBottom: 12,
            }}
          >
            <LabelTag label={result.label} />
            <SourceTag source={result.source} />
            <Typography.Text>
              {t('common:common.confidence')}{' '}
              <Typography.Text strong style={{ fontVariantNumeric: 'tabular-nums' }}>
                {formatPercent(result.confidence)}
              </Typography.Text>
            </Typography.Text>
            <Typography.Text type="secondary">
              {t('common:common.latency')} {formatMs(result.latency_ms)}
            </Typography.Text>
          </div>
          <Descriptions size="small" column={1} colon={false} labelStyle={{ width: 100 }}>
            <Descriptions.Item label={t('route:preview.group')}>
              {result.group_name || <Typography.Text type="secondary">-</Typography.Text>}
            </Descriptions.Item>
            <Descriptions.Item label={t('route:preview.models')}>
              {result.models?.length ? (
                <Space size={[4, 4]} wrap>
                  {result.models.map((m) => (
                    <NeutralTag key={m} mono>
                      {m}
                    </NeutralTag>
                  ))}
                </Space>
              ) : (
                <Typography.Text type="secondary">-</Typography.Text>
              )}
            </Descriptions.Item>
          </Descriptions>
          <Collapse
            ghost
            size="small"
            style={{ marginBottom: 8 }}
            items={[
              {
                key: 'normalized',
                label: t('route:preview.normalized'),
                children: <div className="yz-code-block">{result.normalized || '-'}</div>,
              },
            ]}
          />
          <SectionTitle>{t('route:preview.topK')}</SectionTitle>
          <TopKTable items={result.top_k} />
        </div>
      ) : (
        <EmptyState title={t('route:preview.empty')} style={{ padding: '24px 0 8px' }} />
      )}
    </Modal>
  );
}
