import type { ModelType, Protocol } from '@/types';

export const MODEL_TYPES: ModelType[] = ['text', 'image', 'embedding'];

export const PROTOCOLS: Protocol[] = [
  'openai-completions',
  'openai-responses',
  'anthropic-messages',
  'openai-embeddings',
  'openai-images',
];

export const PROTOCOLS_BY_TYPE: Record<ModelType, Protocol[]> = {
  text: ['openai-completions', 'openai-responses', 'anthropic-messages', 'gemini-generate'],
  image: ['openai-images'],
  embedding: ['openai-embeddings'],
};

export const PROTOCOL_LABELS: Record<Protocol, string> = {
  'openai-completions': 'OpenAI Chat Completions',
  'openai-responses': 'OpenAI Responses',
  'anthropic-messages': 'Anthropic Messages',
  'gemini-generate': 'Gemini generateContent',
  'openai-embeddings': 'OpenAI Embeddings',
  'openai-images': 'OpenAI Images',
};

export const CHART_PALETTE = ['#2563eb', '#0891b2', '#64748b', '#d97706', '#16a34a', '#dc2626', '#7c3aed', '#db2777'];

export const PRIMARY = '#2563eb';

/** Semantic colors used for status dots and small text; never large filled areas. */
export const SEMANTIC = {
  success: '#16a34a',
  warning: '#d97706',
  danger: '#dc2626',
  neutral: '#a1a1aa',
  info: '#2563eb',
} as const;

export type SemanticKey = keyof typeof SEMANTIC;

/** Model types are all rendered as neutral outlined tags. */
export const TYPE_COLORS: Record<ModelType, SemanticKey> = {
  text: 'neutral',
  image: 'neutral',
  embedding: 'neutral',
};

export const HEALTH_COLORS: Record<string, SemanticKey> = {
  available: 'success',
  cooling: 'warning',
  unavailable: 'danger',
};

export const RESULT_COLORS: Record<string, SemanticKey> = {
  success: 'success',
  client_error: 'warning',
  upstream_error: 'danger',
  blocked: 'danger',
  rate_limited: 'warning',
};

export const PAGE_SIZE = 20;
