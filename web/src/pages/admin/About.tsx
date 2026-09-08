import { Card, Col, Descriptions, Row, Space, Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { BookOutlined, GithubOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { settingsApi, systemApi } from '@/api';
import { BrandMark, NeutralTag, PageHeader, SectionTitle, TimeCell } from '@/components';
import { useThemeStore } from '@/stores/theme';
import { formatDuration } from '@/utils/format';

const REPO_URL = 'https://github.com/yzapi';
const DOCS_URL = 'https://github.com/yzapi#readme';

interface EndpointRow {
  key: string;
  method: 'GET' | 'POST';
  path: string;
}

const ENDPOINTS: EndpointRow[] = [
  { key: 'models', method: 'GET', path: '/v1/models' },
  { key: 'chat', method: 'POST', path: '/v1/chat/completions' },
  { key: 'responses', method: 'POST', path: '/v1/responses' },
  { key: 'messages', method: 'POST', path: '/v1/messages' },
  { key: 'embeddings', method: 'POST', path: '/v1/embeddings' },
  { key: 'images', method: 'POST', path: '/v1/images/generations' },
];

export default function About() {
  const { t } = useTranslation(['about', 'common']);
  const dark = useThemeStore((s) => s.mode) === 'dark';
  const info = useQuery({ queryKey: ['system', 'info'], queryFn: systemApi.info });
  const settings = useQuery({ queryKey: ['settings'], queryFn: settingsApi.all });

  const baseUrl = settings.data?.basic.base_url ?? '';
  const siteName = settings.data?.basic.site_name || t('common:app.name');
  const origin = baseUrl.replace(/\/v1\/?$/, '');

  const columns: ColumnsType<EndpointRow> = [
    {
      title: t('about:endpoints.method'),
      dataIndex: 'method',
      width: 90,
      render: (m: EndpointRow['method']) => (
        <NeutralTag mono>{m}</NeutralTag>
      ),
    },
    {
      title: t('about:endpoints.path'),
      dataIndex: 'path',
      render: (p: string) => (
        <Typography.Text className="yz-mono" copyable={{ text: `${origin}${p}` }}>
          {p}
        </Typography.Text>
      ),
    },
    {
      title: t('about:endpoints.purpose'),
      key: 'purpose',
      render: (_, r) => t(`about:endpoints.${r.key}`),
    },
  ];

  return (
    <div>
      <PageHeader title={t('about:title')} subtitle={t('about:subtitle')} />
      <Row gutter={[16, 16]}>
        <Col xs={24} xl={10}>
          <Card className="yz-card" styles={{ body: { padding: 20 } }} loading={info.isLoading}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 16 }}>
              <BrandMark size={40} color={dark ? '#fafafa' : '#18181b'} stroke={dark ? '#18181b' : '#ffffff'} />
              <div>
                <Typography.Title level={5} style={{ margin: 0 }}>
                  {siteName}
                </Typography.Title>
                <Typography.Text type="secondary">{t('common:app.tagline')}</Typography.Text>
              </div>
            </div>
            <Descriptions column={1} size="small" bordered>
              <Descriptions.Item label={t('common:about.version')}>
                <Typography.Text className="yz-mono">{info.data?.version || '-'}</Typography.Text>
              </Descriptions.Item>
              <Descriptions.Item label={t('common:about.goVersion')}>
                <Typography.Text className="yz-mono">{info.data?.go_version || '-'}</Typography.Text>
              </Descriptions.Item>
              <Descriptions.Item label={t('common:about.dbDriver')}>{info.data?.db_driver || '-'}</Descriptions.Item>
              <Descriptions.Item label={t('common:about.uptime')}>{formatDuration(info.data?.uptime_sec)}</Descriptions.Item>
              <Descriptions.Item label={t('common:about.startedAt')}>
                <TimeCell value={info.data?.started_at} absolute />
              </Descriptions.Item>
              <Descriptions.Item label={t('common:about.dataDir')}>
                <Typography.Text className="yz-mono" copyable={Boolean(info.data?.data_dir)}>
                  {info.data?.data_dir || '-'}
                </Typography.Text>
              </Descriptions.Item>
            </Descriptions>
            <Space size={16} style={{ marginTop: 20 }} wrap>
              <a href={DOCS_URL} target="_blank" rel="noreferrer">
                <BookOutlined style={{ marginRight: 6 }} />
                {t('common:about.docs')}
              </a>
              <a href={REPO_URL} target="_blank" rel="noreferrer">
                <GithubOutlined style={{ marginRight: 6 }} />
                {t('common:about.repo')}
              </a>
            </Space>
            <Typography.Paragraph type="secondary" style={{ marginTop: 16, marginBottom: 0, fontSize: 12 }}>
              {t('common:about.copyright', { year: new Date().getFullYear() })}
            </Typography.Paragraph>
          </Card>
        </Col>
        <Col xs={24} xl={14}>
          <Card className="yz-card" styles={{ body: { padding: 20 } }}>
            <SectionTitle>{t('about:access.title')}</SectionTitle>
            <Typography.Paragraph type="secondary" style={{ marginBottom: 12 }}>
              {t('about:access.hint')}
            </Typography.Paragraph>
            <div className="yz-code-block" style={{ marginBottom: 20, display: 'flex', alignItems: 'center', gap: 8 }}>
              <Typography.Text className="yz-mono" copyable={Boolean(baseUrl)} style={{ fontSize: 13 }}>
                {settings.isLoading ? t('common:common.loading') : baseUrl || '-'}
              </Typography.Text>
            </div>
            <SectionTitle>{t('about:endpoints.title')}</SectionTitle>
            <Table<EndpointRow>
              className="yz-table"
              size="small"
              rowKey="key"
              columns={columns}
              dataSource={ENDPOINTS}
              pagination={false}
              scroll={{ x: 'max-content' }}
            />
            <Typography.Paragraph type="secondary" style={{ marginTop: 16, marginBottom: 0, fontSize: 12 }}>
              {t('about:endpoints.authHint')}
            </Typography.Paragraph>
          </Card>
        </Col>
      </Row>
    </div>
  );
}
