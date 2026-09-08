import { useMemo, useState } from 'react';
import { Button, Card, Input, Segmented, Skeleton, Typography } from 'antd';
import { ReadOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { userApi } from '@/api';
import { EmptyState, FilterBar, PageHeader } from '@/components';
import type { ModelType, UserModel } from '@/types';
import { MODEL_TYPES } from '@/utils/constants';
import ApiDocModal from './models/ApiDocModal';
import ModelCard from './models/ModelCard';

type Category = 'all' | ModelType;

export default function Models() {
  const { t } = useTranslation(['console', 'common']);
  const [category, setCategory] = useState<Category>('all');
  const [keyword, setKeyword] = useState('');
  const [docOpen, setDocOpen] = useState(false);

  const query = useQuery({ queryKey: ['user', 'models'], queryFn: userApi.models });
  const models: UserModel[] = useMemo(() => query.data?.models ?? [], [query.data]);
  const baseUrl = query.data?.base_url ?? '';

  const counts = useMemo(() => {
    const c: Record<Category, number> = { all: models.length, text: 0, image: 0, embedding: 0 };
    for (const m of models) if (m.type in c) c[m.type] += 1;
    return c;
  }, [models]);

  const filtered = useMemo(() => {
    const kw = keyword.trim().toLowerCase();
    return models.filter(
      (m) => (category === 'all' || m.type === category) && (!kw || m.name.toLowerCase().includes(kw)),
    );
  }, [models, category, keyword]);

  const exampleModel = useMemo(() => models.find((m) => m.type === 'text')?.name, [models]);

  const categoryOptions = (['all', ...MODEL_TYPES] as Category[]).map((k) => ({
    value: k,
    label: `${k === 'all' ? t('console:models.tabAll') : t(`common:type.${k}`)} (${counts[k]})`,
  }));

  return (
    <div>
      <PageHeader
        title={t('console:models.title')}
        subtitle={t('console:models.subtitle')}
        extra={
          <Button icon={<ReadOutlined />} onClick={() => setDocOpen(true)}>
            {t('console:models.apiDoc')}
          </Button>
        }
      />

      <Card className="yz-card" style={{ marginBottom: 16 }} styles={{ body: { padding: '18px 24px' } }}>
        {query.isLoading ? (
          <Skeleton active paragraph={{ rows: 1 }} title={{ width: 120 }} />
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <Typography.Text type="secondary" style={{ fontSize: 12.5, fontWeight: 500, letterSpacing: 0.3 }}>
              {t('console:models.baseUrl')}
            </Typography.Text>
            <Typography.Text
              code
              copyable={{ text: baseUrl }}
              style={{ fontSize: 17, fontWeight: 600, wordBreak: 'break-all', alignSelf: 'flex-start' }}
            >
              {baseUrl || '-'}
            </Typography.Text>
            <Typography.Text type="secondary" style={{ fontSize: 12.5 }}>
              {t('console:models.baseUrlHint')}
            </Typography.Text>
          </div>
        )}
      </Card>

      <Card className="yz-card">
        <FilterBar
          extra={
            <Input.Search
              allowClear
              placeholder={t('console:models.searchPlaceholder')}
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
              style={{ width: 240 }}
            />
          }
        >
          <Segmented value={category} options={categoryOptions} onChange={(v) => setCategory(v as Category)} />
        </FilterBar>

        {query.isLoading ? (
          <div className="yz-grid-cards">
            {Array.from({ length: 6 }).map((_, i) => (
              <Card key={i} className="yz-card" styles={{ body: { padding: 16 } }}>
                <Skeleton active avatar paragraph={{ rows: 1 }} />
              </Card>
            ))}
          </div>
        ) : filtered.length === 0 ? (
          <EmptyState title={t('console:models.empty')} hint={t('console:models.emptyHint')} />
        ) : (
          <>
            <div className="yz-grid-cards">
              {filtered.map((m) => (
                <ModelCard key={`${m.kind}:${m.name}`} model={m} />
              ))}
            </div>
            <Typography.Text type="secondary" style={{ display: 'block', marginTop: 16, fontSize: 12 }}>
              {t('console:models.modelCount', { count: filtered.length })}
            </Typography.Text>
          </>
        )}
      </Card>

      <ApiDocModal open={docOpen} onClose={() => setDocOpen(false)} baseUrl={baseUrl} exampleModel={exampleModel} />
    </div>
  );
}
