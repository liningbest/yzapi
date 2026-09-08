import { useMemo } from 'react';
import { Alert, App, Button, Modal, Space, Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { CopyOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { NeutralTag, SectionTitle } from '@/components';

interface Props {
  open: boolean;
  onClose: () => void;
  baseUrl: string;
  /** First text model name, used in the curl example. */
  exampleModel?: string;
}

type Method = 'GET' | 'POST';

interface Endpoint {
  key: string;
  method: Method;
  path: string;
}

const ENDPOINTS: Endpoint[] = [
  { key: 'models', method: 'GET', path: '/v1/models' },
  { key: 'chat', method: 'POST', path: '/v1/chat/completions' },
  { key: 'responses', method: 'POST', path: '/v1/responses' },
  { key: 'messages', method: 'POST', path: '/v1/messages' },
  { key: 'embeddings', method: 'POST', path: '/v1/embeddings' },
  { key: 'images', method: 'POST', path: '/v1/images/generations' },
];

/** Strip a trailing `/v1` (and slash) so endpoint paths can be appended. */
export function apiRoot(baseUrl: string): string {
  return (baseUrl || '').trim().replace(/\/+$/, '').replace(/\/v1$/, '');
}

export default function ApiDocModal({ open, onClose, baseUrl, exampleModel }: Props) {
  const { t } = useTranslation(['console', 'common']);
  const { message } = App.useApp();
  const root = apiRoot(baseUrl);

  const curl = useMemo(() => {
    const model = exampleModel || 'gpt-4o';
    return [
      `curl ${root}/v1/chat/completions \\`,
      `  -H "Content-Type: application/json" \\`,
      `  -H "Authorization: Bearer $YZ_API_KEY" \\`,
      `  -d '{`,
      `    "model": "${model}",`,
      `    "messages": [{"role": "user", "content": "Hello"}]`,
      `  }'`,
    ].join('\n');
  }, [root, exampleModel]);

  const copyCurl = () => {
    void navigator.clipboard.writeText(curl).then(() => message.success(t('common:action.copied')));
  };

  const columns: ColumnsType<Endpoint> = [
    {
      title: t('console:apiDoc.method'),
      dataIndex: 'method',
      width: 80,
      render: (m: Method) => (
        <NeutralTag mono style={{ fontWeight: 600 }}>
          {m}
        </NeutralTag>
      ),
    },
    {
      title: t('console:apiDoc.path'),
      dataIndex: 'path',
      render: (p: string) => (
        <Typography.Text code copyable className="yz-mono" style={{ fontSize: 12.5 }}>
          {`${root}${p}`}
        </Typography.Text>
      ),
    },
    {
      title: t('console:apiDoc.purpose'),
      dataIndex: 'key',
      width: 220,
      render: (k: string) => t(`console:apiDoc.ep.${k}`),
    },
  ];

  return (
    <Modal open={open} onCancel={onClose} footer={null} width={760} title={t('console:apiDoc.title')} destroyOnClose>
      <SectionTitle>{t('console:apiDoc.auth')}</SectionTitle>
      <Alert
        type="info"
        showIcon
        message={
          <ul style={{ margin: 0, paddingLeft: 18 }}>
            <li>{t('console:apiDoc.authBearer')}</li>
            <li>{t('console:apiDoc.authXApiKey')}</li>
            <li>{t('console:apiDoc.authNoSpace')}</li>
          </ul>
        }
      />

      <SectionTitle>{t('console:apiDoc.endpoints')}</SectionTitle>
      <Table
        className="yz-table"
        size="small"
        rowKey="key"
        columns={columns}
        dataSource={ENDPOINTS}
        pagination={false}
        scroll={{ x: 'max-content' }}
      />

      <SectionTitle
        extra={
          <Button size="small" icon={<CopyOutlined />} onClick={copyCurl}>
            {t('console:apiDoc.copyExample')}
          </Button>
        }
      >
        {t('console:apiDoc.example')}
      </SectionTitle>
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <Typography.Text type="secondary" style={{ fontSize: 12.5 }}>
          {t('console:apiDoc.exampleHint')}
        </Typography.Text>
        <pre className="yz-code-block" style={{ margin: 0 }}>
          {curl}
        </pre>
        <Alert type="warning" showIcon message={t('console:apiDoc.modelNote')} />
      </Space>
    </Modal>
  );
}
