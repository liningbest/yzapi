import { Alert, Button, Card, Skeleton, Space, Tabs, Typography } from 'antd';
import { SettingOutlined } from '@ant-design/icons';
import { Link, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { modelGroupsApi, settingsApi } from '@/api';
import { PageHeader } from '@/components';
import SamplesTab from './smart-route/SamplesTab';
import DecisionsTab from './smart-route/DecisionsTab';
import StatsTab from './smart-route/StatsTab';

const TABS = ['samples', 'decisions', 'stats'] as const;
type TabKey = (typeof TABS)[number];

export default function SmartRoute() {
  const { t } = useTranslation(['route', 'common']);
  const [searchParams, setSearchParams] = useSearchParams();
  const rawTab = searchParams.get('tab');
  const tab: TabKey = TABS.includes(rawTab as TabKey) ? (rawTab as TabKey) : 'samples';

  const setTab = (key: string) => {
    const next = new URLSearchParams(searchParams);
    next.set('tab', key);
    setSearchParams(next, { replace: true });
  };

  const settings = useQuery({ queryKey: ['settings'], queryFn: settingsApi.all });
  const smartRoute = settings.data?.smart_route;
  const enabled = Boolean(smartRoute?.enabled);

  const groups = useQuery({
    queryKey: ['model-groups', 'text'],
    queryFn: () => modelGroupsApi.list({ type: 'text', page_size: 200 }),
    enabled,
  });

  const groupName = (id: number | undefined) => {
    if (!id) return t('route:info.unset');
    const g = groups.data?.items.find((x) => x.id === id);
    return g ? g.name : `#${id}`;
  };

  let banner: React.ReactNode = null;
  if (settings.isLoading) {
    banner = <Skeleton.Input active size="small" style={{ width: 360 }} />;
  } else if (settings.data && !enabled) {
    banner = (
      <Alert
        type="warning"
        showIcon
        message={t('route:disabledAlert')}
        action={
          <Link to="/admin/settings?tab=smart_route">
            <Button size="small" icon={<SettingOutlined />}>
              {t('route:goSettings')}
            </Button>
          </Link>
        }
      />
    );
  } else if (smartRoute) {
    banner = (
      <Typography.Text type="secondary" style={{ fontSize: 13 }}>
        <Space size={16} wrap>
          <span>
            {t('route:info.virtualModel')}{' '}
            <Typography.Text code copyable>
              {smartRoute.virtual_model}
            </Typography.Text>
          </span>
          <span>
            {t('route:info.simpleGroup')}{' '}
            <Typography.Text strong>{groupName(smartRoute.simple_group_id)}</Typography.Text>
          </span>
          <span>
            {t('route:info.complexGroup')}{' '}
            <Typography.Text strong>{groupName(smartRoute.complex_group_id)}</Typography.Text>
          </span>
          <Link to="/admin/settings?tab=smart_route">{t('route:goSettings')}</Link>
        </Space>
      </Typography.Text>
    );
  }

  return (
    <div>
      <PageHeader title={t('route:title')} subtitle={t('route:subtitle')} />
      {banner ? <div style={{ marginBottom: 16 }}>{banner}</div> : null}
      <Card className="yz-card">
        <Tabs
          activeKey={tab}
          onChange={setTab}
          items={[
            { key: 'samples', label: t('route:tabs.samples'), children: <SamplesTab /> },
            { key: 'decisions', label: t('route:tabs.decisions'), children: <DecisionsTab /> },
            { key: 'stats', label: t('route:tabs.stats'), children: <StatsTab /> },
          ]}
        />
      </Card>
    </div>
  );
}
