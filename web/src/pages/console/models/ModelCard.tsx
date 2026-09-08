import { Card, Tooltip, Typography } from 'antd';
import { AppstoreOutlined, ThunderboltOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { KindTag, ProviderAvatar, TypeTag } from '@/components';
import type { UserModel } from '@/types';

interface Props {
  model: UserModel;
}

const AVATAR_SIZE = 36;

function GatewayAvatar({ kind }: { kind: 'virtual' | 'group' }) {
  const virtual = kind === 'virtual';
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        width: AVATAR_SIZE,
        height: AVATAR_SIZE,
        borderRadius: Math.round(AVATAR_SIZE * 0.3),
        background: virtual
          ? 'linear-gradient(135deg, #6366f1 0%, #06b6d4 100%)'
          : 'linear-gradient(135deg, #8b5cf6 0%, #ec4899 100%)',
        color: '#fff',
        fontSize: 18,
        flexShrink: 0,
        boxShadow: '0 1px 2px rgba(0,0,0,0.12)',
      }}
    >
      {virtual ? <ThunderboltOutlined /> : <AppstoreOutlined />}
    </span>
  );
}

export default function ModelCard({ model }: Props) {
  const { t } = useTranslation(['console', 'common']);
  const isGateway = model.kind === 'virtual' || model.kind === 'group';
  const groupModels = model.models ?? [];

  return (
    <Card hoverable className="yz-card" styles={{ body: { padding: 16 } }}>
      <div style={{ display: 'flex', gap: 12, alignItems: 'flex-start' }}>
        {model.kind === 'virtual' || model.kind === 'group' ? (
          <GatewayAvatar kind={model.kind} />
        ) : (
          <ProviderAvatar provider={model.provider} size={AVATAR_SIZE} />
        )}
        <div style={{ flex: 1, minWidth: 0 }}>
          <Typography.Text
            strong
            ellipsis={{ tooltip: model.name }}
            copyable={{ text: model.name, tooltips: [t('console:models.copyName'), t('common:action.copied')] }}
            style={{ maxWidth: '100%', fontSize: 14 }}
          >
            {model.name}
          </Typography.Text>
          <Typography.Text type="secondary" style={{ display: 'block', fontSize: 12, marginTop: 2 }}>
            {isGateway ? t('console:models.gateway') : model.provider}
          </Typography.Text>
        </div>
      </div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 14 }}>
        <TypeTag type={model.type} />
        {model.kind === 'virtual' ? <KindTag kind="virtual" /> : null}
        {model.kind === 'group' ? (
          <Tooltip
            title={
              groupModels.length ? (
                <div>
                  <div style={{ fontWeight: 600, marginBottom: 4 }}>{t('console:models.groupOrder')}</div>
                  <div className="yz-mono" style={{ wordBreak: 'break-all' }}>
                    {groupModels.join(' → ')}
                  </div>
                </div>
              ) : undefined
            }
          >
            <span style={{ display: 'inline-flex' }}>
              <KindTag kind="group" />
            </span>
          </Tooltip>
        ) : null}
      </div>
    </Card>
  );
}
