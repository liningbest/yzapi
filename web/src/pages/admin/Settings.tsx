import { useEffect, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Card, Skeleton, Tabs } from 'antd';
import {
  ApiOutlined,
  BranchesOutlined,
  DatabaseOutlined,
  DashboardOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  HistoryOutlined,
} from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { settingsApi } from '@/api';
import { PageHeader } from '@/components';
import BasicTab from './settings/BasicTab';
import ComplianceTab from './settings/ComplianceTab';
import ElasticsearchTab from './settings/ElasticsearchTab';
import PerformanceTab from './settings/PerformanceTab';
import { SETTINGS_KEY } from './settings/shared';
import SmartRouteTab from './settings/SmartRouteTab';
import VectorTab from './settings/VectorTab';
import PricingTab from './settings/PricingTab';
import ConfigTab from './settings/ConfigTab';
import BackupTab from './settings/BackupTab';

export default function Settings() {
  const { t } = useTranslation(['settings', 'common']);
  const [params, setParams] = useSearchParams();
  const VALID = ['basic', 'performance', 'vector', 'smart_route', 'compliance', 'pricing', 'config', 'elasticsearch'];
  const initial = params.get('tab');
  const [tab, setTabState] = useState(initial && VALID.includes(initial) ? initial : 'basic');
  const setTab = (k: string) => {
    setTabState(k);
    setParams({ tab: k }, { replace: true });
  };
  useEffect(() => {
    const q = params.get('tab');
    if (q && VALID.includes(q) && q !== tab) setTabState(q);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [params]);
  const settings = useQuery({ queryKey: SETTINGS_KEY, queryFn: settingsApi.all });
  const data = settings.data;

  const items = [
    {
      key: 'basic',
      label: t('settings:tabs.basic'),
      icon: <SettingOutlined />,
      children: <BasicTab data={data?.basic} />,
    },
    {
      key: 'performance',
      label: t('settings:tabs.performance'),
      icon: <DashboardOutlined />,
      children: <PerformanceTab data={data?.performance} />,
    },
    {
      key: 'vector',
      label: t('settings:tabs.vector'),
      icon: <ApiOutlined />,
      children: <VectorTab data={data?.vector} />,
    },
    {
      key: 'smart_route',
      label: t('settings:tabs.smartRoute'),
      icon: <BranchesOutlined />,
      children: <SmartRouteTab data={data?.smart_route} />,
    },
    {
      key: 'compliance',
      label: t('settings:tabs.compliance'),
      icon: <SafetyCertificateOutlined />,
      children: <ComplianceTab data={data?.compliance} />,
    },
    {
      key: 'pricing',
      label: t('settings:tabs.pricing'),
      icon: <DatabaseOutlined />,
      children: <PricingTab data={data?.pricing} />,
    },
    {
      key: 'config',
      label: t('settings:tabs.config'),
      icon: <HistoryOutlined />,
      children: <ConfigTab />,
    },
    {
      key: 'backup',
      label: t('settings:tabs.backup'),
      icon: <HistoryOutlined />,
      children: <BackupTab data={data?.backup} />,
    },
    {
      key: 'elasticsearch',
      label: t('settings:tabs.elasticsearch'),
      icon: <DatabaseOutlined />,
      children: <ElasticsearchTab data={data?.elasticsearch} />,
    },
  ];

  return (
    <div>
      <PageHeader title={t('settings:title')} subtitle={t('settings:subtitle')} />
      <Card className="yz-card" styles={{ body: { padding: '8px 24px 24px' } }}>
        {settings.isLoading ? (
          <Skeleton active paragraph={{ rows: 8 }} style={{ paddingTop: 16 }} />
        ) : (
          <Tabs activeKey={tab} onChange={setTab} items={items} size="large" />
        )}
      </Card>
    </div>
  );
}
