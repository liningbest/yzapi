import { useMemo } from 'react';
import { Button, Card, Col, Progress, Row, Space, Table, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  ApiOutlined,
  CheckCircleOutlined,
  CloseCircleOutlined,
  ClusterOutlined,
  DatabaseOutlined,
  HourglassOutlined,
  KeyOutlined,
  PercentageOutlined,
  ReloadOutlined,
  SendOutlined,
  SyncOutlined,
  TeamOutlined,
  ThunderboltOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import type { EChartsOption } from 'echarts';
import { overviewApi } from '@/api';
import {
  Chart,
  EmptyState,
  HealthTag,
  PageHeader,
  ProportionBar,
  ProviderAvatar,
  RangeSelector,
  SectionTitle,
  StatCard,
  TokenText,
  useChartTheme,
} from '@/components';
import { useRange } from '@/hooks/useRange';
import type { LiveAccount, LiveGroup } from '@/types';
import { CHART_PALETTE } from '@/utils/constants';
import { dayjs, formatNumber, formatPercent, formatTokens } from '@/utils/format';

const LIVE_INTERVAL = 5000;

/** util may come as 0..1 or 0..100; normalise to a percent number. */
function utilPercent(util: number): number {
  const v = util > 1 ? util : util * 100;
  return Math.max(0, Math.min(100, Math.round(v)));
}

function ratioPercent(current: number, limit: number): number {
  if (!limit) return 0;
  return Math.max(0, Math.min(100, Math.round((current / limit) * 100)));
}

function progressColor(percent: number): string {
  if (percent >= 90) return CHART_PALETTE[4];
  if (percent >= 70) return CHART_PALETTE[2];
  return CHART_PALETTE[0];
}

export default function Overview() {
  const { t } = useTranslation(['overview', 'common']);
  const theme = useChartTheme();
  const { range, setRange, params } = useRange('24h');

  const live = useQuery({
    queryKey: ['overview', 'live'],
    queryFn: overviewApi.live,
    refetchInterval: LIVE_INTERVAL,
  });
  const usage = useQuery({
    queryKey: ['overview', 'usage', params.range],
    queryFn: () => overviewApi.usage(params.range ?? '24h'),
  });

  const liveData = live.data;
  const usageData = usage.data;
  const unlimited = t('common:common.unlimited');

  const accountColumns: ColumnsType<LiveAccount> = [
    {
      title: t('common:common.account'),
      dataIndex: 'name',
      render: (_, r) => (
        <Space size={10}>
          <ProviderAvatar provider={r.provider} size={30} />
          <div style={{ lineHeight: 1.25 }}>
            <div style={{ fontWeight: 500 }}>{r.name}</div>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {r.provider}
            </Typography.Text>
          </div>
        </Space>
      ),
    },
    {
      title: t('common:common.priority'),
      dataIndex: 'priority',
      width: 80,
      align: 'center',
    },
    {
      title: t('common:common.concurrency'),
      key: 'concurrency',
      width: 110,
      render: (_, r) => (
        <span style={{ fontVariantNumeric: 'tabular-nums' }}>
          {r.current} / {r.limit ? r.limit : unlimited}
        </span>
      ),
    },
    {
      title: t('common:common.util'),
      dataIndex: 'util',
      width: 160,
      render: (util: number) => {
        const pct = utilPercent(util);
        return <Progress percent={pct} size="small" strokeColor={progressColor(pct)} />;
      },
    },
    {
      title: t('common:common.health'),
      dataIndex: 'health',
      width: 110,
      render: (_, r) => <HealthTag health={r.health} cooldownUntil={r.cooldown_until} lastError={r.last_error} />,
    },
  ];

  const groupColumns: ColumnsType<LiveGroup> = [
    {
      title: t('common:common.userGroup'),
      dataIndex: 'name',
      render: (name: string) => <span style={{ fontWeight: 500 }}>{name}</span>,
    },
    {
      title: t('common:common.concurrency'),
      key: 'concurrency',
      width: 110,
      render: (_, r) => (
        <span style={{ fontVariantNumeric: 'tabular-nums' }}>
          {r.current} / {r.limit ? r.limit : unlimited}
        </span>
      ),
    },
    {
      title: t('common:common.util'),
      dataIndex: 'util',
      width: 160,
      render: (util: number) => {
        const pct = utilPercent(util);
        return <Progress percent={pct} size="small" strokeColor={progressColor(pct)} />;
      },
    },
    {
      title: t('overview:groups.tokenUsage'),
      key: 'tokens',
      width: 220,
      render: (_, r) => (
        <div>
          <Tooltip title={`${formatNumber(r.tokens_used)} / ${r.token_quota ? formatNumber(r.token_quota) : unlimited}`}>
            <span style={{ fontVariantNumeric: 'tabular-nums' }}>
              {formatTokens(r.tokens_used)} / {r.token_quota ? formatTokens(r.token_quota) : unlimited}
            </span>
          </Tooltip>
          {r.token_quota ? (
            <ProportionBar value={r.tokens_used} total={r.token_quota} color={CHART_PALETTE[1]} width={100} />
          ) : null}
        </div>
      ),
    },
  ];

  const trend = usageData?.trend ?? [];
  const chartOption = useMemo<EChartsOption>(() => {
    const fmt = range === '24h' ? 'MM-DD HH:mm' : 'MM-DD';
    const [c0, c1, c2] = [CHART_PALETTE[0], CHART_PALETTE[1], CHART_PALETTE[2]];
    const area = (color: string) => ({
      color: {
        type: 'linear' as const,
        x: 0,
        y: 0,
        x2: 0,
        y2: 1,
        colorStops: [
          { offset: 0, color: `${color}55` },
          { offset: 1, color: `${color}08` },
        ],
      },
    });
    return {
      tooltip: { trigger: 'axis' },
      legend: { data: [t('common:common.promptTokens'), t('common:common.completionTokens'), t('common:common.requests')] },
      grid: { left: 12, right: 16, top: 40, bottom: 8, containLabel: true },
      xAxis: {
        type: 'category',
        boundaryGap: false,
        data: trend.map((p) => dayjs(p.time).format(fmt)),
      },
      yAxis: [
        {
          type: 'value',
          name: t('common:common.tokens'),
          nameTextStyle: { color: theme.textColor },
          axisLabel: { formatter: (v: number) => formatTokens(v, 0) },
        },
        {
          type: 'value',
          name: t('common:common.requests'),
          nameTextStyle: { color: theme.textColor },
          splitLine: { show: false },
          axisLabel: { formatter: (v: number) => formatTokens(v, 0) },
        },
      ],
      series: [
        {
          name: t('common:common.promptTokens'),
          type: 'line',
          stack: 'tokens',
          smooth: true,
          showSymbol: false,
          itemStyle: { color: c0 },
          lineStyle: { width: 2 },
          areaStyle: area(c0),
          data: trend.map((p) => p.prompt_tokens),
        },
        {
          name: t('common:common.completionTokens'),
          type: 'line',
          stack: 'tokens',
          smooth: true,
          showSymbol: false,
          itemStyle: { color: c1 },
          lineStyle: { width: 2 },
          areaStyle: area(c1),
          data: trend.map((p) => p.completion_tokens),
        },
        {
          name: t('common:common.requests'),
          type: 'line',
          yAxisIndex: 1,
          smooth: true,
          showSymbol: false,
          itemStyle: { color: c2 },
          lineStyle: { width: 2, type: 'dashed' },
          data: trend.map((p) => p.requests),
        },
      ],
    };
  }, [trend, range, t, theme.textColor]);

  const inflightPct = ratioPercent(liveData?.inflight ?? 0, liveData?.limit ?? 0);
  const waitingPct = ratioPercent(liveData?.waiting ?? 0, liveData?.queue_size ?? 0);

  return (
    <div>
      <PageHeader
        title={t('overview:title')}
        subtitle={t('overview:subtitle')}
        extra={
          <Space size={12}>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              <SyncOutlined spin={live.isFetching} style={{ marginRight: 6 }} />
              {t('overview:autoRefresh', { seconds: LIVE_INTERVAL / 1000 })}
            </Typography.Text>
            <Tooltip title={t('common:action.refresh')}>
              <Button
                icon={<ReloadOutlined />}
                onClick={() => {
                  void live.refetch();
                  void usage.refetch();
                }}
              />
            </Tooltip>
          </Space>
        }
      />

      <Row gutter={[16, 16]}>
        <Col xs={24} sm={12} xl={6}>
          <StatCard
            title={t('overview:live.activeUsers')}
            value={formatNumber(liveData?.active_users ?? 0)}
            icon={<UserOutlined />}
            color={CHART_PALETTE[0]}
            loading={live.isLoading}
            hint={t('overview:live.activeUsersHint')}
          />
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <StatCard
            title={t('overview:live.streams')}
            value={formatNumber(liveData?.streams ?? 0)}
            icon={<ThunderboltOutlined />}
            color={CHART_PALETTE[1]}
            loading={live.isLoading}
            hint={t('overview:live.streamsHint')}
          />
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <StatCard
            title={t('overview:live.inflight')}
            value={formatNumber(liveData?.inflight ?? 0)}
            suffix={`/ ${liveData?.limit ? formatNumber(liveData.limit) : unlimited}`}
            icon={<ClusterOutlined />}
            color={CHART_PALETTE[3]}
            loading={live.isLoading}
            footer={
              <Progress
                percent={inflightPct}
                size="small"
                showInfo={Boolean(liveData?.limit)}
                strokeColor={progressColor(inflightPct)}
              />
            }
          />
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <StatCard
            title={t('overview:live.waiting')}
            value={formatNumber(liveData?.waiting ?? 0)}
            suffix={`/ ${liveData?.queue_size ? formatNumber(liveData.queue_size) : unlimited}`}
            icon={<HourglassOutlined />}
            color={CHART_PALETTE[2]}
            loading={live.isLoading}
            footer={
              <Progress
                percent={waitingPct}
                size="small"
                showInfo={Boolean(liveData?.queue_size)}
                strokeColor={progressColor(waitingPct)}
              />
            }
          />
        </Col>
      </Row>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24} lg={12}>
          <Card className="yz-card" styles={{ body: { padding: 20 } }}>
            <SectionTitle
              extra={
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  {t('common:common.totalItems', { total: liveData?.accounts?.length ?? 0 })}
                </Typography.Text>
              }
            >
              {t('overview:accounts.title')}
            </SectionTitle>
            {liveData && liveData.accounts.length === 0 ? (
              <EmptyState title={t('overview:accounts.empty')} hint={t('overview:accounts.emptyHint')} />
            ) : (
              <Table<LiveAccount>
                className="yz-table"
                size="small"
                rowKey="id"
                columns={accountColumns}
                dataSource={liveData?.accounts ?? []}
                loading={live.isLoading}
                pagination={false}
                scroll={{ x: 'max-content' }}
              />
            )}
          </Card>
        </Col>
        <Col xs={24} lg={12}>
          <Card className="yz-card" styles={{ body: { padding: 20 } }}>
            <SectionTitle
              extra={
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  {t('common:common.totalItems', { total: liveData?.groups?.length ?? 0 })}
                </Typography.Text>
              }
            >
              {t('overview:groups.title')}
            </SectionTitle>
            {liveData && liveData.groups.length === 0 ? (
              <EmptyState title={t('overview:groups.empty')} hint={t('overview:groups.emptyHint')} />
            ) : (
              <Table<LiveGroup>
                className="yz-table"
                size="small"
                rowKey="id"
                columns={groupColumns}
                dataSource={liveData?.groups ?? []}
                loading={live.isLoading}
                pagination={false}
                scroll={{ x: 'max-content' }}
              />
            )}
          </Card>
        </Col>
      </Row>

      <Card className="yz-card" style={{ marginTop: 16 }} styles={{ body: { padding: 20 } }}>
        <SectionTitle extra={<RangeSelector value={range} onChange={setRange} size="small" />}>
          {t('overview:usage.title')}
        </SectionTitle>

        <Row gutter={[16, 16]}>
          <Col xs={24} sm={12} xl={4} xxl={4}>
            <StatCard
              size="small"
              title={t('common:common.totalTokens')}
              value={<TokenText value={usageData?.tokens.total} />}
              icon={<DatabaseOutlined />}
              color={CHART_PALETTE[0]}
              loading={usage.isLoading}
            />
          </Col>
          <Col xs={24} sm={12} xl={5}>
            <StatCard
              size="small"
              title={t('common:common.promptTokens')}
              value={<TokenText value={usageData?.tokens.prompt} />}
              icon={<SendOutlined />}
              color={CHART_PALETTE[1]}
              loading={usage.isLoading}
            />
          </Col>
          <Col xs={24} sm={12} xl={5}>
            <StatCard
              size="small"
              title={t('common:common.completionTokens')}
              value={<TokenText value={usageData?.tokens.completion} />}
              icon={<ApiOutlined />}
              color={CHART_PALETTE[5]}
              loading={usage.isLoading}
            />
          </Col>
          <Col xs={24} sm={12} xl={5}>
            <StatCard
              size="small"
              title={t('common:common.cachedTokens')}
              value={<TokenText value={usageData?.tokens.cached} />}
              icon={<DatabaseOutlined />}
              color={CHART_PALETTE[3]}
              loading={usage.isLoading}
            />
          </Col>
          <Col xs={24} sm={12} xl={5}>
            <StatCard
              size="small"
              title={t('overview:usage.cacheRate')}
              value={formatPercent(usageData?.tokens.cache_rate ?? 0)}
              icon={<PercentageOutlined />}
              color={CHART_PALETTE[2]}
              loading={usage.isLoading}
            />
          </Col>
        </Row>

        <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
          <Col xs={24} sm={12} xl={4}>
            <StatCard
              size="small"
              title={t('overview:usage.requests')}
              value={<TokenText value={usageData?.requests.total} />}
              icon={<ApiOutlined />}
              color={CHART_PALETTE[0]}
              loading={usage.isLoading}
            />
          </Col>
          <Col xs={24} sm={12} xl={4}>
            <StatCard
              size="small"
              title={t('overview:usage.success')}
              value={<TokenText value={usageData?.requests.success} />}
              icon={<CheckCircleOutlined />}
              color={CHART_PALETTE[3]}
              loading={usage.isLoading}
            />
          </Col>
          <Col xs={24} sm={12} xl={4}>
            <StatCard
              size="small"
              title={t('overview:usage.failed')}
              value={<TokenText value={usageData?.requests.failed} />}
              icon={<CloseCircleOutlined />}
              color={CHART_PALETTE[4]}
              loading={usage.isLoading}
            />
          </Col>
          <Col xs={24} sm={12} xl={4}>
            <StatCard
              size="small"
              title={t('overview:usage.failRate')}
              value={formatPercent(usageData?.requests.fail_rate ?? 0)}
              icon={<PercentageOutlined />}
              color={CHART_PALETTE[2]}
              loading={usage.isLoading}
            />
          </Col>
          <Col xs={24} sm={12} xl={4}>
            <StatCard
              size="small"
              title={t('overview:usage.activeUsers')}
              value={formatNumber(usageData?.active_users ?? 0)}
              icon={<TeamOutlined />}
              color={CHART_PALETTE[1]}
              loading={usage.isLoading}
            />
          </Col>
          <Col xs={24} sm={12} xl={4}>
            <StatCard
              size="small"
              title={t('overview:usage.activeKeys')}
              value={formatNumber(usageData?.active_keys ?? 0)}
              icon={<KeyOutlined />}
              color={CHART_PALETTE[5]}
              loading={usage.isLoading}
            />
          </Col>
        </Row>

        <SectionTitle style={{ marginTop: 24 }}>{t('overview:usage.trend')}</SectionTitle>
        {!usage.isLoading && trend.length === 0 ? (
          <EmptyState title={t('overview:usage.trendEmpty')} hint={t('overview:usage.trendEmptyHint')} />
        ) : (
          <Chart option={chartOption} height={320} loading={usage.isLoading} />
        )}
      </Card>
    </div>
  );
}
