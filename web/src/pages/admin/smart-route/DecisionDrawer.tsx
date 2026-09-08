import { Descriptions, Drawer, Typography } from 'antd';
import { useTranslation } from 'react-i18next';
import { LabelTag, SectionTitle, StatusDot, TimeCell, TokenText } from '@/components';
import type { RouteDecision } from '@/types';
import { formatMs, formatPercent } from '@/utils/format';
import SourceTag, { useRequestTypeText } from './SourceTag';
import TopKTable from './TopKTable';

interface Props {
  decision: RouteDecision | null;
  onClose: () => void;
}

export default function DecisionDrawer({ decision, onClose }: Props) {
  const { t } = useTranslation(['route', 'common']);
  const requestType = useRequestTypeText();
  const d = decision;

  return (
    <Drawer open={Boolean(d)} onClose={onClose} width={640} title={t('route:decisions.detail')} destroyOnHidden>
      {d ? (
        <div>
          <SectionTitle>{t('route:decisions.detail')}</SectionTitle>
          <Descriptions size="small" column={2} bordered labelStyle={{ width: 110 }}>
            <Descriptions.Item label={t('common:common.requestId')} span={2}>
              <Typography.Text className="yz-mono" copyable={{ text: d.request_id }}>
                {d.request_id || '-'}
              </Typography.Text>
            </Descriptions.Item>
            <Descriptions.Item label={t('common:common.time')} span={2}>
              <TimeCell value={d.created_at} absolute />
            </Descriptions.Item>
            <Descriptions.Item label={t('route:decisions.classification')}>
              <LabelTag label={d.label} />
            </Descriptions.Item>
            <Descriptions.Item label={t('common:common.source')}>
              <SourceTag source={d.source} />
            </Descriptions.Item>
            <Descriptions.Item label={t('common:common.confidence')}>{formatPercent(d.confidence)}</Descriptions.Item>
            <Descriptions.Item label={t('route:decisions.requestType')}>{requestType(d.request_type)}</Descriptions.Item>
            <Descriptions.Item label={t('route:decisions.selectedModel')}>
              <span className="yz-mono">{d.selected_model || '-'}</span>
            </Descriptions.Item>
            <Descriptions.Item label={t('common:common.modelGroup')}>{d.model_group || '-'}</Descriptions.Item>
            <Descriptions.Item label={t('common:common.tokens')}>
              <TokenText value={d.total_tokens} />
            </Descriptions.Item>
            <Descriptions.Item label={t('common:common.latency')}>{formatMs(d.latency_ms)}</Descriptions.Item>
            <Descriptions.Item label={t('route:decisions.failed')} span={2}>
              {d.failed ? (
                <StatusDot tone="danger">{t('route:decisions.failedTag')}</StatusDot>
              ) : (
                <StatusDot tone="success">{t('route:decisions.succeeded')}</StatusDot>
              )}
            </Descriptions.Item>
          </Descriptions>

          <SectionTitle>{t('route:decisions.normalized')}</SectionTitle>
          <div className="yz-code-block">{d.normalized_text || '-'}</div>

          <SectionTitle>{t('route:decisions.topK')}</SectionTitle>
          <TopKTable items={d.top_k} />
        </div>
      ) : null}
    </Drawer>
  );
}
