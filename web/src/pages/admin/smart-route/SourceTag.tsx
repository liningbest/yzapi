import { Tag } from 'antd';
import { useTranslation } from 'react-i18next';

const COLORS: Record<string, string> = {
  rule: 'default',
  context: 'purple',
  vector: 'geekblue',
};

/** Decision source: rule / context / vector (raw value shown when unknown). */
export default function SourceTag({ source }: { source: string }) {
  const { t } = useTranslation(['route']);
  if (!source) return <span>-</span>;
  return (
    <Tag color={COLORS[source] ?? 'default'} bordered={false} style={{ marginInlineEnd: 0 }}>
      {t(`route:source.${source}`, { defaultValue: source })}
    </Tag>
  );
}

export function useRequestTypeText() {
  const { t } = useTranslation(['route']);
  return (type: string) => (type ? t(`route:requestType.${type}`, { defaultValue: type }) : '-');
}
