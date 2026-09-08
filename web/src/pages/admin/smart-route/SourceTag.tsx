import { useTranslation } from 'react-i18next';
import { NeutralTag } from '@/components';

/** Decision source: rule / context / vector (raw value shown when unknown). */
export default function SourceTag({ source }: { source: string }) {
  const { t } = useTranslation(['route']);
  if (!source) return <span>-</span>;
  return <NeutralTag>{t(`route:source.${source}`, { defaultValue: source })}</NeutralTag>;
}

export function useRequestTypeText() {
  const { t } = useTranslation(['route']);
  return (type: string) => (type ? t(`route:requestType.${type}`, { defaultValue: type }) : '-');
}
