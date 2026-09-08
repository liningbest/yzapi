import { Typography } from 'antd';
import { useTranslation } from 'react-i18next';
import { ProviderAvatar } from '@/components';
import type { Provider } from '@/types';

interface Props {
  providers: Provider[];
  value?: string;
  onChange?: (key: string) => void;
  /** Provider keys that cannot be chosen (e.g. do not support the locked type on edit). */
  disabledKeys?: string[];
}

/** Grid of clickable provider cards; works as a controlled Form.Item child. */
export default function ProviderPicker({ providers, value, onChange, disabledKeys = [] }: Props) {
  const { t } = useTranslation(['accounts', 'common']);
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(150px, 1fr))', gap: 10 }}>
      {providers.map((p) => {
        const selected = p.key === value;
        const disabled = disabledKeys.includes(p.key) && !selected;
        const pick = () => {
          if (!disabled) onChange?.(p.key);
        };
        return (
          <div
            key={p.key}
            role="radio"
            aria-checked={selected}
            aria-disabled={disabled}
            tabIndex={disabled ? -1 : 0}
            onClick={pick}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                pick();
              }
            }}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 10,
              padding: '10px 12px',
              borderRadius: 10,
              border: `1.5px solid ${selected ? 'var(--yz-primary)' : 'var(--yz-border)'}`,
              boxShadow: selected ? '0 0 0 3px rgba(79, 70, 229, 0.12)' : undefined,
              background: selected ? 'rgba(79, 70, 229, 0.05)' : 'transparent',
              cursor: disabled ? 'not-allowed' : 'pointer',
              opacity: disabled ? 0.45 : 1,
              transition: 'border-color .15s, box-shadow .15s, background .15s',
              minWidth: 0,
              outline: 'none',
            }}
          >
            <ProviderAvatar provider={p.key} size={36} />
            <div style={{ minWidth: 0 }}>
              <div
                style={{
                  fontWeight: 600,
                  fontSize: 13,
                  whiteSpace: 'nowrap',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  lineHeight: 1.3,
                }}
              >
                {p.name}
              </div>
              <Typography.Text type="secondary" style={{ fontSize: 11, whiteSpace: 'nowrap' }}>
                {p.types.map((x) => t(`common:type.${x}`)).join(' / ')}
              </Typography.Text>
            </div>
          </div>
        );
      })}
    </div>
  );
}
