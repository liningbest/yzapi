import { Tooltip } from 'antd';
import type { CSSProperties, ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import type { Health, LogResult, ModelKind, ModelType, PolicyAction, Protocol, RiskLevel, Role, RouteLabel } from '@/types';
import { HEALTH_COLORS, RESULT_COLORS, SEMANTIC, type SemanticKey } from '@/utils/constants';
import { formatDateTime } from '@/utils/format';
import { useCountdown } from '@/hooks/useCountdown';

/* ---------- primitives ---------- */

export type NeutralTone = 'neutral' | 'danger' | 'warning' | 'success';

interface NeutralTagProps {
  children: ReactNode;
  tone?: NeutralTone;
  mono?: boolean;
  style?: CSSProperties;
  className?: string;
  title?: string;
  onClick?: () => void;
}

/** Outlined neutral tag: 1px border, transparent bg, 11px secondary text. */
export function NeutralTag({ children, tone = 'neutral', mono, style, className, title, onClick }: NeutralTagProps) {
  const cls = ['yz-tag', tone !== 'neutral' ? tone : '', mono ? 'mono' : '', className ?? ''].filter(Boolean).join(' ');
  return (
    <span className={cls} style={{ cursor: onClick ? 'pointer' : undefined, ...style }} title={title} onClick={onClick}>
      {children}
    </span>
  );
}

interface StatusDotProps {
  tone: SemanticKey;
  children: ReactNode;
  style?: CSSProperties;
  className?: string;
}

/** Semantic dot + plain text; used for health/result/enabled states. */
export function StatusDot({ tone, children, style, className }: StatusDotProps) {
  return (
    <span className={`yz-status${className ? ` ${className}` : ''}`} style={style}>
      <i className="yz-dot" style={{ background: SEMANTIC[tone] }} />
      {children}
    </span>
  );
}

/* ---------- domain tags ---------- */

export function TypeTag({ type }: { type: ModelType | string }) {
  const { t } = useTranslation();
  return <NeutralTag>{t(`type.${type}`, { defaultValue: type })}</NeutralTag>;
}

export function ProtocolTag({ protocol }: { protocol: Protocol | string }) {
  const { t } = useTranslation();
  const short: Record<string, string> = {
    'openai-completions': 'Chat',
    'openai-responses': 'Responses',
    'gemini-generate': 'Gemini',
    'anthropic-messages': 'Anthropic',
    'openai-embeddings': 'Embeddings',
    'openai-images': 'Images',
  };
  return (
    <Tooltip title={t(`protocol.${protocol}`, { defaultValue: protocol })}>
      <NeutralTag>{short[protocol] ?? protocol}</NeutralTag>
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
  const tone = HEALTH_COLORS[health] ?? 'neutral';
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
    <StatusDot tone={tone}>
      {label}
      {health === 'cooling' && remaining ? (
        <span className="yz-mono" style={{ color: 'var(--yz-text-tertiary)', marginLeft: 2 }}>
          {remaining}
        </span>
      ) : null}
    </StatusDot>
  );
  return hasTip ? <Tooltip title={tip}>{node}</Tooltip> : node;
}

export function ResultTag({ result }: { result: LogResult | string }) {
  const { t } = useTranslation();
  const tone = RESULT_COLORS[result] ?? 'neutral';
  const text = t(`result.${result}`, { defaultValue: result });
  if (result === 'blocked') {
    return (
      <StatusDot tone="danger" style={{ color: 'var(--yz-danger)' }}>
        {text}
      </StatusDot>
    );
  }
  return <StatusDot tone={tone}>{text}</StatusDot>;
}

export function EnabledBadge({ enabled }: { enabled: boolean }) {
  const { t } = useTranslation();
  return (
    <StatusDot tone={enabled ? 'success' : 'neutral'} style={enabled ? undefined : { color: 'var(--yz-text-secondary)' }}>
      {enabled ? t('common.enabled') : t('common.disabled')}
    </StatusDot>
  );
}

export function RoleTag({ role }: { role: Role | string }) {
  const { t } = useTranslation();
  return <NeutralTag>{t(`role.${role}`, { defaultValue: role })}</NeutralTag>;
}

export function KindTag({ kind }: { kind: ModelKind | string }) {
  const { t } = useTranslation();
  if (kind === 'model') return null;
  return <NeutralTag>{t(`kind.${kind}`, { defaultValue: kind })}</NeutralTag>;
}

export function LabelTag({ label }: { label: RouteLabel | string }) {
  const { t } = useTranslation();
  return <NeutralTag>{t(`label.${label}`, { defaultValue: label })}</NeutralTag>;
}

export function ActionTag({ action }: { action: PolicyAction | string }) {
  const { t } = useTranslation();
  return (
    <NeutralTag tone={action === 'block' ? 'danger' : 'neutral'}>
      {t(`policyAction.${action}`, { defaultValue: action })}
    </NeutralTag>
  );
}

export function RiskTag({ risk }: { risk: RiskLevel | string }) {
  const { t } = useTranslation();
  return (
    <NeutralTag tone={risk === 'high' ? 'danger' : 'neutral'}>{t(`risk.${risk}`, { defaultValue: risk })}</NeutralTag>
  );
}

export function VectorizedBadge({ vectorized, dim }: { vectorized: boolean; dim?: number }) {
  const { t } = useTranslation();
  return (
    <Tooltip title={vectorized && dim ? `${t('common.dimension')}: ${dim}` : undefined}>
      <StatusDot
        tone={vectorized ? 'success' : 'neutral'}
        style={vectorized ? undefined : { color: 'var(--yz-text-secondary)' }}
      >
        {vectorized ? t('common.vectorized') : t('common.notVectorized')}
      </StatusDot>
    </Tooltip>
  );
}

/** HTTP status code rendered as mono text with a semantic tone. */
export function StatusCodeTag({ code }: { code: number | null | undefined }) {
  if (code === null || code === undefined) return <span style={{ color: 'var(--yz-text-tertiary)' }}>-</span>;
  const tone: NeutralTone = code >= 500 ? 'danger' : code >= 400 ? 'warning' : code >= 200 && code < 300 ? 'success' : 'neutral';
  return (
    <NeutralTag tone={tone} mono>
      {code}
    </NeutralTag>
  );
}


/** Usage status of a call log: confirmed numbers, partial / unknown flagged, none shown as a dash. */
export function UsageStatusTag({ status, estPrompt }: { status?: string; estPrompt?: number }) {
  const { t } = useTranslation('common');
  switch (status) {
    case 'partial':
      return <NeutralTag tone="warning" title={t('usageStatus.partialHint')}>{t('usageStatus.partial')}</NeutralTag>;
    case 'unknown':
      return (
        <NeutralTag tone="warning" title={estPrompt ? t('usageStatus.unknownHint', { n: estPrompt }) : undefined}>
          {t('usageStatus.unknown')}
        </NeutralTag>
      );
    case 'none':
      return <span style={{ color: 'var(--yz-text-tertiary)' }}>-</span>;
    default:
      return null;
  }
}
