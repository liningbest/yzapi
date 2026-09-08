import { useEffect, useMemo } from 'react';
import type { CSSProperties } from 'react';
import { App, Button, Form, Input, Radio, Select, Tooltip, Typography } from 'antd';
import {
  ArrowDownOutlined,
  ArrowUpOutlined,
  CloseOutlined,
  HolderOutlined,
} from '@ant-design/icons';
import { DndContext, KeyboardSensor, PointerSensor, closestCenter, useSensor, useSensors } from '@dnd-kit/core';
import type { DragEndEvent } from '@dnd-kit/core';
import { SortableContext, arrayMove, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { modelGroupsApi, providersApi } from '@/api';
import { FormDrawer, KindTag, NeutralTag, ProviderAvatar } from '@/components';
import type { ModelGroup, ModelGroupInput, ModelType, RoutableModel } from '@/types';
import { MODEL_TYPES } from '@/utils/constants';

interface Props {
  open: boolean;
  /** Group to edit; null/undefined when creating. */
  group?: ModelGroup | null;
  onClose: () => void;
  onSaved: () => void;
}

interface FormValues {
  name: string;
  type: ModelType;
  models: string[];
  note?: string;
}

// ---------- sortable row ----------
interface RowProps {
  id: string;
  index: number;
  total: number;
  onUp: () => void;
  onDown: () => void;
  onRemove: () => void;
}

function SortableRow({ id, index, total, onUp, onDown, onRemove }: RowProps) {
  const { t } = useTranslation(['modelGroups', 'common']);
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, transform, transition, isDragging } = useSortable({
    id,
  });
  const style: CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
    display: 'flex',
    alignItems: 'center',
    gap: 6,
    padding: '4px 6px 4px 4px',
    borderRadius: 6,
    border: '1px solid var(--yz-border)',
    background: 'var(--yz-card)',
    opacity: isDragging ? 0.65 : 1,
    boxShadow: isDragging ? '0 4px 12px rgba(0,0,0,0.08)' : undefined,
    position: 'relative',
    zIndex: isDragging ? 2 : undefined,
  };
  return (
    <div ref={setNodeRef} style={style}>
      <Tooltip title={t('modelGroups:form.dragHint')}>
        <Button
          ref={setActivatorNodeRef}
          type="text"
          size="small"
          icon={<HolderOutlined />}
          style={{ cursor: 'grab', color: 'var(--yz-text-tertiary)', touchAction: 'none' }}
          {...attributes}
          {...listeners}
        />
      </Tooltip>
      <span
        className="yz-mono"
        style={{
          width: 22,
          textAlign: 'right',
          color: 'var(--yz-text-tertiary)',
          flexShrink: 0,
        }}
      >
        {index + 1}
      </span>
      <span className="yz-mono" style={{ flex: 1, fontSize: 13, minWidth: 0, wordBreak: 'break-all' }}>
        {id}
      </span>
      <Tooltip title={t('common:action.up')}>
        <Button type="text" size="small" icon={<ArrowUpOutlined />} disabled={index === 0} onClick={onUp} />
      </Tooltip>
      <Tooltip title={t('common:action.down')}>
        <Button type="text" size="small" icon={<ArrowDownOutlined />} disabled={index === total - 1} onClick={onDown} />
      </Tooltip>
      <Tooltip title={t('common:action.remove')}>
        <Button type="text" size="small" danger icon={<CloseOutlined />} onClick={onRemove} />
      </Tooltip>
    </div>
  );
}

// ---------- ordered model list editor (controlled, used inside Form.Item) ----------
interface EditorProps {
  value?: string[];
  onChange?: (v: string[]) => void;
  candidates: RoutableModel[];
  loading?: boolean;
}

function ModelListEditor({ value, onChange, candidates, loading }: EditorProps) {
  const { t } = useTranslation(['modelGroups', 'common']);
  const { message } = App.useApp();
  const list = useMemo(() => value ?? [], [value]);
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const add = (raw: string) => {
    const name = raw.trim();
    if (!name) return;
    if (list.includes(name)) {
      message.warning(t('modelGroups:form.modelExists'));
      return;
    }
    onChange?.([...list, name]);
  };
  const move = (from: number, to: number) => {
    if (to < 0 || to >= list.length) return;
    onChange?.(arrayMove(list, from, to));
  };
  const remove = (i: number) => onChange?.(list.filter((_, idx) => idx !== i));
  const onDragEnd = ({ active, over }: DragEndEvent) => {
    if (!over || active.id === over.id) return;
    const from = list.indexOf(String(active.id));
    const to = list.indexOf(String(over.id));
    if (from >= 0 && to >= 0) onChange?.(arrayMove(list, from, to));
  };

  const options = candidates
    .filter((m) => !list.includes(m.name))
    .map((m) => ({ value: m.name, label: m.name, meta: m }));

  return (
    <div>
      <Select
        mode="tags"
        value={[]}
        loading={loading}
        showSearch
        style={{ width: '100%' }}
        placeholder={t('modelGroups:form.modelsPlaceholder')}
        options={options}
        onSelect={(v) => add(String(v))}
        optionRender={(opt) => {
          const m = (opt.data as { meta?: RoutableModel }).meta;
          return (
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <span className="yz-mono" style={{ flex: 1, fontSize: 13 }}>
                {String(opt.value)}
              </span>
              {m ? (
                m.kind === 'model' ? (
                  <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
                    <ProviderAvatar provider={m.provider} size={14} />
                    <Typography.Text type="secondary" style={{ fontSize: 11 }}>
                      {m.provider}
                    </Typography.Text>
                  </span>
                ) : (
                  <KindTag kind={m.kind} />
                )
              ) : null}
            </div>
          );
        }}
      />
      <div style={{ marginTop: 10 }}>
        {list.length === 0 ? (
          <div
            style={{
              padding: '16px 12px',
              textAlign: 'center',
              color: 'var(--yz-text-secondary)',
              border: '1px dashed var(--yz-border)',
              borderRadius: 6,
              fontSize: 13,
            }}
          >
            {t('modelGroups:form.modelsEmpty')}
          </div>
        ) : (
          <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={onDragEnd}>
            <SortableContext items={list} strategy={verticalListSortingStrategy}>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                {list.map((name, i) => (
                  <SortableRow
                    key={name}
                    id={name}
                    index={i}
                    total={list.length}
                    onUp={() => move(i, i - 1)}
                    onDown={() => move(i, i + 1)}
                    onRemove={() => remove(i)}
                  />
                ))}
              </div>
            </SortableContext>
          </DndContext>
        )}
        {list.length > 0 ? (
          <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 6 }}>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {t('modelGroups:form.orderHint')}
            </Typography.Text>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {t('modelGroups:modelsCount', { count: list.length })}
            </Typography.Text>
          </div>
        ) : null}
      </div>
    </div>
  );
}

