import { useState } from 'react';
import { Alert, Button, Card, Tabs } from 'antd';
import { ExperimentOutlined, SettingOutlined } from '@ant-design/icons';
import { Link, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { complianceApi, settingsApi } from '@/api';
import { PageHeader } from '@/components';
import WordsTab from './compliance/WordsTab';
import SamplesTab from './compliance/SamplesTab';
import PolicyGroupsTab from './compliance/PolicyGroupsTab';
import AuditLogsTab from './compliance/AuditLogsTab';
import TestModal from './compliance/TestModal';
import { POLICY_GROUPS_KEY } from './compliance/keys';

const TABS = ['words', 'samples', 'groups', 'logs'] as const;
type TabKey = (typeof TABS)[number];

export default function Compliance() {
  const { t } = useTranslation(['compliance', 'common']);
  const [searchParams, setSearchParams] = useSearchParams();
  const rawTab = searchParams.get('tab');
  const tab: TabKey = TABS.includes(rawTab as TabKey) ? (rawTab as TabKey) : 'words';
  const [testOpen, setTestOpen] = useState(false);

  const setTab = (key: string) => {
    const next = new URLSearchParams(searchParams);
    next.set('tab', key);
    setSearchParams(next, { replace: true });
  };

  const settings = useQuery({ queryKey: ['settings'], queryFn: settingsApi.all });
  const disabled = Boolean(settings.data) && !settings.data?.compliance.enabled;

  const groupsQuery = useQuery({
    queryKey: POLICY_GROUPS_KEY,
    queryFn: () => complianceApi.policyGroups({ page_size: 200 }),
  });
  const groups = groupsQuery.data?.items ?? [];

  return (
    <div>
      <PageHeader
        title={t('compliance:title')}
        subtitle={t('compliance:subtitle')}
        extra={
          <Button icon={<ExperimentOutlined />} onClick={() => setTestOpen(true)}>
            {t('compliance:test')}
          </Button>
        }
      />
      {disabled ? (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 16 }}
          message={t('compliance:disabledAlert')}
          action={
            <Link to="/admin/settings?tab=compliance">
              <Button size="small" icon={<SettingOutlined />}>
                {t('compliance:goSettings')}
              </Button>
            </Link>
          }
        />
      ) : null}
      <Card className="yz-card">
        <Tabs
          activeKey={tab}
          onChange={setTab}
          items={[
            {
              key: 'words',
              label: t('compliance:tabs.words'),
              children: <WordsTab groups={groups} groupsLoading={groupsQuery.isLoading} />,
            },
            {
              key: 'samples',
              label: t('compliance:tabs.samples'),
              children: <SamplesTab groups={groups} groupsLoading={groupsQuery.isLoading} />,
            },
            {
              key: 'groups',
              label: t('compliance:tabs.groups'),
              children: <PolicyGroupsTab groups={groups} loading={groupsQuery.isLoading} />,
            },
            {
              key: 'logs',
              label: t('compliance:tabs.logs'),
              children: <AuditLogsTab groups={groups} groupsLoading={groupsQuery.isLoading} />,
            },
          ]}
        />
      </Card>
      <TestModal open={testOpen} onClose={() => setTestOpen(false)} />
    </div>
  );
}
