import { Card, Tooltip, Typography } from 'antd';
import { AppstoreOutlined, ThunderboltOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { KindTag, ProviderAvatar, TypeTag } from '@/components';
import type { UserModel } from '@/types';

interface Props {
  model: UserModel;
}

const AVATAR_SIZE = 28;

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
        borderRadius: 4,
        border: '1px solid var(--yz-border)',
        background: 'var(--yz-track)',
        color: 'var(--yz-text-secondary)',
        fontSize: 14,
        flexShrink: 0,
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
    <Card hoverable className="yz-card" styles={{ body: { padding: 14 } }}>
      <div style={{ display: 'flex', gap: 10, alignItems: 'flex-start' }}>
        {model.kind === 'virtual' || model.kind === 'group' ? (
          <GatewayAvatar kind={model.kind} />
        ) : (
          <ProviderAvatar provider={model.provider} size={AVATAR_SIZE} />
        )}
        <div style={{ flex: 1, minWidth: 0 }}>
          <Typography.Text
            strong
            className="yz-mono"
            ellipsis={{ tooltip: model.name }}
            copyable={{ text: model.name, tooltips: [t('console:models.copyName'), t('common:action.copied')] }}
            style={{ maxWidth: '100%', fontSize: 13 }}
          >
            {model.name}
          </Typography.Text>
          <Typography.Text type="secondary" style={{ display: 'block', fontSize: 12, marginTop: 2 }}>
            {isGateway ? t('console:models.gateway') : model.provider}
          </Typography.Text>
        </div>
      </div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 12 }}>
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
