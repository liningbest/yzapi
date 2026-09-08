import { useState } from 'react';
import { App, Button, Input, Popconfirm, Popover, Space, Tooltip, Typography } from 'antd';
import {
  ArrowRightOutlined,
  CheckOutlined,
  CloseOutlined,
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { accountsApi } from '@/api';
import type { NormalizedError } from '@/api';
import { NeutralTag } from '@/components';
import type { Account, AccountTestResult, ModelMapping } from '@/types';

interface Props {
  account: Account;
}

type Row = ModelMapping & { key: string };

type LatencyState = { status: 'idle' } | { status: 'testing' } | { status: 'ok'; ms: number } | { status: 'fail'; message: string };

/** Clickable "N models" tag that opens an editable mapping table with per-model latency tests. */
export default function MappingsPopover({ account }: Props) {
  const { t } = useTranslation(['accounts', 'common']);
  const { message } = App.useApp();
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);
  const [draft, setDraft] = useState<{ request_model: string; upstream_model: string }>({ request_model: '', upstream_model: '' });
  const [adding, setAdding] = useState(false);
  const [latency, setLatency] = useState<Record<string, LatencyState>>({});

  const rows: Row[] = (account.mappings ?? []).map((m, i) => ({ ...m, key: m.id ? String(m.id) : `${m.request_model}-${i}` }));

  const saveMut = useMutation({
    mutationFn: (mappings: ModelMapping[]) => accountsApi.updateMappings(account.id, mappings),
    onSuccess: () => {
      message.success(t('accounts:mappings.saved'));
      setEditing(null);
      setAdding(false);
      void qc.invalidateQueries({ queryKey: ['admin', 'accounts'] });
      void qc.invalidateQueries({ queryKey: ['admin', 'models'] });
    },
  });

  const testOne = async (m: ModelMapping) => {
    const k = m.upstream_model;
    setLatency((s) => ({ ...s, [k]: { status: 'testing' } }));
    try {
      const r: AccountTestResult = await accountsApi.testModel(account.id, m.upstream_model);
      setLatency((s) => ({ ...s, [k]: r.ok ? { status: 'ok', ms: r.latency_ms } : { status: 'fail', message: r.message } }));
    } catch (e) {
      setLatency((s) => ({ ...s, [k]: { status: 'fail', message: (e as NormalizedError).message } }));
    }
  };

  const testAll = async () => {
    for (const m of rows) {
      // sequential to avoid hammering the upstream
      // eslint-disable-next-line no-await-in-loop
      await testOne(m);
    }
  };

  const commit = (next: ModelMapping[]) => {
    if (next.length === 0) {
      message.warning(t('accounts:mappings.lastOne'));
      return;
    }
    saveMut.mutate(next.map(({ request_model, upstream_model }) => ({ request_model, upstream_model })));
  };

  const startEdit = (r: Row) => {
    setAdding(false);
    setEditing(r.key);
    setDraft({ request_model: r.request_model, upstream_model: r.upstream_model });
  };

  const saveEdit = (r: Row) => {
    const rq = draft.request_model.trim();
    if (!rq) return;
    commit(rows.map((x) => (x.key === r.key ? { request_model: rq, upstream_model: draft.upstream_model.trim() || rq } : x)));
  };

  const saveAdd = () => {
    const rq = draft.request_model.trim();
    if (!rq) return;
    commit([...rows, { request_model: rq, upstream_model: draft.upstream_model.trim() || rq }]);
  };

  const remove = (r: Row) => commit(rows.filter((x) => x.key !== r.key));

  const renderLatency = (m: ModelMapping) => {
    const st = latency[m.upstream_model] ?? { status: 'idle' };
    switch (st.status) {
      case 'testing':
        return (
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            …
          </Typography.Text>
        );
      case 'ok':
        return (
          <span className="yz-status">
            <i className="yz-dot" style={{ background: st.ms < 1500 ? 'var(--yz-success)' : 'var(--yz-warning)' }} />
            <span className="yz-mono">{t('accounts:mappings.ok', { ms: st.ms })}</span>
          </span>
        );
      case 'fail':
        return (
          <Tooltip title={st.message}>
            <span className="yz-status" style={{ color: 'var(--yz-danger)', cursor: 'help' }}>
              <i className="yz-dot" style={{ background: 'var(--yz-danger)' }} />
              {t('accounts:mappings.failed')}
            </span>
          </Tooltip>
        );
      default:
        return (
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            -
          </Typography.Text>
        );
    }
  };

  const cell: React.CSSProperties = { padding: '6px 10px', borderBottom: '1px solid var(--yz-border)', verticalAlign: 'middle' };
  const head: React.CSSProperties = { ...cell, fontSize: 12, fontWeight: 500, color: 'var(--yz-text-secondary)', background: 'var(--yz-bg)' };

  const editorRow = (key: string, onSave: () => void, onCancel: () => void) => (
    <tr key={key}>
      <td style={cell}>
        <Input
          size="small"
          className="yz-mono"
          value={draft.request_model}
          autoFocus
          placeholder={t('accounts:form.requestModelPlaceholder')}
          onChange={(e) => setDraft((d) => ({ ...d, request_model: e.target.value }))}
          onPressEnter={onSave}
        />
      </td>
      <td style={{ ...cell, textAlign: 'center', width: 28 }}>
        <ArrowRightOutlined style={{ color: 'var(--yz-text-tertiary)', fontSize: 11 }} />
      </td>
      <td style={cell}>
        <Input
          size="small"
          className="yz-mono"
          value={draft.upstream_model}
          placeholder={t('accounts:form.upstreamModelPlaceholder')}
          onChange={(e) => setDraft((d) => ({ ...d, upstream_model: e.target.value }))}
          onPressEnter={onSave}
        />
      </td>
      <td style={cell} />
      <td style={{ ...cell, whiteSpace: 'nowrap' }}>
        <Space size={0}>
          <Button type="text" size="small" icon={<CheckOutlined />} loading={saveMut.isPending} onClick={onSave} />
          <Button type="text" size="small" icon={<CloseOutlined />} onClick={onCancel} />
        </Space>
      </td>
    </tr>
  );

  const content = (
    <div style={{ width: 640 }}>
      <div style={{ maxHeight: 360, overflow: 'auto', border: '1px solid var(--yz-border)', borderRadius: 6 }}>
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
          <thead>
            <tr>
              <th style={{ ...head, textAlign: 'left' }}>{t('accounts:form.requestModel')}</th>
              <th style={{ ...head, width: 28 }} />
              <th style={{ ...head, textAlign: 'left' }}>{t('accounts:form.upstreamModel')}</th>
              <th style={{ ...head, textAlign: 'left', width: 110 }}>{t('accounts:mappings.latency')}</th>
              <th style={{ ...head, textAlign: 'left', width: 120 }}>{t('accounts:columns.actions')}</th>
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 && !adding ? (
              <tr>
                <td colSpan={5} style={{ ...cell, textAlign: 'center', color: 'var(--yz-text-tertiary)' }}>
                  {t('accounts:mappings.empty')}
                </td>
              </tr>
            ) : null}
            {rows.map((r) =>
              editing === r.key ? (
                editorRow(r.key, () => saveEdit(r), () => setEditing(null))
              ) : (
                <tr key={r.key}>
                  <td style={cell}>
                    <Typography.Text className="yz-mono" copyable={{ tooltips: [t('accounts:mappings.copyName'), t('common:common.copied')] }}>
                      {r.request_model}
                    </Typography.Text>
                  </td>
                  <td style={{ ...cell, textAlign: 'center' }}>
                    <ArrowRightOutlined style={{ color: 'var(--yz-text-tertiary)', fontSize: 11 }} />
                  </td>
                  <td style={cell}>
                    <span className="yz-mono" style={{ color: 'var(--yz-text-secondary)' }}>
                      {r.upstream_model}
                    </span>
                  </td>
                  <td style={cell}>{renderLatency(r)}</td>
                  <td style={{ ...cell, whiteSpace: 'nowrap' }}>
                    <Space size={0}>
                      <Tooltip title={t('accounts:mappings.test')}>
                        <Button
                          type="text"
                          size="small"
                          icon={<ThunderboltOutlined />}
                          loading={latency[r.upstream_model]?.status === 'testing'}
                          onClick={() => void testOne(r)}
                        />
                      </Tooltip>
                      <Tooltip title={t('accounts:mappings.edit')}>
                        <Button type="text" size="small" icon={<EditOutlined />} onClick={() => startEdit(r)} />
                      </Tooltip>
                      <Popconfirm
                        title={t('accounts:mappings.delete')}
                        description={t('accounts:mappings.deleteConfirm', { name: r.request_model })}
                        okButtonProps={{ danger: true }}
                        onConfirm={() => remove(r)}
                      >
                        <Tooltip title={t('accounts:mappings.delete')}>
                          <Button type="text" size="small" danger icon={<DeleteOutlined />} />
                        </Tooltip>
                      </Popconfirm>
                    </Space>
                  </td>
                </tr>
              ),
            )}
            {adding ? editorRow('__new', saveAdd, () => setAdding(false)) : null}
          </tbody>
        </table>
      </div>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 10 }}>
        <Button
          size="small"
          icon={<PlusOutlined />}
          disabled={rows.length >= 100 || adding}
          onClick={() => {
            setEditing(null);
            setDraft({ request_model: '', upstream_model: '' });
            setAdding(true);
          }}
        >
          {t('accounts:mappings.add')}
        </Button>
        <Button size="small" icon={<ThunderboltOutlined />} disabled={rows.length === 0} onClick={() => void testAll()}>
          {t('accounts:mappings.testAll')}
        </Button>
      </div>
    </div>
  );

  return (
    <Popover
      title={
        <span>
          {t('accounts:mappings.title')}
          <Typography.Text type="secondary" style={{ fontSize: 12, marginLeft: 8 }}>
            {account.name}
          </Typography.Text>
        </span>
      }
      trigger="click"
      placement="bottomLeft"
      open={open}
      onOpenChange={(v) => {
        setOpen(v);
        if (!v) {
          setEditing(null);
          setAdding(false);
        }
      }}
      content={content}
      destroyTooltipOnHide
    >
      <NeutralTag style={{ cursor: 'pointer' }}>{t('accounts:modelsCount', { count: rows.length })}</NeutralTag>
    </Popover>
  );
}
