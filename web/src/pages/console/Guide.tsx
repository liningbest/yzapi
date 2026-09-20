import { useMemo, useState } from 'react';
import { Alert, App, Button, Card, Select, Space, Steps, Tabs, Typography } from 'antd';
import { CopyOutlined } from '@ant-design/icons';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { userApi } from '@/api';
import { PageHeader, SectionTitle } from '@/components';
import { apiRoot } from './models/ApiDocModal';

type ClientKey = 'claude-code' | 'codex' | 'opencode' | 'gemini-cli' | 'cline' | 'sdk' | 'custom';

const KEY = '<YOUR_API_KEY>';

/** Per-client, copy-ready configuration for reaching this gateway. The key is never shown
 * (only its creation dialog reveals it); the user pastes it into the placeholder. */
export default function Guide() {
  const { t } = useTranslation(['console', 'common']);
  const { message } = App.useApp();
  const models = useQuery({ queryKey: ['user', 'models'], queryFn: userApi.models });
  const root = apiRoot(models.data?.base_url ?? '');
  const textModels = useMemo(() => (models.data?.models ?? []).filter((m) => m.type === 'text').map((m) => m.name), [models.data]);
  const customModels = useMemo(() => (models.data?.models ?? []).filter((m) => m.type === 'custom' && m.kind === 'model' && (m.endpoints?.length ?? 0) > 0), [models.data]);
  const [model, setModel] = useState<string>('');
  const chosen = model || textModels[0] || 'gpt-5.5';
  const [tab, setTab] = useState<ClientKey>('claude-code');

  const copy = (text: string) => void navigator.clipboard.writeText(text).then(() => message.success(t('common:action.copied')));

  const snippets: Record<ClientKey, { steps: string[]; blocks: { title: string; code: string }[] }> = useMemo(
    () => ({
      'claude-code': {
        steps: [t('console:guide.claudeCode.s1'), t('console:guide.claudeCode.s2'), t('console:guide.claudeCode.s3')],
        blocks: [
          {
            title: '~/.claude/settings.json',
            code: JSON.stringify(
              { env: { ANTHROPIC_BASE_URL: root, ANTHROPIC_AUTH_TOKEN: KEY, ANTHROPIC_MODEL: chosen, ANTHROPIC_DEFAULT_SONNET_MODEL: chosen, ANTHROPIC_DEFAULT_OPUS_MODEL: chosen } },
              null,
              2,
            ),
          },
          {
            title: t('console:guide.orShell'),
            code: [`export ANTHROPIC_BASE_URL="${root}"`, `export ANTHROPIC_AUTH_TOKEN="${KEY}"`, `export ANTHROPIC_MODEL="${chosen}"`, `claude`].join('\n'),
          },
        ],
      },
      codex: {
        steps: [t('console:guide.codex.s1'), t('console:guide.codex.s2'), t('console:guide.codex.s3')],
        blocks: [
          {
            title: '~/.codex/config.toml',
            code: [
              `model_provider = "yzapi"`,
              `model = "${chosen}"`,
              ``,
              `[model_providers.yzapi]`,
              `name = "yzapi"`,
              `base_url = "${root}/v1"`,
              `env_key = "YZAPI_API_KEY"`,
              `wire_api = "responses"`,
            ].join('\n'),
          },
          { title: t('console:guide.orShell'), code: [`export YZAPI_API_KEY="${KEY}"`, `codex`].join('\n') },
        ],
      },
      opencode: {
        steps: [t('console:guide.opencode.s1'), t('console:guide.opencode.s2'), t('console:guide.opencode.s3'), t('console:guide.opencode.s4')],
        blocks: [
          {
            title: '~/.config/opencode/opencode.json',
            code: JSON.stringify(
              {
                $schema: 'https://opencode.ai/config.json',
                provider: {
                  yzapi: {
                    npm: '@ai-sdk/openai-compatible',
                    name: 'yzapi',
                    options: { baseURL: `${root}/v1`, apiKey: '{env:YZAPI_API_KEY}' },
                    models: { [chosen]: { name: chosen } },
                  },
                },
                model: `yzapi/${chosen}`,
              },
              null,
              2,
            ),
          },
          { title: t('console:guide.orShell'), code: [`export YZAPI_API_KEY="${KEY}"`, `opencode`].join('\n') },
        ],
      },
      'gemini-cli': {
        steps: [t('console:guide.geminiCli.s1'), t('console:guide.geminiCli.s2'), t('console:guide.geminiCli.s3'), t('console:guide.geminiCli.s4')],
        blocks: [
          {
            title: t('console:guide.orShell'),
            code: [`export GEMINI_API_KEY="${KEY}"`, `export GOOGLE_GEMINI_BASE_URL="${root}"`, `gemini -m "${chosen}"`].join('\n'),
          },
        ],
      },
      cline: {
        steps: [t('console:guide.cline.s1'), t('console:guide.cline.s2'), t('console:guide.cline.s3')],
        blocks: [
          {
            title: t('console:guide.cline.fields'),
            code: [`API Provider: OpenAI Compatible`, `Base URL: ${root}/v1`, `API Key: ${KEY}`, `Model ID: ${chosen}`].join('\n'),
          },
          {
            title: t('console:guide.cline.anthropic'),
            code: [`API Provider: Anthropic`, `Base URL: ${root}`, `API Key: ${KEY}`, `Model: ${chosen}`].join('\n'),
          },
        ],
      },
      sdk: {
        steps: [t('console:guide.sdk.s1'), t('console:guide.sdk.s2')],
        blocks: [
          {
            title: 'curl (Chat Completions)',
            code: [
              `curl ${root}/v1/chat/completions \\`,
              `  -H "Authorization: Bearer ${KEY}" -H "Content-Type: application/json" \\`,
              `  -d '{"model":"${chosen}","messages":[{"role":"user","content":"Hello"}]}'`,
            ].join('\n'),
          },
          {
            title: 'Python (openai)',
            code: [`from openai import OpenAI`, `client = OpenAI(base_url="${root}/v1", api_key="${KEY}")`, `r = client.chat.completions.create(model="${chosen}", messages=[{"role": "user", "content": "Hello"}])`, `print(r.choices[0].message.content)`].join('\n'),
          },
          {
            title: 'Python (anthropic)',
            code: [`import anthropic`, `client = anthropic.Anthropic(base_url="${root}", api_key="${KEY}")`, `m = client.messages.create(model="${chosen}", max_tokens=256, messages=[{"role": "user", "content": "Hello"}])`, `print(m.content[0].text)`].join('\n'),
          },
        ],
      },
      custom: (() => {
        // Non-chat JSON models (TypeSafe Jev, rerankers, classifiers): each is called on the
        // path(s) its account declares, with the same key as everything else.
        const first = customModels[0];
        const cm = first?.name ?? 'jev';
        const cp = first?.endpoints?.[0] ?? '/systemone';
        const list = customModels.length
          ? customModels.map((m) => `${m.name}  ->  ${(m.endpoints ?? []).map((p) => `POST ${root}/v1${p}`).join('  |  ')}`).join('\n')
          : t('console:guide.custom.none');
        return {
          steps: [t('console:guide.custom.s1'), t('console:guide.custom.s2'), t('console:guide.custom.s3')],
          blocks: [
            { title: t('console:guide.custom.available'), code: list },
            {
              title: 'curl',
              code: [
                `curl ${root}/v1${cp} \\`,
                `  -H "Authorization: Bearer ${KEY}" -H "Content-Type: application/json" \\`,
                `  -d '{"model":"${cm}","state":"I was charged twice.","questions":{"billing":{"type":"noul","instructions":"Is this about billing?"}}}'`,
              ].join('\n'),
            },
            {
              title: 'Python (typesafe_sdk, TypeSafe Jev)',
              code: [
                `# pip install typesafe-sdk   (or: export TYPESAFE_BASE_URL="${root}" TYPESAFE_API_KEY="${KEY}")`,
                `from typesafe_sdk import TypeSafeClient, Noul`,
                `with TypeSafeClient(base_url="${root}", api_key="${KEY}") as client:`,
                `    r = client.system_one(state="I was charged twice.", questions={"billing": Noul(instructions="Is this about billing?")})`,
                `    print(r.answers["billing"])`,
              ].join('\n'),
            },
            {
              title: 'Python (requests)',
              code: [
                `import requests`,
                `r = requests.post("${root}/v1${cp}", headers={"Authorization": "Bearer ${KEY}"},`,
                `                  json={"model": "${cm}", "state": "I was charged twice.", "questions": {"billing": {"type": "noul", "instructions": "Is this about billing?"}}})`,
                `print(r.status_code, r.json())`,
              ].join('\n'),
            },
          ],
        };
      })(),
    }),
    [root, chosen, customModels, t],
  );

  const current = snippets[tab];
  const items = (['claude-code', 'codex', 'opencode', 'gemini-cli', 'cline', 'sdk', 'custom'] as ClientKey[]).map((k) => ({ key: k, label: t(`console:guide.tabs.${k}`) }));

  return (
    <div>
      <PageHeader title={t('console:guide.title')} subtitle={t('console:guide.subtitle')} />
      <Card className="yz-card" style={{ marginBottom: 16 }}>
        <Space wrap size={16}>
          <span>
            <Typography.Text type="secondary">{t('console:models.baseUrl')}：</Typography.Text>
            <Typography.Text className="yz-mono" copyable={{ text: root }}>
              {root || '-'}
            </Typography.Text>
          </span>
          <span>
            <Typography.Text type="secondary">{t('console:guide.model')}：</Typography.Text>
            <Select size="small" showSearch style={{ minWidth: 220 }} value={chosen} onChange={setModel} options={textModels.map((m) => ({ value: m, label: m }))} loading={models.isLoading} />
          </span>
        </Space>
        <Alert type="info" showIcon style={{ marginTop: 12 }} message={t('console:guide.keyHint')} />
      </Card>

      <Card className="yz-card">
        <Tabs activeKey={tab} onChange={(k) => setTab(k as ClientKey)} items={items} />
        <Steps size="small" direction="vertical" current={-1} items={current.steps.map((s) => ({ title: s }))} style={{ marginBottom: 16 }} />
        {current.blocks.map((b) => (
          <div key={b.title} style={{ marginBottom: 16 }}>
            <SectionTitle
              extra={
                <Button size="small" icon={<CopyOutlined />} onClick={() => copy(b.code)}>
                  {t('common:action.copy')}
                </Button>
              }
            >
              {b.title}
            </SectionTitle>
            <pre className="yz-code-block" style={{ margin: 0 }}>{b.code}</pre>
          </div>
        ))}
        <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginTop: 8 }}>
          {t('console:guide.protocolNote')}
        </Typography.Paragraph>
      </Card>
    </div>
  );
}
