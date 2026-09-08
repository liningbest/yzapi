import { useEffect, useMemo, useState } from 'react';
import { Button, Checkbox, Input, Modal, Space, Typography } from 'antd';
import { useTranslation } from 'react-i18next';
import { EmptyState, NeutralTag } from '@/components';

interface Props {
  open: boolean;
  /** Model names returned by the upstream. */
  models: string[];
  /** Names that already exist in the mappings (pre-checked and locked). */
  mapped: string[];
  onCancel: () => void;
  onConfirm: (names: string[]) => void;
}

/** Searchable checkbox list of discovered models. */
export default function DiscoverModal({ open, models, mapped, onCancel, onConfirm }: Props) {
  const { t } = useTranslation(['accounts', 'common']);
  const [q, setQ] = useState('');
  const [selected, setSelected] = useState<string[]>([]);

  useEffect(() => {
    if (open) {
      setQ('');
      setSelected([]);
    }
  }, [open]);

  const mappedSet = useMemo(() => new Set(mapped), [mapped]);
  const filtered = useMemo(() => {
    const kw = q.trim().toLowerCase();
    return kw ? models.filter((m) => m.toLowerCase().includes(kw)) : models;
  }, [models, q]);
  const unmapped = useMemo(() => models.filter((m) => !mappedSet.has(m)), [models, mappedSet]);

  const toggle = (name: string, checked: boolean) => {
    setSelected((prev) => (checked ? Array.from(new Set([...prev, name])) : prev.filter((x) => x !== name)));
  };

  return (
    <Modal
      open={open}
      title={t('accounts:discover.title')}
      onCancel={onCancel}
      width={520}
      destroyOnClose
      footer={
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 8 }}>
          <Space size={4}>
            <Typography.Text type="secondary">{t('accounts:discover.selected', { count: selected.length })}</Typography.Text>
            <Button type="link" size="small" disabled={!unmapped.length} onClick={() => setSelected(unmapped)}>
              {t('accounts:discover.selectAll')}
            </Button>
            <Button type="link" size="small" disabled={!selected.length} onClick={() => setSelected([])}>
              {t('accounts:discover.clear')}
            </Button>
          </Space>
          <Space>
            <Button onClick={onCancel}>{t('common:action.cancel')}</Button>
            <Button type="primary" disabled={!selected.length} onClick={() => onConfirm(selected)}>
              {t('accounts:discover.confirm')}
            </Button>
          </Space>
        </div>
      }
    >
      <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginBottom: 12 }}>
        {t('accounts:discover.hint')}
      </Typography.Paragraph>
      <Input.Search
        allowClear
        value={q}
        onChange={(e) => setQ(e.target.value)}
        placeholder={t('accounts:discover.searchPlaceholder')}
        style={{ marginBottom: 12 }}
      />
      {models.length === 0 ? (
        <EmptyState title={t('accounts:discover.empty')} />
      ) : filtered.length === 0 ? (
        <EmptyState title={t('accounts:discover.noMatch')} />
      ) : (
        <div
          style={{
            maxHeight: 380,
            overflow: 'auto',
            border: '1px solid var(--yz-border)',
            borderRadius: 6,
            padding: '4px 0',
          }}
        >
          {filtered.map((name) => {
            const locked = mappedSet.has(name);
            const checked = locked || selected.includes(name);
            return (
              <label
                key={name}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 10,
                  padding: '7px 12px',
                  cursor: locked ? 'default' : 'pointer',
                }}
              >
                <Checkbox checked={checked} disabled={locked} onChange={(e) => toggle(name, e.target.checked)} />
                <span className="yz-mono" style={{ flex: 1, fontSize: 13, wordBreak: 'break-all' }}>
                  {name}
                </span>
                {locked ? (
                  <NeutralTag>{t('accounts:discover.mapped')}</NeutralTag>
                ) : null}
              </label>
            );
          })}
        </div>
      )}
    </Modal>
  );
}
