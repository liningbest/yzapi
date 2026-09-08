import { Alert, Card, Col, List, Progress, Row, Skeleton, Tag, Tooltip, Typography } from 'antd';
import { AppstoreOutlined, DashboardOutlined, KeyOutlined, PieChartOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { userApi } from '@/api';
import { PageHeader, StatCard, TokenText } from '@/components';
import type { MyGroup } from '@/types';
import { CHART_PALETTE, PRIMARY } from '@/utils/constants';
import { formatNumber, formatTokens } from '@/utils/format';

const MAX_VISIBLE_MODELS = 6;

function ModelTags({ models }: { models: string[] }) {
  const { t } = useTranslation(['console', 'common']);
  const visible = models.slice(0, MAX_VISIBLE_MODELS);
  const rest = models.slice(MAX_VISIBLE_MODELS);
  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
      {visible.map((m, i) => (
        <Tag key={`${i}-${m}`} bordered={false} className="yz-mono" style={{ marginInlineEnd: 0 }}>
          <span style={{ color: 'var(--yz-text-tertiary)', marginRight: 4 }}>{i + 1}</span>
          {m}
        </Tag>
      ))}
      {rest.length ? (
        <Tooltip title={<div className="yz-mono" style={{ wordBreak: 'break-all' }}>{rest.join(' → ')}</div>}>
          <Tag bordered={false} style={{ marginInlineEnd: 0, cursor: 'default' }}>
            +{rest.length}
          </Tag>
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
  const strokeColor = exceeded ? undefined : ratio >= 0.8 ? CHART_PALETTE[2] : PRIMARY;

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
        <TokenText value={used} style={{ fontSize: 34, fontWeight: 700, letterSpacing: -0.5, lineHeight: 1.1 }} />
        <Typography.Text type="secondary">{t('common:common.tokens')}</Typography.Text>
      </div>
      {quota > 0 ? (
        <div style={{ marginTop: 14 }}>
          <Progress
            percent={percent}
            status={exceeded ? 'exception' : 'normal'}
            strokeColor={strokeColor}
            showInfo
            format={() => `${percent.toFixed(1)}%`}
          />
          <Typography.Text type="secondary" style={{ fontSize: 12.5 }}>
            {t('console:group.usedOfQuota', { used: formatNumber(used), quota: formatNumber(quota) })}
          </Typography.Text>
        </div>
      ) : (
        <Typography.Text type="secondary" style={{ display: 'block', marginTop: 14, fontSize: 12.5 }}>
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

      <Card className="yz-card" style={{ marginBottom: 16 }} styles={{ body: { padding: '18px 24px' } }}>
        {loading ? (
          <Skeleton active paragraph={{ rows: 1 }} title={{ width: 160 }} />
        ) : (
          <>
            <Typography.Title level={4} style={{ margin: 0 }}>
              {group?.name || '-'}
            </Typography.Title>
            <Typography.Text type="secondary" style={{ fontSize: 12.5 }}>
              {t('console:group.hint')}
            </Typography.Text>
          </>
        )}
      </Card>

      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        <Col md={8} xs={24}>
          <StatCard
            title={t('console:group.maxConcurrency')}
            value={group && group.max_concurrency > 0 ? formatNumber(group.max_concurrency) : t('common:common.unlimited')}
            icon={<DashboardOutlined />}
            color={CHART_PALETTE[0]}
            loading={loading}
          />
        </Col>
        <Col md={8} xs={24}>
          <StatCard
            title={t('console:group.keyMaxConcurrency')}
            value={
              group && group.key_max_concurrency > 0
                ? formatNumber(group.key_max_concurrency)
                : t('console:group.inheritGroup')
            }
            icon={<KeyOutlined />}
            color={CHART_PALETTE[1]}
            loading={loading}
          />
        </Col>
        <Col md={8} xs={24}>
          <StatCard
            title={t('console:group.tokenQuota')}
            value={group && group.token_quota > 0 ? formatTokens(group.token_quota) : t('common:common.unlimited')}
            tooltip={group && group.token_quota > 0 ? formatNumber(group.token_quota) : undefined}
            hint={t('console:group.monthly')}
            icon={<PieChartOutlined />}
            color={CHART_PALETTE[5]}
            loading={loading}
          />
        </Col>
      </Row>

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
                            width: 36,
                            height: 36,
                            borderRadius: 10,
                            background: 'linear-gradient(135deg, #8b5cf6 0%, #ec4899 100%)',
                            color: '#fff',
                            fontSize: 17,
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
