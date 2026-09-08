import { Badge, Tag, Tooltip } from 'antd';
import { useTranslation } from 'react-i18next';
import type { Health, LogResult, ModelKind, ModelType, PolicyAction, Protocol, RiskLevel, Role, RouteLabel } from '@/types';
import { HEALTH_COLORS, RESULT_COLORS, TYPE_COLORS } from '@/utils/constants';
import { formatDateTime } from '@/utils/format';
import { useCountdown } from '@/hooks/useCountdown';

export function TypeTag({ type }: { type: ModelType | string }) {
  const { t } = useTranslation();
  const color = TYPE_COLORS[type as ModelType] ?? 'default';
  return (
    <Tag color={color} style={{ marginInlineEnd: 0 }}>
      {t(`type.${type}`, { defaultValue: type })}
    </Tag>
  );
}

export function ProtocolTag({ protocol }: { protocol: Protocol | string }) {
  const { t } = useTranslation();
  const short: Record<string, string> = {
    'openai-completions': 'Chat',
    'openai-responses': 'Responses',
    'anthropic-messages': 'Anthropic',
    'openai-embeddings': 'Embeddings',
    'openai-images': 'Images',
  };
  return (
    <Tooltip title={t(`protocol.${protocol}`, { defaultValue: protocol })}>
      <Tag bordered={false} style={{ marginInlineEnd: 0 }}>
        {short[protocol] ?? protocol}
      </Tag>
    </Tooltip>
  );
}

export function HealthTag({
  health,
  cooldownUntil,
  lastError,
}: {
  health: Health | string;
  cooldownUntil?: string | null;
  lastError?: string | null;
}) {
  const { t } = useTranslation();
  const remaining = useCountdown(health === 'cooling' ? cooldownUntil : null);
  const color = HEALTH_COLORS[health as Health] ?? 'default';
  const label = t(`health.${health}`, { defaultValue: health });
  const tip = (
    <div>
      {cooldownUntil && health === 'cooling' ? (
        <div>{t('health.cooldownUntil', { time: formatDateTime(cooldownUntil) })}</div>
      ) : null}
      {lastError ? (
        <div>
          {t('health.lastError')}: {lastError}
        </div>
      ) : null}
    </div>
  );
  const hasTip = Boolean(lastError || (cooldownUntil && health === 'cooling'));
  const node = (
    <Tag color={color} style={{ marginInlineEnd: 0 }}>
      {label}
      {health === 'cooling' && remaining ? ` ${remaining}` : ''}
    </Tag>
  );
  return hasTip ? <Tooltip title={tip}>{node}</Tooltip> : node;
}

export function ResultTag({ result }: { result: LogResult | string }) {
  const { t } = useTranslation();
  const color = RESULT_COLORS[result as LogResult] ?? 'default';
  return (
    <Tag color={color} style={{ marginInlineEnd: 0 }}>
      {t(`result.${result}`, { defaultValue: result })}
    </Tag>
  );
}

export function EnabledBadge({ enabled }: { enabled: boolean }) {
  const { t } = useTranslation();
  return <Badge status={enabled ? 'success' : 'default'} text={enabled ? t('common.enabled') : t('common.disabled')} />;
}

export function RoleTag({ role }: { role: Role | string }) {
  const { t } = useTranslation();
  return (
    <Tag color={role === 'admin' ? 'purple' : 'default'} style={{ marginInlineEnd: 0 }}>
      {t(`role.${role}`, { defaultValue: role })}
    </Tag>
  );
}

export function KindTag({ kind }: { kind: ModelKind | string }) {
  const { t } = useTranslation();
  if (kind === 'model') return null;
  return (
    <Tag color={kind === 'virtual' ? 'blue' : 'purple'} style={{ marginInlineEnd: 0 }}>
      {t(`kind.${kind}`, { defaultValue: kind })}
    </Tag>
  );
}

export function LabelTag({ label }: { label: RouteLabel | string }) {
  const { t } = useTranslation();
  return (
    <Tag color={label === 'complex' ? 'volcano' : 'green'} style={{ marginInlineEnd: 0 }}>
      {t(`label.${label}`, { defaultValue: label })}
    </Tag>
  );
}

export function ActionTag({ action }: { action: PolicyAction | string }) {
  const { t } = useTranslation();
  return (
    <Tag color={action === 'block' ? 'red' : 'blue'} style={{ marginInlineEnd: 0 }}>
      {t(`policyAction.${action}`, { defaultValue: action })}
    </Tag>
  );
}

export function RiskTag({ risk }: { risk: RiskLevel | string }) {
  const { t } = useTranslation();
  const color = risk === 'high' ? 'red' : risk === 'medium' ? 'orange' : 'green';
  return (
    <Tag color={color} style={{ marginInlineEnd: 0 }}>
      {t(`risk.${risk}`, { defaultValue: risk })}
    </Tag>
  );
}

export function VectorizedBadge({ vectorized, dim }: { vectorized: boolean; dim?: number }) {
  const { t } = useTranslation();
  return (
    <Tooltip title={vectorized && dim ? `${t('common.dimension')}: ${dim}` : undefined}>
      <Badge
        status={vectorized ? 'success' : 'default'}
        text={vectorized ? t('common.vectorized') : t('common.notVectorized')}
      />
    </Tooltip>
  );
}
