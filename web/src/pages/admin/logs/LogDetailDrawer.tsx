import { Descriptions, Drawer, Space, Timeline, Typography } from 'antd';
import { useTranslation } from 'react-i18next';
import { NeutralTag, ProtocolTag, ProviderAvatar, ResultTag, SectionTitle, StatusCodeTag, TypeTag } from '@/components';
import { useIsMobile } from '@/hooks/useMediaQuery';
import type { CallLog, LogAttempt } from '@/types';
import { formatDateTime, formatMs, formatNumber } from '@/utils/format';

interface Props {
  open: boolean;
  log: CallLog | null;
  onClose: () => void;
}

function Secondary({ children }: { children: React.ReactNode }) {
  return <Typography.Text type="secondary">{children}</Typography.Text>;
}

export default function LogDetailDrawer({ open, log, onClose }: Props) {
  const { t } = useTranslation(['logs', 'common']);
  const mobile = useIsMobile();
  const dash = '-';

  const tokensNode = (v: number) =>
    log?.tokens_known === false ? <Secondary>{t('common:common.unknown')}</Secondary> : formatNumber(v);

  const attempts: LogAttempt[] = log?.attempts ?? [];

  return (
    <Drawer
      open={open}
      onClose={onClose}
      width={mobile ? '100%' : 640}
      destroyOnClose
      title={
        <Space size={8}>
          <span>{t('logs:detail.title')}</span>
          {log ? <ResultTag result={log.result} /> : null}
        </Space>
      }
    >
      {log ? (
        <div>
          <SectionTitle>{t('logs:detail.basic')}</SectionTitle>
          <Descriptions bordered size="small" column={mobile ? 1 : 2}>
            <Descriptions.Item label={t('common:common.requestId')} span={2}>
              <Typography.Text className="yz-mono" copyable={{ text: log.request_id }}>
                {log.request_id || dash}
              </Typography.Text>
            </Descriptions.Item>
            <Descriptions.Item label={t('common:common.time')}>{formatDateTime(log.created_at)}</Descriptions.Item>
            <Descriptions.Item label={t('logs:detail.clientIp')}>
              <span className="yz-mono">{log.client_ip || dash}</span>
            </Descriptions.Item>
            <Descriptions.Item label={t('common:common.user')}>{log.username || dash}</Descriptions.Item>
            <Descriptions.Item label={t('common:common.userGroup')}>{log.group_name || dash}</Descriptions.Item>
            <Descriptions.Item label={t('common:common.apiKey')} span={2}>
              {log.api_key_name || dash}
            </Descriptions.Item>
          </Descriptions>

          <SectionTitle>{t('logs:detail.routing')}</SectionTitle>
          <Descriptions bordered size="small" column={mobile ? 1 : 2}>
            <Descriptions.Item label={t('common:common.provider')}>
              {log.provider ? (
                <Space size={6}>
                  <ProviderAvatar provider={log.provider} size={18} />
                  <span>{log.provider}</span>
                </Space>
              ) : (
                dash
              )}
            </Descriptions.Item>
            <Descriptions.Item label={t('common:common.account')}>{log.account_name || dash}</Descriptions.Item>
            <Descriptions.Item label={t('common:common.requestModel')}>{log.request_model || dash}</Descriptions.Item>
            <Descriptions.Item label={t('common:common.upstreamModel')}>{log.upstream_model || dash}</Descriptions.Item>
            <Descriptions.Item label={t('common:common.modelGroup')}>{log.model_group || dash}</Descriptions.Item>
            <Descriptions.Item label={t('common:common.apiType')}>
              {log.api_type ? <TypeTag type={log.api_type} /> : dash}
            </Descriptions.Item>
            <Descriptions.Item label={t('logs:detail.clientProtocol')}>
              {log.client_protocol ? <ProtocolTag protocol={log.client_protocol} /> : dash}
            </Descriptions.Item>
            <Descriptions.Item label={t('logs:detail.upstreamProtocol')}>
              {log.upstream_protocol ? <ProtocolTag protocol={log.upstream_protocol} /> : dash}
            </Descriptions.Item>
            <Descriptions.Item label={t('common:common.stream')}>
              {log.stream ? t('common:common.yes') : t('common:common.no')}
            </Descriptions.Item>
            <Descriptions.Item label={t('logs:detail.routeLabel')}>
              {log.route_label ? <NeutralTag>{log.route_label}</NeutralTag> : dash}
            </Descriptions.Item>
          </Descriptions>

          <SectionTitle>{t('logs:detail.performance')}</SectionTitle>
          <Descriptions bordered size="small" column={mobile ? 1 : 2}>
            <Descriptions.Item label={t('common:common.promptTokens')}>{tokensNode(log.prompt_tokens)}</Descriptions.Item>
            <Descriptions.Item label={t('common:common.completionTokens')}>
              {tokensNode(log.completion_tokens)}
            </Descriptions.Item>
            <Descriptions.Item label={t('common:common.totalTokens')}>{tokensNode(log.total_tokens)}</Descriptions.Item>
            <Descriptions.Item label={t('common:common.cachedTokens')}>{tokensNode(log.cached_tokens)}</Descriptions.Item>
            <Descriptions.Item label={t('common:common.result')}>
              <ResultTag result={log.result} />
            </Descriptions.Item>
            <Descriptions.Item label={t('common:common.statusCode')}>
              <StatusCodeTag code={log.status_code} />
            </Descriptions.Item>
            <Descriptions.Item label={t('common:common.totalLatency')}>{formatMs(log.latency_ms)}</Descriptions.Item>
            <Descriptions.Item label={t('common:common.upstreamLatency')}>{formatMs(log.upstream_latency_ms)}</Descriptions.Item>
            <Descriptions.Item label={t('logs:detail.firstByte')} span={2}>
              {formatMs(log.first_byte_ms)}
            </Descriptions.Item>
          </Descriptions>

          <SectionTitle extra={<Secondary>{t('logs:detail.attemptsCount', { count: attempts.length })}</Secondary>}>
            {t('logs:detail.attempts')}
          </SectionTitle>
          {attempts.length ? (
            <Timeline
              style={{ marginTop: 8 }}
              items={attempts.map((a, i) => ({
                key: `${a.account_id}-${i}`,
                color: a.status_code && a.status_code < 400 ? 'green' : 'red',
                children: (
                  <div>
                    <Space size={6} wrap>
                      <ProviderAvatar provider={a.provider} size={18} />
                      <Typography.Text strong>{a.account_name || `#${a.account_id}`}</Typography.Text>
                      <Secondary>({a.provider || dash})</Secondary>
                      <Secondary>·</Secondary>
                      <span>{a.model || dash}</span>
                      {a.protocol ? (
                        <>
                          <Secondary>·</Secondary>
                          <ProtocolTag protocol={a.protocol} />
                        </>
                      ) : null}
                    </Space>
                    <div style={{ marginTop: 4, fontSize: 12 }}>
                      <Space size={8} wrap>
                        <StatusCodeTag code={a.status_code} />
                        <Secondary>{formatMs(a.latency_ms)}</Secondary>
                        {a.error ? (
                          <Typography.Text type="danger" style={{ fontSize: 12, wordBreak: 'break-all' }}>
                            {a.error}
                          </Typography.Text>
                        ) : null}
                      </Space>
                    </div>
                  </div>
                ),
              }))}
            />
          ) : (
            <Secondary>{t('logs:detail.noAttempts')}</Secondary>
          )}

          {log.error ? (
            <>
              <SectionTitle>{t('common:common.error')}</SectionTitle>
              <div className="yz-code-block">{log.error}</div>
            </>
          ) : null}
        </div>
      ) : null}
    </Drawer>
  );
}
