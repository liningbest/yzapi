import { Tooltip } from 'antd';
import { useTranslation } from 'react-i18next';
import { formatDateTime, isZeroTime, relativeTime } from '@/utils/format';

interface Props {
  value: string | null | undefined;
  /** Show absolute time directly instead of relative. */
  absolute?: boolean;
  emptyText?: string;
}

/** Relative time with absolute time on hover. */
export default function TimeCell({ value, absolute, emptyText }: Props) {
  const { t } = useTranslation();
  if (!value || isZeroTime(value)) return <span style={{ color: 'var(--yz-text-tertiary)' }}>{emptyText ?? '-'}</span>;
  const abs = formatDateTime(value);
  if (absolute) {
    return (
      <Tooltip title={relativeTime(value)}>
        <span style={{ whiteSpace: 'nowrap' }}>{abs}</span>
      </Tooltip>
    );
  }
  return (
    <Tooltip title={abs}>
      <span style={{ whiteSpace: 'nowrap' }} aria-label={t('common.time')}>
        {relativeTime(value)}
      </span>
    </Tooltip>
  );
}
