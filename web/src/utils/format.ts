import dayjs from 'dayjs';
import relativeTimePlugin from 'dayjs/plugin/relativeTime';
import 'dayjs/locale/zh-cn';
import 'dayjs/locale/zh-tw';
import 'dayjs/locale/en';

dayjs.extend(relativeTimePlugin);

/** 1234 -> "1.2K", 2500000 -> "2.5M", 1e9 -> "1.0B" */
export function formatTokens(n: number | null | undefined, digits = 1): string {
  if (n === null || n === undefined || Number.isNaN(n)) return '-';
  const abs = Math.abs(n);
  if (abs < 1000) return String(n);
  if (abs < 1_000_000) return `${(n / 1000).toFixed(digits)}K`;
  if (abs < 1_000_000_000) return `${(n / 1_000_000).toFixed(digits)}M`;
  return `${(n / 1_000_000_000).toFixed(digits)}B`;
}

/** Full number with thousands separators. */
/** Money in the gateway's base currency: 2 decimals normally, more for sub-cent values. */
export function formatMoney(v: number | null | undefined, currency = 'CNY'): string {
  if (v == null || Number.isNaN(v)) return '-';
  const sym = currency === 'USD' ? '$' : '¥';
  const abs = Math.abs(v);
  const digits = abs === 0 ? 2 : abs < 0.01 ? 4 : 2;
  return `${sym}${v.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })}`;
}

export function formatNumber(n: number | null | undefined): string {
  if (n === null || n === undefined || Number.isNaN(n)) return '-';
  return n.toLocaleString();
}

export function formatMs(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || Number.isNaN(ms)) return '-';
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(2)} s`;
  const m = Math.floor(ms / 60_000);
  const s = Math.round((ms % 60_000) / 1000);
  return `${m}m ${s}s`;
}

/** rate is 0..1 (or 0..100 if `isPercent`). */
export function formatPercent(rate: number | null | undefined, digits = 1, isPercent = false): string {
  if (rate === null || rate === undefined || Number.isNaN(rate)) return '-';
  const v = isPercent ? rate : rate * 100;
  return `${v.toFixed(digits)}%`;
}

export function formatBytes(b: number | null | undefined): string {
  if (b === null || b === undefined) return '-';
  if (b < 1024) return `${b} B`;
  if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`;
  if (b < 1024 * 1024 * 1024) return `${(b / 1024 / 1024).toFixed(1)} MB`;
  return `${(b / 1024 / 1024 / 1024).toFixed(2)} GB`;
}

export function formatDuration(sec: number | null | undefined): string {
  if (sec === null || sec === undefined) return '-';
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = Math.floor(sec % 60);
  const parts: string[] = [];
  if (d) parts.push(`${d}d`);
  if (h) parts.push(`${h}h`);
  if (m) parts.push(`${m}m`);
  parts.push(`${s}s`);
  return parts.join(' ');
}

export function formatDateTime(v: string | null | undefined, fmt = 'YYYY-MM-DD HH:mm:ss'): string {
  if (!v) return '-';
  const d = dayjs(v);
  return d.isValid() ? d.format(fmt) : '-';
}

export function relativeTime(v: string | null | undefined): string {
  if (!v) return '-';
  const d = dayjs(v);
  return d.isValid() ? d.fromNow() : '-';
}

export function isZeroTime(v: string | null | undefined): boolean {
  if (!v) return true;
  return v.startsWith('0001-01-01');
}

export function shortId(id: string | null | undefined, head = 8): string {
  if (!id) return '-';
  return id.length > head + 2 ? `${id.slice(0, head)}…` : id;
}

export function setDayjsLocale(locale: string) {
  dayjs.locale(locale === 'zh-CN' ? 'zh-cn' : locale === 'zh-TW' ? 'zh-tw' : 'en');
}

/** Token quota unit helpers. */
export const QUOTA_UNITS: { key: string; value: number }[] = [
  { key: 'token', value: 1 },
  { key: 'wan', value: 10_000 },
  { key: 'million', value: 1_000_000 },
  { key: 'tenMillion', value: 10_000_000 },
  { key: 'yi', value: 100_000_000 },
  { key: 'tenYi', value: 10_000_000_000 },
  { key: 'trillion', value: 1_000_000_000_000 },
];

/** Split raw quota into {amount, unit} choosing the largest unit that divides evenly. */
export function splitQuota(raw: number): { amount: number; unit: string } {
  if (!raw) return { amount: 0, unit: 'token' };
  for (let i = QUOTA_UNITS.length - 1; i >= 0; i--) {
    const u = QUOTA_UNITS[i];
    if (raw % u.value === 0) return { amount: raw / u.value, unit: u.key };
  }
  return { amount: raw, unit: 'token' };
}

export function joinQuota(amount: number, unit: string): number {
  const u = QUOTA_UNITS.find((x) => x.key === unit) ?? QUOTA_UNITS[0];
  return Math.round((amount || 0) * u.value);
}

export { dayjs };
