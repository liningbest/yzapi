interface Props {
  size?: number;
  className?: string;
  /** Tile fill; defaults to near-black. Pass e.g. "#fafafa" on dark panels. */
  color?: string;
  /** Stroke color for the "YZ" mark. */
  stroke?: string;
}

/** Inline SVG brand mark: solid dark square with a stylized "YZ" stroke. */
export default function BrandMark({ size = 24, className, color = '#18181b', stroke = '#ffffff' }: Props) {
  return (
    <svg
      className={className}
      width={size}
      height={size}
      viewBox="0 0 64 64"
      xmlns="http://www.w3.org/2000/svg"
      aria-label="YZ AI Gateway"
      style={{ flexShrink: 0, display: 'block' }}
    >
      <rect width="64" height="64" rx="10" fill={color} />
      <path
        d="M18 18l14 16v12M46 18L32 34"
        stroke={stroke}
        strokeWidth="6"
        strokeLinecap="round"
        strokeLinejoin="round"
        fill="none"
      />
    </svg>
  );
}
