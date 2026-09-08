import { Select, Space, Typography } from 'antd';
import { useTranslation } from 'react-i18next';
import { ActionTag, RiskTag } from '@/components';
import type { PolicyGroup, PolicyGroupRef } from '@/types';

/** Compact name + action + risk display for a policy group reference. */
export function GroupTags({ group, compact }: { group: PolicyGroupRef | PolicyGroup | null | undefined; compact?: boolean }) {
  const { t } = useTranslation(['compliance']);
  if (!group) return <Typography.Text type="secondary">{t('compliance:policyGroupMissing')}</Typography.Text>;
  return (
    <Space size={4} wrap={!compact} style={compact ? { fontSize: 12 } : undefined}>
      <span style={{ fontWeight: 500 }}>{group.name}</span>
      <span style={{ transform: compact ? 'scale(0.9)' : undefined, display: 'inline-flex', gap: 4 }}>
        <ActionTag action={group.action} />
        <RiskTag risk={group.risk_level} />
      </span>
    </Space>
  );
}

interface Props {
  groups: PolicyGroup[];
  value?: number;
  onChange?: (value?: number) => void;
  allowClear?: boolean;
  placeholder?: string;
  style?: React.CSSProperties;
  loading?: boolean;
  disabled?: boolean;
}

/** Policy group Select whose options show name + action tag; works standalone or inside Form.Item. */
export default function PolicyGroupSelect({ groups, value, onChange, allowClear, placeholder, style, loading, disabled }: Props) {
  const { t } = useTranslation(['compliance', 'common']);
  return (
    <Select
      value={value}
      onChange={(v) => onChange?.(v)}
      allowClear={allowClear}
      showSearch
      optionFilterProp="name"
      placeholder={placeholder ?? t('compliance:policyGroupPlaceholder')}
      style={{ minWidth: 200, ...style }}
      loading={loading}
      disabled={disabled}
      options={groups.map((g) => ({
        value: g.id,
        name: g.name,
        label: (
          <Space size={6}>
            <span style={{ opacity: g.enabled ? 1 : 0.5 }}>{g.name}</span>
            <ActionTag action={g.action} />
            {!g.enabled ? (
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                {t('common:common.disabled')}
              </Typography.Text>
            ) : null}
          </Space>
        ),
      }))}
    />
  );
}
