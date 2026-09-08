/** Brand-ish colors and initials for provider avatars. */
export interface ProviderStyle {
  bg: string;
  fg: string;
  letter: string;
}

const STYLES: Record<string, ProviderStyle> = {
  openai: { bg: '#10a37f', fg: '#fff', letter: 'O' },
  anthropic: { bg: '#d97757', fg: '#fff', letter: 'A' },
  deepseek: { bg: '#4d6bfe', fg: '#fff', letter: 'D' },
  aliyun: { bg: '#ff6a00', fg: '#fff', letter: '阿' },
  tencent: { bg: '#0052d9', fg: '#fff', letter: '腾' },
  volcengine: { bg: '#1664ff', fg: '#fff', letter: '火' },
  zhipu: { bg: '#3859ff', fg: '#fff', letter: '智' },
  moonshot: { bg: '#111827', fg: '#fff', letter: 'K' },
  minimax: { bg: '#f23f5d', fg: '#fff', letter: 'M' },
  siliconflow: { bg: '#7c3aed', fg: '#fff', letter: 'S' },
  gemini: { bg: '#1a73e8', fg: '#fff', letter: 'G' },
  xai: { bg: '#000000', fg: '#fff', letter: 'X' },
  openrouter: { bg: '#6366f1', fg: '#fff', letter: 'R' },
  vllm: { bg: '#f59e0b', fg: '#fff', letter: 'V' },
  ollama: { bg: '#374151', fg: '#fff', letter: 'L' },
  newapi: { bg: '#0ea5e9', fg: '#fff', letter: 'N' },
  custom: { bg: '#64748b', fg: '#fff', letter: 'C' },
};

export function providerStyle(key: string | undefined | null): ProviderStyle {
  if (key && STYLES[key]) return STYLES[key];
  const letter = (key || '?').slice(0, 1).toUpperCase();
  return { bg: '#64748b', fg: '#fff', letter };
}

export const PROVIDER_DOCS: Record<string, string> = {
  openai: 'https://platform.openai.com/docs',
  anthropic: 'https://docs.anthropic.com',
  deepseek: 'https://api-docs.deepseek.com',
  aliyun: 'https://help.aliyun.com/zh/model-studio',
  tencent: 'https://cloud.tencent.com/document/product/1729',
  volcengine: 'https://www.volcengine.com/docs/82379',
  zhipu: 'https://open.bigmodel.cn/dev/api',
  moonshot: 'https://platform.moonshot.cn/docs',
  minimax: 'https://platform.minimaxi.com/document',
  siliconflow: 'https://docs.siliconflow.cn',
  gemini: 'https://ai.google.dev/gemini-api/docs',
  xai: 'https://docs.x.ai',
  openrouter: 'https://openrouter.ai/docs',
  vllm: 'https://docs.vllm.ai',
  ollama: 'https://github.com/ollama/ollama/blob/main/docs/openai.md',
  newapi: 'https://github.com/QuantumNous/new-api',
};
