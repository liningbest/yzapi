import { useState } from 'react';
import { App, Button, Form, Input, Modal, Popconfirm, Select, Space, Switch, Table, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { DeleteOutlined, EditOutlined, ImportOutlined, PlusOutlined } from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { complianceApi } from '@/api';
import { EmptyState, FilterBar, FormDrawer, TimeCell } from '@/components';
import { useTableQuery } from '@/hooks/useTableQuery';
import type { PolicyGroup, SensitiveWord, SensitiveWordInput, WordListParams } from '@/types';
import PolicyGroupSelect, { GroupTags } from './PolicyGroupSelect';
import { POLICY_GROUPS_KEY, WORDS_KEY } from './keys';

type Filters = Omit<WordListParams, 'page' | 'page_size'>;

function splitLines(raw: string): string[] {
  return raw
    .split(/\r?\n/)
    .map((s) => s.trim())
    .filter(Boolean);
}

interface Props {
  groups: PolicyGroup[];
  groupsLoading: boolean;
}

interface WordForm {
  policy_group_id: number;
  word: string;
  note?: string;
  enabled: boolean;
}

function WordDrawer({
  open,
  word,
  groups,
  onClose,
}: {
  open: boolean;
  word: SensitiveWord | null;
  groups: PolicyGroup[];
  onClose: () => void;
}) {
  const { t } = useTranslation(['compliance', 'common']);
  const { message } = App.useApp();
  const qc = useQueryClient();
  const [form] = Form.useForm<WordForm>();

  const save = useMutation({
    mutationFn: (values: WordForm) => {
      const body: SensitiveWordInput = {
        policy_group_id: values.policy_group_id,
        word: values.word.trim(),
        note: values.note?.trim() ?? '',
        enabled: values.enabled,
      };
      return word ? complianceApi.updateWord(word.id, body) : complianceApi.createWord(body);
    },
    onSuccess: () => {
      message.success(word ? t('common:common.updateSuccess') : t('common:common.createSuccess'));
      onClose();
      void qc.invalidateQueries({ queryKey: WORDS_KEY });
      void qc.invalidateQueries({ queryKey: POLICY_GROUPS_KEY });
    },
  });

  const submit = async () => {
    const values = await form.validateFields();
    save.mutate(values);
  };

  const initialValues: Partial<WordForm> = word
    ? { policy_group_id: word.policy_group_id, word: word.word, note: word.note, enabled: word.enabled }
    : { policy_group_id: groups.length === 1 ? groups[0].id : undefined, word: '', note: '', enabled: true };

  return (
    <FormDrawer
      open={open}
      title={word ? t('compliance:words.edit') : t('compliance:words.add')}
      onClose={onClose}
      onSubmit={submit}
      submitting={save.isPending}
      width={560}
    >
      <Form form={form} layout="vertical" initialValues={initialValues} requiredMark="optional">
        <Form.Item
          name="policy_group_id"
          label={t('compliance:policyGroup')}
          extra={t('compliance:words.groupExtra')}
          rules={[{ required: true, message: t('common:common.required') }]}
        >
          <PolicyGroupSelect groups={groups} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item
          name="word"
          label={t('compliance:words.word')}
          extra={t('compliance:words.wordExtra')}
          rules={[
            { required: true, message: t('common:common.required') },
            { whitespace: true, message: t('common:common.required') },
          ]}
        >
          <Input placeholder={t('compliance:words.wordPlaceholder')} maxLength={200} />
        </Form.Item>
        <Form.Item name="note" label={t('common:common.note')}>
          <Input placeholder={t('common:common.notePlaceholder')} maxLength={200} />
        </Form.Item>
        <Form.Item name="enabled" label={t('common:common.status')} valuePropName="checked">
          <Switch checkedChildren={t('common:common.enabled')} unCheckedChildren={t('common:common.disabled')} />
        </Form.Item>
      </Form>
    </FormDrawer>
  );
}

function BatchImportModal({ open, groups, onClose }: { open: boolean; groups: PolicyGroup[]; onClose: () => void }) {
  const { t } = useTranslation(['compliance', 'common']);
  const { message } = App.useApp();
  const qc = useQueryClient();
  const [form] = Form.useForm<{ policy_group_id: number; lines: string }>();

  const submit = useMutation({
    mutationFn: (v: { policy_group_id: number; lines: string }) =>
      complianceApi.batchWords(v.policy_group_id, splitLines(v.lines)),
    onSuccess: (res) => {
      message.success(t('compliance:words.batch.imported', { created: res.created }));
      onClose();
      void qc.invalidateQueries({ queryKey: WORDS_KEY });
      void qc.invalidateQueries({ queryKey: POLICY_GROUPS_KEY });
    },
  });

  const onOk = async () => {
    const values = await form.validateFields();
    submit.mutate(values);
  };

  return (
    <Modal
      open={open}
      title={t('compliance:words.batch.title')}
      onCancel={onClose}
      onOk={onOk}
      okText={t('compliance:words.batch.submit')}
      confirmLoading={submit.isPending}
      destroyOnHidden
      width={640}
    >
      <Form form={form} layout="vertical" style={{ marginTop: 16 }} initialValues={{ lines: '' }}>
        <Form.Item
          name="policy_group_id"
          label={t('compliance:policyGroup')}
          rules={[{ required: true, message: t('common:common.required') }]}
        >
          <PolicyGroupSelect groups={groups} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item
          name="lines"
          label={t('compliance:words.batch.lines')}
          extra={t('compliance:words.batch.linesExtra')}
          style={{ marginBottom: 0 }}
          rules={[
            {
              validator: (_, v: string) =>
                splitLines(v ?? '').length > 0 ? Promise.resolve() : Promise.reject(new Error(t('common:common.required'))),
            },
          ]}
        >
          <Input.TextArea rows={10} placeholder={t('compliance:words.batch.linesPlaceholder')} />
        </Form.Item>
      </Form>
    </Modal>
  );
}

export default function WordsTab({ groups, groupsLoading }: Props) {
  const { t } = useTranslation(['compliance', 'common']);
  const { message } = App.useApp();
  const qc = useQueryClient();
  const { filters, setFilters, params, pagination } = useTableQuery<Filters>({});
  const [drawer, setDrawer] = useState<{ open: boolean; word: SensitiveWord | null }>({ open: false, word: null });
  const [batchOpen, setBatchOpen] = useState(false);

  const list = useQuery({
    queryKey: [...WORDS_KEY, params],
    queryFn: () => complianceApi.words(params),
    placeholderData: (prev) => prev,
  });

  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: WORDS_KEY });
    void qc.invalidateQueries({ queryKey: POLICY_GROUPS_KEY });
  };

  const toggle = useMutation({
    mutationFn: ({ id, enabled }: { id: number; enabled: boolean }) => complianceApi.setWordEnabled(id, enabled),
    onSuccess: () => {
      message.success(t('common:common.operationSuccess'));
      invalidate();
    },
  });

  const remove = useMutation({
    mutationFn: (id: number) => complianceApi.removeWord(id),
    onSuccess: () => {
      message.success(t('common:common.deleteSuccess'));
      invalidate();
    },
  });

  const columns: ColumnsType<SensitiveWord> = [
    {
      title: t('compliance:words.word'),
      dataIndex: 'word',
      render: (w: string) => <span style={{ fontWeight: 600 }}>{w}</span>,
    },
    {
      title: t('compliance:policyGroup'),
      dataIndex: 'policy_group',
      width: 260,
      render: (_, r) => <GroupTags group={r.policy_group} compact />,
    },
    {
      title: t('common:common.note'),
      dataIndex: 'note',
      width: 200,
      render: (note: string) =>
        note ? (
          <Typography.Text ellipsis={{ tooltip: note }} style={{ maxWidth: 200, display: 'block' }}>
            {note}
          </Typography.Text>
        ) : (
          <Typography.Text type="secondary">-</Typography.Text>
        ),
    },
    {
      title: t('common:common.status'),
      dataIndex: 'enabled',
      width: 90,
      align: 'center',
      render: (enabled: boolean, r) => (
        <Switch
          size="small"
          checked={enabled}
          loading={toggle.isPending && toggle.variables?.id === r.id}
          onChange={(v) => toggle.mutate({ id: r.id, enabled: v })}
        />
      ),
    },
    {
      title: t('common:common.createdAt'),
      dataIndex: 'created_at',
      width: 130,
      render: (v: string) => <TimeCell value={v} />,
    },
    {
      title: t('common:common.actions'),
      key: 'actions',
      width: 100,
      align: 'center',
      fixed: 'right',
      render: (_, r) => (
        <Space size={0}>
          <Tooltip title={t('common:action.edit')}>
            <Button type="text" size="small" icon={<EditOutlined />} onClick={() => setDrawer({ open: true, word: r })} />
          </Tooltip>
          <Popconfirm
            title={t('common:common.confirmDeleteName', { name: r.word })}
            okText={t('common:action.delete')}
            okButtonProps={{ danger: true }}
            onConfirm={() => remove.mutate(r.id)}
          >
            <Tooltip title={t('common:action.delete')}>
              <Button type="text" size="small" danger icon={<DeleteOutlined />} />
            </Tooltip>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <FilterBar
        extra={
          <>
            <Button icon={<ImportOutlined />} onClick={() => setBatchOpen(true)}>
              {t('common:action.batchImport')}
            </Button>
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setDrawer({ open: true, word: null })}>
              {t('compliance:words.add')}
            </Button>
          </>
        }
      >
        <PolicyGroupSelect
          groups={groups}
          loading={groupsLoading}
          allowClear
          value={filters.policy_group_id}
          onChange={(v) => setFilters({ policy_group_id: v })}
          style={{ width: 220 }}
        />
        <Select
          allowClear
          placeholder={t('common:common.status')}
          style={{ width: 110 }}
          value={filters.enabled}
          onChange={(v) => setFilters({ enabled: v })}
          options={[
            { value: true, label: t('common:common.enabled') },
            { value: false, label: t('common:common.disabled') },
          ]}
        />
        <Input.Search
          allowClear
          placeholder={t('compliance:words.searchPlaceholder')}
          style={{ width: 240 }}
          onSearch={(v) => setFilters({ q: v.trim() || undefined })}
        />
      </FilterBar>

      <Table<SensitiveWord>
        className="yz-table"
        size="middle"
        rowKey="id"
        loading={list.isLoading}
        columns={columns}
        dataSource={list.data?.items ?? []}
        pagination={pagination(list.data?.total)}
        scroll={{ x: 'max-content' }}
        locale={{
          emptyText: (
            <EmptyState
              title={t('compliance:words.empty')}
              hint={t('compliance:words.emptyHint')}
              actionText={t('compliance:words.add')}
              onAction={() => setDrawer({ open: true, word: null })}
            />
          ),
        }}
      />

      <WordDrawer
        open={drawer.open}
        word={drawer.word}
        groups={groups}
        onClose={() => setDrawer((d) => ({ ...d, open: false }))}
      />
      <BatchImportModal open={batchOpen} groups={groups} onClose={() => setBatchOpen(false)} />
    </div>
  );
}
