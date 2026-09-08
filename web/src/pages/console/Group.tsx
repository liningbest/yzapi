import { Alert, Card, Col, List, Progress, Row, Skeleton, Tooltip, Typography } from 'antd';
import { AppstoreOutlined, DashboardOutlined, KeyOutlined, PieChartOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { userApi } from '@/api';
import { NeutralTag, PageHeader, StatCard, StatGroup, TokenText } from '@/components';
import type { MyGroup } from '@/types';
import { PRIMARY, SEMANTIC } from '@/utils/constants';
import { formatNumber, formatTokens } from '@/utils/format';

const MAX_VISIBLE_MODELS = 6;

function ModelTags({ models }: { models: string[] }) {
  const { t } = useTranslation(['console', 'common']);
  const visible = models.slice(0, MAX_VISIBLE_MODELS);
  const rest = models.slice(MAX_VISIBLE_MODELS);
  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
      {visible.map((m, i) => (
        <NeutralTag key={`${i}-${m}`} mono>
          <span style={{ color: 'var(--yz-text-tertiary)', marginRight: 4 }}>{i + 1}</span>
          {m}
        </NeutralTag>
      ))}
      {rest.length ? (
        <Tooltip title={<div className="yz-mono" style={{ wordBreak: 'break-all' }}>{rest.join(' → ')}</div>}>
          <NeutralTag style={{ cursor: 'default' }}>+{rest.length}</NeutralTag>
        </Tooltip>
      ) : null}
      {models.length === 0 ? <Typography.Text type="secondary">{t('common:common.none')}</Typography.Text> : null}
    </div>
  );
}

function MonthUsage({ group }: { group: MyGroup }) {
  const { t } = useTranslation(['console', 'common']);
  const used = group.tokens_used_month || 0;
  const quota = group.token_quota || 0;
  const ratio = quota > 0 ? used / quota : 0;
  const percent = Math.min(100, ratio * 100);
  const exceeded = quota > 0 && ratio >= 1;
  const strokeColor = exceeded ? SEMANTIC.danger : ratio >= 0.8 ? SEMANTIC.warning : PRIMARY;

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
        <TokenText value={used} style={{ fontSize: 22, fontWeight: 600, letterSpacing: -0.3, lineHeight: 1.2 }} />
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {t('common:common.tokens')}
        </Typography.Text>
      </div>
      {quota > 0 ? (
        <div style={{ marginTop: 12 }}>
          <Progress
            percent={percent}
            status={exceeded ? 'exception' : 'normal'}
            strokeColor={strokeColor}
            size={['100%', 4]}
            showInfo
            format={() => `${percent.toFixed(1)}%`}
          />
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {t('console:group.usedOfQuota', { used: formatNumber(used), quota: formatNumber(quota) })}
          </Typography.Text>
        </div>
      ) : (
        <Typography.Text type="secondary" style={{ display: 'block', marginTop: 12, fontSize: 12 }}>
          {t('console:group.noQuota')}
        </Typography.Text>
      )}
    </div>
  );
}

export default function Group() {
  const { t } = useTranslation(['console', 'common']);
  const query = useQuery({ queryKey: ['user', 'group'], queryFn: userApi.group });
  const group = query.data;
  const loading = query.isLoading;

  return (
    <div>
      <PageHeader title={t('console:group.title')} subtitle={t('console:group.subtitle')} />

      <Card className="yz-card" style={{ marginBottom: 16 }} styles={{ body: { padding: '14px 16px' } }}>
        {loading ? (
          <Skeleton active paragraph={{ rows: 1 }} title={{ width: 160 }} />
        ) : (
          <>
            <div style={{ fontSize: 15, fontWeight: 600, lineHeight: 1.3 }}>{group?.name || '-'}</div>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {t('console:group.hint')}
            </Typography.Text>
          </>
        )}
      </Card>

      <StatGroup>
        <StatCard
          title={t('console:group.maxConcurrency')}
          value={group && group.max_concurrency > 0 ? formatNumber(group.max_concurrency) : t('common:common.unlimited')}
          icon={<DashboardOutlined />}
          loading={loading}
        />
        <StatCard
          title={t('console:group.keyMaxConcurrency')}
          value={
            group && group.key_max_concurrency > 0 ? formatNumber(group.key_max_concurrency) : t('console:group.inheritGroup')
          }
          icon={<KeyOutlined />}
          loading={loading}
        />
        <StatCard
          title={t('console:group.tokenQuota')}
          value={group && group.token_quota > 0 ? formatTokens(group.token_quota) : t('common:common.unlimited')}
          tooltip={group && group.token_quota > 0 ? formatNumber(group.token_quota) : undefined}
          hint={t('console:group.monthly')}
          icon={<PieChartOutlined />}
          loading={loading}
        />
      </StatGroup>

      <Row gutter={[16, 16]}>
        <Col lg={9} xs={24}>
          <Card className="yz-card" title={t('console:group.monthUsage')} style={{ height: '100%' }}>
            {loading || !group ? <Skeleton active paragraph={{ rows: 2 }} /> : <MonthUsage group={group} />}
          </Card>
        </Col>
        <Col lg={15} xs={24}>
          <Card className="yz-card" title={t('console:group.modelGroups')} style={{ height: '100%' }}>
            {loading || !group ? (
              <Skeleton active paragraph={{ rows: 3 }} />
            ) : group.model_groups.length === 0 ? (
              <Alert type="info" showIcon message={t('console:group.allModels')} />
            ) : (
              <List
                dataSource={group.model_groups}
                split
                renderItem={(mg) => (
                  <List.Item key={mg.id} style={{ paddingInline: 0, alignItems: 'flex-start' }}>
                    <List.Item.Meta
                      avatar={
                        <span
                          style={{
                            display: 'inline-flex',
                            alignItems: 'center',
                            justifyContent: 'center',
                            width: 28,
                            height: 28,
                            borderRadius: 4,
                            border: '1px solid var(--yz-border)',
                            background: 'var(--yz-track)',
                            color: 'var(--yz-text-secondary)',
                            fontSize: 14,
                          }}
                        >
                          <AppstoreOutlined />
                        </span>
                      }
                      title={
                        <span style={{ display: 'inline-flex', alignItems: 'baseline', gap: 8 }}>
                          <Typography.Text strong>{mg.name}</Typography.Text>
                          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                            {t('console:group.modelsCount', { count: mg.models?.length ?? 0 })}
                          </Typography.Text>
                        </span>
                      }
                      description={<ModelTags models={mg.models ?? []} />}
                    />
                  </List.Item>
                )}
              />
            )}
            <Typography.Text type="secondary" style={{ display: 'block', marginTop: 12, fontSize: 12 }}>
              {t('console:group.dispatchHint')}
            </Typography.Text>
          </Card>
        </Col>
      </Row>
    </div>
  );
}