// ---------- drawer ----------
export default function ModelGroupDrawer({ open, group, onClose, onSaved }: Props) {
  const { t } = useTranslation(['modelGroups', 'common']);
  const { message } = App.useApp();
  const qc = useQueryClient();
  const [form] = Form.useForm<FormValues>();
  const isEdit = Boolean(group);
  const type = Form.useWatch('type', form);

  useEffect(() => {
    if (!open) return;
    if (group) {
      form.setFieldsValue({ name: group.name, type: group.type, models: [...group.models], note: group.note });
    }
  }, [open, group, form]);

  const modelsQ = useQuery({ queryKey: ['admin', 'models'], queryFn: providersApi.models, enabled: open });
  const candidates = useMemo(
    () => (modelsQ.data ?? []).filter((m) => m.type === type && m.kind !== 'group'),
    [modelsQ.data, type],
  );

  const saveMut = useMutation({
    mutationFn: (body: ModelGroupInput) => (group ? modelGroupsApi.update(group.id, body) : modelGroupsApi.create(body)),
    onSuccess: () => {
      message.success(t('common:common.saveSuccess'));
      void qc.invalidateQueries({ queryKey: ['admin', 'model-groups'] });
      void qc.invalidateQueries({ queryKey: ['admin', 'models'] });
      onSaved();
    },
  });

  const onSubmit = async () => {
    let v: FormValues;
    try {
      v = await form.validateFields();
    } catch {
      return;
    }
    saveMut.mutate({ name: v.name.trim(), type: v.type, models: v.models, note: v.note?.trim() || undefined });
  };

  const requiredRule = { required: true, message: t('common:common.required') };

  return (
    <FormDrawer
      open={open}
      width={560}
      title={isEdit ? t('modelGroups:edit') : t('modelGroups:add')}
      onClose={onClose}
      onSubmit={() => void onSubmit()}
      submitting={saveMut.isPending}
    >
      <Form<FormValues>
        form={form}
        layout="vertical"
        requiredMark="optional"
        initialValues={{ type: 'text', models: [] }}
        onValuesChange={(changed: Partial<FormValues>) => {
          // switching type clears models that belong to the previous type
          if ('type' in changed && !isEdit) form.setFieldValue('models', []);
        }}
      >
        <Form.Item
          name="name"
          label={t('modelGroups:form.name')}
          extra={t('modelGroups:form.nameExtra')}
          rules={[{ ...requiredRule, whitespace: true }, { max: 100 }]}
        >
          <Input placeholder={t('modelGroups:form.namePlaceholder')} maxLength={100} />
        </Form.Item>
        <Form.Item
          name="type"
          label={t('modelGroups:form.type')}
          extra={isEdit ? t('modelGroups:form.typeLocked') : t('modelGroups:form.typeExtra')}
          rules={[requiredRule]}
        >
          <Radio.Group
            optionType="button"
            buttonStyle="solid"
            disabled={isEdit}
            options={MODEL_TYPES.map((x) => ({ label: t(`common:type.${x}`), value: x }))}
          />
        </Form.Item>
        <Form.Item
          name="models"
          label={t('modelGroups:form.models')}
          extra={t('modelGroups:form.modelsExtra')}
          required
          rules={[
            {
              validator: async (_rule, v: string[] | undefined) => {
                const list = v ?? [];
                if (list.length < 1) throw new Error(t('modelGroups:form.modelsMin'));
                if (list.length > 100) throw new Error(t('modelGroups:form.modelsMax'));
                const seen = new Set<string>();
                for (const m of list) {
                  if (seen.has(m)) throw new Error(t('modelGroups:form.modelsDuplicate', { name: m }));
                  seen.add(m);
                }
              },
            },
          ]}
        >
          <ModelListEditor candidates={candidates} loading={modelsQ.isLoading} />
        </Form.Item>
        <Form.Item name="note" label={t('modelGroups:form.note')}>
          <Input.TextArea rows={3} maxLength={500} showCount placeholder={t('common:common.notePlaceholder')} />
        </Form.Item>
      </Form>
      {isEdit && group?.in_use_by_route ? (
        <NeutralTag style={{ marginTop: 4 }}>{t('modelGroups:inUseByRoute')}</NeutralTag>
      ) : null}
    </FormDrawer>
  );
}
