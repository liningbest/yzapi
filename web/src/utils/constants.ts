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
  text: ['openai-completions', 'openai-responses', 'anthropic-messages'],
  image: ['openai-images'],
  embedding: ['openai-embeddings'],
};

export const PROTOCOL_LABELS: Record<Protocol, string> = {
  'openai-completions': 'OpenAI Chat Completions',
  'openai-responses': 'OpenAI Responses',
  'anthropic-messages': 'Anthropic Messages',
  'openai-embeddings': 'OpenAI Embeddings',
  'openai-images': 'OpenAI Images',
};

export const CHART_PALETTE = ['#4f46e5', '#06b6d4', '#f59e0b', '#10b981', '#ef4444', '#8b5cf6', '#ec4899', '#14b8a6'];

export const PRIMARY = '#4f46e5';

export const TYPE_COLORS: Record<ModelType, string> = {
  text: 'geekblue',
  image: 'magenta',
  embedding: 'cyan',
};

export const HEALTH_COLORS = {
  available: 'success',
  cooling: 'warning',
  unavailable: 'error',
} as const;

export const RESULT_COLORS = {
  success: 'success',
  client_error: 'warning',
  upstream_error: 'error',
  blocked: 'magenta',
  rate_limited: 'orange',
} as const;

export const PAGE_SIZE = 20;
