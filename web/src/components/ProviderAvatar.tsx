import { providerStyle } from '@/utils/provider';

interface Props {
  provider: string | undefined | null;
  size?: number;
  radius?: number;
  style?: React.CSSProperties;
}

/**
 * Flat, 1px-bordered square letter avatar with a muted brand color per provider key.
 * Special-cases a few providers with a tiny inline SVG glyph.
 */
export default function ProviderAvatar({ provider, size = 20, radius = 4, style }: Props) {
  const s = providerStyle(provider);
  const font = Math.round(size * 0.5);
  return (
    <span
      title={provider ?? undefined}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        width: size,
        height: size,
        borderRadius: radius,
        background: s.bg,
        color: s.fg,
        fontSize: font,
        fontWeight: 600,
        lineHeight: 1,
        flexShrink: 0,
        letterSpacing: -0.3,
        boxShadow: 'inset 0 0 0 1px rgba(0,0,0,0.08)',
        ...style,
      }}
    >
      {glyph(provider, size) ?? s.letter}
    </span>
  );
}

function glyph(key: string | null | undefined, size: number) {
  const sz = Math.round(size * 0.6);
  switch (key) {
    case 'openai':
      return (
        <svg width={sz} height={sz} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2">
          <circle cx="12" cy="12" r="8" />
          <path d="M12 4v16M4 12h16M6.3 6.3l11.4 11.4M17.7 6.3L6.3 17.7" opacity="0.7" />
        </svg>
      );
    case 'anthropic':
      return (
        <svg width={sz} height={sz} viewBox="0 0 24 24" fill="currentColor">
          <path d="M13.5 4h3.2l5.3 16h-3.3l-1.1-3.4h-5.5L11 20H7.8L13.5 4zm-.6 9.8h3.9l-1.9-5.9-2 5.9zM2 4h3.2l5.2 16H7.2L2 4z" />
        </svg>
      );
    case 'gemini':
      return (
        <svg width={sz} height={sz} viewBox="0 0 24 24" fill="currentColor">
          <path d="M12 2c.6 5.5 4.5 9.4 10 10-5.5.6-9.4 4.5-10 10-.6-5.5-4.5-9.4-10-10 5.5-.6 9.4-4.5 10-10z" />
        </svg>
      );
    case 'xai':
      return (
        <svg width={sz} height={sz} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4">
          <path d="M4 4l16 16M20 4L4 20" />
        </svg>
      );
    case 'ollama':
      return (
        <svg width={sz} height={sz} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <rect x="6" y="8" width="12" height="12" rx="6" />
          <path d="M8 8V4M16 8V4" />
          <circle cx="10" cy="14" r="1" fill="currentColor" />
          <circle cx="14" cy="14" r="1" fill="currentColor" />
        </svg>
      );
    case 'deepseek':
      return (
        <svg width={sz} height={sz} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2">
          <path d="M4 14c2-6 8-9 16-8-2 4-2 8-7 12-3 2-6 1-9-4z" />
          <path d="M9 18c2-3 5-5 8-6" />
        </svg>
      );
    case 'openrouter':
      return (
        <svg width={sz} height={sz} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4">
          <path d="M3 12h5l3-5 4 10 3-5h3" />
        </svg>
      );
    default:
      return null;
  }
}
